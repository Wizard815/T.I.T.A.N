---
name: game-server
description: A game server (Enshrouded in AMP, Minecraft, Valheim) won't start, won't let people join, or kicks them.
audience: friends
services: [enshrouded, minecraft, valheim]
---
1. Call status. Find the game's service.
2. If it's down ("Off", "Container is exited"), offer start_service. If it says up but people can't join, offer restart_service, once.
3. AMP games: "Ready, 0 of 16 players" means the game is up. If they still can't join, check these before restarting:
   - Other players on ("3 of 16 players")? Then the server is fine and the problem is on their side: their game version, the address, or the password.
   - how_to_join in status: give them that (address, password or where to find it) word for word.
   - No how_to_join: tell them to update the game and check the address and password with Wizard.
   Only offer restart_service when it's up with 0 players and they've already checked those.
4. If a start or restart already ran this hour and it's still down, don't offer it again. Use tell_owner with what you saw.
5. A start takes a minute or two (worlds load slowly). Say so, and say the board checks again by itself.
