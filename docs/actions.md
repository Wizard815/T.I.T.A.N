# Fixes

These are the only things T.I.T.A.N can do. There is no shell.

| Action | Changes anything | Needs | Runs in |
|---|---|---|---|
| `check_now` | no | anyone who sees the service | titan |
| `start_service` | yes, Yes button | `container:` or `amp:` | titan-hands: Docker start, or AMP (below) |
| `restart_service` | yes, Yes button | `container:` or `amp:` | titan-hands: Docker restart, or AMP (below) |
| `run_sync` | yes, Yes button | `sync: <job>` | titan-hands: `POST` to the job's URL |
| `recent_problems` | no | `container:` | titan-hands: last 50 log lines; titan strips secrets. Owner sees the lines. The helper summarises them for others. |

A service allows only the actions in its `actions:` list. People with at least `fix_by` (defaults to `audience`) can ask for them. With `person:` set, that person can always fix their own service.

## Limits

- titan: the same changing fix on the same service at most `limits.per_hour` times an hour (default 3). Wizard is exempt. The next ask goes to his inbox.
- titan-hands: its own cap, `TITAN_HANDS_PER_HOUR` per target (default 6), Wizard included.
- A Yes must come within `limits.confirm_within` (default 10 minutes), from the person who was offered it.

## titan-hands' allowlist

titan-hands checks everything again with its own list, which titan can't change:

```sh
TITAN_HANDS_CONTAINERS="minecraft valheim:restart faith:restart:logs"
TITAN_HANDS_SYNCS="dnd=http://dnd-sync:8080/run"
```

A bare name allows start, restart and logs. `name:op:op` allows only those. A container in `titan.yaml` but not on this list fails with "isn't on titan-hands' list". Keep Unraid's own containers, databases, Argos and titan off the list.

## Game servers in AMP

Enshrouded (and any other AMP game) runs as an instance inside the one AMP container. Restarting that container would stop every game, so a service with `amp:` never touches it. titan-hands talks to AMP's API instead:

```yaml
- id: enshrouded
  name: Enshrouded
  audience: friends
  amp: Enshrouded01          # the instance's name in AMP
  check: {amp: true}         # up when the game is Ready; the card shows players and uptime
  actions: [start_service, restart_service]
```

```sh
TITAN_HANDS_AMP_URL=http://192.168.20.5:8080
TITAN_HANDS_AMP_USER=titan
TITAN_HANDS_AMP_PASSWORD=...
TITAN_HANDS_AMP_INSTANCES=Enshrouded01
```

- In AMP, make a user just for titan-hands, with permission to start and restart only these instances. Don't use your admin login.
- Start: if the instance is off, titan-hands starts the instance; if it's on but the game isn't, it starts the game. Restart restarts the game.
- titan-hands logs in again by itself when AMP's session runs out.
- What goes wrong: "AMP refused titan-hands' login" means the user or password is wrong. "AMP has no instance called ..." means the name doesn't match (use the instance name or the friendly name shown in AMP). "isn't on titan-hands' list of AMP instances" means it's missing from `TITAN_HANDS_AMP_INSTANCES`.
- Checked against a stand-in for AMP's API, not your AMP yet. Try `start_service` once while you watch AMP.

## Adding a service

```yaml
- id: valheim
  name: Valheim
  group: Games
  audience: friends        # friends | family | owner
  container: valheim
  check: {container: true} # or http:, kuma: "<monitor name>", or freshness:
  actions: [start_service, restart_service]
```

Then add `valheim` to `TITAN_HANDS_CONTAINERS` and restart both containers. Run `/titan check` first: it says which service is wrong and why.

A **freshness** check reads a time from a JSON status URL (`field:` is a dot path; RFC 3339 or Unix time) and is down when it's older than `max_age`. Use it for syncs and backups: "Last ran 30 h ago (should be under 6 h)".
