# LEARN.md

Simple notes from each phase. One section per phase, appended when the phase ends.

## Phase 1 — Clean up (repo layout, module names, hygiene)

### 5 ideas you must understand
1. Repo layout: one folder per Go program. `relay/` is the server, `cli/` is the terminal app, `app/` is the future phone app. Draft code goes in `old/` so it cannot break the build.
2. Module path: the `module` line in `go.mod` is the import path root. A typo like `agent/realy` spreads everywhere and is confusing to fix later.
3. `go mod tidy`: deletes unused dependencies and fixes the `// indirect` comments. Run it whenever imports change.
4. `.gitignore`: keeps secrets and generated files (`.env`, `*.db`, compiled binaries) out of git. A committed `.env` is a leaked password.
5. A progress log should be one clean file. Stacking update blocks on top of each other makes it hard to read.

Where:
- Layout and `old/`: repo root, `old/server/`, `old/agent/`, `old/crypto/`
- Module path: `relay/go.mod:1` (module line)
- `go mod tidy`: `relay/go.mod`, `cli/go.mod`
- `.gitignore`: `.gitignore`
- Progress file: `Progress.txt`

### 3 interviewer questions
1. Why are `relay/` and `cli/` separate Go modules instead of one module with two commands?
2. What happens if a `.env` file is committed, and how do you stop it?
3. What exactly does `go mod tidy` change in your repo?

## Phase 2 — Make the relay run (server basics, config, health)

### 5 ideas you must understand
1. A relay is just an HTTP server: it mixes normal REST handlers and WebSocket upgrades on one TCP port.
2. Registering the same path twice (like `/ws`) panics at startup ("multiple registrations"). Keep one route per path and one hub.
3. Configuration comes from the environment: `DATABASE_URL` and `RELAY_ADDR`. If a required value is missing, fail loudly and clearly.
4. A `/health` endpoint that pings the database is the cheapest way to prove the server is alive and the DB is reachable.
5. A tiny `.env` loader can live in the standard library; it reads `KEY=VALUE` lines and never overrides real environment variables.

Where:
- HTTP + WebSocket on one port: `relay/main.go:main`, `relay/websocket.go:handleWebSocket`
- One hub / one route: `relay/websocket.go:Manager`, `relay/main.go:main`
- Config from env: `relay/main.go:main`, `relay/config.go:loadDotEnv`
- Health check: `relay/handlers.go:healthHandler`
- Env loader: `relay/config.go:loadDotEnv`

### 3 interviewer questions
1. How does one Go server handle both REST requests and WebSockets on the same port?
2. What happens if you call `http.HandleFunc` twice with the same path?
3. How would you make the relay listen on a different address in production?

## Phase 3 — Authentication (tokens, middleware, tickets, limits)

### 5 ideas you must understand
1. Never store raw tokens. Store the sha256 hash only, so a leaked database does not leak working tokens.
2. Identity comes from the token, never from URL parameters. The token becomes the user via request context.
3. Long-lived tokens must not appear in URLs (URLs get logged). Use a short-lived one-time ticket for the WebSocket instead.
4. `CheckOrigin` must check the Origin header, not return `true`. Otherwise any website can open a socket to your relay.
5. Simple in-memory rate limiting (attempts + window) is enough for one process, and revocation should also drop the live connection.

Where:
- Token + hashing: `relay/tokens.go:newToken` and `relay/tokens.go:hashToken`
- Middleware + context: `relay/middleware.go:requireAuth`, `relay/middleware.go:userIDFromContext`
- One-time tickets: `relay/handlers.go:wsTicketHandler`, `relay/websocket.go:consumeTicket`
- Origin check: `relay/websocket.go:originAllowed`
- Rate limit: `relay/ratelimit.go:rateLimiter`
- Revoke + disconnect: `relay/handlers.go:revokeDeviceHandler`, `relay/websocket.go:Manager.DisconnectAgent`

### 3 interviewer questions
1. Show how you authenticate a request and where the user ID comes from.
2. Why use a one-time ticket for the WebSocket instead of putting the session token in the query string?
3. A user tells you her device is stolen. What does your system do?

---

## Phase 4 — Pairing and routing (device tokens, heartbeat, peer events)

### 5 ideas you must understand
1. A device token is a second, revocable credential bound to one agent. The agent uses it for tickets, so it works after your session expires and can be killed without changing your password.
2. Routing is ownership-checked twice: at ticket time (who owns the target agent) and at forward time (same user in the manager). A different user's phone can never reach your agent.
3. Peer events keep phones in sync: the relay broadcasts `{"type":"peer",...}` to every connected app of a user whenever an agent goes online or offline. No polling.
4. Heartbeat is "touch per message, expire when stale". The agent pings every 20s; the relay closes and marks offline anything silent for 3 beats.
5. Reconnect uses exponential backoff (1s, 2s, 4s, up to 30s). The model doubles the delay on each failure and resets it on a successful connect.

Where:
- Device tokens: `relay/handlers.go:deviceTokenHandler`, `relay/handlers.go:deviceTokenUser`, `cli/auth.go:fetchDeviceToken`
- Same-user routing: `relay/websocket.go:handleWebSocket` (app case), `relay/handlers.go:wsTicketHandler`
- Peer online/offline: `relay/websocket.go:notifyPeerStatus`, `relay/websocket.go:Manager.notifyApps`
- Heartbeat: `relay/websocket.go:startHeartbeatMonitor`, `relay/websocket.go:Manager.expireIdleAgents`, `cli/ui.go:heartbeat`
- Backoff: `cli/ui.go:Update` (errMsg / reconnectMsg)
- Envelope/ping: `cli/ui.go:wsEnvelope`, `cli/ui.go:listenForCommands`, `relay/websocket.go:isPingMessage`

### 3 interviewer questions
1. Your agent token is a session token that expires in 12 hours. How does the device keep working, and how do you kill it remotely?
2. Two phones of the same user are connected, plus one of another user. Show where in code the wrong phone is blocked.
3. The agent stops sending heartbeats. Walk through exactly what the relay does and what the phone sees.

---

## Phase 5 — The agent in the CLI (safe commands, provider, logging)

### 5 ideas you must understand
1. A remote shell is a loaded gun: every command runs inside one chosen project folder (`cmd.Dir`), with a 30s timeout and a 10KB output cap. Never trust the directory a command asks for.
2. Risky commands are denied by default: if the phone does not answer an approval request within 60s, the command does not run. Timeouts must fail closed, not open.
3. One provider client covers both OpenAI and Ollama because Ollama speaks the OpenAI `/v1/chat/completions` shape. Timeouts and non-200 replies become clear errors, not hangs.
4. Config (project folder, provider) lives in one JSON file written 0600, and secrets (API keys, tokens) are never logged or echoed back.
5. Every action is appended to a local JSONL audit log, but output is truncated when logged so a big command cannot bloat the file.

Where:
- Safe execution: `cli/safe_cmd.go:runCommand`, `cli/safe_cmd.go:isRisky`
- Approval flow: `cli/ui.go` (commandMsg / approvalResponseMsg / approvalTimeoutMsg), `cli/ui.go:sendApprovalRequest`
- Provider client: `cli/provider.go:ProviderConfig.Chat`, `cli/provider.go:ProviderConfig.Validate`
- Config: `cli/config.go:loadConfig`, `cli/config.go:saveConfig`
- Logging: `cli/events.go:logEvent`, `cli/events.go:shortOutput`
- Protocol envelope: `cli/ui.go:wsEnvelope`, `cli/ui.go:listenForCommands`

### 3 interviewer questions
1. A phone asks your Mac to run `rm -rf ~`. Walk through every guard between the request and the disk.
2. How do you support both OpenAI and a local Ollama with one client, and how do you avoid hanging forever on a slow provider?
3. Where do secrets live on the agent, and how do you prove they never reach a log file or the relay?

---

Rule for this file: append a new section at the end of every phase.