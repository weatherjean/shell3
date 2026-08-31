package dash

import (
	"testing"
	"time"
)

func TestMintShapeAndDistinct(t *testing.T) {
	ts := NewTokenStore(nil)
	a, b := ts.Mint(), ts.Mint()
	if len(a) != 64 || len(b) != 64 {
		t.Fatalf("token length = %d, %d, want 64", len(a), len(b))
	}
	if a == b {
		t.Fatal("two minted tokens are identical")
	}
	for _, r := range a {
		hex := r >= '0' && r <= '9' || r >= 'a' && r <= 'f'
		if !hex {
			t.Fatalf("token %q is not lowercase hex", a)
		}
	}
}

func TestValid(t *testing.T) {
	ts := NewTokenStore(nil)
	tok := ts.Mint()
	if !ts.Valid(tok) {
		t.Fatal("fresh token is invalid")
	}
	if ts.Valid("") || ts.Valid("garbage") || ts.Valid(tok+"x") {
		t.Fatal("junk token accepted")
	}
}

func TestExpiry(t *testing.T) {
	now := time.Unix(1000000, 0)
	clock := func() time.Time { return now }
	ts := NewTokenStore(clock)
	tok := ts.Mint()
	now = now.Add(59 * time.Minute)
	if !ts.Valid(tok) {
		t.Fatal("token invalid at 59m")
	}
	now = now.Add(2 * time.Minute)
	if ts.Valid(tok) {
		t.Fatal("token still valid at 61m")
	}
}

func TestConcurrentTokensAndPrune(t *testing.T) {
	now := time.Unix(1000000, 0)
	ts := NewTokenStore(func() time.Time { return now })
	a := ts.Mint()
	now = now.Add(10 * time.Minute)
	b := ts.Mint()
	if !ts.Valid(a) || !ts.Valid(b) {
		t.Fatal("concurrent tokens should both be valid")
	}
	now = now.Add(55 * time.Minute)
	c := ts.Mint()
	if ts.Valid(a) {
		t.Fatal("expired token accepted")
	}
	if !ts.Valid(b) || !ts.Valid(c) {
		t.Fatal("live tokens dropped")
	}
	if n := ts.count(); n != 2 {
		t.Fatalf("count = %d after prune, want 2", n)
	}
}
