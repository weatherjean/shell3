# hooks/tool-call.sh — THE gate for the MAIN agent (subagents each get their
# own: hooks/<name>.tool-call.sh; no file = that agent runs ungated).
#
# shell3 runs this with `bash` before EVERY tool call, with the config dir as
# the working directory:
#   stdin:  {"name":"bash","command":"…","args":"{…}","headless":false}
#           command is the bash text for bash/bash_bg and null otherwise —
#           check name first. headless is true when no human is attached
#           (subagents, cron).
#   stdout: {}                                   run (empty output = run too)
#           {"block": true, "reason": "…"}       block
#           {"command": "…"}                     rewrite (bash tools only)
#           {"argv": ["…"]}                      runner swap (bash tools only)
# A nonzero exit, bad JSON, or 10s timeout BLOCKS the call (fails closed).
# (There is no ask verdict: shell3 runs unattended, where a question is a
# denial with a delay — a hook printing {"ask": …} fails closed.)
#
# ---------------------------------------------------------------------------
# THIS GATE NEVER ASKS. It is a fence, not a prompt.
#
# This harness runs ~95% autonomously: nobody is at the chat when most tool
# calls happen. A block reaches the agent instantly, carrying a reason it can
# act on: pick another path, report back, leave it for the operator.
#
# So every rule here resolves to run or refuse, immediately. Inside the fence
# the agent works unsupervised and untouched. Outside it, no.
#
# WHAT IT IS NOT: the command patterns are a speed bump, not a boundary.
# Matching shell text is an arms race — base64 -d | sh, tr, xxd, $(curl …) all
# arrive at the same place. This stops an honest mistake, which is what actually
# happens unattended. It does not stop someone who holds the Telegram chat; the chat-id allowlist
# does that, and real isolation is a container, VM, or dedicated user.
# ---------------------------------------------------------------------------

set -uo pipefail

# The rules below read the payload with jq. Without jq every field parses to
# empty, no rule matches, and the gate would silently allow EVERYTHING — a
# gate that cannot read its input must refuse, not wave through. Builtins
# only here, so this holds even on a minimal PATH.
if ! command -v jq >/dev/null 2>&1; then
  printf '{"block": true, "reason": "hooks/tool-call.sh needs jq to read the tool payload; without it the gate cannot judge anything and refuses everything. Install jq (or edit the hook) and try again."}'
  exit 0
fi

# ------------------------------------------------------- what is off limits
#
# CONFIG_DIR is where shell3 keeps this config — the hook's own working
# directory, so this file is portable and needs no editing per machine.
CONFIG_DIR="$PWD"

# PROTECTED_PATHS are the only directories the agent may not write to. Note the
# shape: a DENYLIST, not an allowlist of work roots.
#
# This is an autonomous harness — it is asked to do real work in whatever repo
# or directory the task names, and an allowlist means every new project is a
# refusal until a human edits this file. That is the failure that makes an
# operator switch the gate off entirely, which costs more than it saves.
#
# So: everything is writable except the machine's own plumbing and anything
# holding credentials. A confused path inside your home directory is caught by
# review, not by this gate; a confused path into /etc is caught here.
# $HOME/Library is on the list for macOS: LaunchAgents there start processes at
# login, which is a persistence mechanism rather than a place work happens.
# /usr/local and /opt are deliberately NOT here — that is where user-installed
# tooling lives, and installing a tool is ordinary work.
PROTECTED_PATHS="/etc /boot /bin /sbin /usr/bin /usr/sbin /usr/lib /System /Library /var/db $HOME/Library"

in=$(cat)
name=$(printf '%s' "$in" | jq -r '.name // empty')
cmd=$(printf '%s' "$in" | jq -r '.command // empty')
args=$(printf '%s' "$in" | jq -r '.args // empty')

allow() { printf '{}'; exit 0; }

# POLICY is appended to every refusal. It matters as much as the rule itself:
# a model told only "denied" treats the block as an obstacle and goes looking
# for the way around it — another path, another tool, a rewritten command — and
# unattended it has all night to find one. The rule is the operator's decision,
# so the only correct response to it is to stop and say so.
POLICY="Do NOT work around this: no alternative command, path, or tool, and do not edit the gate scripts. \
If the task genuinely requires it, stop and tell the operator which rule blocked you and why you need it — \
they will change the gate if they agree. Never lift the restriction yourself."

block() { jq -cn --arg r "$1" --arg p "$POLICY" '{block: true, reason: ((($r | sub("[. ]+$";"")) + ". " + $p))}'; exit 0; }

haystack="$cmd$args"

# ------------------------------------------------------------ credentials
#
# Blocked for read as well as write — but judged against the right field, not
# a merged blob. A bash command is judged by its command text; any other tool
# (edit_file) is judged by its TARGET PATH argument only, never the file body
# it's writing. That distinction is what lets a lib/bin script read the one
# key it needs at point of use (scripting skill) — its body mentions .env,
# its path does not — while `edit_file` onto ~/.shell3/.env itself, or a
# shell command that cats/greps/redirects into one, still refuses.
case "$name" in
  bash|bash_bg)
    # A heredoc BODY is data being written into some other file, not a live
    # read/write of any path that happens to appear inside it — the
    # scripting skill's documented pattern is exactly `cat > lib/bin/foo
    # <<'SH' ... grep KEY ~/.shell3/.env ... SH`, and that must not trip
    # this rule. Judge only the command text up to the first heredoc marker
    # (a redirect naming the credential file itself, e.g. `cat > ~/.shell3/.env
    # <<EOF`, still appears there and still blocks).
    env_subject="${cmd%%<<*}"
    ;;
  *) env_subject=$(printf '%s' "$args" | jq -r '.path // empty' 2>/dev/null) ;;
esac
case "$env_subject" in
  *.env.example*|*.env.sample*) ;;
  .env|*"/.env"*|*" .env"*|*'"'.env*)
    block "the agent must not read or write .env — have a lib/bin script read the one key it needs at point of use (scripting skill)" ;;
esac

# The read-side check above deliberately looks only before a heredoc marker,
# so it can miss a redirect that comes AFTER one (`sort <<EOF > ~/.shell3/.env`
# is valid bash, and the target would be truncated away). A write target is
# unambiguous wherever it sits, so run it against the FULL, untruncated
# command — heredoc or not, this doesn't need to know. A heredoc body that
# happens to contain a literal `> ~/.shell3/.env` line is a false positive
# this accepts: the failure mode is a refusal, not a leaked or clobbered
# secret.
if [ "$name" = bash ] || [ "$name" = bash_bg ]; then
  if printf '%s\n' "$cmd" | grep -Eq '>{1,2}[[:space:]]*[^|;&[:space:]]*\.env([[:space:]]|$)' ||
     printf '%s\n' "$cmd" | grep -Eq '(^|[|;&[:space:]])(rm|mv|cp)[[:space:]][^|;&]*\.env([[:space:]]|$)'; then
    block "the agent must not read or write .env — have a lib/bin script read the one key it needs at point of use (scripting skill)"
  fi
fi

case "$haystack" in
  *"/.ssh/"*|*"/.ssh "*|*"/.aws/"*|*"/.gnupg/"*|*"/.kube/"*|*"/.docker/config.json"*|\
  *"/.config/gh/"*|*"/.config/gcloud/"*|*"/.git-credentials"*|*"/.netrc"*|*"/.npmrc"*|\
  *"/.pgpass"*|*"/.pypirc"*|*"/etc/sudoers"*|*"/etc/shadow"*|*"/etc/passwd"*)
    block "that path holds credentials and is off limits to the agent" ;;
esac

# The gate itself. CONFIG_DIR is inside the work roots (that is the point — the
# agent maintains its own config), but the scripts that constrain it are not its
# to edit, and neither is the config that says which scripts run.
case "$haystack" in
  *"hooks/"*.sh*|*"/hooks/"*|*shell3.yaml*)
    case "$cmd$args" in
      # Reading them is fine: the agent should be able to explain its own rules.
      cat\ *|*"sed -n"*|*"head "*|*"tail "*|*"rg "*|*"grep "*|*"ls "*|*"wc "*|*"git diff"*|*"git log"*|*"git show"*|*"git status"*) ;;
      *) block "the tool-call gate and shell3.yaml are the operator's, not the agent's — you may read them but not change them." ;;
    esac ;;
esac

# Only the two bash tools carry a command; everything else is done.
case "$name" in
  bash|bash_bg) ;;
  *) allow ;;
esac

# --------------------------------------------------------------- never, at all
#
# `rm -rf /` means the ROOT, not "any absolute path" — an earlier version of
# this rule matched *"rm -rf /"* and refused every `rm -rf /srv/app/build`,
# which is ordinary cleanup. Anchored to end-of-command, a following space, or
# the /* glob; anything else absolute is judged by the work-roots rule below.
flat=$(printf '%s' "$cmd" | tr -s ' ')
case "$flat" in
  *"rm -rf /"|*"rm -fr /"|*"rm -rf / "*|*"rm -fr / "*|*"rm -rf /*"*|*"rm -fr /*"*|  *"--no-preserve-root"*)
    block "that deletes the whole filesystem" ;;
esac
case "$cmd" in
  *mkfs*|*"dd if="*of=/dev/*|*"> /dev/sd"*|*"of=/dev/disk"*)
    block "destroys the machine" ;;
  *":(){ :|:& };:"*|*":(){:|:&};:"*)
    block "fork bomb" ;;
esac

# ------------------------------------------------------ do not kill the harness
#
# An autonomous agent that stops its own shell3 stops itself mid-work, and there
# is nobody watching to start it again. (Borrowed from hermes, which protects
# its gateway the same way.)
case "$cmd" in
  *"pkill"*shell3*|*"killall"*shell3*|*"systemctl"*stop*shell3*|*"systemctl"*restart*shell3*|\
  *"systemctl"*disable*shell3*|*"launchctl"*unload*shell3*|*"docker"*stop*shell3*|*"docker"*kill*shell3*)
    block "that stops shell3 itself, mid-turn, with nobody around to restart it — ask the operator" ;;
  *shutdown*|*reboot*|*"halt -"*|*"init 0"*|*"init 6"*)
    block "that takes the machine down and nothing here can bring it back up" ;;
esac

# ------------------------------------------------------------- unread remote code
#
# Fetch it, read it, then run it. Unattended there is nobody to notice that the
# URL served something else today.
case "$cmd" in
  *curl*\|*sh*|*wget*\|*sh*|*curl*\|*bash*|*wget*\|*bash*|\
  *base64*-d*\|*sh*|*base64*--decode*\|*sh*|*xxd*-r*\|*sh*|*openssl*-d*\|*bash*|\
  *'eval $(curl'*|*'eval $(wget'*|*'`curl'*|*'`wget'*)
    block "never pipe unread remote or decoded content into a shell — download it, read it, then run it" ;;
esac

# ---------------------------------------------------------- public and permanent
#
# Publishing cannot be taken back, and unattended there is no one to catch a
# mistaken version. Force-pushing destroys history other people may hold.
case "$cmd" in
  *"npm publish"*|*"gh release create"*|*"gh release delete"*|*"pypi"*upload*|*"twine upload"*|\
  *"cargo publish"*|*"docker push"*)
    block "publishing is permanent and public — leave this one for the operator" ;;
  *"git push --force"*|*"git push -f"*|*"push --force-with-lease"*)
    block "force-pushing rewrites history others may already have — leave this one for the operator" ;;
  *"git push"*)
    # Ordinary pushes are normal work for an autonomous coding agent, and are
    # recoverable. Deliberately allowed.
    ;;
esac

# --------------------------------------------------- writes into system paths
#
# Best-effort by nature: knowing every path a shell command touches means
# parsing the shell, and $VAR and $(…) hide it until it runs. This catches the
# honest mistake and misses the clever case, which is the right trade for a rule
# that has to decide instantly and alone.
mutating=no
case "$cmd" in
  *rm\ *|*mv\ *|*cp\ *|*tee\ *|*mkdir\ *|*touch\ *|*chmod\ *|*chown\ *|*truncate\ *|*">"*|\
  *"sed -i"*|*"sed --in-place"*|*"perl -i"*|*"awk -i inplace"*|*"find "*-delete*|*"find "*-exec*)
    mutating=yes ;;
esac

if [ "$mutating" = yes ]; then
  # Only genuinely absolute (or ~) paths count. Matching a bare /… anywhere in
  # the string read `./node_modules` and `s/a/b/` as absolute paths and refused
  # both — a gate that blocks `rm -rf ./build` is one you would rip out by
  # morning. So the path must start the command or follow a separator.
  for path in $(printf '%s' "$cmd" \
    | grep -oE '(^|[[:space:]"'"'"'=(<>|;&])(~|/)[A-Za-z0-9._~/-]+' \
    | sed -E 's/^[^~/]//' || true); do
    expanded="${path/#\~/$HOME}"

    # A real path's parent exists. Without this, `sed -i s/a/b/ f` reads as a
    # write to /a/b/ — regexes, URLs, and flag values are full of slashes, and
    # refusing on those would break ordinary work constantly.
    [ -d "$(dirname "$expanded")" ] || continue

    for root in $PROTECTED_PATHS; do
      case "$expanded" in
        "$root"/*|"$root")
          block "$expanded is part of the operating system, not the work — the agent does not modify it." ;;
      esac
    done
  done
fi

allow
