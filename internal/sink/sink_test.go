package sink

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// readLines reads every complete line from a sink file.
func readLines(t *testing.T, path string) []Notification {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open sink: %v", err)
	}
	defer f.Close()
	var out []Notification
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var n Notification
		if err := json.Unmarshal(sc.Bytes(), &n); err != nil {
			t.Fatalf("decode line %q: %v", sc.Text(), err)
		}
		out = append(out, n)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}

func intp(i int) *int { return &i }

func TestAppend_roundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "main.jsonl") // parent created lazily
	n := Notification{Kind: "bg_done", ID: "bg_9c", Exit: intp(0), Log: "/tmp/x.log", Cmd: "npx tsc"}
	if err := Append(path, n); err != nil {
		t.Fatalf("append: %v", err)
	}
	got := readLines(t, path)
	if len(got) != 1 {
		t.Fatalf("want 1 line, got %d", len(got))
	}
	g := got[0]
	if g.Kind != "bg_done" || g.ID != "bg_9c" || g.Log != "/tmp/x.log" || g.Cmd != "npx tsc" {
		t.Fatalf("round-trip mismatch: %+v", g)
	}
	if g.Exit == nil || *g.Exit != 0 {
		t.Fatalf("exit not preserved (a genuine 0 must survive): %+v", g.Exit)
	}
	if g.TS == "" {
		t.Fatalf("TS should be stamped when left empty")
	}
}

// TestAppend_omitemptyDropsZeroExitWhenUnset verifies the Exit pointer lets us
// distinguish "exit code 0" (kept) from "no exit code" (dropped). A non-bg_done
// notification with a nil Exit must not serialize an "exit" key.
func TestAppend_omitemptyExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.jsonl")
	if err := Append(path, Notification{Kind: "agent_done", ID: "a3f", Status: "ok"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"exit"`) {
		t.Fatalf("nil Exit must be omitted, got: %s", data)
	}
}

func TestAppend_emptyPathNoop(t *testing.T) {
	if err := Append("", Notification{Kind: "bg_done"}); err != nil {
		t.Fatalf("empty path should be a no-op, got: %v", err)
	}
}

// TestAppend_concurrentAtomic hammers one sink from many goroutines; every line
// must arrive intact (no interleaving) and decode cleanly.
func TestAppend_concurrentAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.jsonl")
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := Append(path, Notification{Kind: "bg_done", ID: "bg_" + itoa(i), Exit: intp(i)}); err != nil {
				t.Errorf("append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	got := readLines(t, path)
	if len(got) != n {
		t.Fatalf("want %d intact lines, got %d", n, len(got))
	}
}

// itoa is a tiny local int→string to avoid pulling strconv into the test for
// one call.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
