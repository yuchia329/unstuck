package api_test

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/yuchia329/unstuck/internal/api"
)

func TestCreateTaskNeedsNoKeyAndReturnsTaskIDSessionTokenAndDeadlines(t *testing.T) {
	h := newHarness(t)
	before := time.Now()

	res := h.createTask()

	if res.status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %v", res.status, res.body)
	}
	if id, _ := res.body["task_id"].(string); id == "" {
		t.Errorf("missing task_id: %v", res.body)
	}
	if tok, _ := res.body["session_token"].(string); tok == "" {
		t.Errorf("missing session_token: %v", res.body)
	}
	deadline, err := time.Parse(time.RFC3339Nano, res.body["claim_deadline"].(string))
	if err != nil {
		t.Fatalf("claim_deadline: %v", err)
	}
	if d := deadline.Sub(before); d < 90*time.Millisecond || d > time.Second {
		t.Errorf("claim_deadline is %v after request, want ~100ms", d)
	}
	if got := num(res.body["solve_window_ms"]); got != 100 {
		t.Errorf("solve_window_ms = %d, want 100", got)
	}
}

func TestEachTaskHasItsOwnSessionToken(t *testing.T) {
	h := newHarness(t)

	a, b := h.createTask(), h.createTask()

	if a.body["task_id"] == b.body["task_id"] || a.body["session_token"] == b.body["session_token"] {
		t.Errorf("tasks share id or token: %v %v", a.body, b.body)
	}
}

func TestUnclaimedTaskExpires(t *testing.T) {
	h := newHarness(t)
	id := h.createTask().body["task_id"].(string)

	h.eventually(time.Second, func() bool { return h.taskState(id) == "expired" }, "task expired")
}

func TestTaskStaysPendingWithinClaimWindow(t *testing.T) {
	h := newHarness(t, func(c *api.Config) { c.ClaimWindow = time.Hour })
	id := h.createTask().body["task_id"].(string)

	time.Sleep(200 * time.Millisecond)

	if got := h.taskState(id); got != "pending" {
		t.Errorf("state = %s, want pending", got)
	}
}

func TestTaskOverdueAcrossRestartExpiresOnStartup(t *testing.T) {
	// Long enough that the first restart always beats the claim timer.
	h := newHarness(t, func(c *api.Config) { c.ClaimWindow = 500 * time.Millisecond })
	id := h.createTask().body["task_id"].(string)

	h.restart() // stops the claim timer before it fires
	if got := h.taskState(id); got != "pending" {
		t.Fatalf("state after first restart = %s, want pending", got)
	}
	time.Sleep(600 * time.Millisecond)
	h.restart()

	h.eventually(time.Second, func() bool { return h.taskState(id) == "expired" }, "task expired after restart")
}

func TestCreateTaskRejectsMissingOrInvalidPageURL(t *testing.T) {
	h := newHarness(t)

	for _, pageURL := range []string{"", "not a url", "ftp://example.com/x", "/relative"} {
		res := h.do("POST", "/v1/tasks", map[string]any{"page_url": pageURL})
		if res.status != http.StatusBadRequest {
			t.Errorf("page_url %q: status = %d, want 400", pageURL, res.status)
		}
	}
}

func TestInvalidConfigIsRejectedAtStartup(t *testing.T) {
	bad := []func(*api.Config){
		func(c *api.Config) { c.ClaimWindow = 0 },
		func(c *api.Config) { c.SolveWindow = -time.Second },
		func(c *api.Config) { c.PingInterval = -time.Second },
		func(c *api.Config) { c.TURNURLs = []string{"turn:turn.example.com:3478"} }, // no secret
	}
	for i, mutate := range bad {
		cfg := api.Config{
			DBPath:      t.TempDir() + "/unstuck.db",
			ClaimWindow: time.Second,
			SolveWindow: time.Second,
		}
		mutate(&cfg)
		if srv, err := api.New(cfg); err == nil {
			srv.Close()
			t.Errorf("config %d: api.New succeeded, want error", i)
		}
	}
}

// walletEraSchema is the tasks table, and the customers table it pointed at,
// as the backend made them when a Task belonged to a registered Customer and
// its Solver was a wallet address.
const walletEraSchema = `
CREATE TABLE customers (
	id           TEXT PRIMARY KEY,
	wallet       TEXT NOT NULL UNIQUE,
	api_key_hash TEXT NOT NULL UNIQUE,
	available    INTEGER NOT NULL DEFAULT 0 CHECK (available >= 0),
	held         INTEGER NOT NULL DEFAULT 0 CHECK (held >= 0),
	created_at   INTEGER NOT NULL
);
CREATE TABLE tasks (
	id                 TEXT PRIMARY KEY,
	customer_id        TEXT NOT NULL REFERENCES customers(id),
	page_url           TEXT NOT NULL,
	obstacle           TEXT NOT NULL DEFAULT '',
	state              TEXT NOT NULL CHECK (state IN ('pending', 'claimed', 'solved', 'expired', 'failed')),
	session_token_hash TEXT NOT NULL UNIQUE,
	created_at         INTEGER NOT NULL,
	claim_deadline     INTEGER NOT NULL,
	solver_wallet      TEXT,
	solve_deadline     INTEGER,
	ended_at           INTEGER
);
CREATE INDEX tasks_customer ON tasks (customer_id, created_at);
INSERT INTO customers (id, wallet, api_key_hash, available, created_at) VALUES ('cus_1', 'WalletOfTheCustomer', 'hash', 980000, 1);
`

func TestBackendStartsOnADatabaseFromBeforeWalletsWereRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unstuck.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(walletEraSchema); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	const insert = `INSERT INTO tasks (id, customer_id, page_url, obstacle, state, session_token_hash, created_at, claim_deadline, solver_wallet, solve_deadline, ended_at)
		VALUES (?, 'cus_1', 'https://example.com/', ?, ?, ?, ?, ?, ?, ?, ?)`
	for _, row := range [][]any{
		{"tsk_waiting", "Pass the check.", "pending", "h1", now, now + time.Hour.Milliseconds(), nil, nil, nil},
		{"tsk_done", "", "solved", "h2", now - 5000, now - 4000, "WalletOfTheSolver", now - 3000, now - 3500},
	} {
		if _, err := db.Exec(insert, row...); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	h := newHarness(t, func(c *api.Config) { c.DBPath, c.ClaimWindow, c.SolveWindow = path, time.Hour, time.Hour })

	// The Task that was waiting is still queued, with its obstacle, and can be claimed.
	s := h.connectSolver()
	if m := s.next("task_added", "tsk_waiting"); m["obstacle"] != "Pass the check." {
		t.Errorf("task_added = %v, want the obstacle kept", m)
	}
	s.mustClaim("tsk_waiting")
	if got := h.taskState("tsk_done"); got != "solved" {
		t.Errorf("state of the old Solved Task = %s, want solved", got)
	}
	// New Tasks need no Customer.
	if res := h.createTask(); res.status != http.StatusCreated {
		t.Errorf("create after upgrade: status = %d, want 201; body %v", res.status, res.body)
	}
	// A second start finds nothing left to migrate.
	h.restart()
	if got := h.taskState("tsk_waiting"); got != "claimed" {
		t.Errorf("state after restart = %s, want claimed", got)
	}
}
