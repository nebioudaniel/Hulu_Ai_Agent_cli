# DECISIONS.md

Short, honest decisions for each phase. 1-2 lines per big choice.

## Phase 1 — Clean up
- Moved unfinished drafts to `old/` to keep the build clean and stop accidental imports.
- Fixed `agent/realy` → `agent/relay` module name early; `go mod tidy` + `.gitignore` updates.
- Kept a single `Progress.txt` so the state never became a scrolling diff.

## Phase 2 — Make the relay run
- One TCP port handles HTTP and WebSocket (Go `ServeMux` + `upgrader.Upgrade`). Easier for localhost and one Docker port.
- A real DB ping in `/health` proves the database is reachable, not just "process up".
- `.env` is optional and local; real environment variables win. No password is hardcoded.
- Stopped duplicate `/ws` and `/command` registrations; `Hub`/undefined symbols removed.

## Phase 3 — Authentication
- **Tokens are secret, never stored raw.** Only `sha256` of session and ticket tokens goes in Postgres.
- **Tickets, not URLs.** A short-lived, one-time `?ticket=` replaces a long-lived session token in the WebSocket URL (which often ends up in logs).
- **Context for identity.** `requireAuth` puts `userID` in `context`, never taken from `AgentID` or query.
- **Tight `CheckOrigin`.** Non-browser clients send no Origin, browsers must be in `ALLOWED_ORIGINS`.
- **Rate limits in memory.** Enough for one relay process; per IP + per email on login, per user on pairing. Easy to swap later.

## Phase 4 — Pairing and routing
- **Device tokens.** Revocable per agent. The Mac uses an `X-Device-Token` header to get tickets so it works after session expiry; `/devices/revoke` kills live sockets.
- **Same-user only.** The relay checks ownership at `/ws/ticket` (target agent owned by the requesting user) and again on forwarding (manager index only returns the user's agent). Cross-user messages cannot pass.
- **Peer events.** Every app of a user hears `peer online/offline` when an agent connects/disconnects. No polling.
- **Heartbeat.** `touch()` on every message; sweep every 10s to drop peers silent > 3×20s. Keep it cheap (locks small regions).
- **Exponential backoff.** CLI reconnects 1s → 2s → ... → 30s, reset on a successful connect.

## Phase 5 — The agent in the CLI
- **Run in a sandbox by location and limits.** `cmd.Dir = project folder`, `context.WithTimeout(30s)`, output capped at 10KB. Not a VM, but cheap and enforceable.
- **Deny by default.** Risky patterns (disk wipes, root, `curl|sh`, `dd/mkfs`, shutdown, ...) need phone approval; timeout denies them. One pending approval at a time.
- **One provider client.** OpenAI-compatible `/v1/chat/completions` covers Ollama. 60s HTTP timeout, clear errors for non-200. API key never logged.
- **Audit log is local and truncated.** JSONL under the agent's config dir; `output` logged but truncated to 500 chars. Tokens/keys never stored in events.
- **Configuration overrides.** `-project`, env (`AGENT_*`), saved file. No secrets echoed to TUI.
- **JSON protocol.** Explicit `type` (command, approval_*, chat, connect, command_result, chat_result, peer, ping) avoids ambiguity.

## Phase 6 — Tests and docs
- **Tests cover the contract.** Tokens (unique, stable hash), auth middleware, rate limits, pairing rules (wrong user, reuse, expired), ticket one-time use, routing + peers (same-user only, device-token identity/revoke), provider success/error/unreachable, risk detection, command env+truncate+errors, approval machine.
- **Integration tests are DB-gated.** `relay/integration_test.go` and `routing_test.go` need `TEST_DATABASE_URL`; others are pure unit tests and run without Postgres.
- **End-to-end used small throwaway programs.** Tested against a fresh relay (`script` + real CLI) to prove the JSON protocol works, not just unit tests.
- **Docs are minimal and useful.** `README.md` shows the flow, protocol table, configs. `LEARN.md` records 5 ideas, 3 questions, file locations for each phase. `DECISIONS.md` explains "why".
- **Safety first.** Never commit `.env`; never push; never print secrets; only `sha256` in the DB.

## Phase 7 — Hardening
- **Allowlist, not blocklist.** Commands run without approval only if they exactly match a tiny read-only allowlist (`ls`, `pwd`, `git status`, `git diff`, `git log`) AND contain no shell metacharacters; they run with explicit argv, no shell. Everything else goes to 60s phone approval, denied by default; approved commands may use `sh -c`. A blocklist can never enumerate every dangerous command; a deny-by-default allowlist does not have to.
- **A project folder is mandatory and not a sandbox.** No home-directory default. The folder must be absolute, exist, resolve symlinks (`EvalSymlinks`), and not be `/` or home. Docs now say explicitly: this is scoping, not OS sandboxing.
- **Ownership never changes.** Pairing no longer uses `ON CONFLICT (agent_id) DO UPDATE SET user_id = ...`. Claiming an agent owned by another account returns 409; minting a device token for another account's agent returns 403. A server-generated `agent_id` would remove the collision problem entirely and is the natural next step.
- **No message bodies in logs.** The relay logs only `type`, `role`, and message size. Command/prompt payloads are business data, not diagnostics.
- **Password policy.** 10–128 characters (runes), enforced at register and login (the login check also avoids burning argon2 on out-of-policy guesses).
- **Tests encode the guarantees.** `TestAgentOwnershipCannotBeStolen` proves user B cannot take over or un-revoke user A's agent/device.
