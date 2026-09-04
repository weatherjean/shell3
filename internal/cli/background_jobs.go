package cli

import (
	"context"
	"fmt"
	"io"

	"charm.land/lipgloss/v2"

	"github.com/weatherjean/shell3/internal/shell3"
)

var backgroundMeta = lipgloss.NewStyle().Foreground(bannerMuted)

// WaitForBackgroundJobs keeps a one-shot process alive until its background
// commands finish. Completions go to the filesystem inbox and never cause
// another model turn in the CLI.
func WaitForBackgroundJobs(ctx context.Context, w io.Writer, rt *shell3.Runtime, sess *shell3.Session) error {
	announced := 0
	for {
		running := sess.RunningJobs()
		if running == 0 {
			return nil
		}
		if running != announced {
			fmt.Fprintln(w, backgroundMeta.Render(fmt.Sprintf("· waiting for %d background task(s)…", running)))
			announced = running
		}
		if err := waitForJobChangeFn(ctx, rt); err != nil {
			return err
		}
	}
}

func waitForJobChange(ctx context.Context, rt *shell3.Runtime) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-rt.JobCompletions():
		return nil
	}
}

// Tests replace this seam to control completion timing.
var waitForJobChangeFn = waitForJobChange
