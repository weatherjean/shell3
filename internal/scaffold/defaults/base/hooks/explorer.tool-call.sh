# hooks/explorer.tool-call.sh — gate for the `explorer` subagent ONLY.
# Each agent is governed by exactly its own script (or none) — the main
# hooks/tool-call.sh never applies to subagents. Same protocol: JSON on stdin,
# verdict JSON on stdout, nonzero exit/bad JSON/timeout = block.
#
# Example: make explorer truly read-only by allowlisting inspection commands
# (uncomment to enable; needs jq):
# in=$(cat)
# name=$(printf '%s' "$in" | jq -r .name)
# cmd=$(printf '%s' "$in" | jq -r '.command // empty')
# if [ "$name" = "bash" ] || [ "$name" = "bash_bg" ]; then
#   case "$cmd" in
#     rg\ *|cat\ *|ls*|find\ *|sed\ -n*|git\ log*|git\ show*|head\ *|tail\ *|wc\ *) exit 0 ;;
#     *) printf '{"block": true, "reason": "explorer is read-only (rg/cat/ls/find/sed -n/git log)"}'; exit 0 ;;
#   esac
# fi
exit 0
