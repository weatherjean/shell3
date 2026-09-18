package test

import (
	"os"
	"os/exec"
	"testing"
)

func TestOperationalExamples(t *testing.T) {
	// Register subprocess inputs with Go's test cache so Python edits cannot
	// silently reuse a result from an earlier version of these checks.
	for _, path := range []string{"../scripts/retention_inventory.py", "../scripts/test_operational_examples.py", "../examples/verified-outcome/verify_outcome.py"} {
		if _, err := os.ReadFile(path); err != nil {
			t.Fatal(err)
		}
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("python3 is required to verify the operational examples")
	}
	cmd := exec.CommandContext(t.Context(), python, "-B", "-m", "unittest", "discover", "-s", "../scripts", "-p", "test_operational_examples.py")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("operational examples: %v\n%s", err, output)
	}
}
