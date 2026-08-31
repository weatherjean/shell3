package scaffold

// skill-search is shipped, executable policy in the same sense as the gate
// scripts in hooks_test.go: it runs unmodified in a user's config, so it is
// tested by actually running it rather than eyeballed. These tests never
// touch the real ~/.shell3 (HOME is redirected to a temp dir) and never hit
// the network (a stubbed curl on PATH always fails), so the offline path
// and the result cap are exercised deterministically.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func scriptPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join("defaults", "base", "lib", "bin", "skill-search")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("skill-search not found at %s: %v", p, err)
	}
	return p
}

func TestSkillSearchSyntax(t *testing.T) {
	out, err := exec.Command("bash", "-n", scriptPath(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("skill-search fails bash -n: %v\n%s", err, out)
	}
}

func stubbedPATH(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	stub := filepath.Join(bin, "curl")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return bin + string(os.PathListSeparator) + os.Getenv("PATH")
}

func TestSkillSearchOfflineNoCache(t *testing.T) {
	home := t.TempDir()
	cmd := exec.Command("bash", scriptPath(t), "test")
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+stubbedPATH(t))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit with no cache and no network, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "catalog unavailable") {
		t.Errorf("expected a clear \"catalog unavailable\" message, got:\n%s", out)
	}
}

func fakeCatalog(t *testing.T, home string, n int) {
	t.Helper()
	cacheDir := filepath.Join(home, ".shell3", "lib", "cache")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("# Skill Catalog\n\n## widgets (" + strconv.Itoa(n) + ")\n\n")
	b.WriteString("| Skill | Description | Risk | Source | Tags | Triggers |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "| `widget-%d` | Does widget things, number %d, with a description long enough to exceed any reasonable terminal-width truncation so the Source column would be the first casualty of a naive cut -c | safe | someorg/widget-repo-%d | widget, tag%d | widget, trigger%d |\n", i, i, i, i, i)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "CATALOG.md"), []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "CATALOG.sha"), []byte("deadbeef\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSkillSearchCapsResults(t *testing.T) {
	home := t.TempDir()
	fakeCatalog(t, home, 60)

	cmd := exec.Command("bash", scriptPath(t), "widget")
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+stubbedPATH(t))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("skill-search widget: %v\n%s", err, out)
	}
	body := string(out)

	rows := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "`widget-") {
			rows++
		}
	}
	if rows != 40 {
		t.Errorf("expected the cap (40) of 60 matches printed, got %d rows:\n%s", rows, body)
	}
	if !strings.Contains(body, "showing first 40 of 60") {
		t.Errorf("expected a cap notice naming 40 of 60, got:\n%s", body)
	}
	if !strings.Contains(body, "Skill | Risk | Source | Description") {
		t.Errorf("expected a header row, got:\n%s", body)
	}
	if !strings.Contains(body, "someorg/widget-repo-0") {
		t.Errorf("expected the Source field to survive a long description, got:\n%s", body)
	}
}

func TestSkillSearchHandlesPipeInDescription(t *testing.T) {
	home := t.TempDir()
	cacheDir := filepath.Join(home, ".shell3", "lib", "cache")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		t.Fatal(err)
	}
	catalog := "# Skill Catalog\n\n## widgets (1)\n\n" +
		"| Skill | Description | Risk | Source | Tags | Triggers |\n" +
		"| --- | --- | --- | --- | --- | --- |\n" +
		"| `pipe-widget` | Version 1.0 \\| Contains a bare | pipe too | safe | someorg/pipe-widget | widget, pipe | widget, pipe |\n"
	if err := os.WriteFile(filepath.Join(cacheDir, "CATALOG.md"), []byte(catalog), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "CATALOG.sha"), []byte("deadbeef\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", scriptPath(t), "pipe-widget")
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+stubbedPATH(t))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("skill-search pipe-widget: %v\n%s", err, out)
	}
	body := string(out)

	if strings.Contains(body, "unparsed row") {
		t.Fatalf("row should have parsed cleanly, got an unparsed marker:\n%s", body)
	}
	if !strings.Contains(body, "`pipe-widget` | safe | someorg/pipe-widget |") {
		t.Errorf("expected Risk=safe and Source=someorg/pipe-widget preserved despite the extra | in Description, got:\n%s", body)
	}
}

func TestSkillSearchNoMatches(t *testing.T) {
	home := t.TempDir()
	fakeCatalog(t, home, 5)

	cmd := exec.Command("bash", scriptPath(t), "no-such-skill-xyz")
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+stubbedPATH(t))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("skill-search no-such-skill-xyz: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "0 matches") {
		t.Errorf("expected a 0-matches message, got:\n%s", out)
	}
}
