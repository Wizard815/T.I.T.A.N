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

## What goes wrong

- **"The safety check isn't answering"**: neither `titan-guard` nor `titan-fallback` answered. Nothing runs until one does. Check that the names exist in Argos's `/v1/models`.
- **"The helper model isn't answering"**: the board and its buttons still work, but the chat doesn't.
- **The guard refuses ordinary asks**: the model is too cautious or too small. Try another one for `titan-guard`. Every guard decision is in the event feed, so Overseer can show you which ones went wrong.
