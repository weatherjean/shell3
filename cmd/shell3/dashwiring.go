//go:build unix

package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/dash"
	"github.com/weatherjean/shell3/internal/render"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/telegram"
)

// dashURLFileName is where the dash's BASE URL lives, in the config dir. The
// host seeds it with the localhost address; the dash-exposing skill's agent
// overwrites it with a tunnel/tailnet URL. It holds a base URL only — never a
// token (tokens are memory-only and minted per /dash).
const dashURLFileName = "dash_url.txt"

// wireDash starts the read-only web dash for b and returns its closer (a
// no-op when the dash is disabled or failed to bind). Port 0 in the config
// disables; a bind failure is a startup warning, never fatal — the bot runs
// dashless and /dash says so.
func wireDash(b *telegram.Bot, rt *shell3.Runtime, cronSess *shell3.Session,
	cronStatusFn func() []cron.JobStatus, cronCostFn func() map[string]runs.JobCost) func() {
	port := rt.Parts().DashPort()
	if port == 0 {
		return func() {}
	}
	srv := dash.New(port, dash.Sources{
		RunsRoot:  rt.Parts().RunsRoot(),
		ConfigDir: rt.Parts().ConfigDir(),
		// Live state resolved per request — Session.Jobs reports the whole
		// job runtime, so the pinned cron session is as good a window as any,
		// and it survives /reload (its Parts ride the reload's parking rules).
		IndexHTML: func(tok string) string {
			return render.DashIndexHTML(b.LiveSession(), rt, version,
				cronSess.Jobs(), cronStatusFn(), cronCostFn(), tok) +
				render.RoomsSectionHTML(dashRooms(b), tok) +
				render.TextSectionHTML("Inbox", b.Inbox())
		},
		CronStatus: cronStatusFn,
		CronCosts:  cronCostFn,
	}, rt.Parts().Log())
	if err := srv.Start(); err != nil {
		// Never stdout: `shell3 serve` speaks JSONL there and the first line
		// must be the hello. The app log is also where /dash's down-reply
		// points the user ("check the app log").
		rt.Parts().Log().Warn("dash disabled: could not bind", "port", port, "err", err)
		return func() {}
	}
	urlFile := filepath.Join(rt.Parts().ConfigDir(), dashURLFileName)
	if err := seedDashURLFile(urlFile, srv.Addr()); err != nil {
		rt.Parts().Log().Warn("dash url file not written", "path", urlFile, "err", err)
	}
	b.SetDash(func() (string, error) {
		return dashMintURL(urlFile, srv), nil
	})
	// Info, not stdout: serve's stdout is the JSONL wire.
	rt.Parts().Log().Info("dash listening", "addr", srv.Addr())
	return func() { _ = srv.Close() }
}

// seedDashURLFile writes the localhost base URL when the file is absent — or
// when it holds a STALE loopback URL (the port changed between runs).
// Loopback content is host-owned by construction; a tunnel URL the exposure
// agent wrote is theirs and is never touched.
func seedDashURLFile(urlFile, addr string) error {
	want := "http://" + addr
	data, err := os.ReadFile(urlFile)
	if err == nil {
		cur := strings.TrimSpace(string(data))
		u, perr := url.Parse(cur)
		hostOwned := perr == nil && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")
		if !hostOwned || cur == want {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(urlFile, []byte(want+"\n"), 0o644)
}

// dashMintURL composes the /dash reply: the base URL from the config-dir file
// (falling back to the listener's own address when the file is missing or not
// a parseable http(s) URL) plus a fresh token.
func dashMintURL(urlFile string, srv *dash.Server) string {
	base := "http://" + srv.Addr()
	if data, err := os.ReadFile(urlFile); err == nil {
		if u := strings.TrimSpace(string(data)); isHTTPURL(u) {
			base = strings.TrimRight(u, "/")
		}
	}
	return base + "/?t=" + srv.Mint()
}

// isHTTPURL reports whether s parses as an absolute http(s) URL with a host.
func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// dashRooms projects the bot's live rooms onto the render package's row type.
// The two structs are deliberately separate: internal/render lays out HTML
// for every front-end and must not import a transport.
func dashRooms(b *telegram.Bot) []render.RoomInfo {
	rooms := b.Rooms()
	out := make([]render.RoomInfo, 0, len(rooms))
	for _, r := range rooms {
		out = append(out, render.RoomInfo{
			ChatID: r.ChatID, Title: r.Title, Busy: r.Busy,
			Jobs: r.Jobs, Queued: r.Queued, SessionID: r.SessionID,
		})
	}
	return out
}
