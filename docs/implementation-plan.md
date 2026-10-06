# Implementation plan

Status: draft, revised 2026-10-03. Nothing here is built.

**Out of date since 2026-10-05.** The code no longer has wallets, Customers, API keys or payments: see [Change of 2026-10-05](production-plan.md#change-of-2026-10-05-no-wallets-or-payments-in-the-code) in the production plan. The table under [Where the code stands](#where-the-code-stands) is corrected for that. The slices are not: C1 (the payments flag) no longer applies as written, choices 6 and 7 and slices A1, A2 and A6 assume wallets, Customers and API keys that are gone, and every slice that names `solver_wallet`, the Ledger, Deposits, Earnings or Withdrawals needs a second look before it becomes a ticket.

[production-plan.md](production-plan.md) records *what* was agreed for the public beta and why. This document says *how* to build it against the code as it stands at commit `961e5e1`: which tables, endpoints, messages and files change, in what order, and what blocks what. It is written to be split into tickets: every slice below has an id, is meant to land as one pull request with its tests, and names the slices that block it.

Terms follow [CONTEXT.md](../CONTEXT.md). Each slice updates the glossary for the terms it changes, as listed under "Glossary changes" in the production plan.

## Contents

- [Where the code stands](#where-the-code-stands)
- [Choices this plan makes](#choices-this-plan-makes)
- [Build order and milestones](#build-order-and-milestones)
- [Phase 0: Foundations](#phase-0-foundations) (F1–F5)
- [Phase 1: Product core](#phase-1-product-core) (C1–C9)
- [Phase 2: Accounts](#phase-2-accounts) (A1–A9)
- [Phase 3: Notification base](#phase-3-notification-base) (N1)
- [Phase 4: Team](#phase-4-team) (T1–T6)
- [Phase 5: Integration](#phase-5-integration) (I1–I14)
- [Phase 6: Boundary](#phase-6-boundary) (B1–B5)
- [Phase 7: Site](#phase-7-site) (S1–S7)
- [Phase 8: Operations](#phase-8-operations) (O1–O9)
- [Phase 9: Public notifications](#phase-9-public-notifications) (N2–N6)
- [Deferred until payments switch on](#deferred-until-payments-switch-on)
- [Still to check](#still-to-check)

## Where the code stands

Facts the slices below depend on.

| Area | Today | Consequence |
|---|---|---|
| Schema | Each package owns a `CREATE TABLE IF NOT EXISTS` string; [store.go](../internal/store/store.go) applies them; [task.go](../internal/task/task.go) adds columns by hand in `Migrate`. | The beta needs about twenty schema changes, several of them table rebuilds. A versioned migration runner comes first (F2). |
| Task states | `pending → claimed → solved / failed`, `pending → expired`. The Failed reason exists only on the in-memory `Event`; it is not stored. Nor is the time of the Claim. | History, stats and webhooks need both stored (C3). No new states are needed: a requeue is `claimed → pending`. |
| Claim window | One global `-claim-window` (30s on the server), set in `task.Config`. | Replaced by a per-Task `max_wait` (C2). |
| Money | None since 2026-10-05. There is no Ledger, no price and no payment code; a Task is free. | The beta is free without a flag. C1 has no Ledger to build the Shadow price on. |
| Solver identity | `GET /v1/queue?solver=…` with no proof. The id is 32 random hex characters that the Queue page makes up and keeps in the browser. It is the identity in `tasks.solver`, the Queue hub and the Session relay. | A2 replaces it with an account. Until then abuse control is per IP only. |
| Customer identity | None since 2026-10-05. There is no `customers` table and no API key: `POST /v1/tasks` takes no credentials, and a Task records no Customer. | A1 and A6 create the Customer and its keys instead of migrating them. Anyone can create Tasks until then. |
| Queue | The hub broadcasts every Pending Task to every connected Solver. | Team Tasks need a filter per Solver (T2). |
| Bridge | TypeScript only, written against Playwright's `Page` (`page.mouse`, `page.keyboard`, `newCDPSession`). Frames come from `Page.startScreencast` and cover the whole viewport. Input is checked for shape, not position. | The CDP core (I2) and the boundary (B2) both rewrite the frame and input paths. |
| Transport stats | `readBridge` logs relayed frame bytes per Session and discards them. | C9 stores them. |
| Web | One embedded page (the Queue) at `/`, plain HTML and JS. | A3 moves it to `/solver` and adds the other pages in the same style. |
| Ops | `deploy/deploy.sh` builds locally and installs a systemd unit on one EC2 host behind Cloudflare and k3s Traefik. No CI, no `/healthz`, no metrics, no backups. | Phase 0 adds CI and `/healthz`; Phase 8 adds the rest. |
| Repo | Public, no `LICENSE`. | F1. |

## Choices this plan makes

The production plan leaves these open. Each line is a choice made here so the slices are concrete. Review them before the tickets are cut; changing one only touches the slices named.

| # | Choice | Why | Slices |
|---|---|---|---|
| 1 | A Phase 0 comes first: license, migration runner, CI that runs the tests, `/healthz` and `/version`. Deploy automation and build provenance stay in Operations. | Every later slice adds a migration and tests. | F1–F4 |
| 2 | The CDP core of the TypeScript Bridge is built before the Python Bridge and before the Boundary. | Both are written against it. Built on Playwright's API they would each be rewritten. | I2 |
| 3 | The Queue gets `claim_left_ms`, not an absolute deadline. | A Solver's phone clock can be minutes off. The page already receives `waited_ms` and `solve_left_ms` this way. | C2 |
| 4 | When the solve window passes, the Task still Fails. Only give up and can't reach requeue. | The production plan names only those two. | C4 |
| 5 | If the Agent's cleared check passes while the Task is back in the Queue with no claimant, `solve()` resolves and the Task ends Failed with reason `cleared_without_solver`. | Today that report is ignored and the Bridge waits until the Task Expires. No Solver should get credit for it. | C4 |
| 6 | An account is one row per sign-in method and subject. It is a Solver from the start; a Customer is an optional second row pointing at it. Every wallet seen so far becomes an account with method `wallet`, which has no sign-in route. | Matches "every Customer is a Solver; a Solver signs up to become a Customer". Keeps today's data and the payments tests working. | A1, A2 |
| 7 | An existing Customer is attached to a signed-in account by presenting its API key once. | The author's Customer, Balance and Task history carry over without a wallet sign-in. | A1 |
| 8 | A guest Solver's key is a non-extractable WebCrypto Ed25519 key in IndexedDB, falling back to a JS key in `localStorage` where the browser lacks it. | A script injected into the page cannot read a non-extractable key. | A2 |
| 9 | When a guest signs in, the guest's Solver history moves to that account and the guest account is retired. | The page tells a guest to sign in "to keep your stats". This is the one exception to "accounts are never merged"; a guest is not a sign-in method. | A2 |
| 10 | One session is active per browser. The switcher lists the accounts linked to it; picking one starts a session for it with no second sign-in. | That is the "easy switch". Linking required signing in to both once. | A8 |
| 11 | Whether a Task counts as a Team Task for the quota and the stats is decided when it is Solved, by who held the Claim. A Team Task carries no Shadow price. | Under "Team first" it is not known at creation who will claim it. | T2, T6 |
| 12 | The Team quota counts Team-solved Tasks per calendar month in UTC. | Only a Solved Task is ever charged, so only a Solved Task should use quota. | T6 |
| 13 | Frames are sent as tiles: one `Page.captureScreenshot` with a `clip` per boundary rectangle, composed on a canvas by the Solver page. | No pixel outside a rectangle leaves the Customer's process, and neither Bridge needs an image library. B1 is a spike to confirm it holds 5–10 frames a second. | B1, B2 |
| 14 | A boundary is optional. Without one the Solver sees the whole viewport, as today, and the Queue labels the Task "full page". | Low friction in the beta. `/security` must say so. The alternative is to require it. | B2, S6 |
| 15 | A selector's box is measured again on every frame, so it follows its element but never grows past the element plus padding. A rectangle given in pixels is fixed, and wheel input is dropped for it. | "The box does not move" was agreed, but a fixed box over a page the Solver can scroll shows the rest of the page through the box. | B2 |
| 16 | Typed text and keys apply only while the focused element lies inside the boundary. | Otherwise a Solver can type into a field the Agent left focused outside the box, or Tab out of it. | B2 |
| 17 | The Python Bridge ships relay-only first. Direct WebRTC (`aiortc`) is a later slice. | `aiortc` brings native dependencies, and the relay works today. | I7 |
| 18 | A test Task's outcome is recorded by the server; the Bridge's `verify` does not run. | Nobody clears the page in test mode, so a real check would always say "not cleared". | I3 |
| 19 | An idempotency record keeps the whole create response, session token included, until the Task ends. | A retried create must return a token the Bridge can use; only its hash is stored otherwise. | I4 |
| 20 | The MCP server's `hire_human` tool returns after at most 50 seconds with the Task's status, and `check_human` waits again. | A Task can wait 15 minutes and then run a solve window. MCP clients time a tool call out long before that. | I13 |
| 21 | Docs are Markdown in `docs/`, rendered to HTML by the server at start and also served raw. | Coding agents read Markdown; `/llms.txt` links to it; GitHub shows the same files. | I11 |
| 22 | Staging is a second systemd unit on the same host at `unstuck-staging.yuchia.dev`. | No second instance to pay for. A name directly under `yuchia.dev` is covered by the existing wildcard DNS and certificate. | O7 |
| 23 | Secrets live in AWS Systems Manager Parameter Store (SecureString). | Free, and the instance role can read it. | O6 |
| 24 | Cloudflare's Bot Fight Mode stays **off**. | Every Agent is a bot calling `/v1` from a data center. On the Free plan Bot Fight Mode cannot be skipped for a path. This reverses one item of the first production plan. | O9 |
| 25 | Numbers not yet agreed: see the defaults in C6, C7, C8 and T1. | They are flags or settings, so they can change without code. | |

One item still waits for a yes or no: Microsoft sign-in for personal accounts (S5). It is in the plan as the last sign-in method and can be dropped without touching anything else.

## Build order and milestones

Agreed on 2026-10-03: Foundations, Product core, Accounts, Telegram, Team, Integration, Boundary, then the rest. The Team comes early because it removes the two things that block a first Customer: trusting strangers, and there being no Solvers yet.

| Milestone | Reached after | What it allows |
|---|---|---|
| M1: free core | Phases 0–1 | The author's own Agents use the hosted service without a Deposit. |
| M2: Team beta | Phases 2–4, plus O4 and O6 | Someone signs in, gets an API key, is pinged on Telegram when their Agent is stuck, and clears it themselves or with people they invited. This is the first version worth showing to a company. |
| M3: integrable | Phase 5 | Python and TypeScript packages, test keys, docs, the MCP server. |
| M4: open Queue | Phases 6–7, plus O9 | Public Solvers see only the boundary; the homepage, profiles and `/security` exist. This is the point to share it widely. |
| M5: unattended | Phases 8–9 | Alerts reach the author, and public Solvers are called in when work is waiting. |

## Phase 0: Foundations

### F1. Open source housekeeping

- Add `LICENSE` (Apache-2.0) and a license line in the README, `bridge/package.json` and `bridge/demo/jev/pyproject.toml`.
- Add `CONTRIBUTING.md` (how to run the Go and Bridge tests, the glossary rule) and `SECURITY.md` (how to report a vulnerability).
- `.DS_Store` is already ignored since the rename.

Blocked by: nothing.

### F2. Versioned migrations

- `store.Open` takes an ordered list of migrations and applies those above `PRAGMA user_version`, each in a transaction.
- Migration 1 is the current schema: the five `Schema` strings plus the columns `task.Migrate` adds. A database created by today's binary must pass through it unchanged.
- Add a helper for the SQLite table-rebuild pattern (create new, copy, drop, rename), needed in A1 and A2.
- Delete `task.Migrate` and the per-package `IF NOT EXISTS` once migration 1 covers them.

Done when: a copy of the production database and an empty file both reach the same schema, and a test proves it.

Blocked by: nothing.

### F3. CI runs the tests

- A GitHub Actions workflow on every push and pull request: `go vet`, `go test ./...`, and in `bridge/`: `npm ci`, `npm run typecheck`, `npm test` (with Playwright's Chromium installed).

Blocked by: nothing.

### F4. `/healthz` and `/version`

- `GET /healthz` returns 200 when the database answers a trivial read, else 503.
- `GET /version` returns the commit and build time, set with `-ldflags` in [deploy.sh](../deploy/deploy.sh).

Blocked by: nothing. Needed by O3 and O8.

### F5. Reserve the package names

- Create the `unstuck` organization on npm, which reserves the `@unstuck` scope without publishing anything.
- PyPI has no way to hold a name without a release: `unstuck` is claimed by the first real publish in I12. If it is taken by then, the Python package is `unstuck-bridge` and the import name stays `unstuck`.
- This is an account step for the repo owner, not a code change.

Blocked by: nothing. Do it now; the names were free on 2026-10-03.

## Phase 1: Product core

### C1. Payments flag and the Shadow price

- New flag `-payments` (default off) and `Config.Payments`.
- `ledger.EarningPercent` becomes 75. `-price` defaults to `400000` (0.40 USDC).
- Migration: `tasks.price INTEGER NOT NULL DEFAULT 0` and `tasks.charged INTEGER NOT NULL DEFAULT 0`. Backfill existing Tasks from `holds.amount` with `charged = 1`.
- Payments off:
  - `task.Create` stores the price with `charged = 0` and places no Hold, so there is no 402.
  - `Solve` records no capture; `Expire` and the Failed paths release nothing.
  - The Deposit poller and the payout worker do not start. The Withdrawal endpoints answer `503 payments_disabled`.
- Payments on: behaviour is as today, at the new split. All existing ledger, Deposit and Withdrawal tests keep running with the flag on.
- API replies:
  - `POST /v1/tasks` adds `price` and `charged`.
  - `GET /v1/balance` adds `payments_enabled`, `shadow_price` and `shadow_spent` (the sum of `price` over Solved, uncharged Tasks).
  - `GET /v1/solvers/{wallet}/earnings` adds `solved_count` and `shadow_earnings`.
  - The Queue's `task_solved` message carries the shadow Earning with `shadow: true`.
- Queue page: a line "Beta: free. Planned price: $0.40 per Task, you would earn $0.30." Earnings show 0.00 with a "beta" label next to the shadow total. The Withdraw button is hidden.

Done when: a Customer with zero Balance creates a Task on a server started without `-payments`, a Solver solves it, and both see the shadow amounts.

Blocked by: F2.

### C2. `max_wait` and the countdown

- `POST /v1/tasks` accepts `max_wait_seconds`: 60 to 900, default 300; out of range is `400 invalid_max_wait`.
- `task.Config.ClaimWindow` is replaced by `MinWait`, `MaxWait` and `DefaultWait` (flags `-min-wait`, `-max-wait`, `-default-wait`, so tests and demos can use seconds). `claim_deadline = created_at + max_wait`. Remove `-claim-window` from [main.go](../cmd/unstuck/main.go), [unstuck.service](../deploy/unstuck.service) and the README.
- `task.Event` and `queue.Task` carry the claim deadline. `task_added` sends `claim_left_ms` in place of `waited_ms`.
- Queue page: each Task counts down to zero; `tick()` updates the label. The server's `task_removed` stays the authority on when a Task leaves.
- Bridge: `solve(page, { maxWait })` in seconds.

Blocked by: F2.

### C3. Store the reason and the Claim time; one Claim per Solver in the schema

- Migration: `tasks.reason TEXT`, `tasks.claimed_at INTEGER`, and
  `CREATE UNIQUE INDEX one_claim_per_solver ON tasks(solver_wallet) WHERE state = 'claimed'`.
- `Claim` sets `claimed_at`. `transition` stores the reason. A unique-index violation in `Claim` maps to `ErrHoldingClaim`; the existing `NOT EXISTS` check stays as the readable path.

Blocked by: F2.

### C4. Give up returns the Task to the Queue

- Migration: `task_exclusions (task_id, solver_wallet, reason, until, created_at, PRIMARY KEY (task_id, solver_wallet))`. `until` is NULL for a permanent exclusion. These rows are also the record of give-up counts. Nothing else follows from giving up: there is no penalty for now.
- `Lifecycle.GiveUp`, in one transaction:
  - If `claim_deadline` has passed: `claimed → failed`, reason `gave_up`, as today.
  - Otherwise: `claimed → pending`, clear `solver_wallet`, `solve_deadline` and `claimed_at`, insert an exclusion until now + 30s, and re-arm the Expire timer for the time left.
- `Claim` refuses an excluded Solver with a new `ErrExcluded`; the Queue replies `claim_failed` with `error: "cooldown"` and `retry_in_ms`, or `error: "excluded"` when permanent.
- The requeue is a Pending `Event` marked as requeued, naming the Solver who let go.
  - Queue hub: broadcast `task_added` again; send that Solver `task_released` with `cooldown_ms`. The Queue page shows the Task disabled until the cooldown ends.
  - Session relay: forget the claimant and the peer token, keep the latest frame, and send the Bridge `{"type": "requeued"}`.
- Bridge, on `requeued`: close the direct peer, release a held button, drop the claim, keep the socket and the cleared poll running. A later `claimed` starts a new Session with a new peer token.
- Bridge, when the cleared check passes with no claimant: send `cleared`; the server ends the Task Failed with reason `cleared_without_solver`, and `solve()` resolves (choice 5).
- `BridgeLost` no longer assumes a Task never returns to Pending: it retries until one of its two transitions wins or the Task has ended.

Done when: a Task given up at minute 1 of a 5-minute `max_wait` reappears in every Queue with about 4 minutes left, the Solver who gave up is refused for 30s, a second Solver claims and solves it, and the Agent's `solve()` returns once.

Blocked by: C2, C3.

### C5. Can't reach

- Queue message `cant_reach` and `Lifecycle.CantReach`, in one transaction: insert a permanent exclusion with reason `cant_reach`, then
  - on the second such report for the Task: `claimed → failed`, reason `boundary_too_small`;
  - else if time is left: requeue as in C4;
  - else: `claimed → failed`, reason `cant_reach`.
- Reports do not count against the Solver (S4 leaves them out of reputation).
- Queue page: a "Can't reach" button beside "Give up", with one line saying when to use it.
- Bridge: a `TaskFailedError` with `boundary_too_small` tells the Customer to widen the boundary.

Blocked by: C4.

### C6. Admission

- New package `internal/admission`: a pure function from a supply snapshot to accept or reject, so the formula is unit-tested on its own.
  - `T` is the Task's `max_wait`.
  - `s` is the mean time from Claim to Solved over the last 50 Solved Tasks, or 90s while there are fewer than 5.
  - `idle` and `busy` come from the Queue hub (distinct connected Solvers) and the claimed Tasks. A busy term below zero counts as zero.
  - `notifiable` is supplied through an interface that returns zero until N4.
  - `pending` is the size of the Queue.
- `handleCreateTask` runs admission and the create under one lock, so two creates cannot both take the last slot.
- Rejection: `503` with `Retry-After` and `{"error": "no_solver_available", "reason": …, "retry_after_seconds": …}`. Reasons reachable now are `all_busy` (shortest time left on any Claim, between 30s and the solve window) and `none` (3600). The other two rows of the table arrive with T3 and N4.
- Bridge: `NoSolverAvailableError` with `reason` and `retryAfterSeconds`. The Agent decides whether to retry.
- Count accepted and rejected creates for C9.

Done when: with no Solver connected a create is rejected at once and nothing is written; with the author's Queue page open the same create is accepted.

Blocked by: C2.

### C7. Rate limits

- Add `golang.org/x/time/rate`. New package `internal/limit`: a limiter per key with idle entries evicted.
- Client IP: `CF-Connecting-IP` when `-trust-cloudflare` is set, else the socket's address. The flag is only safe behind the origin lock (O9).
- Defaults, all flags:

  | Limit | Default | Reply |
  |---|---|---|
  | Task creates per API key | 60 a minute, burst 10 | `429 rate_limited` |
  | Open Tasks (pending or claimed) per Customer | 10 | `429 too_many_open_tasks` |
  | Sign-in and challenge requests per IP | 10 a minute | `429 rate_limited` |
  | Queue socket connects per IP | 30 a minute | `429 rate_limited` |
  | Open Queue sockets per IP | 5 | `429 rate_limited` |
  | Claims per IP | 20 a minute | `claim_failed`, `rate_limited` |

- Every 429 carries `Retry-After` and `retry_after_seconds`.
- Bridge: `RateLimitedError`.

Blocked by: nothing.

### C8. Load shedding

- Caps, as flags: open WebSockets (2000) and Tasks in flight (500). Above either, creates and new sockets get `503 overloaded` with `Retry-After`.
- Track a moving average of commit time in `task.Lifecycle`; above 250 ms, Task creates get the same 503.

Blocked by: C7.

### C9. Measurement from day one

- Migration: `sessions (task_id, solver_wallet, started_at, ended_at, transport, p2p_at, relay_bytes_in, relay_bytes_out, p2p_bytes_in, p2p_bytes_out)`, one row per Claim.
  - The server fills the relay columns from what it already counts in `readBridge` and from the input it forwards.
  - The Bridge reports the direct path, which the server cannot see: a `stats` message when a peer becomes ready, when it closes, and at the end.
- Migration: `supply_snapshots (at, idle, busy, notifiable, pending, accepted, rejected)`, written every minute.
- Give-up and can't-reach counts by obstacle type need no table: they are `task_exclusions` joined to the Task's boundary label (B2).

Blocked by: C4 (a Task can have several Sessions), C6.

## Phase 2: Accounts

The model, as agreed:

- An **account** is one sign-in method plus who the person is there. GitHub-you and Google-you are two accounts and are never merged.
- Every account is a **Solver** from its first sign-in. It becomes a **Customer** when the person asks for an API key.
- A **guest Solver** has no sign-in: a key made silently in the browser. A guest can claim public Tasks only.
- Two accounts can be **linked** as a convenience, which puts both in a switcher.

### A1. Accounts and GitHub sign-in

- Migration: `accounts (id, method, subject, handle UNIQUE, display_name, email, created_at, retired_at, UNIQUE (method, subject))` and `auth_sessions (token_hash, account_id, expires_at, last_seen)`.
- Migration (table rebuild): `customers.account_id UNIQUE`, and `customers.wallet` becomes nullable. Each existing Customer gets an account with method `wallet` and its address as the subject (choice 6).
- New package `internal/account`. Sign-in routes share one shape: `/auth/{method}` and `/auth/{method}/callback`, with a `state` check. GitHub is the first method, using `golang.org/x/oauth2`. Client id and secret come from the environment.
- Cookie: `HttpOnly`, `Secure`, `SameSite=Lax`, 30 days, renewed on use. State-changing requests check `Origin`. `/auth/logout`.
- `auth` accepts an API key or a session whose account has a Customer, so the pages use the same `/v1` endpoints as an Agent.
- `POST /v1/customer`: the signed-in account becomes a Customer. `POST /v1/customer/attach` with an API key: an existing Customer is moved onto the signed-in account (choice 7).
- A new account gets a generated handle such as `quiet-otter-42`.
- The wallet registration endpoints and `cmd/unstuck-register` stay; they are how a wallet is attached when payments are on.

Blocked by: F2, O6 (the OAuth secret).

### A2. The Queue under an account, and guest Solvers

- Migration (table rebuild): `tasks.solver_wallet` becomes `tasks.solver_id`, an account id, and so do `task_exclusions` and `sessions`. Every wallet that ever held a Claim becomes a `wallet` account. The unique index moves to `solver_id`.
- `GET /v1/queue` authenticates by the session cookie; `?wallet=` is removed. The Queue hub and Session relay key on account id.
- Guest: `POST /v1/guest/challenge` and `POST /v1/guest/login`. The page signs a Sign-In With Solana style message (domain, nonce, issued-at) with its key (choice 8); the server verifies it as registration does today. The first login creates an account with method `guest` and requires a Cloudflare Turnstile token, checked server-side. Per-IP limit on new guests (C7).
- Queue page: with no session, become a guest with no prompt. Drop the wallet field and the demo wallet. Show "Sign in to keep your stats".
- When a guest signs in, their Solver history moves to the account and the guest account is retired (choice 9).
- `GET /v1/solvers/{wallet}/earnings` becomes `GET /v1/solver` for the signed-in account.
- With payments on, Earnings are still paid to a wallet. Until that is designed (see Deferred), the payments tests use `wallet` accounts created directly, and the Earning goes to that address.

Blocked by: A1, C7.

### A3. Site layout

- Move the Queue page to `/solver` (`internal/web/static/solver/`); old asset paths redirect. `/` becomes the homepage, first as a pitch with sign-in and links to `/solver`, `/dashboard` and the repo.
- One shared stylesheet and one shared header showing the signed-in account. `routeQueuePage` becomes a router over the page directories with the same cache rules.
- The page remembers, on that device, which method was used last, and offers it first.

Blocked by: nothing; do it with A1.

### A4. Privacy page

- `/privacy`: what an account stores (method, the provider's id, name, email if the provider gives one), what a Task stores, that frames are never stored, and how to have an account deleted.
- Google asks for a public privacy policy before its sign-in is published. This is not Terms of Service; there are still none.

Blocked by: A3.

### A5. Google sign-in

- Google as a second method, through OpenID Connect: the callback checks the ID token's signature, issuer, audience and nonce. Written so another OpenID Connect provider is configuration (S5).

Blocked by: A1, A4.

### A6. API keys

- Migration: `api_keys (id, customer_id, key_hash UNIQUE, mode, name, created_at, last_used_at, revoked_at)`; copy each `customers.api_key_hash` into it as a live key.
- New keys are `unstuck_live_…` or `unstuck_test_…` (test keys do nothing special until I3). Old keys keep working because lookup is by hash.
- `auth` resolves a key to Customer, key id and mode.
- `GET /v1/keys`, `POST /v1/keys`, `DELETE /v1/keys/{id}`. Rotation is create then revoke.

Blocked by: F2.

### A7. Dashboard, first version

- `/dashboard`: "Get an API key" makes the account a Customer; create, name and revoke keys (a new key is shown once); the last 20 Tasks with their outcome, from `GET /v1/balance`; the shadow cost.
- Plain HTML and JS in `internal/web/static/dashboard/`.

Blocked by: A1, A3, A6.

### A8. Telegram sign-in

- Telegram's login on the sign-in page, verified with the bot token's HMAC. It asks for permission for the bot to message the person, so the account's Telegram channel is verified in the same step (N1 uses it).
- Needs the bot from N1 and its domain set to the site.

Blocked by: A1, N1 (the bot).

### A9. Linked accounts and the switcher

- Migration: `account_links (account_a, account_b, created_at)`.
- While signed in, "Link another account" runs a second sign-in and joins the two. Unlink removes the row.
- The header lists the accounts linked to the current one; picking one starts a session for it (choice 10). The list shows each account's method and whether it is a Customer.
- A Task is **self-solved** when the Solver's account is the Customer's account or is linked to it.

Blocked by: A1.

## Phase 3: Notification base

### N1. Channels, time ranges and Telegram

- Package `internal/notify` with a `Notifier` interface: verify a destination, send, and report a permanent failure. A comment in the list of notifiers marks where WhatsApp would go.
- Migration: `notification_channels (id, account_id, kind, destination, verified_at, disabled_at, disabled_reason)`, `notification_windows (account_id, weekday, start_minute, end_minute)`, `accounts.timezone`, `accounts.notify_channel_id`, and `notifications (id, account_id, channel_id, task_id, sent_at, arrived_at)`.
- Telegram: a bot; the Solver opens `t.me/<bot>?start=<one-time token>`; a webhook endpoint with Telegram's secret header consumes it. A 403 from the bot API disables the channel.
- `/solver` settings: turn notifications on, pick one channel, set time ranges and timezone, send a test.
- No sending rule yet beyond the test message. T4 adds the Team rule; N4 the public one.

Blocked by: A2, O6.

## Phase 4: Team

A **Team** is a Customer's approved Solvers. A Customer has at most one. Its only member may be the Customer.

### T1. Team and invites

- Migration: `teams (id, customer_id UNIQUE, created_at)`, `team_members (team_id, account_id, joined_at, PRIMARY KEY (team_id, account_id))`, `team_invites (token_hash, team_id, created_at, expires_at, used_by, used_at)`.
- `/dashboard`, Team section: "Add me" puts the Customer's own account in; "Invite" makes a single-use link that expires after 7 days, for the Customer to send however they like; list members by handle and method; remove a member.
- Opening an invite link: sign in with any method (a guest is asked to sign in), then the account joins. A used or expired link says so.
- At most 20 members in the beta.
- Team members are not paid through Unstuck.

Blocked by: A7.

### T2. Visibility rules and Team Tasks

- Three rules:

  | Rule | Who can see and claim the Task |
  |---|---|
  | `team_only` | Team members. If none claims it within `max_wait`, it Expires. |
  | `team_first` | Team members alone for the first 90 seconds, then everyone. |
  | `public` | Everyone. |

- Migration: `customers.visibility` (default `public`) and `customers.team_first_seconds` (default 90); `tasks.visibility`, `tasks.team_id` and `tasks.public_at` (NULL for `team_only`, the creation time for `public`).
- `POST /v1/tasks` accepts `visibility` to override the Customer's default. A Team rule with an empty Team is `409 no_team`. `team_first_seconds` must be shorter than the Task's `max_wait`.
- Queue hub: each subscription knows its account's Teams, refreshed when a member joins or is removed. A Task is sent only to subscriptions that may claim it. At `public_at` a `team_first` Task is sent to everyone else.
- `Claim` checks the same rule in its `UPDATE`, so the hub filter is not the only guard.
- Queue page: a member sees their Team's Tasks above the public ones, marked with the Customer's name.
- At Solve, record whether the claimant was a Team member (`tasks.team_solved`). A Team-solved Task has no Shadow price (choice 11) and is left out of public stats and reputation.
- Bridge: `solve(page, { visibility })`.
- Dashboard: choose the default rule and the Team-first seconds.

Blocked by: T1, A2, C4.

### T3. Team admission

- `team_only`: accept if any member is connected or notifiable (N1: a verified channel and a time range covering now). Otherwise `503 no_solver_available` with reason `none_until_window` and the seconds until a member's next range opens, or `none` when no member has one.
- `team_first`: accept if the Team rule above passes or the public formula (C6) does.
- No capacity arithmetic for a Team; the cap on open Tasks per Customer (C7) bounds the demand.

Blocked by: T2, C6, N1.

### T4. Team notifications

- When a Team Task is created, notify every member who has a verified channel and whose time range covers now, unless they are connected to the Queue.
- No one-hour gap. At most one message per member per minute; Tasks created inside that minute are named in the next message.
- The message names the Customer, the obstacle sentence and the page's host, and links to `/solver?task=…`, which opens on that Task.
- The first Queue connection after a notification sets `arrived_at`.

Blocked by: T2, N1.

### T5. Onboarding choice

- Shown once when an account becomes a Customer: who may clear your Agent's obstacles?
  - **Only me.** Creates the Team with this account as its member, sets `team_only`, and asks the person to connect a notification channel.
  - **My Team.** The same, and shows the invite link.
  - **Anyone.** Sets `public`. No Team is created.
- The person can also choose to solve under a different account: link it (A9) and add that account to the Team instead.
- The choice can be changed later in the dashboard.

Blocked by: T1, T2, T4, A9.

### T6. Team quota

- Migration: `customers.team_quota INTEGER NOT NULL DEFAULT 30`.
- The dashboard shows Team-solved Tasks this calendar month against the quota (choice 12).
- Above the quota nothing is blocked. The dashboard shows "You have used your free Team Tasks this month. Please get in touch", linking to the contact section of the homepage.
- The operator is told once per Customer per month, by a Telegram message to a configured chat (`-operator-chat`).
- The quota is raised per Customer by hand, in the database.
- How Team Tasks are priced, per Task or by subscription, is not decided. This meter is the data for that decision.

Blocked by: T2, N1.

## Phase 5: Integration

### I1. Protocol reference

- `docs/protocol.md`: every message on the Bridge socket, the Queue socket and the direct channel, with the order they may arrive in. Source for the Python Bridge and for the OpenAPI spec. B2 updates it for tiles.

Blocked by: C4.

### I2. CDP core for the TypeScript Bridge

- Split [index.ts](../bridge/src/index.ts) into a core that needs only a CDP session (`send`, `on`, `detach`) and a Playwright adapter that supplies one.
- The core does input with `Input.dispatchMouseEvent`, `Input.dispatchKeyEvent` and `Input.insertText`, and follows navigation with `Page.frameNavigated`. The `cleared` and `verify` checks become plain `() => Promise<boolean>` supplied by the adapter.
- `solve(page, options)` keeps its signature. All Bridge tests pass unchanged.

Blocked by: nothing.

### I3. Test mode

- Migration: `tasks.mode` (`live` or `test`), set from the key.
- A test Task skips admission, never enters the Queue hub, notifies nobody, and is never charged or counted against a quota.
- `POST /v1/tasks` accepts `test_outcome` (`solved`, `failed`, `expired`; default `solved`) and `test_delay_ms` (default 3000) on test keys only. A bot claims the Task after the delay and the server records the outcome (choice 18). `expired` is recorded at the delay without a Claim.
- Every stats query filters on `mode = 'live'`.
- Bridge tests gain a run against a real backend with a test key.

Blocked by: A6, C6.

### I4. Idempotency

- `Idempotency-Key` header on `POST /v1/tasks`. Migration: `idempotency_keys (customer_id, key, task_id, response, created_at)`.
- The same key returns the stored `201` reply while the Task is live; the row is deleted when the Task ends or after 24 hours (choice 19). Rejections are not stored.
- Both Bridges send a key per `solve()` call and retry the create once on a network error.

Blocked by: F2.

### I5. Task history and stuck-rate fields

- `POST /v1/tasks` accepts optional `attempts_before_hire` and `steps_so_far`, and a `bridge` name and version (`bridge-ts/0.2`) that the Bridge fills in and the Customer can override or blank. Migration: three columns on `tasks`.
- `GET /v1/tasks` with `limit`, `before`, `state` and `mode`; `GET /v1/tasks/{id}`. Fields: state, reason, obstacle, boundary, visibility, created, claimed and ended times, time to Claim, time to solve, price, charged, Team-solved, the stuck-rate fields, transport.
- `GET /v1/stats`: the Customer's own counts by outcome and their average stuck-rate figures.
- `GET /v1/balance` keeps its short Task list for existing callers.

Blocked by: C3, A6.

### I6. Webhooks

- Migration: `webhooks (id, customer_id, url, secret, created_at, disabled_at)` and `webhook_deliveries`.
- `GET`, `POST`, `DELETE /v1/webhooks`. Events `task.solved`, `task.failed`, `task.expired`, fed from `task.Event`.
- Signed with HMAC-SHA256 in an `Unstuck-Signature` header carrying a timestamp. Up to 5 attempts with backoff. HTTPS only, and no private or loopback addresses.

Blocked by: I5.

### I7. Python Bridge with Playwright

- New `bridge-py/`, package `unstuck`: `await unstuck.solve(page, obstacle=…, max_wait=…, visibility=…)`.
- A port of the CDP core from I2, on `asyncio` with `websockets`; the CDP session comes from Playwright for Python. Relay only (choice 17).
- Same errors as TypeScript: `NoSolverAvailableError`, `TaskExpiredError`, `TaskFailedError`, `RateLimitedError`.
- Tests run against a real backend with a test key. Added to CI.
- Switch the Jev demo from the `jev-handoff.ts` subprocess to this package.

Blocked by: I1, I2, I3.

### I8. browser-use

- `unstuck.browser_use`: an `ask_human` action the model can call with an obstacle sentence, and a direct call. Attaches through browser-use's own CDP session.
- Check browser-use's current action-registration API when the slice starts; it has changed between releases.

Blocked by: I7.

### I9. Puppeteer and a bare CDP URL

- `solve(page)` accepts a Puppeteer page; `solveOverCDP({ cdpUrl, targetId })` covers remote browsers such as Browserbase. Both are thin adapters over I2. The Python package gets the same `solve_over_cdp`.

Blocked by: I2; I7 for Python.

### I10. Selenium

- `unstuck.selenium`: attaches through Selenium 4's CDP connection to Chrome.

Blocked by: I7.

### I11. Docs, OpenAPI and `/llms.txt`

- `docs/openapi.yaml` for `/v1`, served at `/v1/openapi.yaml`. A test fails if a route is missing from it.
- One page per framework in `docs/integrations/`: Playwright (TS), Stagehand, Playwright (Python), browser-use, Puppeteer, Selenium, CDP URL, and the MCP server. Each snippet is a file that CI runs with a test key.
- `/docs` renders the Markdown; `/docs/….md` serves it raw; `/llms.txt` lists the raw pages, the OpenAPI spec and the protocol reference (choice 21). Adds a Markdown renderer dependency.

Blocked by: I3, I5; each framework page by its wrapper.

### I12. Packages

- `@unstuck/bridge`: add a build that emits JavaScript and type declarations (today `main` points at a `.ts` file), `exports`, `files`, a README. `unstuck` on PyPI.
- A release workflow on a version tag, publishing with npm provenance and PyPI trusted publishing. `@unstuck/mcp` joins the same workflow once I13 lands.

Blocked by: F5, I7, I9.

### I13. MCP server

For an agent that has tools but no code of its own to change, such as a coding agent driving a browser through Playwright MCP.

- New package `mcp/`, published as `@unstuck/mcp` and run with `npx @unstuck/mcp` over stdio. It depends on `@unstuck/bridge` and reads `UNSTUCK_API_KEY` and `UNSTUCK_URL`.
- Tools:
  - `hire_human`: takes `cdp_url`, an optional `target_id` (default: the active tab), `obstacle`, and optional `max_wait_seconds`, `visibility`, `attempts_before_hire` and `steps_so_far`. It creates the Task through `solveOverCDP` (I9) and waits up to 50 seconds (choice 20). `boundary` is added in B2.
  - `check_human`: takes `task_id` and waits up to 50 seconds more.
  - Both return `status` (`waiting`, `claimed`, `solved`, `failed`, `expired`, `no_solver_available`) with `reason` and `retry_after_seconds` where they apply.
- The server keeps the Bridge Session alive between calls and ends it when the Task ends or the server exits.
- A `solved` Task here means the Solver tapped Done or a cleared check passed. The server cannot run the agent's own `verify`, so the tool's description tells the agent to look at the page itself before it carries on.
- The agent's browser must be reachable over CDP. The docs page shows Chrome started with a remote debugging port and Playwright MCP attached to the same one.
- Tests: an MCP client against a real backend with a test key.

Blocked by: I3, I9.

### I14. Agent skill

- `skills/unstuck/SKILL.md`: when to hire a human (an obstacle only a person can clear, after the agent's own attempts have failed) and when not to; how to write the obstacle sentence; which stuck-rate fields to pass; how to read each status and what to do after `no_solver_available`.
- It calls the tools from I13 and says how to add the MCP server to the agent's configuration.
- Linked from `/llms.txt` and from the docs page for coding agents.

Blocked by: I13.

## Phase 6: Boundary

The Boundary matters most for public Tasks, where the Solver is a stranger. A Team Task can use one too.

### B1. Spike: cropped capture

- Measure `Page.captureScreenshot` with `clip` against a full screencast on the reCAPTCHA demo: frames per second, CPU, bytes per second, for one rectangle and for two.
- Decide between tiles (choice 13) and cropping a screencast frame with an image library. Record the result in this document.

Blocked by: I2.

### B2. Boundary: crop and enforce

- Bridge option `boundary: { selectors?: string[], rects?: Rect[], padding?: number, popups?: boolean }`. Rectangles are in viewport CSS pixels.
- Frame message becomes `{ type: "frame", box: { width, height }, tiles: [{ x, y, width, height, data }] }` on both the relay and the direct channel. A screencast still runs, only as the signal that the page repainted.
- Input: positions are normalized to the box. The core maps them to the viewport and drops any pointer or wheel event outside every rectangle. A drag that leaves a rectangle is released at the last point inside. Wheel and keyboard rules follow choices 15 and 16.
- Replace `FrameMetadata` in [viewport.ts](../bridge/src/viewport.ts) with the box mapping; update its tests.
- Server: `POST /v1/tasks` accepts `boundary`, a label of at most 32 characters (a preset name, `custom`, or empty). Migration: `tasks.boundary`. `task_added` shows it. The server does not interpret it.
- Queue page: draw tiles on a canvas the size of the box; pointer math is unchanged because it is already normalized. A Task without a boundary is labelled "full page".
- MCP: `hire_human` gains `boundary`. The skill (I14) gains how to choose one.
- Tests: rectangle math and enforcement as unit tests; an end-to-end test where a click outside the box does not reach the page and no tile covers the area outside it.

Blocked by: B1.

### B3. Presets

- `boundary: "recaptcha" | "hcaptcha" | "turnstile" | "slider"`. A preset is a list of selectors, including ones that match nothing until the challenge opens (reCAPTCHA's `bframe`), plus a default `cleared` check where one is known.
- Fixture pages under `bridge/demo/` for each; `recaptcha` is also tested against Google's demo page by hand.

Blocked by: B2.

### B4. Popups

- On by default. For a short time after a pointer-up inside the boundary, an iframe or overlay that appears is added to the boundary, clamped to the viewport.
- This is a heuristic: build it against fixtures for a dropdown, a modal and a new iframe, and for a page that opens an unrelated overlay on a timer (which must not be added).
- Can't-reach counts by boundary label (C9) show whether it is good enough.

Blocked by: B2.

### B5. Boundary in the Python Bridge

- Port B2, B3 and B4 to `bridge-py`, with the same fixtures and one end-to-end test.

Blocked by: B2–B4, I7.

## Phase 7: Site

### S1. Dashboard, full

- The Task table from the history API with outcome, reason, visibility and time to solve; stuck stats; webhooks; test keys.

Blocked by: A7, I5, I6.

### S2. Solver page: stats and settings

- Solved count, shadow earnings, own pass rate and average time; edit the handle; the Withdrawal box stays, switched off.

Blocked by: A2, C1.

### S3. Hourly stats, homepage and `/status`

- Migration: `stats_cache (key, value, computed_at)`. A job at start and every hour computes: total Tasks, solve rate, median time to Claim, median time to solve, Customers with at least one counted Task. A Task is **counted** when it is live, not Team-solved and not self-solved. Solvers online is read live from the Queue hub.
- `GET /v1/public/stats` serves the cached copy from memory. The homepage shows it with the pitch, the security story, the top Solvers and a **contact section** (the address is the operator's to supply). `/status` shows uptime, version and the same numbers.

Blocked by: A3, A9, T2, I3.

### S4. Public Solver profiles

- `/solver/{handle}`: pass and fail rate and average time to solve over counted Tasks, shown once the Solver has 10 of them. Never the sign-in identity.
- Reputation counts Solved against Failed by solve window and by give up; it leaves out can't reach.

Blocked by: S3.

### S5. More sign-in methods

- **Email.** A sign-in link sent by mail: `email_tokens (token_hash, email, expires_at, used_at)`, single use, 15 minutes. Sending sits behind a `Mailer` interface; the first implementation is Gmail's SMTP server from a Gmail account made for Unstuck (`unstuck.beta@gmail.com` if it is free), display name "Unstuck", with the app password in the secrets store. Limits per address and per IP, and a Turnstile check on the form, so the form cannot be used to mail strangers or burn the 500-a-day limit. Bounces arrive in that inbox as ordinary mail.
- **Microsoft**, personal accounts only, as configuration of A5. Awaiting a yes or no.

Blocked by: A5, O6.

### S6. `/security`

- Plain language: what a Solver sees (the boundary, or the whole viewport when none is set), who can see a Team Task, what is stored (Task metadata) and what never is (frames), how long, and links to the lines of code that show it. Links to `/version` and `/privacy`.

Blocked by: B2, A3.

### S7. Guest and account housekeeping

- Delete an account on request (the path `/privacy` promises). Retire guest accounts with no Claim after 90 days.

Blocked by: A2.

## Phase 8: Operations

O4 (backups) and O6 (secrets) are needed by M2 and should be done early; O9 before M4.

### O1. Metrics endpoint

- Prometheus metrics on a separate listen address (`-metrics-addr`), not routed by the Ingress.
- Application: requests and latency, Tasks by outcome and reason, admission by result and reason, Queue depth, Solvers idle and busy, open sockets, Sessions by transport with duration and bytes, rate-limit and load-shed counts, notifications sent and failed.
- SQLite: database and WAL file size, commit latency, busy errors.

Blocked by: C6, C9.

### O2. Prometheus, Alertmanager and the Watchdog

- `deploy/monitoring/`: systemd units and configuration for Prometheus, node_exporter and Alertmanager on the host.
- Alerts: the process is down, traffic or rejections are high, the disk or the WAL is filling, commits are slow, busy errors, host CPU and memory.
- Alertmanager sends to Telegram. The always-firing Watchdog alert pings healthchecks.io.

Blocked by: O1, O6.

### O3. Host-down alarms to Slack

- One CloudFormation template in `deploy/aws/`: a CloudWatch alarm on `StatusCheckFailed`, a Route 53 health check on `/healthz` with its alarm, an SNS topic, and the chat integration to Slack.

Blocked by: F4.

### O4. Litestream

- Replicate `/var/lib/unstuck/unstuck.db` to S3 from a systemd unit, with an instance role limited to the bucket.
- Write the restore steps and run them once.

Blocked by: nothing. Do it before M2.

### O5. EBS snapshots

- A Data Lifecycle Manager policy: daily, kept 30 days. Added to the template from O3.

Blocked by: nothing.

### O6. Secrets

- Parameter Store entries for the OAuth client secrets, the Turnstile secret, bot tokens, the mail app password, VAPID keys and the TURN secret. An `ExecStartPre` step writes them to an environment file under `/run` (choice 23).

Blocked by: nothing. Needed by A1, A2, N1.

### O7. Staging

- `unstuck-staging.service` on the same host with its own database, port and Ingress host (choice 22). `deploy.sh` takes the environment as an argument. Staging needs its own OAuth apps and bot, because callbacks are tied to a host.

Blocked by: nothing.

### O8. Continuous delivery and build provenance

- On a merge to `main`: build, attest with `actions/attest-build-provenance`, deploy to staging. Production deploys on a version tag with a manual approval.
- The workflow reaches the host through an OIDC role and Systems Manager, so no SSH key is stored in GitHub.

Blocked by: F3, F4, O7.

### O9. Origin lock and edge rules

- Allow inbound 443 only from Cloudflare's published ranges (security group), then set `-trust-cloudflare`.
- One Cloudflare rate-limiting rule on `/v1/`. Bot Fight Mode stays off (choice 24).

Blocked by: C7. Do it before M4.

## Phase 9: Public notifications

### N2. Slack

- The Solver pastes an incoming-webhook URL; a code is posted to it and typed back. 404 or 410 disables it.

Blocked by: N1.

### N3. Web Push

- A service worker and a web app manifest on `/solver`; VAPID keys; the browser's permission grant is the verification; 404 or 410 from the push service disables it. On iOS the page must first be added to the Home Screen; the settings page says so.

Blocked by: N1.

### N4. Call public Solvers in when traffic is heavy

- `notifiable` is now real for public Tasks: Solvers whose time range covers now, with a verified channel, not sent a public notification in the last hour. Admission (C6) uses `notifiable × p`.
- When demand exceeds online capacity, on a Task create and on the minute tick, notify ⌈deficit / p⌉ notifiable Solvers, least recently notified first. At least one hour between public notifications to the same Solver; coming online does not reset it. Team notifications (T4) are outside this limit.
- The message says work is waiting and links to `/solver`. It names no Customer and no page.
- Admission gains the remaining rejection row for public Tasks: `notified_none_arrived` (300).

Blocked by: N1, C6.

### N5. Response rate

- `p` starts at 0.3 and is recalculated weekly from `notifications.arrived_at` over public notifications, stored in a small `settings` table.

Blocked by: N4.

### N6. Email and SMS channels, switched off

- Both implemented behind `Notifier` with tests against fake providers, and left out of the enabled list in configuration. Email reuses the `Mailer` from S5.

Blocked by: N1, S5.

## Deferred until payments switch on

Not tickets yet. Listed so nothing in the beta closes the door on them.

- How Team Tasks are priced: per Task above the free quota, a subscription, or both. T6 collects the data.
- Customer Withdrawal of unused Balance.
- Which wallet an account's Earnings are paid to, and linking a wallet to a Customer for Deposits.
- A Customer searching the platform's Solvers to invite them to a Team. It waits for payments, because a stranger expects to be paid, and for reputation, because there is nothing to search yet.
- A daily cap on the hot wallet's outflow; the hot wallet key in the secrets store.
- Fiat for Customers through Stripe.
- TURN, decided from the relay figures C9 collects.
- Mail from the product's own domain through Amazon SES, replacing Gmail behind the `Mailer` interface.

## Still to check

- Package names (F5). On 2026-10-03 nothing is published under `@unstuck` on npm or as `unstuck` on PyPI. Whether the `@unstuck` scope can still be registered is only known once the organization is created.
- Whether `unstuck.beta@gmail.com` can be registered (S5).
- Microsoft sign-in: yes or no (S5). If yes, how company accounts treat an unverified app.
- Google's current requirements for publishing a sign-in consent screen (A4, A5).
- Bot Fight Mode: Cloudflare's documentation says it cannot be skipped by a rule on the Free plan. Confirm it is off for the zone (O9).
- Whether a boundary should be required rather than optional (choice 14).
- WebCrypto Ed25519 on the phones Solvers use (choice 8); the fallback covers the gap.
- B1's result: tiles or an image library.
- MCP client time limits for one tool call, when I13 starts (choice 20).
- Cloudflare TURN pricing, when TURN is reconsidered.
- The response rate `p` (N5).
