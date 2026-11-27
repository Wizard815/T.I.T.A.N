# Skills (playbooks)

A skill is a short Markdown note that tells the helper how to look into one kind of problem and which fix fits. Before it answers a matching problem, the helper reads the skill with `read_skill`.

Skills are knowledge, not power:
- A skill can't add an action.
- It can't show someone a service they can't see.
- It can't skip the Yes.

Each person gets only the skills for their role. In `#room`, everyone gets the friend skills only.

Built in (`internal/skills/builtin/`):

| Skill | Who | For |
|---|---|---|
| game-server | friends | Enshrouded in AMP, Minecraft, Valheim won't start or let people in |
| plex-trouble | friends | buffering, unavailable, works at home but not away |
| cant-reach | friends | an app works at home but not away |
| secrets | friends | asks for passwords, tokens, Vaultwarden |
| faith-down | family | FAITH not answering or slow |
| show-coming | family | "when will my show be on Plex" |
| dnd-sync | family | campaign notes look old |
| ai-slow | family | Argos, or something that uses it, is slow |
| container-unhealthy | owner | a found container is down or unhealthy |

## Your own

Put `.md` files in a folder and set `skills_dir: /config/skills`. A file with the same `name:` as a built-in skill replaces it.

```markdown
---
name: game-server
description: A game server won't start or won't let people join.   # the helper picks skills by this line
audience: friends        # friends | family | owner (default friends)
services: [enshrouded]   # for your own notes
---
1. Call status ...
```

Keep each skill under 4000 characters. `titan check` loads them and says which one is broken.
