---
name: game-server
description: A game server (Enshrouded in AMP, Minecraft, Valheim) won't start, won't let people join, or kicks them.
audience: friends
services: [enshrouded, minecraft, valheim]
---
1. Call status. Find the game's service.
2. If it's down ("Off", "Container is exited"), offer start_service. If it says up but people can't join, offer restart_service, once.
3. AMP games: "Ready, 0 of 16 players" means the game is up. If they still can't join, the problem is often their game version or the server password, not the server. Tell them to update the game and check the password with Wizard.
4. If a start or restart already ran this hour and it's still down, don't offer it again. Use tell_owner with what you saw.
5. A start takes a minute or two (worlds load slowly). Say so, and say the board checks again by itself.
