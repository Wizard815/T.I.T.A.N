---
name: dnd-sync
description: The DnD campaign notes (LegendKeeper or Kanka) look old in the wiki, or the DnD sync shows as stale.
audience: family
services: [dnd-sync]
---
1. Call status and find the DnD sync. Its detail says when it last ran.
2. Stale (older than it should be): offer run_sync.
3. Run, but the notes still look old: the change may not be saved in LegendKeeper or Kanka yet, or the sync only copies some pages. Use tell_owner with the page name.
4. Don't offer run_sync more than once an hour.
