# hooks/explorer.tool-call.sh — gate for the `explorer` subagent ONLY.
# Each agent is governed by exactly its own script (or none) — the main
# hooks/tool-call.sh never applies to subagents. Same protocol: JSON on stdin,
# verdict JSON on stdout, nonzero exit/bad JSON/timeout = block.
#
# explorer is declared read-only ("No edits", tools: [bash]) — but that is a
# sentence in a prompt, and a prompt is not a boundary: `bash` can write files,
# push commits, and read credentials whatever the description claims. With no
# gate here explorer also becomes the way AROUND the main agent's gate: delegate
# the forbidden thing to the subagent instead. This enforces what the
# description promises.
#
# ALLOWLIST, not denylist, and deliberately: explorer's whole job is ls / find /
# rg / cat / sed -n / git log — a short, known set, which is exactly the case
# where naming what may run beats guessing at what may not. Subagents are
# headless, so there is nobody to answer an `ask`; the verdict here is run or
# refuse, and anything unrecognised has to refuse.
#
# A refusal tells the model to hand the job back rather than work around it.

set -uo pipefail

in=$(cat)
name=$(printf '%s' "$in" | jq -r '.name // empty')
cmd=$(printf '%s' "$in" | jq -r '.command // empty')
args=$(printf '%s' "$in" | jq -r '.args // empty')

allow() { printf '{}'; exit 0; }

# A subagent has nobody to ask: it is headless by construction, and an `ask`
# here auto-denies. So a refusal tells it to END and hand the problem up rather
# than hunt for another route — the main agent is the one that can raise it with
# the operator, and a subagent quietly working around a rule is the worst case.
POLICY="Do NOT try another command, path, or tool to achieve this. Stop this investigation now, \
return what you have found so far, and state plainly that this step was blocked and what you needed — \
the main agent will raise it with the operator. Never attempt to lift the restriction yourself."

block() { jq -cn --arg r "$1" --arg p "$POLICY" '{block: true, reason: ((($r | sub("[. ]+$";"")) + ". " + $p))}'; exit 0; }

# Credentials are off limits here exactly as for the main agent. Kept in step
# with hooks/tool-call.sh by hand — add a path there, add it here.
case "$cmd$args" in
  *.env.example*|*.env.sample*) ;;
  *"/.env"*|*" .env"*)
    block "explorer must not read .env" ;;
esac
case "$cmd$args" in
  *"/.ssh/"*|*"/.aws/"*|*"/.gnupg/"*|*"/.kube/"*|*"/.config/gh/"*|*"/.config/gcloud/"*|\
  *"/.git-credentials"*|*"/.netrc"*|*"/.npmrc"*|*"/.pgpass"*|*"/etc/shadow"*|*"/etc/sudoers"*)
    block "that path holds credentials and is off limits" ;;
esac

case "$name" in
  bash) ;;
  *) block "explorer investigates with bash only" ;;
esac

# Redirection settles intent on its own: this is not a read. Checked before the
# allowlist because `cat x > y` starts with an allowed verb and still writes.
case "$cmd" in
  *">"*)
    block "explorer is read-only; report the finding and let the main agent write" ;;
esac

# Reading tools with a writing mode. These are the allowlist's blind spot: the
# verb is a legitimate one and the flag is what mutates, so the check has to be
# on the flag. (`sed -i` rewrites in place; `find -delete` needs no other verb.)
case "$cmd" in
  *"sed -i"*|*"sed --in-place"*|*"perl -i"*|*"awk -i inplace"*|*"gawk -i inplace"*)
    block "that edits files in place; explorer is read-only" ;;
  *"find "*-delete*|*"find "*-exec*|*"find "*-execdir*|*"find "*-fprint*|*"find "*-fls*)
    block "find may only list here — -delete/-exec act on what it finds" ;;
esac

# Every segment of a pipeline or list is checked, so `ls | xargs rm` cannot ride
# in behind an allowed first word.
IFS='
'
segments=$(printf '%s' "$cmd" | tr ';|&' '\n')
for segment in $segments; do
  # Strip leading space, env-var prefixes (FOO=bar cmd), then any leading path.
  verb=$(printf '%s' "$segment" \
    | sed -E 's/^[[:space:]]*//; s/^([A-Za-z_][A-Za-z0-9_]*=[^[:space:]]*[[:space:]]+)*//' \
    | awk '{print $1}' | sed -E 's#.*/##')
  [ -z "$verb" ] && continue

  case "$verb" in
    # Structure and search
    ls|find|tree|rg|grep|egrep|fgrep|ag|fd|which|type|command) continue ;;
    # Reading
    cat|bat|head|tail|sed|awk|cut|tr|sort|uniq|wc|nl|jq|yq|column|xxd|file|stat|realpath|basename|dirname|readlink) continue ;;
    # History — git's own verbs are narrowed below
    git) continue ;;
    # Harmless plumbing
    echo|printf|true|test|pwd|date|env|uname|hostname|whoami|xargs) continue ;;
  esac
  block "explorer may not run '$verb' — it investigates only (ls/find/rg/cat/sed/git log). Report what you found and let the main agent act."
done
unset IFS

# git is allowed for history, not for changing anything.
case "$cmd" in
  *"git "*)
    case "$cmd" in
      *"git log"*|*"git show"*|*"git diff"*|*"git status"*|*"git blame"*|*"git branch"*|\
      *"git ls-files"*|*"git rev-parse"*|*"git describe"*|*"git remote"*|*"git shortlog"*|*"git tag"*) ;;
      *)
        block "explorer may only use git for history (log/show/diff/status/blame/ls-files)" ;;
    esac ;;
esac

# xargs is plumbing, not a way to run something else.
case "$cmd" in
  *xargs*rm*|*xargs*mv*|*xargs*sed\ -i*|*xargs*tee*)
    block "explorer is read-only; that pipeline mutates files" ;;
esac

allow
