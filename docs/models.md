# Models

T.I.T.A.N asks Argos for three model names. Which real model each name means is set in Argos, so you can switch between local and cloud models without touching T.I.T.A.N.

| Name (in `titan.yaml` → `models`) | Job | Good fit |
|---|---|---|
| `titan-guard` | reads every message first; answers JSON `allow`, `ask_owner` or `refuse` | a small, fast model that follows instructions well |
| `titan-agent` | answers and offers fixes with tools (`status`, `recent_problems`, `propose_fix`, `tell_owner`) | a model that is good with tool calls |
| `titan-fallback` | takes over when the guard or the helper errors or times out | a different model from the other two, maybe a cloud one, so one failure doesn't take out both |

T.I.T.A.N sends `X-Argos-App: titan` on every call, so Argos can show its usage and apply limits or a dollar budget to it alone.

## Setting the names in Argos

Any Argos way of naming a model works. Two examples:

```yaml
# a local model answers to an extra name
models:
  "qwen3-8b":
    aliases: ["titan-guard"]

# the helper: first ready model wins, a cloud one as the last resort
selectors:
  "titan-agent":
    strategy: warm
    targets: ["gpt-oss-20b", "mistral-small-24b"]
```

For a cloud model, add the provider (for example OpenRouter) in Argos's settings, then point the name at it with an alias, a profile pin or a selector target. Give the `titan` app a dollar budget there if you use one.

## The ranker (optional)

A decisions model answers four yes/no questions about each message (does it ask to destroy something, for a secret, to get round the rules, or to set something up) in about 0.2 seconds. It decides the clear cases and sends the rest to `titan-guard`. If the ranker is down, the guard decides everything.

```yaml
models:
  ranker: {model: respan/span-01-lite, key_env: OPENROUTER_API_KEY}
```

Tested on 16 family-style messages (2026-11-28):

| Model | Right | Sent to the guard | Wrong | Cost |
|---|---|---|---|---|
| `respan/span-01-lite` | 15 | 0 | 1 ("make me an admin account" refused, not asked of Wizard) | free |
| `baseten/joey-1` | 14 | 2 | 0 | about $0.000004 a message |

Decisions models aren't chat models, so Argos doesn't serve them; T.I.T.A.N calls OpenRouter's `/api/alpha/decisions` directly (`url:` changes that).

## What goes wrong

- **"The safety check isn't answering"**: neither `titan-guard` nor `titan-fallback` answered. Nothing runs until one does. Check that the names exist in Argos's `/v1/models`.
- **"The helper model isn't answering"**: the board and its buttons still work, but the chat doesn't.
- **The guard refuses ordinary asks**: the model is too cautious or too small. Try another one for `titan-guard`. Every guard decision is in the event feed, so Overseer can show you which ones went wrong.
