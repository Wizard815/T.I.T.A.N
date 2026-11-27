# Overseer feed

Overseer is Wizard's alone. It never signs anyone in to T.I.T.A.N. It only reads this feed to collect training data.

```
GET http://titan:8110/api/events?after_id=0&limit=500
Authorization: Bearer <TITAN_EXPORT_TOKEN>
```

The answer is `{"events": [...], "next_after_id": N}`. Pass `next_after_id` back to carry on. The feed is off (503) until `TITAN_EXPORT_TOKEN` is set.

| kind | when | data |
|---|---|---|
| `guard` | every chat message | `who`, `role`, `text`, `verdict`, `reason`, `model` (empty when the code check decided) |
| `conversation` | each allowed message | `who`, `role`, `text`, `reply`, `model`, `calls` (tool calls), `proposals` |
| `action` | a fix declined, done or failed | `who`, `role`, `service`, `action`, `state`, `result`, `via` |
| `state` | a service goes down or comes back | `service`, `name`, `ok`, `detail` |

`who` is a pseudonym (`p_…`), stable per person but never their name. Overseer's redaction still runs over `text` and `reply`.

Guard decisions are labelled examples (allowed against refused, and which model decided). That's good material for training a guard that knows this house. Events are kept for `keep_days` (default 90).
