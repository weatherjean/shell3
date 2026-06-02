package web

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHost      = "127.0.0.1"
	defaultPort      = 8080
	defaultCookieTTL = 7 * 24 * time.Hour
)

// Config is the resolved web-serving configuration. It owns all defaulting,
// validation, and request-allow logic so cmd/boot/luacfg stay free of web
// behavior. An empty Password disables the auth guard; empty AllowedOrigins
// disables the origin/host guard.
type Config struct {
	Host           string
	Port           int
	Password       string
	CookieTTL      time.Duration
	AllowedOrigins []string
}

// Resolve fills unset fields with defaults and prepends the same-origin
// defaults to AllowedOrigins. Safe to call once during wiring.
func (c Config) Resolve() Config {
	if c.Host == "" {
		c.Host = defaultHost
	}
	if c.Port == 0 {
		c.Port = defaultPort
	}
	if c.CookieTTL == 0 {
		c.CookieTTL = defaultCookieTTL
	}
	c.AllowedOrigins = append(c.defaultOrigins(), c.AllowedOrigins...)
	return c
}

// defaultOrigins are the loopback + configured-host origins always trusted.
func (c Config) defaultOrigins() []string {
	port := strconv.Itoa(c.Port)
	hosts := []string{"127.0.0.1", "localhost", "::1"}
	if c.Host != "" && c.Host != "0.0.0.0" && c.Host != "::" {
		hosts = append(hosts, c.Host)
	}
	var out []string
	for _, h := range hosts {
		out = append(out, "http://"+net.JoinHostPort(h, port))
	}
	return out
}

// Addr is the listen address for net/http.
func (c Config) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// Validate refuses to bind a non-loopback host without a password.
func (c Config) Validate() error {
	if c.Password == "" && !isLoopback(c.Host) {
		return fmt.Errorf("refusing to bind %s without a password: set "+
			"shell3.web{ password = ... } in shell3.lua, or bind a loopback "+
			"address (127.0.0.1) for local-only use", c.Host)
	}
	return nil
}

// Warnings returns non-fatal configuration advisories, evaluated on the raw
// (pre-Resolve) config. It flags a non-loopback bind with no explicit
// allowed_origins: Resolve only trusts loopback by default, so clients reaching
// the server by its LAN IP or hostname would be rejected with 403 on every
// route. Call before Resolve.
func (c Config) Warnings() []string {
	var out []string
	if !isLoopback(c.Host) && c.Host != "" && len(c.AllowedOrigins) == 0 {
		out = append(out, fmt.Sprintf("binding %s but allowed_origins is empty: "+
			"only loopback origins are trusted, so browsers reaching this server by "+
			"its IP or hostname will get 403 on every route. Add the client-facing "+
			"origin(s) to shell3.web{ allowed_origins = { \"http://HOST:PORT\" } }.", c.Host))
	}
	return out
}

func isLoopback(host string) bool {
	switch host {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// hostAllowed reports whether the request Host header matches a trusted origin.
func (c Config) hostAllowed(host string) bool {
	for _, o := range c.AllowedOrigins {
		if stripScheme(o) == host {
			return true
		}
	}
	return false
}

// originAllowed reports whether the request Origin header is trusted. An empty
// Origin is never allowed (callers only consult this on state-changing POSTs).
func (c Config) originAllowed(origin string) bool {
	if origin == "" {
		return false
	}
	for _, o := range c.AllowedOrigins {
		if o == origin {
			return true
		}
	}
	return false
}

func stripScheme(o string) string {
	if i := strings.Index(o, "://"); i >= 0 {
		return o[i+3:]
	}
	return o
}
