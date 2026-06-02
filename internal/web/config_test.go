package web

import (
	"testing"
	"time"
)

func TestConfigResolveDefaults(t *testing.T) {
	c := Config{}.Resolve()
	if c.Host != "127.0.0.1" {
		t.Errorf("host = %q, want 127.0.0.1", c.Host)
	}
	if c.Port != 8080 {
		t.Errorf("port = %d, want 8080", c.Port)
	}
	if c.CookieTTL != 7*24*time.Hour {
		t.Errorf("cookie ttl = %v, want 168h", c.CookieTTL)
	}
	if !containsStr(c.AllowedOrigins, "http://127.0.0.1:8080") ||
		!containsStr(c.AllowedOrigins, "http://localhost:8080") {
		t.Errorf("default origins missing: %v", c.AllowedOrigins)
	}
}

func TestConfigResolveKeepsExplicit(t *testing.T) {
	c := Config{Host: "0.0.0.0", Port: 9000, CookieTTL: time.Hour,
		AllowedOrigins: []string{"https://app.test"}}.Resolve()
	if c.Port != 9000 || c.CookieTTL != time.Hour {
		t.Fatalf("explicit values overwritten: %+v", c)
	}
	if !containsStr(c.AllowedOrigins, "https://app.test") {
		t.Fatalf("user origin dropped: %v", c.AllowedOrigins)
	}
}

func TestConfigAddr(t *testing.T) {
	if a := (Config{Host: "127.0.0.1", Port: 8080}).Addr(); a != "127.0.0.1:8080" {
		t.Fatalf("addr = %q", a)
	}
}

func TestConfigValidateBindSafety(t *testing.T) {
	if err := (Config{Host: "0.0.0.0", Port: 8080}).Resolve().Validate(); err == nil {
		t.Fatal("want error binding 0.0.0.0 without password")
	}
	if err := (Config{Host: "0.0.0.0", Password: "x"}).Resolve().Validate(); err != nil {
		t.Fatalf("0.0.0.0 + password should be ok, got %v", err)
	}
	if err := (Config{}).Resolve().Validate(); err != nil {
		t.Fatalf("loopback default should be ok, got %v", err)
	}
}

func TestHostAndOriginAllowed(t *testing.T) {
	c := Config{AllowedOrigins: []string{"http://app.test:8080"}}
	if !c.hostAllowed("app.test:8080") {
		t.Error("host should be allowed")
	}
	if c.hostAllowed("evil.test:8080") {
		t.Error("foreign host should be rejected")
	}
	if !c.originAllowed("http://app.test:8080") {
		t.Error("origin should be allowed")
	}
	if c.originAllowed("http://evil.test:8080") {
		t.Error("foreign origin should be rejected")
	}
	if c.originAllowed("") {
		t.Error("empty origin must be rejected on POST")
	}
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
