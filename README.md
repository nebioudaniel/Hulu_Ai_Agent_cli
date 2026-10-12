# Agent CLI

Control your own Mac from your phone through a relay you run yourself.
Two small Go programs and one database. No third-party cloud in the middle.

## What is here

- `relay/` — the server (Go + Postgres). Handles accounts, pairing, routing, heartbeat.
- `cli/` — the agent that runs on your Mac (Go + [Bubble Tea](https://github.com/charmbracelet/bubbletea) TUI).
- `app/` — reserved for the phone app. Not built yet; the protocol is usable with `curl` and a WebSocket client.
- `old/` — first draft code, kept for reference.
- `docs/` — `LEARN.md` (study notes per phase) and `DECISIONS.md` (why each choice).

## Architecture

```
   Phone app (later)                         Mac (cli agent)
        |                                          |
        | ws  /ws?ticket=...                       | ws  /ws?ticket=...
        v                                          v
   +----------------------- relay ------------------------+
   |  HTTP   /register /login /command                     |
   |         /pair/start /pair/claim                       |
   |         /devices/token /devices/revoke /ws/ticket     |
   |  WS     /ws   routing + heartbeat                     |
   |  DB     users, pairing_codes, devices,                |
   |         sessions, ws_tickets                          |
   +-------------------------------------------------------+
```

```mermaid
sequenceDiagram
    participant A as Phone
    participant R as Relay
    participant C as Mac CLI
    C->>R: login, /pair/start (code)
    C-->>A: shows pairing code / QR
    A->>R: login, /pair/claim(code, agent_id)
    C->>R: /devices/token -> revocable device token
    C->>R: /ws/ticket (device token) -> connect /ws
    A->>R: /ws/ticket (session) -> connect /ws
    R-->>A: peer online
    A->>R: {"type":"command","command":"ls"}
    R->>C: forwarded (same user only)
    C->>R: {"type":"command_result",...}
    R-->>A: result
```

## How a session works

1. **Register + log in.** Login returns a short-lived session token (only its
   sha256 is stored).
2. **Pair.** The CLI shows a one-time code (2 minutes, one use). The phone
   claims it. Both devices now belong to the same account.
3. **Connect.** Each side gets a one-time 30s ticket and opens a WebSocket.
   The agent also stores a revocable device token so it survives session
   expiry.
4. **Talk.** The phone sends commands and chat. The relay forwards messages
   only between devices of the same user. Risky commands need phone approval.
5. **Stay alive.** The agent pings every 20s. Three missed beats means offline,
   and the phone is told.

## Run it

Prerequisites: Go 1.26+, Postgres 14+.

### Relay

```sh
cd relay
cp .env.example .env          # then edit DATABASE_URL
go run .
```

### CLI (your Mac)

```sh
cd cli
go run . -server 127.0.0.1:8080 -id my-macbook -project ~/code
```

Press `p` to show the pairing code, `ctrl+p` for the command log.

### Phone (temporary, until the app exists)

```sh
# login
curl -s -X POST localhost:8080/login -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"..."}'
# claim a CLI pairing code
curl -s -X POST localhost:8080/pair/claim -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"pairing_code":"<code>","agent_id":"my-macbook"}'
# get a ticket, then open the socket with any WebSocket client
curl -s -X POST localhost:8080/ws/ticket -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"role":"app","agent_id":"my-macbook"}'
```

## Configuration

Relay (`relay/.env`):

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | — | Postgres connection string (required) |
| `RELAY_ADDR` | `127.0.0.1:8080` | Listen address |
| `SESSION_TTL` | `12h` | Login session lifetime |
| `TICKET_TTL` | `30s` | One-time WebSocket ticket lifetime |
| `HEARTBEAT_INTERVAL` | `20s` | Agent heartbeat period |
| `HEARTBEAT_TIMEOUT` | `60s` | Offline after this much silence |
| `ALLOWED_ORIGINS` | empty | Browser origins allowed to open the socket |

CLI:

| Variable | Meaning |
| --- | --- |
| `AGENT_TOKEN` | Use this session token instead of the login prompt |
| `AGENT_PROJECT_DIR` | Folder where remote commands may run |
| `AGENT_CONFIG` | Override the config file path |
| `AGENT_SESSION` | Override the session file path |
| `AGENT_EVENT_LOG` | Override the audit log path |

## Message protocol (app <-> agent)

| Type | Direction | Meaning |
| --- | --- | --- |
| `command` | phone -> agent | run a shell command in the project folder |
| `command_result` | agent -> phone | command output (`ok`, `output`) |
| `approval_request` | agent -> phone | risky command needs approval |
| `approval_response` | phone -> agent | approve/deny a request |
| `chat` | phone -> agent | ask the configured AI provider |
| `chat_result` | agent -> phone | provider reply |
| `connect` | phone -> agent | set provider (`base_url`, `model`, `api_key`) |
| `ping` | agent -> relay | heartbeat (not forwarded) |
| `peer` | relay -> phone | an agent went online/offline |

## Tests

```sh
# relay (unit + integration; integration skips without a test DB)
cd relay
TEST_DATABASE_URL="postgres://agentuser:pass@127.0.0.1:5432/agentdb_test?sslmode=disable" go test -race ./...

# cli
cd cli && go test -race ./...
```

## Not built yet

- The phone app itself.
- TLS / `wss`. Run the relay behind a reverse proxy for real use.
- OS-level sandboxing. Commands are limited by folder, timeout, output size,
  and phone approval, not by a kernel sandbox.
- Streaming AI replies and multi-agent UI.
