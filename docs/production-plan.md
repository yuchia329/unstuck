# Production plan

Status: agreed 2026-10-03. Nothing here is built yet unless it says so.

This records the decisions taken in moving Unstuck from a hackathon demo to a public beta, and the order to build them in. Terms follow [CONTEXT.md](../CONTEXT.md); new terms are listed under [Glossary changes](#glossary-changes).

## Direction

Unstuck is **human-in-the-loop for agents**: when a browser Agent dies or gets stuck on something only a human can clear, a Solver takes over the stuck part of the page and hands it back. It is not marketed as a CAPTCHA solver; reCAPTCHA is one obstacle among many.

The beta exists to answer one question: **is there a need?** Everything else (payments, legal, scale) waits until that is answered. The beta is free, open to anyone, and shared mostly with the author's own network. The first Solver is the author.

A Customer who does not want strangers on their Agent's page can keep Tasks inside a **Team** of Solvers they approved, which may be only themselves. For them Unstuck is the notification and the remote-control link between their own devices. See [Team](#team).

The code is open source. The moat is the Solver network and the ecosystem of integrations, not the code.

## Beta principles

- **Free, indefinitely.** No Deposits are required and no Solver is paid. There is no paid launch date; payments switch on when the market test says so.
- **Payment code stays, behind a flag.** Deposits, Holds, Earnings and Withdrawals keep working in code and tests, but are off in the beta. Dashboards show Earnings as 0.00 with a "beta" label.
- **Shadow price.** Both sides see what a Task would cost and earn: "Beta: free. Planned price: $0.40/Task."
- **Low friction over identity.** No wallet is needed, and a Solver can start as a guest with no sign-in; but every Solver has an identity (see [Identity and login](#identity-and-login)).

## Pricing

| | Amount |
|---|---|
| Shadow Price per Task | $0.40 |
| Solver share (75%) | $0.30 |
| Unstuck share (25%) | $0.10 |

The split moves from 80/20 to 75/25. Raising the Solver share later is goodwill; cutting it later is not.

Why $0.40 is defensible to a well-funded customer: a screenshot-driven agent step costs roughly $0.02–0.05 in model tokens, so a stuck loop of ~20 retries burns $0.50–1.00, a lost run that must restart costs more, and an engineer looking into it costs $15+. Unstuck's own cost per Task is near zero (relay bandwidth well under a cent, payouts amortized), so margin is close to the take rate.

At $0.40 the business is volume-driven. The beta therefore measures **how often each Customer gets stuck**, not just whether they would pay. Willingness to pay is tested in conversations (Van Westendorp: too cheap / bargain / expensive / too expensive) rather than inferred from shadow-price clicks. Real-time supply/demand pricing is a later option; the beta records the data it would need.

Production Customers will likely pay in fiat (Stripe) while Solvers are paid in USDC; Customer identity must therefore never require a wallet. This is a future decision, not beta scope.

**Team Tasks** are priced differently, because the Solver is the Customer's own person and Unstuck supplies only the infrastructure. In the beta each Customer has **30 free Team Tasks a month**. Passing 30 blocks nothing: the dashboard asks the Customer to get in touch, and the operator is told. Whether heavy use is later charged per Task or by subscription is undecided; the monthly counts are the data for it. A Team Task has no Shadow price, and Team members are not paid through Unstuck.

## Identity and login

- **An account is one sign-in method plus who the person is there.** Signing in with GitHub and signing in with Google give two accounts. They are never merged.
- **Sign-in methods:** GitHub, Google and Telegram at launch; email (a sign-in link) after that. Microsoft for personal accounts is under consideration. Facebook and LinkedIn are not offered.
- **Every account is a Solver** from its first sign-in. It becomes a **Customer** when the person asks for an API key in the dashboard.
- **Guest Solvers** need no sign-in: on first visit `/solver` makes an ed25519 key in the browser and signs each login challenge with it, with no prompt. A guest can claim public Tasks only, and is nudged to sign in to keep their stats; joining a Team or becoming a Customer requires signing in.
  - Abuse control: per-IP limits on new guests and Claims, and one Cloudflare Turnstile check when a guest is created.
- **Linked accounts** are a convenience for someone who wants to be a Customer under one sign-in and a Solver under another: while signed in to one, they sign in to the other, and the two then appear in a switcher, as a browser lists several mail accounts. Switching needs no second sign-in. Unlinking is always possible.
- The page remembers which method was used last on that device, so nobody lands in an empty second account by mistake.
- Sessions use a cookie lasting 30 days.
- No wallet is involved in signing in. A wallet is only an address for Deposits and Withdrawals, attached when payments switch on. Unstuck never generates or shows seed phrases.
- An account may claim its own Customer's Tasks (needed for testing). **Self-solved** Tasks, where the Solver's account is the Customer's or is linked to it, are left out of public reputation and public stats; so are Team-solved Tasks.
- A public **privacy page** says what an account stores. Google requires one before its sign-in can be published.
- Email sign-in links are sent from a Gmail account made for Unstuck, behind an interface that lets the sender change later.

## Team

A **Team** is the set of Solvers a Customer has approved. A Customer has at most one, of up to 20 members in the beta. Its only member may be the Customer.

- **Onboarding.** When an account becomes a Customer it is asked who may clear its Agent's obstacles: only me, my Team, or anyone. The first two create the Team with the Customer in it.
- **Invites** are single-use links that expire after 7 days. The Customer sends the link however they like; whoever opens it signs in and joins. The Customer can remove a member at any time.
- **Visibility rule**, a Customer setting that a Task can override:

  | Rule | Who can see and claim the Task |
  |---|---|
  | Team only | Team members. If none claims it within `max_wait`, it Expires. |
  | Team first | Team members alone for the first 90 seconds (adjustable), then everyone. |
  | Public | Everyone. |

- **Queue.** Every Solver sees public Tasks. A Team member sees their Team's Tasks above them.
- **Notification.** A Team Task notifies every member who has a verified channel and whose time range covers now. The one-hour gap does not apply; messages are collapsed to at most one a minute per member. The message names the Customer, the obstacle and the page's host, and opens `/solver` on that Task.
- **Admission.** A Team-only Task is accepted if any member is online or notifiable; otherwise it is rejected with the time a member's range next opens. A Team-first Task is accepted if that holds or the public formula does.

## Task lifecycle changes

### Waiting time

- Each Task has a **`max_wait`** set by the Customer: 1–15 minutes, default 5. It replaces the global claim window and runs on the wall clock from creation.
- The server sends the claim deadline as milliseconds; the Queue page counts down to it (instead of counting up).
- The solve window stays a separate setting.

### Admission

A Task is accepted only if a Solver can plausibly claim it before `max_wait` runs out. Otherwise it is rejected at once, before any Hold. The formula below is for public Tasks; a Team Task follows the simpler rule under [Team](#team).

```
T          = max_wait
s          = average time to solve (recent Solved Tasks)
idle       = Solvers on /solver not holding a Claim
busy       = Solvers holding a Claim
notifiable = Solvers whose notification time range covers now, with a verified channel,
             not notified in the last hour
p          = notification response rate (start 0.3, recalculated weekly)

capacity ≈ idle × (T / s) + busy × ((T − time left on their Claim) / s) + notifiable × p
demand   = pending Tasks + 1
accept if demand ≤ capacity
```

Solvers notified within the last hour who have not shown up count as zero.

Rejection is `503 no_solver_available` with a `Retry-After` header and a JSON body carrying `retry_after_seconds` and `reason`:

| Situation | `retry_after_seconds` |
|---|---|
| Solvers online but all busy | shortest time left on any Claim; at most the solve window, at least 30 |
| Solvers notified in the last hour, none arrived | 300 |
| No one online or notifiable, but a Solver's time range opens within 24 h | seconds until it opens |
| No one at all | 3600 |

These defaults become measured medians once there is data.

### Claim

- One Claim per Solver at a time, enforced by the database. Today's conditional `UPDATE` is already race-free on SQLite (one writer, one connection); add the index anyway so the rule survives a move to Postgres:

  ```sql
  CREATE UNIQUE INDEX one_claim_per_solver ON tasks(solver_wallet) WHERE state = 'claimed';
  ```

- No Redis. The lock lives in the same transaction as the Ledger.

### Give up and can't reach

- **Give up** returns the Task to the Queue with whatever `max_wait` is left. The Solver's Claim is dropped. That Solver cannot reclaim the same Task for 30 seconds. If no time is left, the Task Fails.
- **Can't reach** is a Solver button for "the boundary doesn't let me do this". The Task returns to the Queue, the reporting Solver is excluded from that Task permanently, and it does not count against their reputation. After **2** can't-reach reports the Task Fails with reason `boundary_too_small`, so the Customer learns fast.

This reverses the hackathon rule that a Task is never requeued.

## Boundary

The Customer declares where the Solver may act. The Solver sees only that region and can act only inside it.

- **Presets** for known obstacles: `recaptcha` (checkbox iframe plus the challenge iframe), `hcaptcha`, `turnstile`, `slider`.
- **Custom:** a list of CSS selectors or rectangles, with padding. The box does not move during a Session.
- **Popups:** on by default, an iframe or overlay that appears shortly after a click inside the box is added to the boundary (heuristic; needs tests). Lists or popups that open outside the box are the main risk of a too-strict boundary; can't reach is the escape hatch.
- **Enforced in the Bridge, not the server.** Direct P2P input never passes through the server ([input.ts](../bridge/src/input.ts)), so the Bridge drops any pointer event outside the boundary and crops frames to it before they leave the Customer's process. Cropping also cuts bandwidth by roughly 5–10×.

## Notifications

Optional. A Solver verifies a channel only if they turn notifications on.

| Tier | Channel | Verification | Turned off when |
|---|---|---|---|
| 1 | Telegram | deep link `t.me/<bot>?start=<one-time token>` | bot API returns 403 (blocked) |
| 1 | Slack | code sent to the incoming webhook, typed back | webhook returns 404/410 |
| 1 | Web Push | the browser's permission grant | push service returns 404/410 |
| 2 | Email | confirmation link | bounce |
| 2 | SMS | code by text | carrier failure |

- Tier 2 is built behind the same `Notifier` interface but disabled (budget; US SMS also needs a registered number).
- WhatsApp is not built; leave a comment in the `Notifier` list for it.
- No JWT and no expiry: a `verified_at` row per channel, re-verified only when the destination changes.
- Each Solver sets the time ranges (with timezone) when they may be notified, and picks one channel. Time ranges are respected for Team Tasks too.
- Signing in with Telegram verifies the Telegram channel in the same step.
- **When to notify, public Tasks:** only when traffic is heavy, meaning demand exceeds online capacity (the first two terms of the admission formula). Notify ⌈deficit / p⌉ notifiable Solvers. The message names no Customer and no page.
- **Gap, public Tasks:** at least 1 hour between notifications to the same Solver. It does not reset when the Solver comes online.
- **Team Tasks** notify every available member each time; see [Team](#team).

## Rate limiting and load protection

1. **Cloudflare edge** (already in front): DDoS protection and the free rate-limiting rule. Lock the origin to Cloudflare's IP ranges so the EC2 address can't be hit directly. Bot Fight Mode stays off: every Agent is a bot calling the API, and on the Free plan it cannot be skipped for a path.
2. **Application:** per API key, per account and per IP, using `golang.org/x/time/rate`. Real client IP from `CF-Connecting-IP` (trusted only because of the origin lock). A cap on open Tasks per Customer.
3. **Load shedding:** caps on concurrent WebSockets and Tasks in flight; fast 503 when SQLite write latency climbs.

Not used: Redis (single process), SQS (a queue is not a rate limiter, and Tasks are short-lived), AWS WAF (needs an ALB or CloudFront; Cloudflare covers it).

## Payments (off in beta)

- Solana only, USDC. Cheapest fees, already built. No Polygon or EVM.
- Withdrawals are available to Solvers and, for unused balance, Customers.
- The withdrawer pays the network cost: it is subtracted from the payout (fee plus token-account rent for a wallet without one, as `-account-fee` does today).
- No alerts or limits while nothing is charged. When payments switch on, add a daily cap on the hot wallet's total outflow (it limits damage from a bug or a leaked key; it is not a per-user limit) and move the hot wallet key to a secrets manager.

## Integration

### Bridges

One core Bridge that attaches over CDP, with thin wrappers per framework.

| Framework | Language | Usage | Priority |
|---|---|---|---|
| Playwright | TS | `unstuck.solve(page)` | exists |
| Stagehand | TS | wraps the Playwright page | demo exists |
| Playwright | Python | `await unstuck.solve(page, obstacle=..., boundary=...)` | 1 |
| browser-use | Python | an `ask_human` action the LLM can call, plus a direct call | 1 |
| Puppeteer | TS | `unstuck.solve(page)` | 2 |
| Selenium (Chrome) | Python | via Selenium 4's CDP access | 3 |
| Remote browsers (Browserbase, etc.) | any | pass a CDP URL | falls out of the core |

Firefox and Safari are out of scope (no CDP).

### Docs for developers and coding agents

- `/llms.txt` as the entry point for coding agents.
- An OpenAPI spec for `/v1`.
- One page per framework with a working snippet.
- Packages: `@unstuck/bridge` on npm and `unstuck` on PyPI.
- An MCP server (`@unstuck/mcp`) with a `hire_human` tool, and an agent skill that says when and how to call it, for agents that have tools but no code to change.

### Test mode

`test_` API keys on the production server. Test Tasks never enter the real Queue; a bot taps Done after a few seconds; a request parameter forces the outcome (solved, failed, expired); test Tasks are left out of public stats.

### API basics

- Idempotency key on Task create.
- Task history API.
- Webhooks for Task outcomes.
- Multiple API keys per Customer, with rotation, in the dashboard.

### Stuck-rate data

Optional Task fields filled in by the Agent, never inferred by Unstuck: `attempts_before_hire`, `steps_so_far`. The Bridge sends its name and version (e.g. `bridge-py/0.3`), which the Customer can override or blank. The Customer's dashboard shows their own stuck stats.

## Site and dashboards

All on `unstuck.yuchia.dev`:

| Path | Page |
|---|---|
| `/` | product homepage: pitch, sign-in, account switcher, live system stats, Solver reputation, the security story, how to contact the operator |
| `/solver` | Solver app: Queue with countdown (Team Tasks on top), Claim, Session, solved count, shadow earnings, notification settings, Withdrawal (off) |
| `/dashboard` | Customer app: API keys, Team and invites, visibility rule, Team quota, Tasks with pass/fail and time to solve, shadow cost, stuck stats |
| `/privacy` | what an account and a Task store |
| `/docs` | integration docs |
| `/llms.txt` | coding-agent entry point |
| `/security` | what Solvers see, what is stored, links to the code |
| `/status` | uptime and system stats |
| `/v1/...` | API, unchanged |

The Queue page moves from `/` to `/solver`; `/` redirects old Queue links.

- **Public Solver profiles** show pass/fail rate and average time to solve, under a handle, never the sign-in identity. Stats appear only after a minimum number of Tasks.
- **Homepage stats** (total Tasks, solve rate, median time to Claim and to solve, Solvers online, Agents integrated) are recomputed and cached hourly; daily at scale. Page views never hit SQLite.

## Data to record from day one

- Per Session: transport (`p2p` or `relay`), when it fell back, duration, bytes each way. Used to decide later whether to keep, move or drop the backend relay.
- Every minute, a supply/demand snapshot: idle, busy, notifiable, pending, accepted, rejected. Input for future pricing.
- Can't-reach and give-up counts by obstacle type, to tune boundaries and presets.

## Security and privacy

Session frames are never recorded. The public evidence, strongest first:

1. The Bridge crops to the boundary inside the Customer's own process; the Customer can verify this locally, and even a malicious server would see only the box.
2. P2P Sessions are DTLS-encrypted between Bridge and Solver and never reach the server.
3. CI builds and attests the binary (`actions/attest-build-provenance`); `/version` returns the deployed commit, so anyone can read the code that runs.
4. `/security` states in plain language what Solvers see, what is stored (Task metadata yes, frames never), and for how long, linking to the code.

No Terms of Service for the beta.

## Operations

- **Metrics:** Prometheus + node_exporter + Alertmanager for application, SQLite (file and WAL size, write latency, busy errors) and host metrics. Alertmanager notifies on **Telegram**.
- **Monitoring the monitor:** Alertmanager's always-firing Watchdog alert pings healthchecks.io; silence means Prometheus is down.
- **Host down:** CloudWatch alarms on the EC2 `StatusCheckFailed` metric and a Route 53 health check on `/healthz`, via SNS to **Slack** (AWS Chatbot). No SMS. A different channel from Alertmanager on purpose.
- **Backups:** Litestream streams SQLite to S3; daily EBS snapshots via Data Lifecycle Manager, kept 30 days.
- **Delivery:** CI/CD, a staging environment, secrets in a secrets manager.
- **TURN:** deferred. Sessions already fall back to the backend relay. Decide from the relay metrics; candidates are a single coturn host, Cloudflare's TURN, or a host with bundled transfer. Note TURN relays encrypted bytes, which is more private than the WebSocket relay.

## Open source

- License: **Apache-2.0** for the whole repo (permissive, with a patent grant companies' legal teams expect; Playwright uses it). The repo is public with no `LICENSE` yet, which legally means all rights reserved; add it first.
- Add `.DS_Store` to `.gitignore`.
- Add `CONTRIBUTING.md` and `SECURITY.md`.

## Not doing now

- Paid launch date (dropped; payments switch on when the market test says so).
- Polygon or other EVM chains; fiat on-ramps or off-ramps.
- Redis, SQS, AWS WAF.
- WhatsApp; email and SMS stay disabled.
- TURN.
- Terms of Service; KYC; tax forms.
- Vetted Solver pool (a possible paid tier later). A Team is the self-serve form of it.
- A penalty for Solvers who give up often. Give-ups are counted and shown, nothing more.
- Web3Auth, and signing in with a wallet.
- Facebook and LinkedIn sign-in.
- A Customer searching the platform's Solvers to invite to a Team (waits for payments and for reputation).
- Pricing for Team Tasks beyond the free quota.
- Mail from the product's own domain (Amazon SES); Gmail is enough for the beta.

## Glossary changes

To apply to [CONTEXT.md](../CONTEXT.md) as each piece is built:

- **Claim:** no longer "never requeued". A Task returns to the Queue on give up or can't reach.
- **Claim window:** replaced by **max wait**, set per Task by the Customer (1–15 min, default 5), measured from creation.
- **Failed:** "the Solver gave up" no longer Fails a Task while time is left. New reason `boundary_too_small` after 2 can't-reach reports.
- **Earning / Fee:** 75% / 25%.
- **Price:** in beta, a shadow price ($0.40) that is shown but not charged.
- New: **Boundary**, **Can't reach**, **Admission**, **Notification channel**, **Test key**, **Shadow price**, **Account**, **Guest Solver**, **Linked accounts**, **Team**, **Team Task**, **Visibility rule**, **Self-solved**.
- **Customer:** an account that asked for an API key; not identified by a wallet. A wallet is attached only for Deposits.
- **Solver:** every account, and every guest. No longer identified by a wallet.
- **Queue:** no longer shown in full to all Solvers; a Team Task is shown only to its Team until its rule opens it.

## Build order

Revised 2026-10-03 to bring the Team forward. [implementation-plan.md](implementation-plan.md) breaks each step into slices.

0. **Foundations:** `LICENSE`, `CONTRIBUTING.md`, `SECURITY.md`; versioned migrations; CI that runs the tests; `/healthz` and `/version`; reserve the package names.
1. **Product core:** `max_wait` and the countdown; the one-claim index; give up and can't reach with requeue and cooldowns; admission and the 503 reply; rate limits and load shedding; payments behind a flag; shadow price at 75/25; measurement.
2. **Accounts:** sign-in with GitHub, Google and Telegram; guest Solvers; `/solver` and `/dashboard`; API keys; linked accounts and the switcher; the privacy page.
3. **Notification base:** channels, time ranges, Telegram.
4. **Team:** Team and invites; visibility rules; Team admission and notifications; the onboarding choice; the quota.
5. **Integration:** CDP core; Python Bridge (Playwright, browser-use); test keys; API basics; OpenAPI, `/llms.txt`, framework docs; npm and PyPI packages; the MCP server and agent skill.
6. **Boundary:** presets, selectors, popup heuristic, enforcement and cropping in the Bridge.
7. **Site:** full dashboard; homepage with stats and contact; public profiles; `/security`, `/status`; email sign-in.
8. **Operations:** Prometheus stack and Telegram alerts, Watchdog, CloudWatch to Slack, Litestream, EBS snapshots, CI/CD, staging, build provenance, origin lock. Backups and secrets come early.
9. **Public notifications:** Slack, Web Push; email and SMS built but off; heavy-traffic trigger with the 1-hour gap.

## Still to check

- Microsoft sign-in for personal accounts: yes or no.
- Whether a Boundary should be required rather than optional.
- Cloudflare TURN pricing, when TURN is reconsidered.
- The notification response rate `p`, recalculated weekly from real data.
