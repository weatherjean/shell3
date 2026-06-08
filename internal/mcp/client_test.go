package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestMain doubles as a fake MCP server when MCP_FAKE=1 is set. The client
// under test spawns os.Args[0] with that env, so no external binary is needed.
func TestMain(m *testing.M) {
	if os.Getenv("MCP_FAKE") == "1" {
		runFakeServer()
		return
	}
	os.Exit(m.Run())
}

// runFakeServer speaks newline-delimited JSON-RPC: initialize, tools/list,
// tools/call (echoes its args), and ignores notifications.
func runFakeServer() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var probe struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		if probe.Method != "initialize" && probe.Method != "tools/list" && probe.Method != "tools/call" {
			continue // notification or unknown — no reply
		}
		var result string
		switch probe.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"fake","version":"0"}}`
		case "tools/list":
			result = `{"tools":[{"name":"echo","description":"echo args","inputSchema":{"type":"object","properties":{"msg":{"type":"string"}}}}]}`
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(probe.Params, &p)
			msg, _ := p.Arguments["msg"].(string)
			result = fmt.Sprintf(`{"content":[{"type":"text","text":%q}],"isError":false}`, "echo:"+msg)
		}
		fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%d,"result":%s}`+"\n", probe.ID, result)
	}
}

func fakeSpec() Spec {
	return Spec{Name: "fake", Command: os.Args[0], Args: []string{"-test.run=TestMain"}, Env: map[string]string{"MCP_FAKE": "1"}}
}

func TestClientListAndCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := New(fakeSpec())
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	res, err := c.CallTool(ctx, "echo", map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.Text != "echo:hi" {
		t.Fatalf("unexpected result: %+v", res)
	}
}
