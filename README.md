# Unstuck

**A human unblocks your AI Agent when its browser gets stuck.**

An Agent's browser hits a Challenge it cannot pass, such as a reCAPTCHA or a
slider puzzle. The Agent calls `solve(page)`. A human Solver claims the job
on the Queue page, controls the Agent's browser remotely from a phone or
desktop, and clears the Challenge. The Agent then carries on.

Nobody signs in and nothing is paid: an Agent needs no key, and a Solver
only opens the Queue page. See [No sign-in](#no-sign-in).

**Unstuck is a demo that you run yourself.** You start the backend, run one
of the demo Agents, and play the Solver on your phone. The Bridge is not an
npm package: it is TypeScript source in [bridge/src/](bridge/src/), and an
Agent imports it by path. See
[The Bridge and the demo Agents](#the-bridge-and-the-demo-agents).

```ts
// bridge/demo/my-agent.ts
import { solve } from "../src/index.ts";

await page.goto("https://example.com/login");
await solve(page); // returns once a human has cleared the Challenge
await page.click("#submit");
```

Terms like Task, Claim and Session have exact meanings here. See
[CONTEXT.md](CONTEXT.md) for the glossary.

## Contents

- [How it works](#how-it-works)
- [No sign-in](#no-sign-in)
- [System architecture](#system-architecture)
- [The Bridge and the demo Agents](#the-bridge-and-the-demo-agents)
- [Repository layout](#repository-layout)
- [Run the demo locally](#run-the-demo-locally)
- [Run the demo against the public backend](#run-the-demo-against-the-public-backend)
- [Configuration](#configuration)
- [Troubleshooting](#troubleshooting)
- [Tests](#tests)

## How it works

### The three people involved

| Who          | What they do                                                    | How they talk to Unstuck                    |
| ------------ | --------------------------------------------------------------- | ------------------------------------------- |
| **Customer** | Owns the Agent.                                                 | Not at all: no account and no key           |
| **Agent**    | The Customer's Playwright program. Imports the Bridge.          | `solve(page)` from the Bridge               |
| **Solver**   | A human who clears Challenges.                                  | Queue page in a browser, with no sign-in    |

### One Task, start to finish

```mermaid
sequenceDiagram
    autonumber
    participant A as Agent + Bridge
    participant B as Unstuck backend
    participant S as Solver (Queue page)

    A->>B: POST /v1/tasks
    B-->>A: task_id + session_token
    A->>B: open Bridge WebSocket
    B-->>S: task_added (live Queue)
    S->>B: claim (first Solver wins)
    B-->>A: claimed (+ peer token, ICE servers)
    B-->>S: claimed (+ peer token, ICE servers)
    loop Session, until cleared or solve window ends
        A-->>S: page frames (JPEG screencast)
        S-->>A: pointer / wheel / keyboard input
    end
    A->>A: cleared check passes
    A->>B: solved
    B-->>A: solved, so solve(page) returns
    B-->>S: task_solved
```

In words:

1. **Create.** The Agent calls `solve(page)` and the Bridge creates a Task.
2. **Queue.** Every connected Solver sees the Task appear live.
3. **Claim.** The first Solver to tap **Claim** gets it. A Task is claimed at
   most once and is never requeued.
4. **Session.** The Bridge streams the page to the Solver with a CDP
   screencast. The Solver taps, drags, scrolls and types. The Bridge replays
   that input through Playwright's mouse and keyboard APIs, so the page sees
   trusted events and never synthetic DOM events. Typing allows text and a
   short list of named keys (Enter, Backspace, Tab, arrows...), never
   Ctrl/Cmd shortcuts.
5. **Solved.** The Bridge polls the Agent's cleared check every 500 ms (by
   default, "reCAPTCHA has issued a token"). The Solver can also tap
   **Done**: the Agent takes the page back and runs its verify check (the
   cleared check unless it passes its own). If the Challenge is still there,
   the Solver is told so and keeps the page. Once either check passes, the
   Agent has the page for good, the Bridge reports Solved and `solve(page)`
   returns.

### How a Task can end

Every Task ends in exactly one of three outcomes. The first one recorded
wins.

```mermaid
stateDiagram-v2
    [*] --> Queued: POST /v1/tasks
    Queued --> Claimed: Solver claims
    Queued --> Expired: claim window passes
    Queued --> Failed: Bridge disconnects
    Claimed --> Solved: cleared check passes / verify check passes after Done
    Claimed --> Failed: solve window passes / Solver gives up / Bridge disconnects
    Solved --> [*]
    Expired --> [*]
    Failed --> [*]
```

| Outcome     | Bridge throws         |
| ----------- | --------------------- |
| **Solved**  | nothing, it returns   |
| **Expired** | `TaskExpiredError`    |
| **Failed**  | `TaskFailedError`     |

## No sign-in

Unstuck has no accounts, no API keys, no wallets and no payments.

- **An Agent needs nothing.** `POST /v1/tasks` takes no key, so anyone who
  can reach the backend can create Tasks.
- **A Solver needs nothing.** On a first visit the Queue page makes up a
  random id and keeps it in the browser's local storage. A reload or a
  second tab is the same Solver and resumes its Claim. Another browser or
  device is another Solver.
- **The id is the Solver.** Whoever presents it holds its Claim, so it is
  32 random hex characters and the page never shows it.
- **Nothing is charged and nobody is paid.**

## System architecture

```mermaid
flowchart TB
    subgraph Agent machine
        AG[Demo Agent<br/>bridge/demo, run with tsx]
        BR[Bridge<br/>bridge/src, imported by path]
        CH[Chromium page<br/>with the Challenge]
        AG -- "solve(page)" --> BR
        BR -- "CDP screencast +<br/>page.mouse" --> CH
    end

    subgraph Backend ["Unstuck backend (Go, cmd/unstuck)"]
        API[HTTP API<br/>internal/api]
        Q[Queue + Claim<br/>internal/queue]
        SE[Session relay<br/>internal/session]
        TA[Task lifecycle<br/>internal/task]
        DB[(SQLite<br/>unstuck.db)]
        WEB[Queue page<br/>internal/web/static]
        API --- Q & SE & TA
        TA --- DB
    end

    subgraph Solver device
        QP[Queue page<br/>phone or desktop browser]
    end

    BR -- "REST: create Task" --> API
    BR <-- "Bridge WebSocket:<br/>lifecycle + relayed frames/input" --> SE
    QP <-- "Queue WebSocket:<br/>tasks, claim, relayed frames/input" --> Q
    WEB -- "serves" --> QP
    BR <-. "WebRTC data channel (direct):<br/>frames + input" .-> QP
```

### Components

| Component          | Where                            | Job                                                                                   |
| ------------------ | -------------------------------- | ------------------------------------------------------------------------------------- |
| **Bridge**         | [bridge/src/](bridge/src/)       | `solve(page)`: creates the Task, streams frames, replays input, runs the cleared check. TypeScript source, not a published package. |
| **Demo Agents**    | [bridge/demo/](bridge/demo/)     | Playwright scripts that get stuck on a Challenge and call the Bridge.                  |
| **HTTP API**       | [internal/api/](internal/api/)   | REST endpoints plus the Bridge and Queue WebSockets.                                   |
| **Queue**          | [internal/queue/](internal/queue/) | Live list of unclaimed Tasks. First Claim wins, one Claim per Solver at a time.       |
| **Session**        | [internal/session/](internal/session/) | Pairs one Bridge with its Solver. Relays frames and input, and keeps the Session alive across a Solver reconnect. |
| **Task lifecycle** | [internal/task/](internal/task/) | Task state and its transitions, with the claim and solve timers.                     |
| **Queue page**     | [internal/web/static/](internal/web/static/) | Solver UI: claim, see the page, send taps and drags.                       |

### Two ways frames travel

Frames and input use one of two paths during a Session:

1. **Direct (WebRTC), preferred.** At Claim the backend gives both sides a
   per-Session peer token and STUN/TURN servers. The Solver's page sends an
   offer through the backend, the Bridge answers, and a WebRTC data channel
   opens between them. The Solver must present the peer token before the
   Bridge sends anything. Frames go as chunked binary JPEG. The backend is out
   of the data path.
2. **Relayed (WebSocket), fallback.** If WebRTC is unavailable, blocked, or
   drops, frames and input go through the backend's two WebSockets.

The Bridge WebSocket stays open either way and carries the Task lifecycle
(claimed, solved, expired, failed). A direct connection exposes each side's IP
address to the other. Solvers can opt out on the Queue page, and Agents can
pass `solve(page, { p2p: false })`. WebRTC in the Bridge comes from
[werift](https://github.com/shinyoshiaki/werift-webrtc), an optional
dependency. Without it, Sessions stay relayed.

### HTTP API

| Method + path                    | Auth          | Purpose                |
| -------------------------------- | ------------- | ---------------------- |
| `POST /v1/tasks`                 | none          | Create a Task          |
| `GET /v1/tasks/{id}/bridge`      | session token | Bridge WebSocket       |
| `GET /v1/queue?solver=...`       | Solver id     | Solver Queue WebSocket |
| `GET /`                          | none          | Queue page             |

## The Bridge and the demo Agents

The Bridge is not published to npm and has no build step.
[bridge/package.json](bridge/package.json) points `main` at
`src/index.ts`, and everything runs as TypeScript through
[tsx](https://tsx.is). An Agent uses the Bridge by importing
[bridge/src/index.ts](bridge/src/index.ts) by path, as every demo Agent
does.

### Demo Agents

Run these from `bridge/`.

| Command                  | Agent                                             | What it does                                                                 | Also needs                              |
| ------------------------ | ------------------------------------------------- | ---------------------------------------------------------------------------- | --------------------------------------- |
| `npm run demo`           | [agent.ts](bridge/demo/agent.ts)                  | Opens a local fake Challenge: a button to click or a slider to drag.         | nothing                                 |
| `npm run demo:recaptcha` | [recaptcha.ts](bridge/demo/recaptcha.ts)          | Opens Google's reCAPTCHA demo page and submits the form once it is cleared.  | nothing                                 |
| `npm run demo:stagehand` | [stagehand.ts](bridge/demo/stagehand.ts)          | A Stagehand LLM agent tries the reCAPTCHA itself. After three failed attempts the demo hands its tab to a Solver. | `ANTHROPIC_API_KEY` |
| `npm run demo:jev`       | [jev/run.py](bridge/demo/jev/run.py)              | Jev Ultrafast, a Python agent, decides itself when to hire a human. See [below](#a-fast-browser-agent-jev-ultrafast). | uv, `TYPESAFE_API_KEY`, `GEMINI_API_KEY` |

### Your own Agent

Clone the repo, install the Bridge's dependencies (see
[Prerequisites](#prerequisites)), and put a script next to the demos:

```ts
// bridge/demo/my-agent.ts
import { chromium } from "playwright";

import { solve } from "../src/index.ts";

const browser = await chromium.launch({ headless: false });
const page = await browser.newPage({ viewport: { width: 480, height: 720 } });
await page.goto("https://example.com/login");
await solve(page, { obstacle: "Pass the reCAPTCHA check above the Log in button." });
await page.click("#submit");
await browser.close();
```

```sh
cd bridge
npx tsx demo/my-agent.ts
```

- **Chromium only.** The Bridge streams the page with a CDP screencast, so
  `page` must be a Playwright page in Chromium or Chrome.
- **Say when the page is unblocked.** Unless the Challenge is a reCAPTCHA,
  pass `cleared`. See [Configuration](#configuration).
- **An Agent that is not TypeScript, or does not use Playwright,** needs a
  small helper. [jev-handoff.ts](bridge/demo/jev-handoff.ts) is one: it
  connects to the Agent's Chrome over CDP, finds the Agent's tab by its
  target ID and calls `solve()` on it.

## Repository layout

```
cmd/unstuck/            Go backend entry point
internal/                Backend packages (api, queue, session, task, ...)
internal/web/static/     Queue page (plain HTML + JS)
bridge/src/              Bridge (TypeScript source, imported by path)
bridge/demo/             Demo Agents: fake Challenge, real reCAPTCHA, Stagehand, Jev
bridge/test/             Bridge tests
deploy/                  systemd unit, k3s Ingress, deploy script
jev_start_example.sh     Launcher for the Jev demo against the public backend
CONTEXT.md               Glossary
```

## Run the demo locally

The Solver uses a phone through an ngrok tunnel. You need three terminals.
Run every command from the repo root unless the step says otherwise.

### Prerequisites

- Go 1.26+ and Node 22+
- ngrok with an authtoken (`ngrok config add-authtoken <token>`)
- Bridge dependencies:

  ```sh
  cd bridge && npm ci && npx playwright install chromium
  ```

### 1. Start the backend (terminal 1)

If an old server is still on port 8080, stop it first:

```sh
lsof -ti tcp:8080 | xargs kill
```

Then start it:

```sh
go run ./cmd/unstuck -claim-window 30s -solve-window 60s
```

State lives in `unstuck.db` in the current directory (change it with
`-db path`).

### 2. Open the tunnel (terminal 2)

```sh
ngrok http 8080
```

Copy the `https://….ngrok-free.dev` URL. On the phone:

1. Open the URL.
2. ngrok's free tier shows a warning page the first time. Tap **Visit Site**.
3. The Queue page connects by itself. The status line should read
   "Connected.".

The phone can be on cellular. Only the phone uses the tunnel; the Agent
talks to `http://localhost:8080`.

### 3. Run an Agent (terminal 3)

```sh
cd bridge
npm run demo              # local fake Challenge: button click or slider drag
npm run demo:recaptcha    # Google's reCAPTCHA demo page
```

[Demo Agents](#demo-agents) lists the others. A Chromium window opens and the Agent creates a Task. On the phone:

1. The Task appears in the Queue. Tap **Claim** within 30s.
2. The Agent's page appears, with its URL and a countdown.
3. Clear the Challenge: tap to click and swipe to scroll, as on any page.
   To drag the slider, hold a finger still on it until the page's border
   lights up, then move. With a mouse, click, drag and use the wheel. To type, tap a field on the Agent's page, then use the
   "Tap here to type" box (on a computer, just type). If the Task did not
   finish by itself, tap **Done** and the Agent checks the page.
4. The phone shows "Solved!" and the Agent continues.

If the phone's connection drops, reopen the URL in the same browser before
the solve window ends. The Session resumes.

Options:

- No browser window: `HEADLESS=1 npm run demo`
- Another backend: `UNSTUCK_URL=https://… npm run demo`

### A fast browser agent: Jev Ultrafast

`npm run demo:jev` runs [Jev Ultrafast](https://github.com/browser-use/jev-ultrafast),
a Python browser agent from Browser Use, on Indiana's business search (INBiz).
Jev works on its own, the reCAPTCHA included: the demo shows it the controls
inside frames, which Jev does not read by itself. Jev also gets a
`HIRE_HUMAN` operation, offered after three attempts at an obstacle, and
decides itself whether to use it. It then describes the obstacle in one
sentence, the Solver's whole job, and its tab goes to a Solver.

The Task is Solved when the CAPTCHA issues a new token, or when the Solver
taps Done and Jev, looking at the page, agrees the obstacle is gone.
Otherwise the Solver keeps the page until the solve window ends. Either way
Jev then carries on. When Jev is blocked, it looks again with `HIRE_HUMAN`
on offer, at most twice a run. `JEV_TRACE=file.json` saves Jev's decisions.

Jev is Python and the Bridge is TypeScript, so a helper process does the
hand-off:

```mermaid
flowchart LR
    JEV["Jev Ultrafast (Python)<br/>demo/jev/run.py"] -- CDP --> CHR[Chrome on port 9335<br/>own profile]
    JEV -- "HIRE_HUMAN: starts" --> HO["jev-handoff.ts<br/>(tsx + Playwright)"]
    HO -- "connectOverCDP,<br/>finds Jev's tab" --> CHR
    HO -- "solve(page)" --> BR[Bridge]
    BR --> BE[Unstuck backend]
    HO -. "Done: is the obstacle gone?<br/>yes / no over stdin and stdout" .-> JEV
```

Needs [uv](https://docs.astral.sh/uv/), a TypeSafe key
(console.typesafe.ai/keys) and a Gemini API key for typing into fields:

```sh
cd bridge
TYPESAFE_API_KEY=… GEMINI_API_KEY=… npm run demo:jev
```

[jev_start_example.sh](jev_start_example.sh) is a launcher for the public
backend: copy it and fill in the two keys.

The first run launches a separate Chrome with its own profile in
`~/.unstuck/jev-chrome` and a debugging port on 9335. Leave it open between
runs. Your everyday Chrome would ask "Allow remote debugging?" each time the
Bridge connects. Set `AGENT_URL` and `AGENT_TASK` for another site, or
`AGENT_ASK_TASK=1` to type the task in the terminal once the page is open.

The page fills the Chrome window, and resizing the window lays it out again.
`JEV_VIEWPORT` sets its starting size (default `800x900`). The Solver sees the
same page, so `480x720` gives a Solver on a phone bigger image tiles to tap.

## Run the demo against the public backend

The backend runs at `https://unstuck.yuchia.dev` on the `hubstream` EC2
instance. The Agent stays on the laptop; the Solver uses a phone or iPad over
the internet.

```mermaid
flowchart LR
    AG[Agent on laptop] -- HTTPS / WSS --> CF[Cloudflare<br/>TLS, *.yuchia.dev]
    PH[Solver phone] -- HTTPS / WSS --> CF
    CF -- "TLS (SSL mode Full)" --> TR[k3s Traefik<br/>websecure only]
    TR --> OV["unstuck (systemd)<br/>10.42.0.1:8080"]
    OV --> DB[(/var/lib/unstuck/unstuck.db)]
```

### 1. Deploy

```sh
deploy/deploy.sh hubstream
```

This cross-compiles `cmd/unstuck` for the instance, installs the binary and
systemd unit, restarts the service and applies the Ingress. The database is
kept. Follow logs with `ssh hubstream journalctl -u unstuck -f`.

### 2. Run the Agent and solve

```sh
cd bridge
UNSTUCK_URL=https://unstuck.yuchia.dev npm run demo:recaptcha
```

Open `https://unstuck.yuchia.dev` on the phone and Claim within 30s. The public backend gives the Solver 5 minutes
to clear the Challenge.

### Deployment notes

- **Cloudflare SSL mode must be Full**, not Flexible or Full (strict).
  Cloudflare connects to Traefik over TLS, and Traefik serves its default
  self-signed certificate.
- **HTTPS only.** The Ingress ([deploy/ingress.yaml](deploy/ingress.yaml))
  uses Traefik's `websecure` entrypoint only, so a session token or a
  Solver's id never crosses the internet in cleartext.
- **Not reachable directly.** The service
  ([deploy/unstuck.service](deploy/unstuck.service)) listens on the k3s pod
  bridge address `10.42.0.1:8080`. Traefik, the host and pods can reach it;
  the internet cannot. The instance's `cni0` must be `10.42.0.1`, the k3s
  default.
- **A database from the version with wallets and payments** is upgraded
  when the backend starts. Its Tasks are kept. Its other tables (Customers,
  Balances, Deposits, Earnings, Withdrawals) are left as they are and no
  longer read.
- **Keepalive pings.** The backend pings every socket every 20s. Cloudflare
  closes WebSockets idle for 100s, and a still page sends no frames.
- **Renamed from Overpass.** On an instance still running `overpass.service`,
  `deploy.sh` first runs
  [deploy/migrate-from-overpass.sh](deploy/migrate-from-overpass.sh): it stops
  the old service, copies its state to `/var/backups/overpass-<time>`, moves
  the database to `/var/lib/unstuck/unstuck.db`, and removes the old unit and
  the `overpass` Ingress. Later deploys skip it.
- **Keep Traefik's access log off.** The Bridge's session token and the
  Solver's id are in their WebSocket URLs.

## Configuration

Flags for `cmd/unstuck`:

| Flag              | Default                               | Meaning                                              |
| ----------------- | ------------------------------------- | ---------------------------------------------------- |
| `-addr`           | `:8080`                               | Listen address                                       |
| `-db`             | `unstuck.db`                         | SQLite path                                          |
| `-claim-window`   | `60s`                                 | Time in the Queue before a Task Expires              |
| `-solve-window`   | `120s`                                | Time after Claim before a Task Fails                 |
| `-ping-interval`  | `20s`                                 | WebSocket keepalive (0 turns it off)                 |
| `-stun`           | `stun:stun.l.google.com:19302`        | STUN URLs for direct Sessions (empty for none)       |
| `-turn`           | none                                  | TURN URLs; needs `$UNSTUCK_TURN_SECRET` (coturn `static-auth-secret`) |

`solve(page, options)` in the Bridge:

| Option    | Default                                   | Meaning                                  |
| --------- | ----------------------------------------- | ---------------------------------------- |
| `url`     | `$UNSTUCK_URL`, then `http://localhost:8080` | Backend URL                          |
| `cleared` | reCAPTCHA check                           | `(page) => Promise<boolean>`: is the page unblocked? Polled. |
| `verify`  | `cleared`                                 | `(page) => Promise<boolean>`: run once when the Solver taps Done |
| `obstacle`| none                                      | One sentence (at most 200 characters) naming the Challenge; Solvers see it before they Claim |
| `p2p`     | `true`                                    | Allow a direct WebRTC Session            |

Tip: the Solver sees exactly the page's viewport and scrolls it by swiping,
or with a mouse wheel. A Challenge inside the viewport is still quicker to
clear. A small portrait viewport such
as 480x720 keeps tap targets large.

## Troubleshooting

| Problem                                                        | Fix                                                                                              |
| -------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| Phone can't reach `http://<laptop-ip>:8080` over Wi-Fi         | Venue Wi-Fi usually isolates clients. Use ngrok, or Tailscale: open `http://<tailscale ip -4>:8080`. |
| Queue page stuck on "Connecting…"                              | Reload and tap **Visit Site** again. The ngrok cookie may have expired.                          |
| `TaskExpiredError`                                             | No Solver claimed in time. Run the Agent again.                                                  |
| `TaskFailedError`                                              | The solve window passed, the Solver gave up, or the Bridge disconnected. Run again.              |
| `address already in use`                                       | Something else is on 8080. See step 1.                                                           |

## Tests

```sh
go vet ./... && go test ./...
cd bridge && npm run typecheck && npm test
```
