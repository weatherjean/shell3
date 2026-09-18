package lispconfig

import (
	"strings"
	"testing"
)

func TestScheduleFailureDestination(t *testing.T) {
	base := `(shell3 (version 1) (schedule daily
 (cron "0 8 * * *") (timezone "UTC") (run (wrkfile "daily.wrk.lisp"))
 (output "report.md") (timeout "1m") (notify "quiet") %s))`
	for _, tc := range []struct{ name, form, want string }{
		{"default", "", ""},
		{"override", `(notify-failure "main")`, "main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse("test", []byte(strings.Replace(base, "%s", tc.form, 1)))
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Schedules[0]; got.Notify != "quiet" || got.NotifyFailure != tc.want {
				t.Fatalf("schedule = %+v", got)
			}
		})
	}
	for _, form := range []string{`(notify-failure "")`, `(notify-failure "  ")`, `(notify-failure)`, `(notify-failure "a" "b")`, `(notify-failure "main") (notify-failure "other")`} {
		if _, err := Parse("test", []byte(strings.Replace(base, "%s", form, 1))); err == nil {
			t.Fatalf("accepted %s", form)
		}
	}
}
