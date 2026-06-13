package shell3

import (
	"os"
	"testing"

	"github.com/weatherjean/shell3/internal/store"
)

func TestRouteReport_LiveParentGetsSocket(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	parent, _ := st.StartSession("", "")
	// Use this test process's pid so the liveness probe sees a running process.
	_ = st.SetLiveness(parent, os.Getpid(), "/tmp/p.sock", "live")

	route, sock, pid := routeReport(st, parent)
	if route != routeSocket || sock != "/tmp/p.sock" || pid != os.Getpid() {
		t.Fatalf("got route=%v sock=%q pid=%d; want socket /tmp/p.sock pid=%d", route, sock, pid, os.Getpid())
	}
}

func TestRouteReport_DormantParentGetsInboxRevive(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	parent, _ := st.StartSession("", "")
	_ = st.SetLiveness(parent, 0, "", "dormant")

	route, _, _ := routeReport(st, parent)
	if route != routeRevive {
		t.Fatalf("got route=%v; want revive", route)
	}
}

// TestRouteReport_CrashedLiveParentGoesToRevive: a parent stuck "live" whose
// process is dead must NOT be routed to its (dead) socket — it falls to revive,
// carrying the dead pid so reportTo can reclaim it. Regression guard for the
// crash-strand hole.
func TestRouteReport_CrashedLiveParentGoesToRevive(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	parent, _ := st.StartSession("", "")
	const deadPID = 2147483646 // not a running process
	_ = st.SetLiveness(parent, deadPID, "/tmp/p.sock", "live")

	route, sock, pid := routeReport(st, parent)
	if route != routeRevive || sock != "" || pid != deadPID {
		t.Fatalf("got route=%v sock=%q pid=%d; want revive, no sock, pid=%d", route, sock, pid, deadPID)
	}
}
