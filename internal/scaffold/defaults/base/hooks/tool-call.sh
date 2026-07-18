# hooks/tool-call.sh — THE gate for the MAIN agent (subagents each get their
# own: hooks/<name>.tool-call.sh; no file = that agent runs ungated).
#
# UNSAFE BY DEFAULT: this scaffold gates nothing (the example below is
# commented out). shell3 runs this script with `bash` before EVERY tool call:
#   stdin:  {"name":"bash","command":"…","args":"{…}","headless":false}
#           command is the bash text for bash/bash_bg and null otherwise —
#           check name first. headless is true when no human can answer an
#           ask (subagents, cron) — an ask then auto-denies.
#   stdout: {}                                   run (empty output = run too)
#           {"block": true, "reason": "…"}       block
#           {"ask": "prompt", "reason": "…"}     Allow/Deny buttons in the chat
#           {"command": "…"}                     rewrite (bash tools only)
#           {"argv": ["…"]}                      runner swap (bash tools only)
# A nonzero exit, bad JSON, or 10s timeout BLOCKS the call (fails closed).
# Compose everything in this one script; there is no chaining.
#
# Example gate (uncomment to enable; needs jq):
# in=$(cat)
# name=$(printf '%s' "$in" | jq -r .name)
# cmd=$(printf '%s' "$in" | jq -r '.command // empty')
# headless=$(printf '%s' "$in" | jq -r .headless)
# if [ "$name" = "bash" ] || [ "$name" = "bash_bg" ]; then
#   case "$cmd" in
#     *'rm -rf /'*|*mkfs*|*'dd if='*)
#       printf '{"block": true, "reason": "hard_deny"}'; exit 0 ;;
#   esac
#   case "$cmd" in
#     *'rm -rf'*|*'git push'*)
#       if [ "$headless" = "true" ]; then
#         printf '{"block": true, "reason": "needs approval; rerun interactively"}'
#       else
#         printf '{"ask": "Run?\n%s", "reason": "denied"}' "$cmd"
#       fi
#       exit 0 ;;
#   esac
#   # keep secrets out of the conversation: scripts read .env at point of use
#   # (see the scripting skill); the model must not read it directly.
#   case "$cmd" in
#     *.env*) printf '{"block": true, "reason": "read secrets via a lib/bin script, not directly (scripting skill)"}'; exit 0 ;;
#   esac
# fi
exit 0
