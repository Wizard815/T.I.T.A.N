# T.I.T.A.N agent guide

T.I.T.A.N is a status board and help desk for Wizard's friends and family. Go 1.24, one module, no build step for the page (`internal/web/page.html` is embedded).

| Path | What |
|---|---|
| `cmd/titan` | board, login, chat, Overseer feed |
| `cmd/titan-hands` | the only process with the Docker socket; named operations from its own allowlist |
| `internal/agent` | guard model, helper model with fallback, the code screen |
| `internal/checks` | http, container, Uptime Kuma and freshness checks; uptime |
| `internal/web` | routes, roles, fixes, owner pages |
| `demo/` | `demo/run.sh` runs everything against `demo/fakeworld` |

## Rules

- **No shell, ever.** Fixes are the named actions in `internal/config` and the ops in `internal/hands`. A new fix means a new named action, checked in titan and again in titan-hands.
- **Guard first, allowlist always.** Every chat message goes through `Agent.Check`. The guard fails closed (ask_owner). The allowlist, not the guard, is what keeps things safe.
- **Changing actions need the person's Yes**, checked against their role again when they press it.
- **Events carry pseudonyms, not names.** Overseer is Wizard's alone and only reads `/api/events`.
- **Models are Argos names** (`titan-guard`, `titan-agent`, `titan-fallback`); pick real models in Argos.
- Secrets in env vars only. Dependencies pinned.
- New behaviour gets a test (`go test ./...`); security rules especially. Test names: `TestTitan_*`, `TestHands_*`, `TestConfig_*`.
- Run `gofmt -w .` before committing.
