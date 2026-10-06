package api_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/yuchia329/unstuck/internal/api"
)

// newSolverID makes up a Solver id, as the Queue page does on a first visit.
func newSolverID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// harness runs the real backend on an httptest server with short windows.
type harness struct {
	t    *testing.T
	cfg  api.Config
	url  string
	stop func()
}

func newHarness(t *testing.T, mutate ...func(*api.Config)) *harness {
	t.Helper()
	cfg := api.Config{
		DBPath:      filepath.Join(t.TempDir(), "unstuck.db"),
		ClaimWindow: 100 * time.Millisecond,
		SolveWindow: 100 * time.Millisecond,
	}
	for _, m := range mutate {
		m(&cfg)
	}
	h := &harness{t: t, cfg: cfg}
	h.start()
	t.Cleanup(func() { h.stop() })
	return h
}

func (h *harness) start() {
	h.t.Helper()
	srv, err := api.New(h.cfg)
	if err != nil {
		h.t.Fatalf("api.New: %v", err)
	}
	ts := httptest.NewServer(srv)
	h.url = ts.URL
	h.stop = func() {
		ts.Close()
		srv.Close()
	}
}

// restart stops the backend and starts a new one on the same database.
func (h *harness) restart() {
	h.t.Helper()
	h.stop()
	h.start()
}

type response struct {
	status int
	body   map[string]any
}

// do sends a JSON request.
func (h *harness) do(method, path string, body any) response {
	h.t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.url+path, r)
	if err != nil {
		h.t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	out := response{status: res.StatusCode}
	raw, _ := io.ReadAll(res.Body)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out.body)
	}
	return out
}

// taskState reads a Task's state from the database: once a Task has left the
// Queue, no endpoint reports it.
func (h *harness) taskState(taskID string) string {
	h.t.Helper()
	db, err := sql.Open("sqlite", "file:"+h.cfg.DBPath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		h.t.Fatal(err)
	}
	defer db.Close()
	var state string
	if err := db.QueryRow(`SELECT state FROM tasks WHERE id = ?`, taskID).Scan(&state); err != nil {
		h.t.Fatalf("state of task %s: %v", taskID, err)
	}
	return state
}

func (h *harness) createTask() response {
	h.t.Helper()
	return h.do("POST", "/v1/tasks", map[string]any{"page_url": "https://www.google.com/recaptcha/api2/demo"})
}

// eventually polls cond until it holds or the deadline passes.
func (h *harness) eventually(within time.Duration, cond func() bool, msg string) {
	h.t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("condition not met within %v: %s", within, msg)
}

func num(v any) int64 {
	f, _ := v.(float64)
	return int64(f)
}

// solver is a Solver's connection to the Queue socket.
type solver struct {
	t       *testing.T
	id      string
	conn    *websocket.Conn
	msgs    chan map[string]any
	backlog []map[string]any // received but not yet awaited
}

// connectSolver opens the Queue socket as a Solver with a fresh id.
func (h *harness) connectSolver() *solver {
	h.t.Helper()
	return h.connectSolverAs(newSolverID())
}

func (h *harness) connectSolverAs(id string) *solver {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(h.url, "http")+"/v1/queue?solver="+id, nil)
	if err != nil {
		h.t.Fatalf("dial queue: %v", err)
	}
	s := &solver{t: h.t, id: id, conn: conn, msgs: make(chan map[string]any, 64)}
	go func() {
		defer close(s.msgs)
		for {
			var m map[string]any
			if err := wsjson.Read(context.Background(), conn, &m); err != nil {
				return
			}
			s.msgs <- m
		}
	}()
	h.t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return s
}

func (s *solver) send(msg map[string]any) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, s.conn, msg); err != nil {
		s.t.Fatalf("send %v: %v", msg, err)
	}
}

// next waits for the next message of type typ for taskID.
func (s *solver) next(typ, taskID string) map[string]any {
	s.t.Helper()
	m, ok := s.await(time.Second, func(m map[string]any) bool { return m["type"] == typ && m["task_id"] == taskID })
	if !ok {
		s.t.Fatalf("no %s message for task %s within 1s", typ, taskID)
	}
	return m
}

// never asserts no message of type typ for taskID arrives within d.
func (s *solver) never(typ, taskID string, d time.Duration) {
	s.t.Helper()
	if m, ok := s.await(d, func(m map[string]any) bool { return m["type"] == typ && m["task_id"] == taskID }); ok {
		s.t.Fatalf("unexpected %s message for task %s: %v", typ, taskID, m)
	}
}

// claim sends a Claim and returns the reply: "claimed" or "claim_failed".
func (s *solver) claim(taskID string) map[string]any {
	s.t.Helper()
	s.send(map[string]any{"type": "claim", "task_id": taskID})
	return s.claimReply(taskID)
}

func (s *solver) claimReply(taskID string) map[string]any {
	s.t.Helper()
	m, ok := s.await(time.Second, func(m map[string]any) bool {
		return (m["type"] == "claimed" || m["type"] == "claim_failed") && m["task_id"] == taskID
	})
	if !ok {
		s.t.Fatalf("no claim reply for task %s within 1s", taskID)
	}
	return m
}

// claimAll sends solvers[i]'s Claim on taskIDs[i] all at once and returns
// the replies in the same order.
func claimAll(t *testing.T, solvers []*solver, taskIDs []string) []map[string]any {
	t.Helper()
	errs := make(chan error, len(solvers))
	var wg sync.WaitGroup
	for i, s := range solvers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			errs <- wsjson.Write(ctx, s.conn, map[string]any{"type": "claim", "task_id": taskIDs[i]})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("send claim: %v", err)
		}
	}
	replies := make([]map[string]any, len(solvers))
	for i, s := range solvers {
		replies[i] = s.claimReply(taskIDs[i])
	}
	return replies
}

// mustClaim claims taskID and fails the test unless the Claim wins.
func (s *solver) mustClaim(taskID string) {
	s.t.Helper()
	if reply := s.claim(taskID); reply["type"] != "claimed" {
		s.t.Fatalf("claim %s: reply %v, want claimed", taskID, reply)
	}
}

// await returns the first message, already received or arriving within d,
// that matches. Messages it passes over stay available to later waits.
func (s *solver) await(d time.Duration, match func(map[string]any) bool) (map[string]any, bool) {
	return awaitMsg(&s.backlog, s.msgs, d, match)
}

func awaitMsg(backlog *[]map[string]any, msgs <-chan map[string]any, d time.Duration, match func(map[string]any) bool) (map[string]any, bool) {
	for i, m := range *backlog {
		if match(m) {
			*backlog = append((*backlog)[:i], (*backlog)[i+1:]...)
			return m, true
		}
	}
	timeout := time.After(d)
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				return nil, false
			}
			if match(m) {
				return m, true
			}
			*backlog = append(*backlog, m)
		case <-timeout:
			return nil, false
		}
	}
}

// bridge is a Bridge's connection to its Task's Session.
type bridge struct {
	t       *testing.T
	conn    *websocket.Conn
	msgs    chan map[string]any
	backlog []map[string]any
}

// dialBridge opens the Bridge socket for taskID with a session token and
// returns the HTTP status the upgrade got.
func (h *harness) dialBridge(taskID, token string) (*websocket.Conn, int) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, res, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(h.url, "http")+"/v1/tasks/"+taskID+"/bridge?token="+token, nil)
	if err != nil {
		if res == nil {
			h.t.Fatalf("dial bridge: %v", err)
		}
		return nil, res.StatusCode
	}
	h.t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return conn, http.StatusSwitchingProtocols
}

// connectBridge joins the Session of a Task returned by createTask.
func (h *harness) connectBridge(created response) *bridge {
	h.t.Helper()
	conn, status := h.dialBridge(created.body["task_id"].(string), created.body["session_token"].(string))
	if conn == nil {
		h.t.Fatalf("dial bridge: status %d", status)
	}
	conn.SetReadLimit(-1)
	b := &bridge{t: h.t, conn: conn, msgs: make(chan map[string]any, 64)}
	go func() {
		defer close(b.msgs)
		for {
			var m map[string]any
			if err := wsjson.Read(context.Background(), conn, &m); err != nil {
				return
			}
			b.msgs <- m
		}
	}()
	return b
}

func (b *bridge) send(msg map[string]any) {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, b.conn, msg); err != nil {
		b.t.Fatalf("bridge send %v: %v", msg, err)
	}
}

// next waits for the next message of type typ.
func (b *bridge) next(typ string) map[string]any {
	b.t.Helper()
	m, ok := awaitMsg(&b.backlog, b.msgs, time.Second, func(m map[string]any) bool { return m["type"] == typ })
	if !ok {
		b.t.Fatalf("bridge: no %s message within 1s", typ)
	}
	return m
}

// never asserts no message of type typ arrives within d.
func (b *bridge) never(typ string, d time.Duration) {
	b.t.Helper()
	if m, ok := awaitMsg(&b.backlog, b.msgs, d, func(m map[string]any) bool { return m["type"] == typ }); ok {
		b.t.Fatalf("bridge: unexpected %s message: %v", typ, m)
	}
}
