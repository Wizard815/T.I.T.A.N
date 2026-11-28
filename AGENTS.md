# T.I.T.A.N agent guide

T.I.T.A.N is a status board and help desk for Wizard's friends and family. Go 1.24, one module, no build step for the page (`internal/web/page.html` is embedded).

| Path | What |
|---|---|
| `cmd/titan` | board, login, chat, Overseer feed |
| `cmd/titan-hands` | the only process with the Docker socket and the AMP login; named operations from its own allowlist, plus the read-only container list (`list`) |
| `internal/agent` | guard model, helper model with fallback, the code screen |
| `internal/checks` | http, container, Uptime Kuma and freshness checks; uptime |
| `internal/web` | routes, roles, fixes, owner pages |
| `internal/skills` | playbooks (Markdown) the helper reads with read_skill, filtered by role |
| `internal/censor` | the room's word filter (masking, not blocking) |
| `internal/fakes` | stand-ins (AMP) shared by tests and the demo |
| `demo/` | `demo/run.sh` runs everything against `demo/fakeworld`; `demo/unraid/run.sh` is a copy of Wizard's Unraid with real models via `demo/orproxy` (OpenRouter, spend cap) |

## Rules

- **No shell, ever.** Fixes are the named actions in `internal/config` and the ops in `internal/hands`. A new fix means a new named action, checked in titan and again in titan-hands.
- **Guard first, allowlist always.** Every chat message goes through `Agent.Check`. The guard fails closed (ask_owner). The allowlist, not the guard, is what keeps things safe.
- **Changing actions need the person's Yes**, checked against their role again when they press it. The one exception is self-heal (`heal`): after triage, T.I.T.A.N may start or restart a listed service that went down, once an hour, never a found container, never while it is quiet. Triage only picks from the fixes it is handed.
- **The room is public to everyone signed in.** `/titan` there runs as `RoomUser` (friend at most, no personal services, its own history). Never give it the asker's role or private history.
- **Found containers are look-only.** `discover:` cards never get actions; fixes need a `services:` entry and titan-hands' allowlist. `list` passes on only the labels in `keepLabel`.
- **Events carry pseudonyms, not names.** Overseer is Wizard's alone and only reads `/api/events`.
- **Models are Argos names** (`titan-guard`, `titan-agent`, `titan-fallback`); pick real models in Argos.
- Secrets in env vars only. Dependencies pinned.
- New behaviour gets a test (`go test ./...`); security rules especially. Test names: `TestTitan_*`, `TestHands_*`, `TestConfig_*`.
- Run `gofmt -w .` before committing.
