package shell3

import (
	"testing"

	"github.com/weatherjean/shell3/internal/store"
)

func TestRouteReport_LiveParentGetsSocket(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	parent, _ := st.StartSession("", "")
	_ = st.SetLiveness(parent, 123, "/tmp/p.sock", "live")

	route, sock := routeReport(st, parent)
	if route != routeSocket || sock != "/tmp/p.sock" {
		t.Fatalf("got route=%v sock=%q; want socket /tmp/p.sock", route, sock)
	}
}

func TestRouteReport_DormantParentGetsInboxRevive(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	parent, _ := st.StartSession("", "")
	_ = st.SetLiveness(parent, 0, "", "dormant")

	route, _ := routeReport(st, parent)
	if route != routeRevive {
		t.Fatalf("got route=%v; want revive", route)
	}
}
