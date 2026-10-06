// Package task owns Task state and its transitions.
//
// Every transition is a conditional update that only succeeds from the
// expected prior state, so the first outcome recorded is final and later
// attempts are no-ops.
package task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/yuchia329/unstuck/internal/secret"
)

// Schema is the Task lifecycle's part of the database schema.
const Schema = `
CREATE TABLE IF NOT EXISTS tasks (
	id                 TEXT PRIMARY KEY,
	page_url           TEXT NOT NULL,
	obstacle           TEXT NOT NULL DEFAULT '',
	state              TEXT NOT NULL CHECK (state IN ('pending', 'claimed', 'solved', 'expired', 'failed')),
	session_token_hash TEXT NOT NULL UNIQUE,
	created_at         INTEGER NOT NULL,
	claim_deadline     INTEGER NOT NULL,
	solver             TEXT,
	solve_deadline     INTEGER,
	ended_at           INTEGER
);
`

// Migrate brings a tasks table from when a Task belonged to a registered
// Customer and its Solver was a wallet address up to Schema. CREATE TABLE IF
// NOT EXISTS leaves such a table as it was, and its customer_id column cannot
// be dropped in place, so the table is rebuilt. Every Task is kept; which
// Customer it belonged to is not.
func Migrate(db *sql.DB) error {
	var legacy int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('tasks') WHERE name = 'customer_id'`).Scan(&legacy); err != nil {
		return fmt.Errorf("migrate tasks: %w", err)
	}
	if legacy == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("migrate tasks: %w", err)
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`ALTER TABLE tasks RENAME TO tasks_legacy`,
		Schema,
		`INSERT INTO tasks (id, page_url, obstacle, state, session_token_hash, created_at, claim_deadline, solver, solve_deadline, ended_at)
		 SELECT id, page_url, obstacle, state, session_token_hash, created_at, claim_deadline, solver_wallet, solve_deadline, ended_at
		 FROM tasks_legacy`,
		`DROP TABLE tasks_legacy`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate tasks: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate tasks: %w", err)
	}
	return nil
}

type State string

const (
	Pending State = "pending"
	Claimed State = "claimed"
	Solved  State = "solved"
	Expired State = "expired"
	Failed  State = "failed"
)

type Config struct {
	ClaimWindow time.Duration
	SolveWindow time.Duration
}

// Event reports a committed change to a Task. Events for a Task are
// delivered in the order its changes were committed.
type Event struct {
	TaskID        string
	State         State
	PageURL       string
	Obstacle      string // what the Solver is to clear, as the Agent described it; may be empty
	CreatedAt     time.Time
	Solver        string    // the claiming Solver's id, set once the Task is claimed
	SolveDeadline time.Time // set on the Claimed Event
	Reason        Reason    // why a Task Failed
}

// Reason says why a Task Failed.
type Reason string

const (
	SolveWindowPassed  Reason = "solve_window"
	GaveUp             Reason = "gave_up"
	BridgeDisconnected Reason = "bridge_disconnected"
)

var (
	ErrUnknownTask    = errors.New("unknown task")
	ErrExpired        = errors.New("task expired")
	ErrAlreadyClaimed = errors.New("task already claimed")
	ErrNotYourClaim   = errors.New("task is not claimed by this solver")
	ErrHoldingClaim   = errors.New("solver already holds a claim")
	ErrBadToken       = errors.New("session token does not match task")
)

// Created is what the Agent gets back from creating a Task.
type Created struct {
	ID            string
	SessionToken  string // shown once; only its hash is stored
	ClaimDeadline time.Time
}

// Lifecycle creates Tasks and drives their transitions and timers.
type Lifecycle struct {
	db     *sql.DB
	cfg    Config
	notify func(Event)

	// commitMu spans each change's transaction and its notify, so Events
	// are delivered in commit order. SQLite serializes the writes anyway.
	commitMu sync.Mutex

	mu       sync.Mutex
	closed   bool
	timers   map[string]*time.Timer
	inflight sync.WaitGroup
}

// New returns a Lifecycle that calls notify after every committed change.
// notify must not block or call back into the Lifecycle.
func New(db *sql.DB, cfg Config, notify func(Event)) *Lifecycle {
	return &Lifecycle{db: db, cfg: cfg, notify: notify, timers: map[string]*time.Timer{}}
}

// Create queues a new Pending Task. obstacle is the Agent's description of
// what the Solver is to clear, or empty.
func (l *Lifecycle) Create(ctx context.Context, pageURL, obstacle string) (Created, error) {
	now := time.Now()
	c := Created{
		ID:            secret.New("tsk_"),
		SessionToken:  secret.New("st_"),
		ClaimDeadline: now.Add(l.cfg.ClaimWindow),
	}
	_, err := l.commit(ctx, func(tx *sql.Tx) (*Event, error) {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tasks (id, page_url, obstacle, state, session_token_hash, created_at, claim_deadline)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			c.ID, pageURL, obstacle, Pending, secret.Hash(c.SessionToken), now.UnixMilli(), c.ClaimDeadline.UnixMilli()); err != nil {
			return nil, fmt.Errorf("insert task: %w", err)
		}
		return &Event{TaskID: c.ID, State: Pending, PageURL: pageURL, Obstacle: obstacle, CreatedAt: now}, nil
	})
	if err != nil {
		return Created{}, err
	}
	l.schedule(c.ID, time.Until(c.ClaimDeadline), l.Expire)
	return c, nil
}

// Claim gives a Pending Task to solver and starts its solve
// window. Only the first Claim succeeds; a Task is never requeued, so any
// later Claim returns ErrAlreadyClaimed. A Solver holds at most one Claim at
// a time: until their claimed Task ends, Claim returns ErrHoldingClaim.
func (l *Lifecycle) Claim(ctx context.Context, id, solver string) (solveDeadline time.Time, err error) {
	won, err := l.commit(ctx, func(tx *sql.Tx) (*Event, error) {
		// Measured from the moment the Claim is recorded.
		now := time.Now()
		solveDeadline = now.Add(l.cfg.SolveWindow)
		e := &Event{TaskID: id, State: Claimed, Solver: solver, SolveDeadline: solveDeadline}
		var created int64
		// The claim deadline is checked here too, in case the Expire timer runs late.
		err := tx.QueryRowContext(ctx,
			`UPDATE tasks SET state = ?1, solver = ?2, solve_deadline = ?3
			 WHERE id = ?4 AND state = ?5 AND claim_deadline > ?6
			   AND NOT EXISTS (SELECT 1 FROM tasks WHERE solver = ?2 AND state = ?1)
			 RETURNING page_url, created_at`,
			Claimed, solver, solveDeadline.UnixMilli(), id, Pending, now.UnixMilli()).Scan(&e.PageURL, &created)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, claimRefusal(ctx, tx, id, now)
		}
		if err != nil {
			return nil, fmt.Errorf("claim: %w", err)
		}
		e.CreatedAt = time.UnixMilli(created)
		return e, nil
	})
	if err != nil || !won {
		return time.Time{}, err
	}
	l.schedule(id, time.Until(solveDeadline), l.failOverdue)
	return solveDeadline, nil
}

// claimRefusal explains why a Task could not be claimed at now.
func claimRefusal(ctx context.Context, tx *sql.Tx, id string, now time.Time) error {
	var state State
	var claimDeadline int64
	err := tx.QueryRowContext(ctx, `SELECT state, claim_deadline FROM tasks WHERE id = ?`, id).Scan(&state, &claimDeadline)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrUnknownTask
	case err != nil:
		return fmt.Errorf("claim: %w", err)
	case state == Expired:
		return ErrExpired
	case state != Pending:
		return ErrAlreadyClaimed
	case claimDeadline <= now.UnixMilli(): // about to Expire
		return ErrExpired
	}
	return ErrHoldingClaim // the Task is claimable, but the Solver is busy
}

// failOverdue Fails a claimed Task whose solve window has passed.
func (l *Lifecycle) failOverdue(ctx context.Context, id string) (bool, error) {
	return l.transition(ctx, id, Claimed, Failed, SolveWindowPassed, "")
}

// GiveUp Fails a Task at once for the Solver holding its Claim. It returns
// ErrNotYourClaim if solver does not hold a live Claim.
func (l *Lifecycle) GiveUp(ctx context.Context, id, solver string) error {
	won, err := l.transition(ctx, id, Claimed, Failed, GaveUp, solver)
	if err == nil && !won {
		return ErrNotYourClaim
	}
	return err
}

// Expire moves a Pending Task to Expired.
// It reports whether this call recorded the outcome.
func (l *Lifecycle) Expire(ctx context.Context, id string) (bool, error) {
	return l.transition(ctx, id, Pending, Expired, "", "")
}

// Solve records that the Bridge's cleared check passed on a claimed Task. It
// reports whether this call recorded the outcome: a Task that already ended,
// or was never claimed, is left as it is.
func (l *Lifecycle) Solve(ctx context.Context, id string) (bool, error) {
	return l.transition(ctx, id, Claimed, Solved, "", "")
}

// BridgeLost Fails a Pending or claimed Task whose Bridge disconnected, so no
// Solver claims or keeps working on a dead Session.
func (l *Lifecycle) BridgeLost(ctx context.Context, id string) (bool, error) {
	// Pending first: a Task never returns to Pending, so if it was claimed
	// in between, the second attempt still sees it.
	won, err := l.transition(ctx, id, Pending, Failed, BridgeDisconnected, "")
	if err != nil || won {
		return won, err
	}
	return l.transition(ctx, id, Claimed, Failed, BridgeDisconnected, "")
}

// Resume re-arms timers after a restart, ending overdue Tasks at once, and
// reports every Pending Task so the Queue is rebuilt.
func (l *Lifecycle) Resume(ctx context.Context) error {
	rows, err := l.db.QueryContext(ctx,
		`SELECT id, state, page_url, obstacle, created_at, claim_deadline, solve_deadline FROM tasks
		 WHERE state IN (?, ?) ORDER BY created_at`, Pending, Claimed)
	if err != nil {
		return fmt.Errorf("resume: %w", err)
	}
	type live struct {
		Event
		claimDeadline, solveDeadline int64
	}
	var ls []live
	for rows.Next() {
		var t live
		var created int64
		var solveDeadline sql.NullInt64
		if err := rows.Scan(&t.TaskID, &t.State, &t.PageURL, &t.Obstacle, &created, &t.claimDeadline, &solveDeadline); err != nil {
			rows.Close()
			return fmt.Errorf("resume: %w", err)
		}
		t.CreatedAt = time.UnixMilli(created)
		t.solveDeadline = solveDeadline.Int64
		ls = append(ls, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("resume: %w", err)
	}
	for _, t := range ls {
		if t.State == Claimed {
			l.schedule(t.TaskID, time.Until(time.UnixMilli(t.solveDeadline)), l.failOverdue)
			continue
		}
		l.commitMu.Lock()
		l.notify(t.Event)
		l.commitMu.Unlock()
		l.schedule(t.TaskID, time.Until(time.UnixMilli(t.claimDeadline)), l.Expire)
	}
	return nil
}

// Session checks that token is the Task's session token and returns where
// the Task stands, as an Event. It returns ErrBadToken for an unknown Task or
// a wrong token, so a token can never reach another Task's Session.
func (l *Lifecycle) Session(ctx context.Context, id, token string) (Event, error) {
	e := Event{TaskID: id}
	var created int64
	var solver sql.NullString
	var solveDeadline sql.NullInt64
	err := l.db.QueryRowContext(ctx,
		`SELECT state, page_url, created_at, solver, solve_deadline FROM tasks WHERE id = ? AND session_token_hash = ?`,
		id, secret.Hash(token)).Scan(&e.State, &e.PageURL, &created, &solver, &solveDeadline)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrBadToken
	}
	if err != nil {
		return Event{}, fmt.Errorf("session: %w", err)
	}
	e.CreatedAt = time.UnixMilli(created)
	e.Solver = solver.String
	if solveDeadline.Valid {
		e.SolveDeadline = time.UnixMilli(solveDeadline.Int64)
	}
	return e, nil
}

// ClaimOf returns the Task whose live Claim solver holds, as a Claimed Event,
// so a Solver who reconnects can resume its Session. ok is false if the
// Solver holds no Claim or its solve window has passed.
func (l *Lifecycle) ClaimOf(ctx context.Context, solver string) (e Event, ok bool, err error) {
	e = Event{State: Claimed, Solver: solver}
	var created, solveDeadline int64
	err = l.db.QueryRowContext(ctx,
		`SELECT id, page_url, obstacle, created_at, solve_deadline FROM tasks
		 WHERE solver = ? AND state = ? AND solve_deadline > ?`,
		solver, Claimed, time.Now().UnixMilli()).Scan(&e.TaskID, &e.PageURL, &e.Obstacle, &created, &solveDeadline)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, false, nil
	}
	if err != nil {
		return Event{}, false, fmt.Errorf("claim of: %w", err)
	}
	e.CreatedAt = time.UnixMilli(created)
	e.SolveDeadline = time.UnixMilli(solveDeadline)
	return e, true, nil
}

// Close stops all timers and waits for any running transition to finish.
func (l *Lifecycle) Close() {
	l.mu.Lock()
	l.closed = true
	for id, t := range l.timers {
		t.Stop()
		delete(l.timers, id)
	}
	l.mu.Unlock()
	l.inflight.Wait()
}

// transition moves a Task from one state to another only if it is still in
// `from` (and, when solver is set, claimed by solver). It reports whether it won.
func (l *Lifecycle) transition(ctx context.Context, id string, from, to State, reason Reason, solver string) (bool, error) {
	return l.commit(ctx, func(tx *sql.Tx) (*Event, error) {
		e := &Event{TaskID: id, State: to, Reason: reason}
		var created int64
		var claimant sql.NullString
		err := tx.QueryRowContext(ctx,
			`UPDATE tasks SET state = ?1, ended_at = ?2 WHERE id = ?3 AND state = ?4 AND (?5 = '' OR solver = ?5)
			 RETURNING page_url, created_at, solver`,
			to, time.Now().UnixMilli(), id, from, solver).Scan(&e.PageURL, &created, &claimant)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // another outcome was recorded first
		}
		if err != nil {
			return nil, fmt.Errorf("transition %s -> %s: %w", from, to, err)
		}
		e.CreatedAt = time.UnixMilli(created)
		e.Solver = claimant.String
		return e, nil
	})
}

// commit runs change in a transaction. If change returns an Event, the
// change counts as recorded: the Event is delivered after commit and commit
// reports true.
func (l *Lifecycle) commit(ctx context.Context, change func(*sql.Tx) (*Event, error)) (bool, error) {
	l.commitMu.Lock()
	defer l.commitMu.Unlock()
	var e *Event
	err := l.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		e, err = change(tx)
		return err
	})
	if err != nil {
		return false, err
	}
	if e != nil {
		l.notify(*e)
	}
	return e != nil, nil
}

// schedule runs fn for the Task after d, unless the Lifecycle is closed first.
// It replaces the Task's earlier timer, if any.
func (l *Lifecycle) schedule(id string, d time.Duration, fn func(context.Context, string) (bool, error)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	if old, ok := l.timers[id]; ok {
		old.Stop()
	}
	var t *time.Timer
	t = time.AfterFunc(d, func() {
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return
		}
		if l.timers[id] == t {
			delete(l.timers, id)
		}
		l.inflight.Add(1)
		l.mu.Unlock()
		defer l.inflight.Done()
		if _, err := fn(context.Background(), id); err != nil {
			log.Printf("task %s timer: %v", id, err)
		}
	})
	l.timers[id] = t
}

func (l *Lifecycle) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
