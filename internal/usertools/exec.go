package usertools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const defaultTimeout = 30 * time.Second

// Run executes a user tool. rawArgs is the JSON string from the LLM. secrets
// is the merged map of available secrets (only those listed in tool.Secrets
// are injected). defaultCwd is used when tool.Cwd is empty.
//
// The combined stdout+stderr is returned. Secret values are redacted from
// the output before returning. Errors from the subprocess (non-zero exit,
// timeout) are returned as Go errors and the partial output is still
// returned.
//
// SECURITY: tool.Command is treated as trusted (authored by the user via
// .shell3/tools/*.yaml). LLM-controlled args are passed only via
// environment variables, never interpolated into the command string. Do
// not change this — interpolating args into the command would be a
// command-injection vulnerability.
func Run(ctx context.Context, tool Tool, rawArgs string, secrets map[string]string, defaultCwd string) (string, error) {
	timeout := tool.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	if tool.Before != "" {
		newArgs, blockErr := runHook(ctx, tool.Before, effectiveCwd(tool, defaultCwd), rawArgs, timeout)
		if blockErr != nil {
			// %w is safe here: hooks do not receive declared secrets in env, so the wrapped error cannot leak them.
			return "", fmt.Errorf("usertools: %s: before hook: %w", tool.Name, blockErr)
		}
		if trimmed := strings.TrimSpace(newArgs); trimmed != "" {
			var probe map[string]any
			if json.Unmarshal([]byte(trimmed), &probe) == nil {
				rawArgs = trimmed
			}
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args, parseErr := parseArgsJSON(rawArgs)
	if parseErr != nil {
		return "", fmt.Errorf("usertools: %s: malformed args JSON: %w", tool.Name, parseErr)
	}

	cmd := exec.CommandContext(runCtx, "bash", "-c", tool.Command)
	cmd.Dir = effectiveCwd(tool, defaultCwd)
	cmd.Env = buildEnv(args, rawArgs, tool.Secrets, secrets)

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()

	out := buf.String()

	if tool.After != "" {
		if newOut, hookErr := runHook(ctx, tool.After, effectiveCwd(tool, defaultCwd), out, timeout); hookErr == nil {
			out = newOut
		} else {
			out = out + "\n[after-hook failed: " + hookErr.Error() + "]"
		}
	}

	var secretValues []string
	for _, name := range tool.Secrets {
		if v, ok := secrets[name]; ok && v != "" {
			secretValues = append(secretValues, v)
		}
	}
	out = Redact(out, secretValues)

	if ctx.Err() == context.Canceled {
		return out, fmt.Errorf("usertools: %s: canceled", tool.Name)
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("usertools: %s: timed out after %s", tool.Name, timeout)
	}
	if runErr != nil {
		redactedMsg := Redact(runErr.Error(), secretValues)
		return out, fmt.Errorf("usertools: %s: %s", tool.Name, redactedMsg)
	}
	return out, nil
}

// buildEnv composes the subprocess environment: parent env minus the keys
// we are about to set, plus declared secrets, plus ARGS_JSON, plus
// uppercased flat scalar args.
func buildEnv(args map[string]any, rawArgs string, declaredSecrets []string, available map[string]string) []string {
	skip := map[string]struct{}{"ARGS_JSON": {}}
	for _, s := range declaredSecrets {
		skip[s] = struct{}{}
	}
	for k := range args {
		skip[strings.ToUpper(k)] = struct{}{}
	}

	env := []string{}
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		if _, drop := skip[kv[:eq]]; drop {
			continue
		}
		env = append(env, kv)
	}

	for _, name := range declaredSecrets {
		if v, ok := available[name]; ok {
			env = append(env, name+"="+v)
		}
	}

	if rawArgs == "" {
		rawArgs = "{}"
	}
	env = append(env, "ARGS_JSON="+rawArgs)

	secretSet := make(map[string]struct{}, len(declaredSecrets))
	for _, s := range declaredSecrets {
		secretSet[s] = struct{}{}
	}
	for k, v := range args {
		key := strings.ToUpper(k)
		if _, isSecret := secretSet[key]; isSecret {
			// Don't let a tool arg overwrite a declared secret.
			continue
		}
		switch t := v.(type) {
		case string:
			env = append(env, key+"="+t)
		case bool:
			if t {
				env = append(env, key+"=true")
			} else {
				env = append(env, key+"=false")
			}
		case float64, int, int64:
			env = append(env, fmt.Sprintf("%s=%v", key, t))
		default:
			b, _ := json.Marshal(v)
			env = append(env, key+"="+string(b))
		}
	}
	return env
}

// runHook runs a before/after shell command with stdin piped in. Returns
// stdout. Non-zero exit returns an error whose message is the hook's
// stderr (or the run error if stderr was empty).
//
// Hooks inherit the shell3 process environment. Declared tool.Secrets are
// intentionally NOT injected into hooks — hooks are not the place to mint
// authenticated requests; that is the tool command's job.
//
// Each hook gets its own timeout budget equal to the full tool.Timeout. A
// tool with both before and after hooks plus a slow command can therefore
// take up to 3 * tool.Timeout in the worst case.
func runHook(ctx context.Context, command, cwd, stdin string, timeout time.Duration) (string, error) {
	hCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := exec.CommandContext(hCtx, "bash", "-c", command)
	c.Dir = cwd
	c.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return stdout.String(), nil
}

func effectiveCwd(tool Tool, defaultCwd string) string {
	if tool.Cwd != "" {
		return tool.Cwd
	}
	return defaultCwd
}

func parseArgsJSON(raw string) (map[string]any, error) {
	if raw == "" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return map[string]any{}, err
	}
	return m, nil
}
