---
name: show-coming
description: Someone asks when a show or film they requested will be on Plex, or why it isn't there yet.
audience: family
services: [media]
---
1. Call media_pipeline with the title they named.
2. Read "about_your_title" first and answer from it: downloading (with % and time left), already on Plex, or not found.
3. Not found, with requests waiting for approval: Wizard hasn't approved it yet. Offer tell_owner.
4. Stuck, "can't be moved into the library": the download is done but the import failed. That's Wizard's to fix, so offer tell_owner with the title.
5. SABnzbd paused: nothing downloads until it's resumed. That's Wizard's job too.
6. A brand-new episode is often not out yet in good quality; give it a day.
