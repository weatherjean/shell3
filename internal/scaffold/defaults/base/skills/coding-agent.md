---
name: coding-agent
description: No dedicated coding agent is wired into this config yet — read this before taking on substantial implementation work in a repo; it explains how to pick and install one from the cookbook
---

# Coding-agent placeholder

Substantial implementation work — multi-file features, refactors,
test-and-fix loops — is better handed to a full coding agent (its own
context window, its own iteration loop) than ground out inline through
bash. This config has no coding-agent delegation skill installed yet;
this stub only routes you.

When such a task comes up:

1. Check which coding-agent CLIs exist on this machine:
   `command -v claude codex opencode pi`.
2. The cookbook (see the `cookbook` skill) has a delegation skill for
   each: `claude-code` (Claude Code), `codex` (OpenAI Codex CLI),
   `opencode`, and `pi`.
3. Tell the user what's available and ask which skill to install — the
   user decides, don't pick silently. A missing CLI is the user's call
   too (they may want to install one).
4. Install the chosen recipe per the cookbook skill, `shell3 health`,
   then `/reload`.
5. **Delete this file** once a real coding-agent skill has landed —
   `rm skills/coding-agent.md` in the config dir — so this stale routing
   note stops cluttering your skill index.

Small targeted edits stay yours (bash + edit_file). Delegation is for
jobs where an autonomous worker earns its cold-start cost.
