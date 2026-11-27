# What's on a card

Each card shows its check's result, uptime (24 h, 7 days, 30 days) and the last 40 checks. Two settings add more.

## Extra info

`info:` adds lines to a card. They're read every check round. A line that can't be read is left out (and logged), and it never turns a card red. Keys come from env vars in the titan container and are never shown.

| Kind | Reads | Shows |
|---|---|---|
| `tautulli` | `/api/v2?cmd=get_activity` | "2 people watching (1 converting, which is harder on the server)" |
| `seerr` | `/api/v1/request/count` (`X-Api-Key`) | "1 request waiting for approval, 2 on the way" |
| `arr` | `/api/v3/queue` (v1 for Readarr) | "TV downloads: 3 in the queue" |
| `argos` | Argos's `/running` | "Loaded: qwen3-8b" or "No model loaded; the first question will be slow" |
| `json` | any JSON URL, `field:` is a dot path | "Array used: 81.2%" |

```yaml
info:
  - tautulli: {url: http://tautulli:8181, key_env: TAUTULLI_API_KEY}
  - arr: {url: http://binhex-sonarr:8989, key_env: SONARR_API_KEY, label: TV downloads}
  - json: {url: http://some-service/status, field: array.used_pct, label: Array used, suffix: "%", header_env: STATUS_HEADER}
```

`header_env` names an env var holding a whole header, like `X-Api-Key: abc`.

The ports in `titan.example.yaml` are the apps' usual ones inside Docker (Tautulli 8181, Sonarr 8989, Radarr 7878), not your LAN ports. Check them if a line doesn't show up.

## Down because of something else

`depends_on:` lists services this one needs. When it's down and one of those is down too, the card says "Because Outside access is down", turns amber, and the summary counts it apart. Only the root problem goes to Wizard's inbox, not every card behind it. The helper tells people the real cause instead of offering a restart that won't help.

```yaml
- id: plex
  depends_on: [tunnel]
- id: faith
  depends_on: [argos]
```

A `depends_on` that names a missing service, or goes round in a circle, stops titan at start with the service named.

## Other checks and uptime apps

- `check: {tcp: "host:port"}` is up when the port accepts a connection. Use it for game ports and databases.
- `kuma:` can read Uptime Kuma (a published status page, `slug:`) or Gatus (`kind: gatus`, which reads `/api/v1/endpoints/statuses`). A service uses it with `check: {kuma: "Monitor name"}`. `import_all: true` shows the app's other monitors read only.

## The media pipeline

`pipeline:` lets the chat answer "is my show coming?". It reads, read only:

- Seerr's request counts,
- the Sonarr and Radarr queues,
- the SABnzbd queue,
- Tautulli's recently added.

Each stage is optional. A stage that can't be read is named in the answer, not guessed. A service with `check: {pipeline: true}` turns red only when something is stuck. Examples: an import is blocked ("downloaded but can't be moved into the library"), a download failed, or SABnzbd is paused with things in its queue.

```yaml
pipeline:
  audience: family            # who may ask; default family
  seerr:    {url: http://overseerr:5055, key_env: SEERR_API_KEY}
  sonarr:   {url: http://binhex-sonarr:8989, key_env: SONARR_API_KEY}
  radarr:   {url: http://binhex-radarr:7878, key_env: RADARR_API_KEY}
  sabnzbd:  {url: http://binhex-sabnzbd:8080, key_env: SABNZBD_API_KEY}
  tautulli: {url: http://tautulli:8181, key_env: TAUTULLI_API_KEY}
```
