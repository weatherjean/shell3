package shell3

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
)

func pollTestSession(t *testing.T) (*Runtime, *Session) {
	t.Helper()
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnableJobPolling(); err != nil {
		t.Fatal(err)
	}
	return rt, s
}

func startPollTestJob(t *testing.T, rt *Runtime, s *Session) string {
	t.Helper()
	id, err := rt.jobs.startCommand(s, "sleep 60", t.TempDir(), []string{"sleep", "60"}, nil, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestJobPollOneShotOwnershipAndRearm(t *testing.T) {
	rt, s := pollTestSession(t)
	id := startPollTestJob(t, rt, s)
	other, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.PollJob(id, time.Minute); err == nil {
		t.Fatal("another session rearmed this job")
	}
	if got := other.TakeDueJobPolls(time.Now().Add(time.Hour)); len(got) != 0 {
		t.Fatalf("another session consumed checks: %+v", got)
	}
	if got := s.TakeDueJobPolls(time.Now()); len(got) != 0 {
		t.Fatal("poll fired early")
	}
	due := s.TakeDueJobPolls(time.Now().Add(time.Hour))
	if len(due) != 1 || due[0].JobID != id || due[0].LogPath == "" {
		t.Fatalf("due = %+v", due)
	}
	if got := s.TakeDueJobPolls(time.Now().Add(time.Hour)); len(got) != 0 {
		t.Fatal("one-shot poll repeated")
	}
	first, err := s.PollJob(id, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	later, err := s.PollJob(id, 5*time.Minute)
	if err != nil || !later.PollAt.After(first.PollAt) {
		t.Fatalf("replacement: %+v %v", later, err)
	}
	if got := s.TakeDueJobPolls(first.PollAt); len(got) != 0 {
		t.Fatal("replaced deadline still fired")
	}
	if got := s.TakeDueJobPolls(later.PollAt); len(got) != 1 {
		t.Fatalf("replacement never fired: %+v", got)
	}
	if !strings.Contains(JobPollPrompt(due), "scheduled progress check") {
		t.Fatal("check-in does not identify its host origin")
	}
}

func TestJobPollCompletionAndCancellation(t *testing.T) {
	for _, mode := range []string{"success", "failure", "start-failure", "superstop", "shutdown", "clear", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			rt, s := pollTestSession(t)
			var id string
			var err error
			switch mode {
			case "success", "failure":
				command := "exit 0"
				if mode == "failure" {
					command = "exit 7"
				}
				id, err = rt.jobs.startCommand(s, command, t.TempDir(), []string{"sh", "-c", command}, nil, time.Minute)
				rt.jobs.wait()
			case "start-failure":
				_, err = rt.jobs.startCommand(s, "missing", t.TempDir(), []string{"/no/such/program"}, nil, time.Minute)
				if err == nil {
					t.Fatal("expected launch failure")
				}
				err = nil
			default:
				id = startPollTestJob(t, rt, s)
				switch mode {
				case "superstop":
					rt.KillAllForStop()
				case "shutdown":
					rt.jobs.cancelAll()
				case "clear":
					s.ClearJobPolls()
				case "cancel":
					_, err = s.PollJob(id, 0)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "success" || mode == "failure" {
				if s.RunningJobs() != 0 {
					t.Fatal("completed check retained a live job slot")
				}
				if got := s.TakeDueJobPolls(time.Now()); len(got) != 0 {
					t.Fatal("completion fired the check early")
				}
				if got := s.TakeDueJobPolls(time.Now().Add(24 * time.Hour)); len(got) != 1 || got[0].JobID != id || got[0].LogPath == "" {
					t.Fatalf("lost promised check after %s: %+v", mode, got)
				}
			}
			if got := s.TakeDueJobPolls(time.Now().Add(24 * time.Hour)); len(got) != 0 {
				t.Fatalf("unexpected or repeated check after %s: %+v", mode, got)
			}
			if mode == "clear" || mode == "cancel" {
				if s.RunningJobs() != 1 {
					t.Fatal("cancelling a check stopped its job")
				}
			} else if _, err := s.PollJob(id, time.Minute); err == nil {
				t.Fatal("rearmed a finished or stopped command")
			}
		})
	}
}

func TestCompletedJobCheckOwnershipAndCancellation(t *testing.T) {
	for _, mode := range []string{"cancel", "clear", "superstop", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			rt, s := pollTestSession(t)
			id, err := rt.jobs.startCommand(s, "true", t.TempDir(), []string{"sh", "-c", "exit 0"}, nil, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			rt.jobs.wait()
			other, err := rt.Session(SessionOpts{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := other.PollJob(id, 0); err == nil {
				t.Fatal("another session cancelled the check")
			}
			other.ClearJobPolls()
			if got := other.TakeDueJobPolls(time.Now().Add(time.Hour)); len(got) != 0 {
				t.Fatal("another session consumed the check")
			}
			if _, err := s.PollJob(id, time.Minute); err == nil {
				t.Fatal("rearmed completed command")
			}
			switch mode {
			case "cancel":
				if _, err := s.PollJob(id, 0); err != nil {
					t.Fatal(err)
				}
			case "clear":
				s.ClearJobPolls()
			case "superstop":
				rt.KillAllForStop()
			case "shutdown":
				rt.jobs.cancelAll()
			}
			if got := s.TakeDueJobPolls(time.Now().Add(time.Hour)); len(got) != 0 {
				t.Fatalf("stale completed check after %s", mode)
			}
		})
	}
}

func TestCompletionDoesNotCreateUnrequestedCheck(t *testing.T) {
	rt, s := pollTestSession(t)
	if _, err := rt.jobs.startCommand(s, "true", t.TempDir(), []string{"sh", "-c", "exit 0"}, nil); err != nil {
		t.Fatal(err)
	}
	rt.jobs.wait()
	if got := s.TakeDueJobPolls(time.Now().Add(24 * time.Hour)); len(got) != 0 {
		t.Fatalf("completion created an unrequested turn: %+v", got)
	}
}

func TestJobPollToolAndReload(t *testing.T) {
	rt, s := pollTestSession(t)
	id := startPollTestJob(t, rt, s)
	tool := s.JobPollTool()
	for _, raw := range []string{
		`{"action":"poll","job_id":"` + id + `","poll_in":"0s"}`,
		`{"action":"poll","poll_in":"2m"}`,
		`{"action":"cancel_poll","job_id":"` + id + `","poll_in":null}`,
		`{"action":"poll","job_id":"` + id + `","poll_in":"2m","unknown":true}`,
	} {
		if _, err := tool.Handler(t.Context(), raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	raw := `{"action":"poll","job_id":"` + id + `","poll_in":"3m"}`
	out, err := tool.Handler(t.Context(), raw)
	var result map[string]any
	if err != nil || json.Unmarshal([]byte(out), &result) != nil || result["ok"] != true {
		t.Fatalf("rearm = %s, %v", out, err)
	}
	if err := rt.ReloadConfig(func(SessionOpts) (chat.Config, error) {
		cfg := fakeCfg("reloaded")()
		cfg.Store = rt.Store()
		return cfg, nil
	}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	capable := s.turnConfigLocked().StartBashBgPolled != nil
	s.mu.Unlock()
	if !capable {
		t.Fatal("reload dropped poll capability")
	}
	if due := s.TakeDueJobPolls(time.Now().Add(time.Hour)); len(due) != 1 {
		t.Fatal("reload lost pending check")
	}
}
