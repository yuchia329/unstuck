package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yuchia329/unstuck/internal/api"
)

func TestQueuePageLoadsFromBackend(t *testing.T) {
	h := newHarness(t)

	for path, want := range map[string]string{"/": "<html", "/queue.js": "/v1/queue"} {
		res, err := http.Get(h.url + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.Contains(string(body), want) {
			t.Errorf("GET %s: status %d, body lacks %q", path, res.StatusCode, want)
		}
	}
}

// A proxy or browser must never pair a new index.html with an old queue.js.
func TestQueuePageIsNeverServedStale(t *testing.T) {
	h := newHarness(t)
	get := func(path string) string {
		t.Helper()
		res, err := http.Get(h.url + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d", path, res.StatusCode)
		}
		if got := res.Header.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s: Cache-Control = %q, want no-cache", path, got)
		}
		return string(body)
	}

	page := get("/")
	_, rest, ok := strings.Cut(page, `src="queue.js?v=`)
	if !ok {
		t.Fatalf("index.html does not load a versioned queue.js:\n%s", page)
	}
	version, _, _ := strings.Cut(rest, `"`)
	if version == "" {
		t.Fatal("queue.js version is empty")
	}
	if js := get("/queue.js?v=" + version); !strings.Contains(js, "/v1/queue") {
		t.Error("versioned queue.js is not the Queue page script")
	}
}

func TestQueueSocketRequiresASolverID(t *testing.T) {
	h := newHarness(t)

	// Too short to be unguessable, or not the characters an id is made of.
	for _, id := range []string{"", "short", "has%20spaces%20in%20the%20id"} {
		res, err := http.Get(h.url + "/v1/queue?solver=" + id)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("solver %q: status = %d, want 400", id, res.StatusCode)
		}
	}
}

func TestNewTaskAppearsOnEveryQueueWithoutRefresh(t *testing.T) {
	h := newHarness(t, func(c *api.Config) { c.ClaimWindow = time.Hour })
	a, b := h.connectSolver(), h.connectSolver()

	id := h.createTask().body["task_id"].(string)

	for _, s := range []*solver{a, b} {
		m := s.next("task_added", id)
		if m["page_url"] != "https://www.google.com/recaptcha/api2/demo" {
			t.Errorf("page_url = %v", m["page_url"])
		}
		if _, ok := m["waited_ms"].(float64); !ok {
			t.Errorf("missing waited_ms: %v", m)
		}
	}
}

func TestQueueShowsTasksAlreadyWaitingWhenSolverConnects(t *testing.T) {
	h := newHarness(t, func(c *api.Config) { c.ClaimWindow = time.Hour })
	id := h.createTask().body["task_id"].(string)
	time.Sleep(20 * time.Millisecond)

	m := h.connectSolver().next("task_added", id)

	if waited := num(m["waited_ms"]); waited < 20 {
		t.Errorf("waited_ms = %d, want >= 20", waited)
	}
}

func TestExpiredTaskIsRemovedFromEveryQueue(t *testing.T) {
	h := newHarness(t)
	a, b := h.connectSolver(), h.connectSolver()

	id := h.createTask().body["task_id"].(string)

	for _, s := range []*solver{a, b} {
		s.next("task_added", id)
		s.next("task_removed", id)
	}
	if got := h.taskState(id); got != "expired" {
		t.Errorf("state = %s, want expired", got)
	}
}

func TestClaimedTaskIsRemovedFromEveryQueue(t *testing.T) {
	h := newHarness(t, func(c *api.Config) { c.ClaimWindow, c.SolveWindow = time.Hour, time.Hour })
	a, b := h.connectSolver(), h.connectSolver()
	id := h.createTask().body["task_id"].(string)
	a.next("task_added", id)
	b.next("task_added", id)

	reply := a.claim(id)

	if reply["type"] != "claimed" {
		t.Fatalf("claim reply = %v, want claimed", reply)
	}
	if got := num(reply["solve_window_ms"]); got != time.Hour.Milliseconds() {
		t.Errorf("solve_window_ms = %d, want %d", got, time.Hour.Milliseconds())
	}
	a.next("task_removed", id)
	b.next("task_removed", id)
	if got := h.taskState(id); got != "claimed" {
		t.Errorf("state = %s, want claimed", got)
	}
}

func TestOnlyFirstOfConcurrentClaimsWins(t *testing.T) {
	h := newHarness(t, func(c *api.Config) { c.ClaimWindow, c.SolveWindow = time.Hour, time.Hour })
	solvers := make([]*solver, 5)
	for i := range solvers {
		solvers[i] = h.connectSolver()
	}
	id := h.createTask().body["task_id"].(string)
	for _, s := range solvers {
		s.next("task_added", id)
	}

	ids := make([]string, len(solvers))
	for i := range ids {
		ids[i] = id
	}

	won, lost := 0, 0
	for _, r := range claimAll(t, solvers, ids) {
		switch {
		case r["type"] == "claimed":
			won++
		case r["type"] == "claim_failed" && r["error"] == "already_claimed":
			lost++
		default:
			t.Errorf("unexpected reply %v", r)
		}
	}
	if won != 1 || lost != len(solvers)-1 {
		t.Errorf("won/lost = %d/%d, want 1/%d", won, lost, len(solvers)-1)
	}
}

func TestClaimRefusesExpiredAndUnknownTasks(t *testing.T) {
	h := newHarness(t)
	s := h.connectSolver()
	id := h.createTask().body["task_id"].(string)
	s.next("task_removed", id)

	if reply := s.claim(id); reply["type"] != "claim_failed" || reply["error"] != "expired" {
		t.Errorf("claim reply = %v, want claim_failed expired", reply)
	}
	if reply := s.claim("tsk_nope"); reply["type"] != "claim_failed" || reply["error"] != "unknown_task" {
		t.Errorf("claim reply = %v, want claim_failed unknown_task", reply)
	}
}

func TestClaimStopsTheClaimWindow(t *testing.T) {
	h := newHarness(t, func(c *api.Config) { c.ClaimWindow, c.SolveWindow = 300*time.Millisecond, time.Hour })
	s := h.connectSolver()
	id := h.createTask().body["task_id"].(string)
	s.mustClaim(id)

	time.Sleep(400 * time.Millisecond) // past the claim window

	if got := h.taskState(id); got != "claimed" {
		t.Errorf("state = %s, want claimed", got)
	}
}

func TestClaimedTaskFailsAfterSolveWindow(t *testing.T) {
	h := newHarness(t, func(c *api.Config) { c.ClaimWindow = time.Hour })
	s := h.connectSolver()
	id := h.createTask().body["task_id"].(string)
	s.mustClaim(id)

	m := s.next("task_failed", id)

	if m["reason"] != "solve_window" {
		t.Errorf("reason = %v, want solve_window", m["reason"])
	}
	if got := h.taskState(id); got != "failed" {
		t.Errorf("state = %s, want failed", got)
	}
}

func TestGivingUpFailsTask(t *testing.T) {
	h := newHarness(t, func(c *api.Config) { c.ClaimWindow, c.SolveWindow = time.Hour, time.Hour })
	s := h.connectSolver()
	id := h.createTask().body["task_id"].(string)
	s.mustClaim(id)

	s.send(map[string]any{"type": "give_up", "task_id": id})

	if m := s.next("task_failed", id); m["reason"] != "gave_up" {
		t.Errorf("reason = %v, want gave_up", m["reason"])
	}
	if got := h.taskState(id); got != "failed" {
		t.Errorf("state = %s, want failed", got)
	}
}

func TestOnlyTheClaimingSolverCanGiveUp(t *testing.T) {
	h := newHarness(t, func(c *api.Config) { c.ClaimWindow, c.SolveWindow = time.Hour, time.Hour })
	claimer, other := h.connectSolver(), h.connectSolver()
	id := h.createTask().body["task_id"].(string)
	claimer.mustClaim(id)

	other.send(map[string]any{"type": "give_up", "task_id": id})

	if m := other.next("error", id); m["error"] != "not_your_claim" {
		t.Errorf("error = %v, want not_your_claim", m["error"])
	}
	if got := h.taskState(id); got != "claimed" {
		t.Errorf("state = %s, want claimed", got)
	}
}

func TestFailedTaskIsNeverRequeued(t *testing.T) {
	h := newHarness(t, func(c *api.Config) { c.ClaimWindow, c.SolveWindow = time.Hour, time.Hour })
	claimer := h.connectSolver()
	id := h.createTask().body["task_id"].(string)
	claimer.mustClaim(id)
	claimer.send(map[string]any{"type": "give_up", "task_id": id})
	claimer.next("task_failed", id)

	late := h.connectSolver()

	late.never("task_added", id, 100*time.Millisecond)
	if reply := late.claim(id); reply["type"] != "claim_failed" || reply["error"] != "already_claimed" {
		t.Errorf("claim reply = %v, want claim_failed already_claimed", reply)
	}
	if got := h.taskState(id); got != "failed" {
		t.Errorf("state = %s, want failed", got)
	}
}

func TestPendingTaskIsStillQueuedAfterRestart(t *testing.T) {
	h := newHarness(t, func(c *api.Config) { c.ClaimWindow = time.Hour })
	id := h.createTask().body["task_id"].(string)

	h.restart()

	h.connectSolver().next("task_added", id)
}

func TestClaimedTaskOverdueAcrossRestartFailsOnStartup(t *testing.T) {
	// Long enough that the first restart always beats the solve timer.
	h := newHarness(t, func(c *api.Config) { c.ClaimWindow, c.SolveWindow = time.Hour, 500*time.Millisecond })
	id := h.createTask().body["task_id"].(string)
	h.connectSolver().mustClaim(id)

	h.restart() // stops the solve timer before it fires
	if got := h.taskState(id); got != "claimed" {
		t.Fatalf("state after first restart = %s, want claimed", got)
	}
	time.Sleep(600 * time.Millisecond)
	h.restart()

	h.eventually(time.Second, func() bool { return h.taskState(id) == "failed" }, "task failed after restart")
}

func TestSolverHoldsOneClaimUntilItsTaskEnds(t *testing.T) {
	h := newHarness(t, func(c *api.Config) { c.ClaimWindow, c.SolveWindow = time.Hour, time.Hour })
	s := h.connectSolver()
	first := h.createTask().body["task_id"].(string)
	second := h.createTask().body["task_id"].(string)
	s.mustClaim(first)

	if reply := s.claim(second); reply["type"] != "claim_failed" || reply["error"] != "holding_claim" {
		t.Fatalf("second claim reply = %v, want claim_failed holding_claim", reply)
	}
	s.send(map[string]any{"type": "give_up", "task_id": first})
	s.next("task_failed", first)

	s.mustClaim(second)
}

func TestSolverOnTwoConnectionsWinsAtMostOneClaim(t *testing.T) {
	h := newHarness(t, func(c *api.Config) { c.ClaimWindow, c.SolveWindow = time.Hour, time.Hour })
	id := newSolverID()
	a, b := h.connectSolverAs(id), h.connectSolverAs(id)
	ids := []string{h.createTask().body["task_id"].(string), h.createTask().body["task_id"].(string)}

	won := 0
	for _, r := range claimAll(t, []*solver{a, b}, ids) {
		if r["type"] == "claimed" {
			won++
		} else if r["error"] != "holding_claim" {
			t.Errorf("unexpected reply %v", r)
		}
	}
	if won != 1 {
		t.Errorf("won = %d, want 1", won)
	}
}
