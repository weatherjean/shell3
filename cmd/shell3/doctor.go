package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/ref"
	"github.com/weatherjean/shell3/internal/scaffold"
	"github.com/weatherjean/shell3/internal/secrets"
)

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Validate shell3 setup",
		Long:  `Check global and project configuration. Exit 0 if all checks pass.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if code := runDoctor(homeDir, cwd, cmd.OutOrStdout()); code != 0 {
				return fmt.Errorf("doctor: %d check(s) failed", code)
			}
			return nil
		},
	}
}

func runDoctor(homeDir, cwd string, out io.Writer) int {
	g := paths.NewGlobal(homeDir)
	l := paths.NewLocal(cwd)
	failures := 0
	fail := func() { failures++ }

	_, _ = fmt.Fprintln(out, "Global")
	checkGlobalDoctor(out, homeDir, g, fail)

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Project")
	checkProjectDoctor(out, l, g, cwd, fail)

	if failures > 0 {
		_, _ = fmt.Fprintf(out, "\n%d check(s) failed.\n", failures)
		return 1
	}
	_, _ = fmt.Fprintln(out, "\nAll checks passed.")
	return 0
}

func checkGlobalDoctor(out io.Writer, homeDir string, g paths.Global, fail func()) {
	doctorCheck(out, fail, dirExists(g.Root), "~/.shell3/ exists")
	doctorCheck(out, fail, dirExists(g.Skills), "global skills dir")
	doctorCheck(out, fail, dirExists(g.Tools), "global tools dir")

	credStore, err := config.LoadCredStore(homeDir)
	if err == nil && len(credStore.List()) > 0 {
		names := make([]string, 0, len(credStore.List()))
		for _, m := range credStore.List() {
			names = append(names, m.Instance)
		}
		doctorCheck(out, fail, true, fmt.Sprintf("credentials: %v", names))
	} else {
		doctorCheck(out, fail, false, "no credentials — run: shell3 auth")
	}

	_, err = secrets.Load(homeDir)
	doctorCheck(out, fail, err == nil, "secrets store accessible")
}

func checkProjectDoctor(out io.Writer, l paths.Local, g paths.Global, cwd string, fail func()) {
	doctorCheck(out, fail, dirExists(l.Root), ".shell3/ exists")

	uuid, err := ref.Load(l)
	if err != nil || uuid == "" {
		doctorCheck(out, fail, false, ".ref missing — run shell3 in this directory to bootstrap")
		return
	}
	p := paths.NewProject(g, uuid)
	doctorCheck(out, fail, true, fmt.Sprintf(".ref → ~/.shell3/projects/%s/", uuid))

	meta, err := ref.ReadMeta(p)
	if err == nil {
		resolvedCWD, _ := filepath.EvalSymlinks(cwd)
		resolvedMeta, _ := filepath.EvalSymlinks(meta.CWD)
		if resolvedCWD == "" {
			resolvedCWD = cwd
		}
		if resolvedMeta == "" {
			resolvedMeta = meta.CWD
		}
		doctorCheck(out, fail, resolvedMeta == resolvedCWD, fmt.Sprintf("meta.json: project=%s", meta.Name))
	} else {
		doctorCheck(out, fail, false, "meta.json unreadable")
	}

	doctorCheck(out, fail, dirExists(p.Dir), "project state dir")

	personaName := scaffold.DefaultPersonaName + ".md"
	personaOk := fileExists(filepath.Join(l.Personas, personaName)) ||
		fileExists(filepath.Join(g.Personas, personaName))
	doctorCheck(out, fail, personaOk, fmt.Sprintf("persona: %s", scaffold.DefaultPersonaName))
}

func doctorCheck(out io.Writer, fail func(), ok bool, msg string) {
	if ok {
		_, _ = fmt.Fprintf(out, "  ✓ %s\n", msg)
	} else {
		_, _ = fmt.Fprintf(out, "  ✗ %s\n", msg)
		fail()
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
