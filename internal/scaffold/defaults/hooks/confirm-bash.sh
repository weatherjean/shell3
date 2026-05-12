#!/usr/bin/env bash
# Default on_tool_call hook: prompts the user before destructive shell tool
# calls. Safe commands run unprompted.
#
# Coverage: bash, bash_bg, shell_interactive. All other tools allowed.
#
# Headless mode (set by shell3 when --out is given OR stdin is piped with no
# TTY): no widget prompt is possible, so this hook applies a safe policy:
#   - SHELL3_HEADLESS=1, SHELL3_HEADLESS_TRUST unset → block dangerous, allow safe.
#   - SHELL3_HEADLESS=1, SHELL3_HEADLESS_TRUST=1     → allow everything (orchestrator's risk).
#
# Match strategy: each pattern is an Extended Regular Expression run against
# the raw command string with `grep -E -q`. If ANY pattern matches in
# interactive mode, the user is prompted via the `shell3 widget pick` selector.
# In headless mode the same match drives the auto-block.
#
# To extend / override:
#   - Copy this file and edit the DANGER_PATTERNS array below.
#   - Point your persona's `on_tool_call:` at the copy.
#
# Hook contract: stdin = on_tool_call JSON, stdout = action JSON.
# Actions:
#   allow  — proceed
#   block  — deny THIS call only; model can pick a different approach
#   cancel — abort the entire turn

INPUT=$(cat)
TOOL=$(echo "$INPUT" | jq -r '.tool')

case "$TOOL" in
  bash|bash_bg|shell_interactive) ;;
  *) echo '{"action":"allow"}'; exit 0 ;;
esac

CMD=$(echo "$INPUT" | jq -r '.params.command // empty')

# Destructive-pattern denylist. Conservative on false-positives: prefer to
# prompt on a borderline case than silently allow. Each entry is an ERE.
DANGER_PATTERNS=(
  # File deletion / truncation. (^|[ ;|&]) avoids matching `--rm` etc.
  '(^|[[:space:];|&])rm[[:space:]]'
  '(^|[[:space:];|&])rmdir[[:space:]]'
  '(^|[[:space:];|&])shred([[:space:]]|$)'
  '(^|[[:space:];|&])truncate([[:space:]]|$)'
  '(^|[[:space:];|&])unlink([[:space:]]|$)'

  # Disk / filesystem
  '(^|[[:space:];|&])dd[[:space:]].*[[:space:]]of='
  '(^|[[:space:];|&])mkfs(\.[a-z0-9]+)?([[:space:]]|$)'
  '(^|[[:space:];|&])wipefs([[:space:]]|$)'

  # Privilege elevation
  '(^|[[:space:];|&])sudo([[:space:]]|$)'
  '(^|[[:space:];|&])su[[:space:]]'
  '(^|[[:space:];|&])doas([[:space:]]|$)'

  # Broad permission / ownership changes
  '(^|[[:space:];|&])chmod[[:space:]]+[+-]?[0-7]*7[0-7]*7'
  '(^|[[:space:];|&])chmod[[:space:]]+-R'
  '(^|[[:space:];|&])chown[[:space:]]+-R'

  # Irreversible VCS
  '(^|[[:space:];|&])git[[:space:]]+push[[:space:]].*(--force|-f([[:space:]]|$)|--mirror|--delete)'
  '(^|[[:space:];|&])git[[:space:]]+reset[[:space:]]+--hard'
  '(^|[[:space:];|&])git[[:space:]]+clean[[:space:]]+-[a-zA-Z]*[fF]'
  '(^|[[:space:];|&])git[[:space:]]+branch[[:space:]]+-D'
  '(^|[[:space:];|&])git[[:space:]]+checkout[[:space:]]+--([[:space:]]|$)'
  '(^|[[:space:];|&])git[[:space:]]+restore[[:space:]]+--source'
  '(^|[[:space:];|&])git[[:space:]]+filter-branch'
  '(^|[[:space:];|&])git[[:space:]]+update-ref[[:space:]]+-d'

  # Package managers (uninstall)
  '(^|[[:space:];|&])npm[[:space:]]+(uninstall|rm|remove)([[:space:]]|$)'
  '(^|[[:space:];|&])pnpm[[:space:]]+remove([[:space:]]|$)'
  '(^|[[:space:];|&])yarn[[:space:]]+remove([[:space:]]|$)'
  '(^|[[:space:];|&])pip[[:space:]]+uninstall'
  '(^|[[:space:];|&])brew[[:space:]]+(uninstall|remove)([[:space:]]|$)'
  '(^|[[:space:];|&])apt(-get)?[[:space:]]+(remove|purge|autoremove)'
  '(^|[[:space:];|&])yum[[:space:]]+(remove|erase)'
  '(^|[[:space:];|&])dnf[[:space:]]+(remove|erase)'
  '(^|[[:space:];|&])pacman[[:space:]]+-R'
  '(^|[[:space:];|&])go[[:space:]]+clean[[:space:]]+-modcache'
  '(^|[[:space:];|&])cargo[[:space:]]+clean([[:space:]]|$)'

  # SQL destructive
  'DROP[[:space:]]+(TABLE|DATABASE|SCHEMA|INDEX)'
  'TRUNCATE[[:space:]]+TABLE'
  'DELETE[[:space:]]+FROM'

  # Pipe-to-shell
  '(curl|wget)[[:space:]][^|]*\|[[:space:]]*(sudo[[:space:]]+)?(bash|sh|zsh)([[:space:]]|$)'

  # System control
  '(^|[[:space:];|&])systemctl[[:space:]]+(stop|disable|mask)'
  '(^|[[:space:];|&])service[[:space:]]+[^[:space:]]+[[:space:]]+stop'
  '(^|[[:space:];|&])shutdown([[:space:]]|$)'
  '(^|[[:space:];|&])reboot([[:space:]]|$)'
  '(^|[[:space:];|&])halt([[:space:]]|$)'
  '(^|[[:space:];|&])kill[[:space:]]+-9'
  '(^|[[:space:];|&])killall([[:space:]]|$)'
  '(^|[[:space:];|&])pkill([[:space:]]|$)'

  # Firewall
  '(^|[[:space:];|&])iptables[[:space:]]+-F'
  '(^|[[:space:];|&])nft[[:space:]]+flush'

  # Docker / container — volume and bulk deletes
  '(^|[[:space:];|&])docker[[:space:]]+volume[[:space:]]+(rm|remove)'
  '(^|[[:space:];|&])docker[[:space:]]+volume[[:space:]]+prune'
  '(^|[[:space:];|&])docker[[:space:]]+system[[:space:]]+prune'
  '(^|[[:space:];|&])docker[[:space:]]+(container[[:space:]]+)?prune'
  '(^|[[:space:];|&])docker[[:space:]]+image[[:space:]]+prune'
  '(^|[[:space:];|&])docker[[:space:]]+network[[:space:]]+prune'
  '(^|[[:space:];|&])docker[[:space:]]+rm[[:space:]].*-[a-zA-Z]*v'
  '(^|[[:space:];|&])docker([[:space:]]+container)?[[:space:]]+rm[[:space:]]+-f'
  '(^|[[:space:];|&])docker(-| )compose[[:space:]]+down[[:space:]].*-[a-zA-Z]*v'
  '(^|[[:space:];|&])docker(-| )compose[[:space:]]+rm'
  '(^|[[:space:];|&])podman[[:space:]]+volume[[:space:]]+(rm|prune)'
  '(^|[[:space:];|&])podman[[:space:]]+system[[:space:]]+prune'

  # Risky redirects
  '>[[:space:]]*/etc/'
  '>[[:space:]]*/dev/sd[a-z]'
  '>[[:space:]]*/dev/nvme'
  '>[[:space:]]*/dev/disk'
  '>[[:space:]]*~/\.[a-zA-Z]'

  # Fork bomb
  ':\([[:space:]]*\)[[:space:]]*\{'
)

HIT=""
for pat in "${DANGER_PATTERNS[@]}"; do
  if echo "$CMD" | grep -qE "$pat"; then
    HIT=1
    break
  fi
done

# Headless policy: dangerous → block (model adapts), safe → allow.
# Set SHELL3_HEADLESS_TRUST=1 to bypass and use the same logic as interactive.
if [[ "$SHELL3_HEADLESS" == "1" && "$SHELL3_HEADLESS_TRUST" != "1" ]]; then
  if [[ -n "$HIT" ]]; then
    echo '{"action":"block","reason":"Headless mode: destructive command requires human approval. Try a non-destructive alternative or skip this step."}'
    exit 0
  fi
  echo '{"action":"allow"}'
  exit 0
fi

if [[ -z "$HIT" ]]; then
  echo '{"action":"allow"}'
  exit 0
fi

RESULT=$(jq -n --arg input "${TOOL}: $CMD" '
  {
    "input": $input,
    "default": "block",
    "choices": [
      {"value":"allow","label":"Allow","hint":"yes, run it"},
      {"value":"block","label":"Deny this call","hint":"model picks a different approach"},
      {"value":"cancel","label":"Cancel turn","hint":"stop generation entirely"}
    ]
  }' | shell3 widget pick 2>/dev/null)

CHOICE=$(echo "$RESULT" | jq -r '.value // "block"')

case "$CHOICE" in
  allow)
    echo '{"action":"allow"}'
    ;;
  cancel)
    echo '{"action":"cancel","reason":"User cancelled generation."}'
    ;;
  *)
    echo '{"action":"block","reason":"User denied this bash command. Acknowledge and try a different approach."}'
    ;;
esac
