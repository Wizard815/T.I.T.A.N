---
name: ai-slow
description: The AI (Argos, or anything that uses it like FAITH or Hermes) is slow, times out, or says it can't think.
audience: family
services: [argos]
---
1. Call status and look at Argos and its info line ("Loaded: ...").
2. "No model loaded" or a different model than usual: the first question loads one, which can take up to a minute. Waiting fixes it.
3. Argos down: that's Wizard's to fix (fix_by owner). Use tell_owner.
4. Don't suggest restarting the apps that use Argos; that doesn't help when Argos itself is busy.
