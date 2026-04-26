package usertools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mkTool(cmd string, secrets []string) Tool {
	return Tool{
		Spec: Spec{
			Name:        "t",
			Description: "d",
			Enabled:     true,
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			Command:     cmd,
			Secrets:     secrets,
			Timeout:     5 * time.Second,
		},
	}
}

func TestRun_EchoArgs(t *testing.T) {
	tool := mkTool(`echo "$QUERY"`, nil)
	out, err := Run(context.Background(), tool, `{"query":"hi there"}`, nil, "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(out) != "hi there" {
		t.Errorf("got %q", out)
	}
}

func TestRun_ArgsJSONEnv(t *testing.T) {
	tool := mkTool(`echo "$ARGS_JSON"`, nil)
	out, err := Run(context.Background(), tool, `{"a":1,"b":"x"}`, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"a":1`) || !strings.Contains(out, `"b":"x"`) {
		t.Errorf("ARGS_JSON missing: %q", out)
	}
}

func TestRun_SecretInjection(t *testing.T) {
	tool := mkTool(`echo "tok=$API_TOKEN"`, []string{"API_TOKEN"})
	secrets := map[string]string{"API_TOKEN": "s3cr3t", "OTHER": "ignored"}
	out, err := Run(context.Background(), tool, `{}`, secrets, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tok=") || strings.Contains(out, "s3cr3t") {
		t.Errorf("expected redacted secret, got %q", out)
	}
}

func TestRun_OnlyDeclaredSecretsExposed(t *testing.T) {
	tool := mkTool(`echo "other=$OTHER"`, []string{"API_TOKEN"})
	secrets := map[string]string{"API_TOKEN": "s3cr3t", "OTHER": "leaked"}
	out, err := Run(context.Background(), tool, `{}`, secrets, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "leaked") {
		t.Errorf("non-declared secret leaked: %q", out)
	}
}

func TestRun_Timeout(t *testing.T) {
	tool := mkTool(`sleep 5`, nil)
	tool.Timeout = 100 * time.Millisecond
	_, err := Run(context.Background(), tool, `{}`, nil, "")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestRun_RedactsErrorMessage(t *testing.T) {
	// Tool that exits non-zero. Force the error path. We can't easily get
	// the secret into runErr.Error() (it's "exit status 1" usually) but
	// the redaction layer must be wired in either way; assert it doesn't
	// crash and the partial output (which contained the secret) is
	// redacted.
	tool := mkTool(`echo "leak=$API_TOKEN"; exit 1`, []string{"API_TOKEN"})
	secrets := map[string]string{"API_TOKEN": "supersecretvalue"}
	out, err := Run(context.Background(), tool, `{}`, secrets, "")
	if err == nil {
		t.Fatal("expected error from non-zero exit")
	}
	if strings.Contains(out, "supersecretvalue") {
		t.Errorf("secret leaked in output: %q", out)
	}
	if !strings.Contains(out, "***REDACTED***") {
		t.Errorf("expected REDACTED marker in output, got %q", out)
	}
}

func TestRun_RedactsLongerSecretFirst(t *testing.T) {
	tool := mkTool(`echo "$LONG $SHORT"`, []string{"LONG", "SHORT"})
	// "abcd" is a substring of "abcd1234"
	secrets := map[string]string{"LONG": "abcd1234", "SHORT": "abcd"}
	out, err := Run(context.Background(), tool, `{}`, secrets, "")
	if err != nil {
		t.Fatal(err)
	}
	// Both should be replaced with REDACTED markers; importantly the
	// longer one should not have been mangled by the shorter one.
	if strings.Contains(out, "abcd") {
		t.Errorf("found unredacted substring: %q", out)
	}
	if strings.Contains(out, "1234") {
		t.Errorf("longer secret was mangled by shorter: %q", out)
	}
}

func TestRun_ArgDoesNotOverrideSecret(t *testing.T) {
	tool := mkTool(`echo "tok=$API_TOKEN"`, []string{"API_TOKEN"})
	secrets := map[string]string{"API_TOKEN": "realsecret"}
	out, err := Run(context.Background(), tool, `{"api_token":"hijack"}`, secrets, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "hijack") {
		t.Errorf("user arg overrode secret: %q", out)
	}
	// And the real secret value must be redacted.
	if strings.Contains(out, "realsecret") {
		t.Errorf("secret leaked: %q", out)
	}
}

func TestRun_MalformedArgsReturnsError(t *testing.T) {
	tool := mkTool(`echo "should not run"`, nil)
	out, err := Run(context.Background(), tool, `{this is not json`, nil, "")
	if err == nil {
		t.Fatal("expected error for malformed JSON args")
	}
	if strings.Contains(out, "should not run") {
		t.Errorf("command ran despite parse error: %q", out)
	}
}

func TestRun_Cwd(t *testing.T) {
	tool := mkTool(`pwd`, nil)
	cwd := t.TempDir()
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Run(context.Background(), tool, `{}`, nil, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != resolved {
		t.Errorf("expected %q, got %q", resolved, strings.TrimSpace(out))
	}
}

func TestRun_BeforeBlocks(t *testing.T) {
	tool := mkTool(`echo should-not-run`, nil)
	tool.Before = `bash -c 'echo blocked >&2; exit 1'`
	out, err := Run(context.Background(), tool, `{}`, nil, "")
	if err == nil {
		t.Fatal("expected block error")
	}
	if strings.Contains(out, "should-not-run") {
		t.Errorf("command ran despite block: %q", out)
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("err should contain hook stderr: %v", err)
	}
}

func TestRun_BeforeRewritesArgs(t *testing.T) {
	tool := mkTool(`echo "q=$QUERY"`, nil)
	// before reads stdin args, returns a new args object on stdout
	tool.Before = `bash -c 'cat > /dev/null; echo "{\"query\":\"rewritten\"}"'`
	out, err := Run(context.Background(), tool, `{"query":"original"}`, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "q=rewritten") {
		t.Errorf("before did not rewrite args: %q", out)
	}
}

func TestRun_AfterRewritesOutput(t *testing.T) {
	tool := mkTool(`echo original`, nil)
	tool.After = `bash -c 'cat > /dev/null; echo transformed'`
	out, err := Run(context.Background(), tool, `{}`, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "transformed" {
		t.Errorf("after did not rewrite output: %q", out)
	}
}

func TestRun_AfterHookFailureKeepsOutput(t *testing.T) {
	tool := mkTool(`echo original`, nil)
	tool.After = `bash -c 'echo broken >&2; exit 1'`
	out, err := Run(context.Background(), tool, `{}`, nil, "")
	if err != nil {
		t.Fatalf("after-hook failure should not fail Run: %v", err)
	}
	if !strings.Contains(out, "original") {
		t.Errorf("expected original output preserved, got %q", out)
	}
	if !strings.Contains(out, "after-hook failed") {
		t.Errorf("expected after-hook failure sentinel, got %q", out)
	}
}

func TestRun_BeforeHookInvalidJSONIgnored(t *testing.T) {
	tool := mkTool(`echo "q=$QUERY"`, nil)
	tool.Before = `bash -c 'cat > /dev/null; echo "not json at all"'`
	out, err := Run(context.Background(), tool, `{"query":"original"}`, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "q=original") {
		t.Errorf("expected original args preserved when before-hook returns invalid JSON, got %q", out)
	}
}

func TestRun_AfterHookSecretsRedacted(t *testing.T) {
	tool := mkTool(`echo placeholder`, []string{"API_TOKEN"})
	tool.After = `bash -c 'cat > /dev/null; echo "post: realsecret123"'`
	secrets := map[string]string{"API_TOKEN": "realsecret123"}
	out, err := Run(context.Background(), tool, `{}`, secrets, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "realsecret123") {
		t.Errorf("after-hook output not redacted: %q", out)
	}
	if !strings.Contains(out, "***REDACTED***") {
		t.Errorf("expected REDACTED marker, got %q", out)
	}
}
