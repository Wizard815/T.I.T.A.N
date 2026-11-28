# T.I.T.A.N and Hermes

Two links, both optional.

## T.I.T.A.N tells Hermes

When something goes down by itself, T.I.T.A.N waits one check, then looks: it reads the check, the logs (secrets removed) and the playbook, and decides whether a simple start or restart should help. It sends Hermes one of:

| `event_type` | When |
|---|---|
| `needs_owner` | It looked and a restart won't fix it (bad config, full disk, crash loop), or it's still down after T.I.T.A.N already tried this hour |
| `self_fix` | It started or restarted the service by itself |
| `back_up` | The service is answering again |

The body is `{"source":"titan","event_type":"needs_owner","service":"plex","name":"Plex","detail":"…","why":"…","at":1795826820}`. It never carries people's names.

In titan.yaml:

```yaml
notify:
  hook: {url: http://hermes:8644/webhooks/titan, secret_env: TITAN_HERMES_SECRET}
```

In Hermes (`config.yaml`), a webhook route with the same secret:

```yaml
platforms:
  webhook:
    enabled: true
    extra:
      routes:
        titan:
          secret: "<same as TITAN_HERMES_SECRET>"
          signature: generic
          events: ["needs_owner"]
          prompt: |
            T.I.T.A.N needs Wizard: {name} ({service}) is down.
            Check said: {detail}
            T.I.T.A.N's look: {why}
          deliver: telegram   # or mattermost, ntfy, …
```

Add `self_fix` and `back_up` to `events` if you want those too. With `deliver_only: true` Hermes just passes the message on without running a model.

## Hermes tells T.I.T.A.N

Set `TITAN_HOOK_TOKEN` in T.I.T.A.N's `.env`, and give Hermes the same token. Then Hermes can:

```bash
# "I'm messing with plex": no alerts, no automatic checks or fixes for 2 hours
curl -X POST http://titan:8130/api/hooks/quiet -H "Authorization: Bearer $TITAN_HOOK_TOKEN" \
  -H 'Content-Type: application/json' -d '{"service":"plex","hours":2,"why":"swapping drives"}'
# everything, until lifted
curl -X POST … -d '{"service":"all","hours":0}'
# done
curl -X DELETE http://titan:8130/api/hooks/quiet/plex -H "Authorization: Bearer $TITAN_HOOK_TOKEN"
# what's up and down (the owner's view)
curl http://titan:8130/api/hooks/status -H "Authorization: Bearer $TITAN_HOOK_TOKEN"
```

You can do the same on the board (🤫 on a card, or "Quiet all"), or tell T.I.T.A.N in chat: "messing with plex, don't worry".

## What goes wrong

- **Hermes says 401**: the secret differs, or the route isn't `signature: generic`. T.I.T.A.N signs with `X-Webhook-Signature-V2`.
- **Nothing arrives**: `notify.hook` isn't set, or the thing is quiet (quiet sends nothing).
- **"Hooks are off"**: `TITAN_HOOK_TOKEN` isn't set in T.I.T.A.N.
