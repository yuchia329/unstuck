package api_test

import (
	"crypto/ed25519"
	"net/http"
	"testing"
	"time"

	"github.com/yuchia329/unstuck/internal/api"
)

// freeHarness runs the backend with payments off.
func freeHarness(t *testing.T, mutate ...func(*api.Config)) *harness {
	t.Helper()
	return newHarness(t, append([]func(*api.Config){func(c *api.Config) { c.Payments = false }}, mutate...)...)
}

// createTaskAs creates a Task with no API key, naming the Customer by wallet.
func (h *harness) createTaskAs(address string) response {
	h.t.Helper()
	return h.do("POST", "/v1/tasks", "", map[string]any{
		"page_url": "https://www.google.com/recaptcha/api2/demo", "wallet": address,
	})
}

func TestWithPaymentsOffAWalletCreatesATaskWithoutAnAPIKeyOrBalance(t *testing.T) {
	h := freeHarness(t)

	res := h.createTaskAs(newWallet(t).address)

	if res.status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %v", res.status, res.body)
	}
	if id, _ := res.body["task_id"].(string); id == "" {
		t.Errorf("missing task_id: %v", res.body)
	}
	if tok, _ := res.body["session_token"].(string); tok == "" {
		t.Errorf("missing session_token: %v", res.body)
	}
}

func TestWithPaymentsOffAnAPIKeyCreatesATaskWithoutBalance(t *testing.T) {
	h := freeHarness(t)
	key := h.register()

	res := h.createTask(key)

	if res.status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %v", res.status, res.body)
	}
	if available, held := h.balance(key); available != 0 || held != 0 {
		t.Errorf("balance = %d/%d, want 0/0", available, held)
	}
}

func TestWithPaymentsOffATaskIsSolvedAndNoMoneyMoves(t *testing.T) {
	h := freeHarness(t, func(c *api.Config) { c.ClaimWindow, c.SolveWindow = time.Hour, time.Hour })
	key := h.register()
	h.credit(key, 15_000)
	created := h.createTask(key)
	id := created.body["task_id"].(string)
	b := h.connectBridge(created)
	s := h.connectSolver()
	s.mustClaim(id)

	b.send(map[string]any{"type": "solved"})

	b.next("solved")
	m := s.next("task_solved", id)
	if num(m["earning"]) != 0 || num(m["fee"]) != 0 {
		t.Errorf("task_solved = %v, want earning 0 and fee 0", m)
	}
	if got := h.taskState(key, id); got != "solved" {
		t.Errorf("state = %s, want solved", got)
	}
	if available, held := h.balance(key); available != 15_000 || held != 0 {
		t.Errorf("balance = %d/%d, want 15000/0", available, held)
	}
	res := h.do("GET", "/v1/solvers/"+s.wallet+"/earnings", "", nil)
	if num(res.body["available"]) != 0 {
		t.Errorf("earnings = %v, want 0 available", res.body)
	}
}

func TestWithPaymentsOffAnUnclaimedTaskStillExpires(t *testing.T) {
	h := freeHarness(t)
	key := h.register()
	id := h.createTask(key).body["task_id"].(string)

	h.eventually(time.Second, func() bool { return h.taskState(key, id) == "expired" }, "task expired")
}

func TestATaskCreatedByWalletBelongsToThatWalletsCustomer(t *testing.T) {
	h := freeHarness(t, func(c *api.Config) { c.ClaimWindow = time.Hour })
	w := newWallet(t)
	first := h.createTaskAs(w.address)
	second := h.createTaskAs(w.address)

	// Registering proves the wallet and issues the Customer's first usable API key.
	reg := h.registerAs(w)

	if reg.status != http.StatusOK {
		t.Fatalf("register: status %d, want 200 for a Customer that exists; body %v", reg.status, reg.body)
	}
	key := reg.body["api_key"].(string)
	for _, created := range []response{first, second} {
		if got := h.taskState(key, created.body["task_id"].(string)); got != "pending" {
			t.Errorf("state = %s, want pending", got)
		}
	}
}

func TestATaskCreatedByWalletJoinsARegisteredCustomer(t *testing.T) {
	h := freeHarness(t, func(c *api.Config) { c.ClaimWindow = time.Hour })
	w := newWallet(t)
	key := h.registerAs(w).body["api_key"].(string)

	id := h.createTaskAs(w.address).body["task_id"].(string)

	if got := h.taskState(key, id); got != "pending" {
		t.Errorf("state = %s, want pending", got)
	}
}

func TestWithPaymentsOffATaskNeedsAnAPIKeyOrAValidWallet(t *testing.T) {
	h := freeHarness(t)
	page := "https://www.google.com/recaptcha/api2/demo"

	if res := h.do("POST", "/v1/tasks", "", map[string]any{"page_url": page}); res.status != http.StatusUnauthorized {
		t.Errorf("no key, no wallet: status %d, want 401", res.status)
	}
	res := h.do("POST", "/v1/tasks", "", map[string]any{"page_url": page, "wallet": "not-a-wallet"})
	if res.status != http.StatusBadRequest || res.body["error"] != "invalid_wallet" {
		t.Errorf("bad wallet: %d %v, want 400 invalid_wallet", res.status, res.body)
	}
	// A wrong API key is refused even when the body names a wallet.
	res = h.do("POST", "/v1/tasks", "unstuck_nope", map[string]any{"page_url": page, "wallet": newWallet(t).address})
	if res.status != http.StatusUnauthorized {
		t.Errorf("wrong key: status %d, want 401", res.status)
	}
}

func TestWithPaymentsOnAWalletAloneCannotCreateATask(t *testing.T) {
	h := newHarness(t)
	w := newWallet(t)
	key := h.registerAs(w).body["api_key"].(string)
	h.credit(key, 10_000)

	res := h.createTaskAs(w.address)

	if res.status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body %v", res.status, res.body)
	}
	if available, held := h.balance(key); available != 10_000 || held != 0 {
		t.Errorf("balance = %d/%d, want 10000/0", available, held)
	}
}

func TestWithPaymentsOffTheBalanceStillNeedsAnAPIKey(t *testing.T) {
	h := freeHarness(t)
	w := newWallet(t)
	h.createTaskAs(w.address)

	res := h.do("GET", "/v1/balance?wallet="+w.address, "", nil)

	if res.status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", res.status)
	}
}

func TestWithPaymentsOffWithdrawalsAreRefusedEvenWithAPayoutKey(t *testing.T) {
	_, hot, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := freeHarness(t, func(c *api.Config) {
		c.PayoutKey, c.PayoutRPCURL = hot, "http://127.0.0.1:1"
		c.MinWithdrawal, c.AccountFee = 100_000, 400_000
	})

	res := h.do("POST", "/v1/withdrawals/challenge", "", map[string]any{"wallet": newWallet(t).address})

	if res.status != http.StatusServiceUnavailable || res.body["error"] != "withdrawals_disabled" {
		t.Errorf("challenge = %d %v, want 503 withdrawals_disabled", res.status, res.body)
	}
}

// A Task whose Hold was placed with payments on must still end, and give the
// Hold back, on a backend restarted with payments off.
func TestAHeldTaskEndsAfterPaymentsAreSwitchedOff(t *testing.T) {
	h := newHarness(t)
	key := h.register()
	h.credit(key, 10_000)
	id := h.createTask(key).body["task_id"].(string)

	h.cfg.Payments = false
	h.restart()

	h.eventually(time.Second, func() bool { return h.taskState(key, id) == "expired" }, "task expired")
	if available, held := h.balance(key); available != 10_000 || held != 0 {
		t.Errorf("balance = %d/%d, want 10000/0", available, held)
	}
}

// A Task created with payments off has no Hold; it must still end on a
// backend restarted with payments on.
func TestAFreeTaskEndsAfterPaymentsAreSwitchedOn(t *testing.T) {
	h := freeHarness(t)
	key := h.register()
	id := h.createTask(key).body["task_id"].(string)

	h.cfg.Payments = true
	h.restart()

	h.eventually(time.Second, func() bool { return h.taskState(key, id) == "expired" }, "task expired")
}
