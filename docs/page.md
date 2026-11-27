# The page: chat, room, list, updates

The board is one page, top to bottom:

1. **Header:** how many services are up, down or blocked by another one, and when they were last checked.
2. **Chat, the top third.** On the left is your own chat with T.I.T.A.N. On the right is `#room`, which everyone signed in shares. On a phone they are two tabs.
3. **The list,** grouped by `group:` with the broken ones first. A row has its status, detail, card info, the last 40 checks, 24 h uptime, and buttons.

## Your chat

T.I.T.A.N talks like a caveman by default: "🔴 Minecraft sleep. Me start it? Press Yes." Under each answer:

- **Explain** asks for the long version in full sentences. It reuses your last answer and sends nothing new, so it skips the guard.
- **Respond** puts the cursor back in the box.

For "thanks" or "ok" it answers with one emoji. While it works, you see 👀 🔎 🤔 🔧.

Put `style: plain` in `titan.yaml` for full sentences everywhere.

A down row has an **Ask T.I.T.A.N** button. It sends "Minecraft is down. Can you check on it?" to your chat.

## The room

`#room` works like an old IRC channel: `[23:04] <alex> anyone on tonight?`. Anyone signed in can read and talk.

- **Swearing and slurs are masked** before a line is saved: "the f****** server". The list catches l33t spellings (sh1t), stretched letters (fuuuck) and spaced letters (f.u.c.k). Words that hide inside innocent ones (class, cocktail, Scunthorpe) are left alone. Add your own words with `room: {words: [...]}`.
- **No list catches everything.** The owner sees a **remove** button on every line, and the line shows "(removed by Wizard)".
- **Flooding is slowed:** 8 lines per 30 seconds per person.
- **`/agent <message>`** (or `/titan`) asks T.I.T.A.N in the room, and the answer is posted there as `@alex …`. Everyone reads it, so it only knows what friends can see, whatever your role. It leaves out personal services, even yours, and keeps its own history, apart from your private chat. A fix it offers shows its Yes only to you, and only your Yes runs it. For family things, ask in your own chat.
- Room talk is **not** sent to Overseer. `/agent` asks are logged like any chat, under a pseudonym with `"room": true`.
- Lines are kept for `keep_days`. Turn the room off with `room: {off: true}`.

```yaml
room:
  words: [grobble, someword]   # extra words to mask
  # off: true
```

## Updates

Each row has **Updates (n)**, where n is the number of posts in the last 24 h. It opens a popup with:

- the last check,
- who is working on it ("I'm on it" / "I'm done"; this clears after 12 hours, or when the service comes back),
- a timeline.

Anyone who can see the service can post there. T.I.T.A.N posts too: when a service goes down or comes back, when it's down because of another service, and when someone runs a fix through it.

![The updates popup](images/updates.png)
