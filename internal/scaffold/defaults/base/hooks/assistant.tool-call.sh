# hooks/assistant.tool-call.sh — the gate for the `assistant` subagent.
#
# Subagents are ungated unless a hook exists for them by name, and there is
# no fallback to the main agent's gate. Without this file the assistant
# would run with no rules at all, which turns delegation into a way around
# every rule the main agent has: the main agent may not read `.env`, but it
# could dispatch a subagent that can, and the secret would land in the job
# transcript the Jobs view shows.
#
# The assistant is not advertised as restricted, so it needs no allowlist of
# its own — it needs exactly the main agent's rules. Rather than copy them
# (the deleted explorer gate copied them by hand and drifted), delegate:
# same script, same stdin payload, same verdict on stdout.
exec bash "$(dirname "$0")/tool-call.sh"
