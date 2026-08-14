//go:build unix

package telegram

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
)

func TestCommand_Run(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	fired := ""
	b.SetJobRunner(func(name string) error { fired = name; return nil })
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/run nightly"})
	if fired != "nightly" {
		t.Fatalf("expected /run to fire job 'nightly', fired %q", fired)
	}
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "nightly") {
		t.Fatalf("expected an ack mentioning the job, got %v", fc.sentTexts())
	}
}

func TestCommand_RunNoRunner(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/run x"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "no scheduled jobs") {
		t.Fatalf("expected a no-jobs reply, got %v", fc.sentTexts())
	}
}

// /run with no argument replies with usage instead of calling the runner —
// the usage text wraps <job> in a code span (like /cancel's usage) so the
// markdown->HTML renderer doesn't swallow it as a bogus tag.
func TestCommand_RunMissingArg(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	called := false
	b.SetJobRunner(func(name string) error { called = true; return nil })

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/run"})
	if called {
		t.Fatal("run must not be invoked with no job name")
	}
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "usage: /run") {
		t.Fatalf("expected the usage reply, got %v", fc.sentTexts())
	}
}

func TestCommand_Reload(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	called := false
	b.SetReloader(func() (shell3.ReloadResult, error) {
		called = true
		return shell3.ReloadResult{Agents: 3, Jobs: 1}, nil
	})
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/reload"})
	if !called {
		t.Fatal("expected /reload to invoke the reloader")
	}
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "reloaded") {
		t.Fatalf("expected a success reply, got %v", fc.sentTexts())
	}
}

func TestCommand_ReloadNoReloader(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/reload"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "reload not available") {
		t.Fatalf("expected unavailable reply, got %v", fc.sentTexts())
	}
}

// TestCommand_UnknownDropped pins that a removed command (/clear, /set, …) is no
// longer routed — the trimmed command set answers "unknown command".
func TestCommand_UnknownDropped(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/clear"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "unknown command") {
		t.Fatalf("expected /clear to be an unknown command now, got %v", fc.sentTexts())
	}
}

// /jobs renders the render.Jobs view over whatever the jobs-source closure
// returns — the bot does no formatting of its own beyond the shared
// render/document delivery path.
func TestJobsCommandListsRunning(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetJobsSource(func() []shell3.JobInfo {
		return []shell3.JobInfo{{ID: "sub1", Kind: shell3.JobSubagent, Agent: "explorer", Cmd: "do the thing"}}
	}, nil)

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/jobs"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "sub1") || !strings.Contains(all, "explorer") {
		t.Fatalf("expected the listing to be relayed, got %v", fc.sentTexts())
	}
}

// /jobs with nothing running relays render.Jobs' empty-state text.
func TestJobsCommandEmpty(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetJobsSource(func() []shell3.JobInfo { return nil }, nil)

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/jobs"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "No background jobs") {
		t.Fatalf("expected the empty-state reply, got %v", fc.sentTexts())
	}
}

// /jobs with no source wired says so rather than silently rendering nothing.
func TestJobsCommandNoSource(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/jobs"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "job control not available") {
		t.Fatalf("expected unavailable reply, got %v", fc.sentTexts())
	}
}

// /job <id> renders render.JobDetail for a matching job, including its
// transcript.
func TestJobDetailCommand(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetJobsSource(func() []shell3.JobInfo {
		return []shell3.JobInfo{{ID: "sub1", Kind: shell3.JobSubagent, Agent: "explorer", Cmd: "do the thing"}}
	}, func(id string) string { return "transcript for " + id })

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/job sub1"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "sub1") || !strings.Contains(all, "transcript for sub1") {
		t.Fatalf("expected job detail rendered, got %v", fc.sentTexts())
	}
}

// /job with an id that matches no job says so instead of rendering a blank
// detail view.
func TestJobDetailCommandUnknownID(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetJobsSource(func() []shell3.JobInfo { return nil }, nil)

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/job bogus"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, `no such job "bogus"`) {
		t.Fatalf("expected a no-such-job reply, got %v", fc.sentTexts())
	}
}

// /job with no argument replies with usage instead of scanning the job list.
func TestJobDetailCommandMissingArg(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	called := false
	b.SetJobsSource(func() []shell3.JobInfo { called = true; return nil }, nil)

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/job"})
	if called {
		t.Fatal("the jobs source must not be consulted with no id")
	}
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "usage: /job") {
		t.Fatalf("expected the usage reply, got %v", fc.sentTexts())
	}
}

// /cancel <id> relays the cancel closure's confirmation.
func TestCancelCommandOK(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	cancelled := ""
	b.SetJobControl(func(id string) error {
		cancelled = id
		return nil
	})

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/cancel sub1"})
	if cancelled != "sub1" {
		t.Fatalf("expected /cancel to invoke cancel('sub1'), got %q", cancelled)
	}
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "sub1") {
		t.Fatalf("expected a confirmation mentioning the id, got %v", fc.sentTexts())
	}
}

// /cancel with an unknown id surfaces the runtime's "no such task" error text
// verbatim — no bot-side reinterpretation.
func TestCancelCommandUnknownID(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetJobControl(func(id string) error {
		return fmt.Errorf("no such task %q", id)
	})

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/cancel bogus"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, `no such task "bogus"`) {
		t.Fatalf("expected the no-such-task error text, got %v", fc.sentTexts())
	}
}

// /cancel with no argument replies with usage instead of calling cancel.
func TestCancelCommandMissingArg(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	called := false
	b.SetJobControl(func(id string) error {
		called = true
		return nil
	})

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/cancel"})
	if called {
		t.Fatal("cancel must not be invoked with no id")
	}
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "usage: /cancel") {
		t.Fatalf("expected the usage reply, got %v", fc.sentTexts())
	}
}

// /status renders render.Status inline when it's small — nil session (no
// live chat session) and a wired version are enough for a document.
func TestStatusCommand_InlineWhenSmall(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetVersion("v0.2.0-test")

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/status"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "shell3 status") || !strings.Contains(all, "v0.2.0-test") {
		t.Fatalf("expected status text inline, got %v", fc.sentTexts())
	}
	if _, ok := fc.lastDoc(); ok {
		t.Fatal("a small status view must not be sent as a document")
	}
}

// /cron with nothing declared relays render.Cron's empty-state text.
func TestCronCommand_Empty(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/cron"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "No cron jobs") {
		t.Fatalf("expected the empty-state cron reply, got %v", fc.sentTexts())
	}
}

// /cron renders the runtime's declared jobs plus the wired last-run history.
func TestCronCommand_RendersJobs(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	rt.SetCronForTest([]shell3.CronJob{
		{Name: "nightly", Schedule: "0 3 * * *", Agent: "explorer", Prompt: "sweep the logs"},
	})
	b := newBot(t, fc, rt)
	when := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	b.SetCronLastRuns(func() map[string]time.Time { return map[string]time.Time{"nightly": when} })

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/cron"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "nightly") || !strings.Contains(all, "sweep the logs") || !strings.Contains(all, "2026-07-20") {
		t.Fatalf("expected the cron job and last-run time rendered, got %v", fc.sentTexts())
	}
}

// /runs with no runs root wired says so.
func TestRunsCommand_NotConfigured(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/runs"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "runs not available") {
		t.Fatalf("expected unavailable reply, got %v", fc.sentTexts())
	}
}

// seedRuns stores n minimal sessions under a fresh root and wires it into b,
// returning the root and ids oldest-first.
func seedRuns(t *testing.T, b *Bot, n int) (string, []string) {
	t.Helper()
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	ids := make([]string, n)
	for i := range ids {
		id, err := st.NewSession(runs.Meta{Workdir: "/tmp/work", Model: "kimi-k2"})
		if err != nil {
			t.Fatalf("new session: %v", err)
		}
		if err := st.AppendMessage(id, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("prompt %d", i)}); err != nil {
			t.Fatalf("append: %v", err)
		}
		ids[i] = id
	}
	b.SetRunsRoot(root)
	return root, ids
}

// /runs with no arg posts page 1 inline: tappable /run_N entries, newest
// first, with a paging footer.
func TestRunsCommand_ListsPageOne(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	seedRuns(t, b, 10)

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/runs"})
	all := strings.Join(fc.sentTexts(), "\n")
	for _, want := range []string{"/run_1", "/run_8", "prompt 9", "page 1/2", "/runs 2"} {
		if !strings.Contains(all, want) {
			t.Fatalf("page 1 missing %q, got %v", want, fc.sentTexts())
		}
	}
	if strings.Contains(all, "/run_9") {
		t.Fatalf("page 1 leaked a page-2 entry: %v", fc.sentTexts())
	}
	if _, ok := fc.lastDoc(); ok {
		t.Fatal("the runs page must be an inline message, not a document — commands in documents aren't tappable")
	}
}

// /runs 2 shows the second page.
func TestRunsCommand_PageTwo(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	seedRuns(t, b, 10)

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/runs 2"})
	all := strings.Join(fc.sentTexts(), "\n")
	for _, want := range []string{"/run_9", "/run_10", "page 2/2"} {
		if !strings.Contains(all, want) {
			t.Fatalf("page 2 missing %q, got %v", want, fc.sentTexts())
		}
	}
}

// A page past the end answers with the valid range instead of an empty page.
func TestRunsCommand_PagePastEnd(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	seedRuns(t, b, 3)

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/runs 7"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "page 7 of 1") || !strings.Contains(all, "/runs 1 is the last") {
		t.Fatalf("expected the out-of-range reply, got %v", fc.sentTexts())
	}
}

// Tapping /run_N after /runs replays that run via the stored index — /run_1 is
// the newest. An @botname suffix (autocomplete/group taps) routes the same.
func TestRunTap_ReplaysIndexedRun(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	_, ids := seedRuns(t, b, 3)

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/runs"})
	fc.reset()
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/run_1@shellibot"})
	newest := ids[len(ids)-1]
	// The replay is an HTML document; the message beside it names the run, and
	// the run's content lives in the file.
	doc, ok := fc.lastDoc()
	if !ok || doc.filename != "run-"+newest+".html" {
		t.Fatalf("expected /run_1 to send run-%s.html, got %+v ok=%v", newest, doc.filename, ok)
	}
	if !strings.Contains(string(doc.data), "prompt 2") {
		t.Fatalf("replayed page does not contain the run's prompt")
	}
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), newest) {
		t.Fatalf("expected the caption to name run %s, got %v", newest, fc.sentTexts())
	}
}

// A tap with no stored index (bot restarted, or /runs never rendered) errors
// cleanly — it must never open a re-derived guess.
func TestRunTap_StaleIndex(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	seedRuns(t, b, 3)

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/run_2"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "run /runs again") {
		t.Fatalf("expected the stale-index reply, got %v", fc.sentTexts())
	}
}

// /runs <id> replays one stored session in full; when the rendered replay
// blows past the inline threshold it is sent as a run-<id>.md document plus a
// capped summary.
func TestRunsCommand_ReplaysOneAsDocumentWhenLarge(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)

	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	id, err := st.NewSession(runs.Meta{Workdir: "/tmp/work", Model: "kimi-k2"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := st.AppendMessage(id, llm.Message{Role: llm.RoleUser, Content: strings.Repeat("x", 5000)}); err != nil {
		t.Fatalf("append: %v", err)
	}
	b.SetRunsRoot(root)

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/runs " + id})
	doc, ok := fc.lastDoc()
	if !ok || doc.filename != "run-"+id+".html" {
		t.Fatalf("expected a run-%s.html document, got %+v ok=%v", id, doc.filename, ok)
	}
	all := strings.Join(fc.sentTexts(), "\n")
	if all == "" || len(all) > 400 {
		t.Fatalf("expected a short caption alongside the document, got %d bytes", len(all))
	}
}

// /runs <id> with an unknown id surfaces render.RunReplay's error text.
func TestRunsCommand_UnknownID(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)

	root := t.TempDir()
	if _, err := runs.Open(root); err != nil {
		t.Fatalf("open store: %v", err)
	}
	b.SetRunsRoot(root)

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/runs bogus"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "no such run") {
		t.Fatalf("expected the no-such-run error text, got %v", fc.sentTexts())
	}
}

// Telegram appends "@yourbot" to a command typed in a group (and some clients
// do it after an autocomplete tap in a private chat), so the suffix must be
// stripped before routing.
func TestCommand_BotnameSuffixIsStripped(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/reload@my_shell3_bot"})
	joined := strings.Join(fc.sentTexts(), "\n")
	if strings.Contains(joined, "unknown command") {
		t.Fatalf("/reload@botname must route to /reload, got %v", fc.sentTexts())
	}
	if !strings.Contains(joined, "reload not available") {
		t.Fatalf("expected the /reload reply, got %v", fc.sentTexts())
	}
}

// /quiet reports and flips the persisted toggle; junk gets usage.
func TestQuietCommand(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))
	qs := &QuietStore{Path: filepath.Join(t.TempDir(), "quiet_mode.json")}
	b.SetQuiet(qs)
	ctx := context.Background()

	b.handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/quiet"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "quiet is off")
	})

	b.handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/quiet on"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "quiet is on")
	})
	if !qs.Get() {
		t.Error("/quiet on did not persist")
	}

	b.handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/quiet off"})
	waitFor(t, func() bool {
		return strings.Count(strings.Join(fc.sentTexts(), "\n"), "quiet is off") >= 2
	})
	if qs.Get() {
		t.Error("/quiet off did not persist")
	}

	b.handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/quiet sideways"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "usage: /quiet on|off")
	})
	if qs.Get() {
		t.Error("junk arg flipped the store")
	}
}
