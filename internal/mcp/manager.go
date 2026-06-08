package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/weatherjean/shell3/internal/llm"
)

// clientAPI is the subset of *Client the manager needs (seam for tests).
type clientAPI interface {
	Start(ctx context.Context) error
	ListTools(ctx context.Context) ([]ToolSchema, error)
	CallTool(ctx context.Context, name string, args map[string]any) (Result, error)
	Close() error
}

// Manager owns declared servers: schema discovery+cache, lazy sessions, dispatch.
type Manager struct {
	specs    map[string]Spec
	cacheDir string

	newClient func(Spec) clientAPI

	mu       sync.Mutex
	schemas  map[string][]ToolSchema // server -> discovered (allowlist-filtered) tools
	sessions map[string]clientAPI    // server -> live session
}

// NewManager builds a manager for the given servers; cacheDir holds schema caches.
func NewManager(specs []Spec, cacheDir string) *Manager {
	m := &Manager{
		specs:     map[string]Spec{},
		cacheDir:  cacheDir,
		schemas:   map[string][]ToolSchema{},
		sessions:  map[string]clientAPI{},
		newClient: func(s Spec) clientAPI { return New(s) },
	}
	for _, s := range specs {
		m.specs[s.Name] = s
	}
	return m
}

// stat is a tiny seam used by tests.
func stat(p string) (os.FileInfo, error) { return os.Stat(p) }

func specHash(s Spec) string {
	h := sha256.New()
	h.Write([]byte(s.Command))
	for _, a := range s.Args {
		h.Write([]byte{0})
		h.Write([]byte(a))
	}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte{1})
		h.Write([]byte(k))
	}
	for _, t := range s.Tools {
		h.Write([]byte{2})
		h.Write([]byte(t))
	}
	return hex.EncodeToString(h.Sum(nil))
}

type cacheFile struct {
	Hash  string       `json:"hash"`
	Tools []ToolSchema `json:"tools"`
}

// discover loads a server's schemas: cache hit if hash matches, else one-shot probe.
func (m *Manager) discover(ctx context.Context, name string) ([]ToolSchema, error) {
	if s, ok := m.schemas[name]; ok {
		return s, nil
	}
	spec, ok := m.specs[name]
	if !ok {
		return nil, fmt.Errorf("mcp: unknown server %q", name)
	}
	want := specHash(spec)
	cachePath := filepath.Join(m.cacheDir, name+".tools.json")

	if data, err := os.ReadFile(cachePath); err == nil {
		var cf cacheFile
		if json.Unmarshal(data, &cf) == nil && cf.Hash == want {
			m.schemas[name] = cf.Tools
			return cf.Tools, nil
		}
	}

	// Probe: spawn a throwaway client just to list tools, then close it.
	cl := m.newClient(spec)
	if err := cl.Start(ctx); err != nil {
		return nil, err
	}
	tools, err := cl.ListTools(ctx)
	_ = cl.Close()
	if err != nil {
		return nil, err
	}
	tools = filterTools(tools, spec.Tools)

	if err := os.MkdirAll(m.cacheDir, 0o755); err == nil {
		if b, err := json.Marshal(cacheFile{Hash: want, Tools: tools}); err == nil {
			_ = os.WriteFile(cachePath, b, 0o644)
		}
	}
	m.schemas[name] = tools
	return tools, nil
}

func filterTools(tools []ToolSchema, allow []string) []ToolSchema {
	if len(allow) == 0 {
		return tools
	}
	set := map[string]bool{}
	for _, a := range allow {
		set[a] = true
	}
	out := tools[:0:0]
	for _, t := range tools {
		if set[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

// ToolDefinitionsFor returns prefixed llm.ToolDefinition for the named servers.
func (m *Manager) ToolDefinitionsFor(ctx context.Context, names []string) ([]llm.ToolDefinition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var defs []llm.ToolDefinition
	for _, name := range names {
		tools, err := m.discover(ctx, name)
		if err != nil {
			return nil, err
		}
		for _, t := range tools {
			defs = append(defs, llm.ToolDefinition{
				Name:        name + "__" + t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			})
		}
	}
	return defs, nil
}

// ToolNamesFor reports the prefixed names owned by the named servers (best-effort:
// uses already-discovered schemas; call ToolDefinitionsFor first).
func (m *Manager) ToolNamesFor(names []string) map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]bool{}
	for _, name := range names {
		for _, t := range m.schemas[name] {
			out[name+"__"+t.Name] = true
		}
	}
	return out
}

// Dispatch routes a prefixed tool call to its server's session (lazily created).
func (m *Manager) Dispatch(ctx context.Context, prefixedName, argsJSON string) (string, error) {
	server, tool, ok := strings.Cut(prefixedName, "__")
	if !ok {
		return "", fmt.Errorf("mcp: not a prefixed tool name: %q", prefixedName)
	}
	var args map[string]any
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("mcp: bad args json: %w", err)
		}
	}

	sess, err := m.session(ctx, server)
	if err != nil {
		return "", err
	}
	res, err := sess.CallTool(ctx, tool, args)
	if err != nil {
		// Transport error: drop the poisoned session so the next call reconnects.
		m.mu.Lock()
		if m.sessions[server] == sess {
			delete(m.sessions, server)
		}
		m.mu.Unlock()
		_ = sess.Close()
		return "", err
	}
	if res.IsError {
		return "error: " + res.Text, nil
	}
	return res.Text, nil
}

func (m *Manager) session(ctx context.Context, server string) (clientAPI, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[server]; ok {
		return s, nil
	}
	spec, ok := m.specs[server]
	if !ok {
		return nil, fmt.Errorf("mcp: unknown server %q", server)
	}
	cl := m.newClient(spec)
	if err := cl.Start(ctx); err != nil {
		return nil, err
	}
	m.sessions[server] = cl
	return cl, nil
}

// Shutdown closes all live sessions.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = map[string]clientAPI{}
	m.mu.Unlock()
	for _, s := range sessions {
		_ = s.Close()
	}
}
