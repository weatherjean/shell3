//go:build unix

package webui

import "testing"

// The bell's read state lives server-side: opening the bell marks everything
// seen, and the replay a page reload triggers must come back read — a badge
// that resurrects itself teaches people to ignore it.
func TestNoticeSeenStateSurvivesReplay(t *testing.T) {
	srv := newTestServer(t, "ok")
	srv.publishNotification(notification{Title: "one", Body: "b"})
	srv.publishNotification(notification{Title: "two", Body: "b"})

	for _, n := range srv.recentNotices() {
		if n.Read {
			t.Fatalf("%s should be unread before the bell is opened", n.Title)
		}
	}

	srv.markNoticesSeen()
	for _, n := range srv.recentNotices() {
		if !n.Read {
			t.Fatalf("%s should replay as read after being seen", n.Title)
		}
	}

	// New arrivals are unread; the seen ones stay read.
	srv.publishNotification(notification{Title: "three", Body: "b"})
	notes := srv.recentNotices()
	if len(notes) != 3 || !notes[0].Read || !notes[1].Read || notes[2].Read {
		t.Fatalf("read flags wrong after a new arrival: %+v", notes)
	}
}

func TestNoticeDismissAndClear(t *testing.T) {
	srv := newTestServer(t, "ok")
	srv.publishNotification(notification{Title: "one", Body: "b"})
	srv.publishNotification(notification{Title: "two", Body: "b"})

	id := srv.recentNotices()[0].ID
	srv.dismissNotice(id)
	notes := srv.recentNotices()
	if len(notes) != 1 || notes[0].Title != "two" {
		t.Fatalf("dismiss should remove exactly the named entry, got %+v", notes)
	}

	srv.clearNotices()
	if got := srv.recentNotices(); len(got) != 0 {
		t.Fatalf("clear should empty the buffer, got %+v", got)
	}
	// Clearing counts as seeing: nothing published before the clear may come
	// back unread, and the marker sits at the newest seq.
	srv.publishNotification(notification{Title: "after", Body: "b"})
	notes = srv.recentNotices()
	if len(notes) != 1 || notes[0].Read {
		t.Fatalf("a post-clear arrival should be the only, unread entry: %+v", notes)
	}
}
