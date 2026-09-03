package wrk

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Compile renders an executable Bash workflow. Bash owns graph scheduling and
// loop control; shell3's hidden _agent command remains the process boundary
// that applies the typed runner protocol.
func Compile(def *Definition, configPath string) (string, error) {
	if def == nil {
		return "", fmt.Errorf("wrk: definition is required")
	}
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return "", fmt.Errorf("wrk: resolve config path: %w", err)
	}
	workdir := def.Root
	if !filepath.IsAbs(workdir) {
		workdir = filepath.Join(filepath.Dir(def.Path), workdir)
	}
	workdir, err = filepath.Abs(workdir)
	if err != nil {
		return "", fmt.Errorf("wrk: resolve task root: %w", err)
	}
	for _, node := range def.Nodes {
		if node.Kind == WaitNode {
			return "", fmt.Errorf("%s: wait-node execution requires the durable beat runtime", node.Pos)
		}
	}
	waves, err := executionWaves(def)
	if err != nil {
		return "", err
	}
	source, err := os.ReadFile(def.Path)
	if err != nil {
		return "", fmt.Errorf("wrk: read source for compilation: %w", err)
	}
	hash := sha256.Sum256(source)

	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\nset -euo pipefail\n\n")
	fmt.Fprintf(&b, "# Generated from %q by shell3 wrk compile.\n", def.Path)
	fmt.Fprintf(&b, "# Source SHA-256: %s\n", hex.EncodeToString(hash[:]))
	b.WriteString("# Regenerate this file; do not edit it.\n\n")
	fmt.Fprintf(&b, "TASK_NAME=%s\n", shellQuote(def.Name))
	fmt.Fprintf(&b, "TASK_HASH=%s\n", shellQuote(hex.EncodeToString(hash[:])))
	fmt.Fprintf(&b, "CONFIG=%s\n", shellQuote(configPath))
	fmt.Fprintf(&b, "WORKDIR=%s\n", shellQuote(workdir))
	b.WriteString("SHELL3_BIN=${SHELL3_BIN:-shell3}\n")
	b.WriteString("NOTIFY_TO=${SHELL3_WRK_NOTIFY_TO:-}\n")
	b.WriteString("NOTIFY_STATE=${SHELL3_WRK_NOTIFY_STATE:-}\n")
	b.WriteString("REQUEST=${1:-}\n")
	b.WriteString("RUN_ID=${SHELL3_WRK_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}\n")
	b.WriteString("case $RUN_ID in ''|*[!A-Za-z0-9_.-]*) printf 'invalid run id: %s\\n' \"$RUN_ID\" >&2; exit 2;; esac\n")
	b.WriteString("STATE_BASE=${SHELL3_WRK_STATE:-$WORKDIR/.shell3_project/wrk}\n")
	b.WriteString("RUN_DIR=$STATE_BASE/$TASK_NAME/$RUN_ID\n")
	b.WriteString("ARTIFACTS=$RUN_DIR/artifacts\n")
	b.WriteString("export TASK_ID=$TASK_NAME TASK_RUN=$RUN_ID TASK_ROOT=$WORKDIR TASK_ARTIFACTS=$ARTIFACTS\n")
	b.WriteString("mkdir -p \"$RUN_DIR/nodes\" \"$ARTIFACTS\"\n")
	b.WriteString("printf '%s\\n' \"$TASK_HASH\" > \"$RUN_DIR/definition.sha256\"\n")
	b.WriteString("printf '%s\\n' \"$REQUEST\" > \"$RUN_DIR/request.md\"\n\n")
	b.WriteString(`notify_exit() {
  code=$?
  trap - EXIT
  if [ -n "$NOTIFY_TO" ]; then
    event=wrk.completed
    status=completed
    if [ "$code" -ne 0 ]; then event=wrk.failed; status=failed; fi
    if [ -n "$NOTIFY_STATE" ]; then
      "$SHELL3_BIN" notify --state "$NOTIFY_STATE" --to "$NOTIFY_TO" --source "wrk:$RUN_ID" \
        --event "$event" --correlation "$RUN_ID" \
        "workflow $TASK_NAME $status (run $RUN_ID)" > "$RUN_DIR/notify.json" 2> "$RUN_DIR/notify.err" || true
    else
      "$SHELL3_BIN" notify --to "$NOTIFY_TO" --source "wrk:$RUN_ID" \
        --event "$event" --correlation "$RUN_ID" \
        "workflow $TASK_NAME $status (run $RUN_ID)" > "$RUN_DIR/notify.json" 2> "$RUN_DIR/notify.err" || true
    fi
  fi
  exit "$code"
}
trap notify_exit EXIT

`)
	b.WriteString(`set_status() {
  node=$1
  status=$2
  dir="$RUN_DIR/nodes/$node"
  mkdir -p "$dir"
  printf '%s\n' "$status" > "$dir/status.tmp"
  mv "$dir/status.tmp" "$dir/status"
}

`)
	for i, node := range def.Nodes {
		compileNode(&b, i, node)
	}
	for i, wave := range waves {
		fmt.Fprintf(&b, "run_wave_%d() {\n  failed=0\n  pids=\"\"\n", i)
		for _, index := range wave {
			fmt.Fprintf(&b, "  run_node_%d &\n  pids=\"$pids $!\"\n", index)
		}
		b.WriteString("  for pid in $pids; do\n    wait \"$pid\" || failed=1\n  done\n  [ \"$failed\" -eq 0 ]\n}\n\n")
	}
	b.WriteString("main() {\n")
	for i := range waves {
		fmt.Fprintf(&b, "  run_wave_%d || { printf 'workflow failed in wave %d\\n' >&2; return 1; }\n", i, i+1)
	}
	b.WriteString("  printf 'completed %s (%s)\\n' \"$TASK_NAME\" \"$RUN_ID\"\n")
	b.WriteString("}\n\nmain\n")
	return b.String(), nil
}

func compileNode(b *strings.Builder, index int, node Node) {
	fmt.Fprintf(b, "run_node_%d() {\n", index)
	fmt.Fprintf(b, "  node=%s\n", shellQuote(node.Name))
	b.WriteString("  node_dir=\"$RUN_DIR/nodes/$node\"\n  mkdir -p \"$node_dir\"\n  set_status \"$node\" running\n")
	switch node.Kind {
	case AgentNode:
		compileAgentCall(b, node, "1")
		if node.Accept != nil {
			compileCheck(b, node.Accept, "accept.log")
			b.WriteString("  if [ \"$check_ok\" -ne 1 ]; then set_status \"$node\" failed; return 1; fi\n")
		}
		b.WriteString("  set_status \"$node\" passed\n")
	case LoopNode:
		fmt.Fprintf(b, "  attempt=1\n  while [ \"$attempt\" -le %d ]; do\n", node.Max)
		compileAgentCall(b, node, "$attempt")
		compileCheck(b, node.Until, "verify-$attempt.log")
		b.WriteString("    if [ \"$check_ok\" -eq 1 ]; then\n      set_status \"$node\" passed\n      return 0\n    fi\n")
		b.WriteString("    attempt=$((attempt + 1))\n  done\n  set_status \"$node\" failed\n  return 1\n")
	case CommandNode:
		writeScriptInvocation(b, node.Run, "command.log", "  ")
		b.WriteString("  if [ \"$check_ok\" -ne 1 ]; then set_status \"$node\" failed; return 1; fi\n")
		if node.Accept != nil {
			compileCheck(b, node.Accept, "accept.log")
			b.WriteString("  if [ \"$check_ok\" -ne 1 ]; then set_status \"$node\" failed; return 1; fi\n")
		}
		b.WriteString("  set_status \"$node\" passed\n")
	}
	b.WriteString("}\n\n")
}

func compileAgentCall(b *strings.Builder, node Node, attempt string) {
	delimiter := literalDelimiter(node.Prompt)
	b.WriteString("  {\n")
	fmt.Fprintf(b, "    cat <<'%s'\n%s", delimiter, node.Prompt)
	if !strings.HasSuffix(node.Prompt, "\n") {
		b.WriteByte('\n')
	}
	fmt.Fprintf(b, "%s\n", delimiter)
	b.WriteString("    printf '\\nOriginal request:\\n%s\\n' \"$REQUEST\"\n")
	b.WriteString("  } | \"$SHELL3_BIN\" wrk _agent \\\n")
	fmt.Fprintf(b, "    --config \"$CONFIG\" --agent %s --workdir \"$WORKDIR\" \\\n", shellQuote(node.Using))
	fmt.Fprintf(b, "    --run-dir \"$node_dir/attempt-%s\" \\\n", attempt)
	b.WriteString("    --slot \"task-id=$TASK_NAME\" --slot \"task-run=$RUN_ID\" \\\n")
	b.WriteString("    --slot \"task-root=$WORKDIR\" --slot \"task-artifacts=$ARTIFACTS\" \\\n")
	fmt.Fprintf(b, "    --slot \"task-attempt=%s\" > \"$node_dir/result-%s.md\"\n", attempt, attempt)
}

func compileCheck(b *strings.Builder, check *Check, logName string) {
	switch check.Kind {
	case "file":
		fmt.Fprintf(b, "  check_ok=0\n  [ -f \"$ARTIFACTS/%s\" ] && check_ok=1\n", escapeDouble(check.Value))
	case "sh":
		writeScriptInvocation(b, check.Value, logName, "  ")
	}
}

func writeScriptInvocation(b *strings.Builder, script, logName, indent string) {
	delimiter := literalDelimiter(script)
	fmt.Fprintf(b, "%scheck_ok=0\n%sif (cd \"$WORKDIR\" && bash <<'%s' > \"$node_dir/%s\" 2>&1\n%s", indent, indent, delimiter, logName, script)
	if !strings.HasSuffix(script, "\n") {
		b.WriteByte('\n')
	}
	fmt.Fprintf(b, "%s\n%s); then\n%s  check_ok=1\n%sfi\n", delimiter, indent, indent, indent)
}

func executionWaves(def *Definition) ([][]int, error) {
	done := map[string]bool{}
	var waves [][]int
	for len(done) < len(def.Nodes) {
		var runnable []int
		for i, node := range def.Nodes {
			if done[node.Name] {
				continue
			}
			ready := true
			for _, dep := range node.After {
				ready = ready && done[dep]
			}
			if ready {
				runnable = append(runnable, i)
			}
		}
		if len(runnable) == 0 {
			return nil, fmt.Errorf("wrk: no runnable nodes")
		}
		// A shared writer runs alone. Otherwise readers may fill the configured
		// parallelism. Declaration order makes the plan deterministic.
		selected := runnable
		for _, i := range runnable {
			if def.Nodes[i].Access == "write" {
				selected = []int{i}
				break
			}
		}
		if len(selected) > def.Parallel {
			selected = selected[:def.Parallel]
		}
		waves = append(waves, append([]int{}, selected...))
		for _, i := range selected {
			done[def.Nodes[i].Name] = true
		}
	}
	return waves, nil
}

func literalDelimiter(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "SHELL3_" + strings.ToUpper(hex.EncodeToString(sum[:8]))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func escapeDouble(value string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`")
	return r.Replace(value)
}
