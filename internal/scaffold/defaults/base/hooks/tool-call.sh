# hooks/tool-call.sh — THE gate for the MAIN agent (subagents each get their
# own: hooks/<name>.tool-call.sh; no file = that agent runs ungated).
#
# shell3 runs this with `bash` before EVERY tool call, with the config dir as
# the working directory:
#   stdin:  {"name":"bash","command":"…","args":"{…}","headless":false}
#           command is the bash text for bash/bash_bg and null otherwise —
#           check name first.
#   stdout: {}                              run (empty output = run too)
#           {"block": true, "reason": "…"}   refuse
#           {"command": "…"} / {"argv": […]} rewrite (bash tools only)
# A nonzero exit, bad JSON, or 10s timeout BLOCKS the call (fails closed).
#
# ---------------------------------------------------------------------------
# THE DEFAULT IS RUN.
#
# This gate refuses six things, all irreversible and none of them the work:
# destroying the machine, leaking credentials, stopping shell3 itself,
# publishing, running unread remote code, and editing this file. Everything
# else runs — including deleting things, which is most of what cleanup is.
#
# It is deliberately short, because a long one is worse. An earlier version
# had sixteen rules across 284 lines; across every recorded session it blocked
# nothing, while refusing `rm -rf ~/Library/Caches/<browser>` (the agent was
# repairing a browser that would not launch), `cd ~/.shell3 && cat hooks/…`
# (the same read that passed without the `cd`), and any command that so much
# as mentioned a project's function names. An agent refused for ordinary work
# does not learn where the boundary is; it learns the whole subject is
# forbidden, and stops trying things it was supposed to do.
#
# IT NEVER ASKS. shell3 runs unattended, where an ask is a denial with a delay
# — the turn parks until the ask times out, then denies anyway.
#
# WHAT IT IS NOT: matching shell text is a speed bump, not a boundary —
# base64, xxd and $(…) all arrive at the same place. This stops an honest
# mistake, which is what actually happens unattended. Real isolation is a
# container, a VM, or a dedicated user.
# ---------------------------------------------------------------------------

set -uo pipefail

# Every rule below parses the payload with jq, and an unparsed payload matches
# no rule — so without jq the default case would wave every call through.
# Refuse instead: a gate that cannot read its input is not a gate.
if ! command -v jq >/dev/null 2>&1; then
  printf '%s' '{"block":true,"reason":"this gate needs jq to parse its payload and jq is not installed, so every tool call is refused until it is. Tell the operator to install jq."}'
  exit 0
fi

in=$(cat)
name=$(printf '%s' "$in" | jq -r '.name // empty')
cmd=$(printf '%s' "$in" | jq -r '.command // empty')
args=$(printf '%s' "$in" | jq -r '.args // empty')

allow() { printf '{}'; exit 0; }

# Appended to every refusal. A model told only "denied" treats the block as an
# obstacle and goes looking for the way around it — and unattended it has all
# night to find one. The rule is the operator's decision, so the only correct
# response is to stop and say so.
POLICY="Do NOT work around this: no alternative command, path, or tool, and do not edit the gate scripts. \
If the task genuinely requires it, stop and tell the operator which rule blocked you and why you need it — \
they will change the gate if they agree. Never lift the restriction yourself."

block() { jq -cn --arg r "$1" --arg p "$POLICY" '{block: true, reason: ((($r | sub("[. ]+$";"")) + ". " + $p))}'; exit 0; }

# Does this command WRITE? The rules below that care about a location care
# about writing to it, never about reading it. Reading anything is fine,
# including these rules.
writes() {
  printf '%s\n' "$1" | grep -Eq \
    '(^|[|;&[:space:]])(rm|mv|cp|tee|chmod|chown|truncate|ln|install|dd|mkdir|touch)[[:space:]]|>|sed[[:space:]]+-i|perl[[:space:]]+-i|awk[[:space:]]+-i[[:space:]]+inplace'
}

# --- 1. Credentials -------------------------------------------------------
# The one mistake that cannot be undone: a leaked key is leaked into a
# transcript forever. Judged against the right field — a bash command by its
# text up to the first heredoc (so the documented pattern, a lib/bin script
# whose BODY greps one key at point of use, still works), any other tool by
# its target path alone, never the file body it writes.
case "$name" in
  bash|bash_bg) subject="${cmd%%<<*}" ;;
  *) subject=$(printf '%s' "$args" | jq -r '.path // empty' 2>/dev/null) ;;
esac
case "$subject" in
  *.env.example*|*.env.sample*) ;;
  .env|*"/.env"*|*" .env"*|*'"'.env*)
    block "the agent must not read or write .env — have a lib/bin script read the one key it needs at point of use (scripting skill)" ;;
  *"/.shell3/secrets"*)
    block "the agent must not read the secrets/ dir — a script reads the one key file it needs at point of use; printing keys into the chat leaks them into transcripts" ;;
esac

# A write TARGET is unambiguous wherever it sits, so this runs against the
# full command: a redirect placed after a heredoc marker escapes the
# prefix-truncated check above.
case "$name" in
  bash|bash_bg)
    if printf '%s\n' "$cmd" | grep -Eq '>{1,2}[[:space:]]*[^|;&[:space:]]*\.env([[:space:]]|$)' ||
       printf '%s\n' "$cmd" | grep -Eq '(^|[|;&[:space:]])(rm|mv|cp)[[:space:]][^|;&]*\.env([[:space:]]|$)'; then
      block "the agent must not read or write .env — have a lib/bin script read the one key it needs at point of use (scripting skill)"
    fi ;;
esac

case "$cmd$args" in
  *"/.ssh/"*|*"/.ssh "*|*"/.aws/"*|*"/.gnupg/"*|*"/.kube/"*|*"/.docker/config.json"*|\
  *"/.config/gh/"*|*"/.config/gcloud/"*|*"/.git-credentials"*|*"/.netrc"*|*"/.npmrc"*|\
  *"/.pgpass"*|*"/.pypirc"*|*"/etc/sudoers"*|*"/etc/shadow"*)
    block "that path holds credentials and is off limits to the agent" ;;
esac

# --- 2. This gate ---------------------------------------------------------
# Readable — the agent should be able to explain its own rules — but not
# writable, or "ask the operator to lift this" has an obvious shortcut.
case "$cmd$args" in
  *"hooks/"*.sh*|*"/hooks/"*|*shell3.yaml*)
    writes "$cmd$args" && block "the tool-call gate and the wiring are the operator's, not the agent's — you may read them but not change them" ;;
esac

# Only the two bash tools carry a command; everything else is done.
case "$name" in
  bash|bash_bg) ;;
  *) allow ;;
esac

# --- 3. Destroying the machine --------------------------------------------
# `rm -rf /` means the ROOT, not any absolute path — anchored, so ordinary
# cleanup like `rm -rf /srv/app/build` is untouched.
flat=$(printf '%s' "$cmd" | tr -s ' ')
case "$flat" in
  *"rm -rf /"|*"rm -fr /"|*"rm -rf / "*|*"rm -fr / "*|*"rm -rf /*"*|*"rm -fr /*"*|*"--no-preserve-root"*)
    block "that deletes the whole filesystem" ;;
  *mkfs*|*"dd if="*of=/dev/*|*"> /dev/sd"*|*"of=/dev/disk"*)
    block "that destroys the machine" ;;
  *":(){ :|:& };:"*|*":(){:|:&};:"*)
    block "fork bomb" ;;
esac

# System plumbing, writes only. Reading /etc is ordinary. On macOS the
# persistence mechanism is ~/Library/LaunchAgents specifically; the rest of
# ~/Library is caches, logs and app support, where clearing a cache is the
# most ordinary cleanup there is.
if writes "$cmd"; then
  if printf '%s\n' "$cmd" | grep -Eq '(^|[|;&[:space:]>])(/etc|/boot|/bin|/sbin|/usr/bin|/usr/sbin|/usr/lib|/System|/Library|/var/db)(/|[[:space:]]|$)' ||
     printf '%s\n' "$cmd" | grep -Eq '(~|\$HOME)/Library/Launch(Agents|Daemons)'; then
    block "that path is part of the operating system, not the work — the agent does not modify it"
  fi
fi

# --- 4. Stopping shell3, or the machine -----------------------------------
# An agent that stops its own shell3 stops itself mid-work, with nobody around
# to start it again.
case "$cmd" in
  *"pkill"*shell3*|*"killall"*shell3*|*"systemctl"*stop*shell3*|*"systemctl"*restart*shell3*|\
  *"systemctl"*disable*shell3*|*"launchctl"*unload*shell3*|*"docker"*stop*shell3*|*"docker"*kill*shell3*)
    block "that stops shell3 itself, mid-turn, with nobody around to restart it — ask the operator" ;;
  *shutdown*|*reboot*|*"halt -"*|*"init 0"*|*"init 6"*)
    block "that takes the machine down and nothing here can bring it back up" ;;
esac

# --- 5. Unread remote code ------------------------------------------------
# Fetch it, read it, then run it. Unattended there is nobody to notice that
# the URL served something else today.
case "$cmd" in
  *curl*\|*sh*|*wget*\|*sh*|*curl*\|*bash*|*wget*\|*bash*|\
  *base64*-d*\|*sh*|*base64*--decode*\|*sh*|*xxd*-r*\|*sh*|*openssl*-d*\|*bash*|\
  *'eval $(curl'*|*'eval $(wget'*|*'`curl'*|*'`wget'*)
    block "never pipe unread remote or decoded content into a shell — download it, read it, then run it" ;;
esac

# --- 6. Public and permanent ----------------------------------------------
# An ordinary `git push` is normal work and is recoverable. These are not.
case "$cmd" in
  *"npm publish"*|*"gh release create"*|*"gh release delete"*|*"pypi"*upload*|*"twine upload"*|\
  *"cargo publish"*|*"docker push"*)
    block "publishing is permanent and public — leave this one for the operator" ;;
  *"git push --force"*|*"git push -f"*|*"push --force-with-lease"*)
    block "force-pushing rewrites history others may already have — leave this one for the operator" ;;
esac

allow
