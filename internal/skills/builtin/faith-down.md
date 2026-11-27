---
name: faith-down
description: FAITH (the family assistant) isn't answering, is slow, or its voice or phone app can't reach it.
audience: family
services: [faith]
---
1. Call status and look at FAITH, and at Argos if it's listed: FAITH thinks with Argos.
2. FAITH down: offer restart_service on FAITH.
3. FAITH up but slow or "can't think": Argos may be loading a model (the first question after a while takes up to a minute). Say to wait a minute and ask again. Don't restart FAITH for that.
4. FAITH up, but the phone or speaker can't reach it: the problem is usually Wi-Fi or being away from home. Ask if they're home.
5. If recent_problems is allowed, read it and give the gist in one line (for example "out of memory" or "can't reach Home Assistant").
