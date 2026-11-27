# How T.I.T.A.N connects to the homelab

Most apps need no setup. T.I.T.A.N finds them through Docker. Only add a line in `titan.yaml` when you want fixes, extra card info, or a check that's better than "is the container running".

```
                      browser (friends, family, you) ──► titan :8130
                                                          │
         ┌───────────────────────┬────────────────────────┼─────────────────────────┐
         ▼                       ▼                        ▼                         ▼
  titan-hands :8111        Argos :8080/v1          Uptime Kuma / Gatus     app APIs (read only)
  (titan_inside net)       (ai_net)                (optional)              Tautulli, Seerr, *arr,
  the Docker socket,       guard, helper and                               SABnzbd, Argos /running
  the AMP login            fallback models
         │
   Docker on Unraid ── every container: name, state, health, ports, Unraid web UI link
   AMP ── game servers (Enshrouded)

  Overseer ──reads──► titan /api/events (pseudonyms, Bearer token). Nothing flows back.
```

## 1. Containers: automatic

Turn this on once:

```yaml
discover:
  docker: true
  host: 192.168.20.5     # your Unraid IP, fills [IP] in Unraid's web UI links
  audience: owner        # found containers are yours alone unless a label says otherwise
  skip: [some-helper]    # names to leave out
```

Every running container becomes a card under **Containers**, with an **Open** button taken from Unraid's web UI setting. A container that was running and then stops stays on the board as down. Containers that were already stopped when T.I.T.A.N started (like Immich today) stay hidden. Set `stopped: true` to show them too.

To show a container to friends or family, or to tidy it up, add labels. On Unraid that's the container's **Extra Parameters**, for example `--label titan.audience=family --label titan.group=Media --label titan.name=Plex`.

| Label | Does |
|---|---|
| `titan.enable=false` | leaves it out |
| `titan.name`, `titan.group`, `titan.about` | the card's name, group and line |
| `titan.audience=friends` / `family` | who sees it (default `discover.audience`) |
| `titan.url` | the Open button, instead of Unraid's web UI link |

Found cards are look-only. To let people **start or restart** something, list it under `services:` with `actions:` **and** put it in `TITAN_HANDS_CONTAINERS`. A listed service replaces its found card, matched by container name.

titan-hands only passes on names, images, state, health, published ports and these few labels. It never sends env vars or mounts. Set `TITAN_HANDS_DISCOVER=0` on titan-hands to turn listing off.

## 2. Uptime: built in, or your uptime app

- T.I.T.A.N checks on its own: containers, `http`, `tcp`, `freshness` (a "last ran" time), AMP and the media pipeline.
- If you already run Uptime Kuma or Gatus, point `kuma:` at it. With `import_all: true`, its monitors show up too. Use this for things outside Docker, like the router or the NUC.

## 3. Card info and "is my show coming?"

These are read-only API calls with keys from env vars, set up per app: `info:` on a card, and `pipeline:`. See [cards.md](cards.md).

## 4. Links to apps

`link: http://...` on any service in `titan.yaml` adds the same Open button.

## 5. DASH, the handheld

DASH's T.I.T.A.N mod reads `GET /api/glance` with `Authorization: Bearer <TITAN_GLANCE_TOKEN>`. It gets counts (up, down, pending, owner notes), up to 6 down services with the reason and the "down because" service, and up to 40 services with their status:

```json
{"up": 11, "down": 1, "pending": 0, "notes": 2, "every": 60,
 "down_list": [{"name": "Plex", "detail": "no answer", "cause": "Outside access"}],
 "services": [{"name": "Plex", "status": "down"}]}
```

It sees what a family member sees in the room: no one's personal services, no actions, no chat. It's off (503) until `TITAN_GLANCE_TOKEN` is set. Use a different token from `TITAN_EXPORT_TOKEN`.

## What's not connected (yet)

- **Argos's system monitor** sees only Argos's own box (CPU, GPUs). T.I.T.A.N can show GPU numbers from `GET /api/gpu/latest` with a `json` info line, but that's not a source of containers.
- **Overseer** already collects Docker, Unraid, Home Assistant, Hermes, Argos and Omi. A read-only `GET /api/inventory` there could later feed T.I.T.A.N everything at once, including non-Docker things. That's an Overseer change, not built.
