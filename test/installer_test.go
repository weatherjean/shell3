package test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerRequiresExactReleaseChecksum(t *testing.T) {
	if _, err := os.ReadFile("../install.sh"); err != nil {
		t.Fatal(err)
	}
	const asset = "shell3_0.33.0_linux_amd64.tar.gz"
	const binary = "#!/bin/sh\necho verified-release\n"
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "shell3", Mode: 0755, Size: int64(len(binary))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(binary)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
	for _, tc := range []struct {
		name, manifest, hashTool string
		success                  bool
	}{
		{"sha256sum", digest + "  " + asset + "\n", "sha256sum", true},
		{"shasum", digest + "  " + asset + "\n", "shasum", true},
		{"missing manifest", "", "sha256sum", false},
		{"wrong digest", strings.Repeat("0", 64) + "  " + asset + "\n", "sha256sum", false},
		{"wrong asset", digest + "  other.tar.gz\n", "sha256sum", false},
		{"duplicate", strings.Repeat(digest+"  "+asset+"\n", 2), "sha256sum", false},
		{"no verifier", digest + "  " + asset + "\n", "", false},
		{"broken verifier", digest + "  " + asset + "\n", "broken", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "tools")
			prefix := filepath.Join(root, "installed")
			for _, dir := range []string{bin, prefix} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, body string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			installed := filepath.Join(prefix, "shell3")
			write(installed, "previous installation")
			if err := os.WriteFile(filepath.Join(root, asset), archive.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.manifest != "" {
				write(filepath.Join(root, "checksums.txt"), tc.manifest)
			}
			// A closed PATH ensures the no-verifier case cannot accidentally use
			// a utility installed on the machine. Downloads are entirely local.
			for _, name := range []string{"mktemp", "rm", "mkdir", "cp", "chmod", "tar", "gzip", "install", "awk"} {
				path, err := exec.LookPath(name)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path, filepath.Join(bin, name)); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(bin, "uname"), "#!/bin/sh\ncase \"$1\" in -s) echo Linux;; -m) echo x86_64;; esac\n")
			write(filepath.Join(bin, "curl"), "#!/bin/sh\ncase \"$2\" in */checksums.txt) cp \"$FIXTURE/checksums.txt\" \"$4\";; */"+asset+") cp \"$FIXTURE/"+asset+"\" \"$4\";; *) exit 22;; esac\n")
			if tc.hashTool != "" {
				name := tc.hashTool
				body := "#!/bin/sh\nprintf '%s  %s\\n' '" + digest + "' 'archive'\n"
				if name == "broken" {
					name = "sha256sum"
					body = "#!/bin/sh\nexit 1\n"
				}
				write(filepath.Join(bin, name), body)
			}
			cmd := exec.CommandContext(t.Context(), "/bin/sh", "../install.sh")
			cmd.Env = []string{"PATH=" + bin, "PREFIX=" + prefix, "VERSION=v0.33.0", "FIXTURE=" + root}
			output, err := cmd.CombinedOutput()
			if (err == nil) != tc.success {
				t.Fatalf("success=%v err=%v\n%s", tc.success, err, output)
			}
			got, err := os.ReadFile(installed)
			if err != nil {
				t.Fatal(err)
			}
			want := "previous installation"
			if tc.success {
				want = binary
			}
			if string(got) != want {
				t.Fatalf("installed=%q want=%q", got, want)
			}
		})
	}
}
