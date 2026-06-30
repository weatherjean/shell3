package shell3

import "testing"

func TestTranscriptPath(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{
			"subagent run command",
			`/bin/shell3 run --config /c/shell3.lua --agent explorer --out /root/.shell3_project/agents/explore1.jsonl --inbox /root/.shell3_project/inbox.jsonl --parent-session p --id explore1 --prompt "go"`,
			"/root/.shell3_project/agents/explore1.jsonl",
		},
		{"--out= form", `shell3 run --out=/a/b.jsonl --id x`, "/a/b.jsonl"},
		{"plain bash_bg has no --out", `npm run build`, ""},
		{"--out at end with no value", `shell3 run --id x --out`, ""},
		{"empty", "", ""},
		// A non-shell3 command that merely uses --out is NOT a transcript: it must
		// fall back to its stdout log, not render an empty/garbled JSONL view.
		{"ffmpeg --out is not a transcript", `ffmpeg -i in.mov --out=out.mp4`, ""},
		{"arbitrary tool --out is not a transcript", `mytool --out report.csv`, ""},
		{"non-run shell3 subcommand", `shell3 jobs --out=/x.jsonl`, ""},
		{"env-prefixed subagent run", `FOO=bar /bin/shell3 run --out /x.jsonl --parent-session p`, "/x.jsonl"},
	}
	for _, c := range cases {
		if got := transcriptPath(c.cmd); got != c.want {
			t.Errorf("%s: transcriptPath(%q) = %q, want %q", c.name, c.cmd, got, c.want)
		}
	}
}
