---
name: container-unhealthy
description: Owner only. A found container is down or unhealthy and the owner asks what to do.
audience: owner
services: [docker]
---
1. Call status. Found containers (ids starting docker-) are look-only: there is no fix to propose for them.
2. Say the state in one line ("exited", "running but unhealthy").
3. If it has a matching service with recent_problems, read those and give the gist.
4. To make one fixable from the board, it needs a services: entry with actions, and its name in TITAN_HANDS_CONTAINERS. Say that once if the owner wants a button.
