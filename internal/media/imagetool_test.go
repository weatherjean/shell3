//go:build unix

package media

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

// fakeRegistrar is a hostToolRegistrar test double recording registered tools.
type fakeRegistrar struct {
	tools    []shell3.HostTool
	headless bool
}

func (f *fakeRegistrar) RegisterHostTool(t shell3.HostTool) error {
	f.tools = append(f.tools, t)
	return nil
}

func (f *fakeRegistrar) Headless() bool { return f.headless }

func TestImageToolHandlerHeadlessSaysReport(t *testing.T) {
	r := &fakeRegistrar{headless: true}
	c := &Clients{Generate: func(ctx context.Context, prompt, size string) (string, error) {
		return "/tmp/shell3-media/img-abc.png", nil
	}}
	if err := RegisterImageTool(r, c); err != nil {
		t.Fatalf("RegisterImageTool: %v", err)
	}
	tool := r.tools[0]
	if strings.Contains(tool.Description, "send_media_telegram") {
		t.Errorf("headless description mentions send_media_telegram: %q", tool.Description)
	}
	out, err := tool.Handler(context.Background(), `{"prompt":"a cat"}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if !strings.Contains(out, "/tmp/shell3-media/img-abc.png") || !strings.Contains(out, "final report") {
		t.Errorf("headless result should point at the path and say to report it, got %q", out)
	}
	if strings.Contains(out, "send_media_telegram") {
		t.Errorf("headless result mentions send_media_telegram (subagents don't have it): %q", out)
	}
}

func TestImageToolRegistersOnlyWhenConfigured(t *testing.T) {
	// nil Generate -> no-op.
	r := &fakeRegistrar{}
	if err := RegisterImageTool(r, &Clients{}); err != nil {
		t.Fatalf("RegisterImageTool: %v", err)
	}
	if len(r.tools) != 0 {
		t.Fatalf("want no tools registered, got %d", len(r.tools))
	}

	// nil Clients -> no-op.
	r2 := &fakeRegistrar{}
	if err := RegisterImageTool(r2, nil); err != nil {
		t.Fatalf("RegisterImageTool: %v", err)
	}
	if len(r2.tools) != 0 {
		t.Fatalf("want no tools registered for nil Clients, got %d", len(r2.tools))
	}

	// Generate set -> registers image_generate.
	r3 := &fakeRegistrar{}
	c := &Clients{Generate: func(ctx context.Context, prompt, size string) (string, error) {
		return "/tmp/img-x.png", nil
	}}
	if err := RegisterImageTool(r3, c); err != nil {
		t.Fatalf("RegisterImageTool: %v", err)
	}
	if len(r3.tools) != 1 {
		t.Fatalf("want 1 tool registered, got %d", len(r3.tools))
	}
	tool := r3.tools[0]
	if tool.Name != "image_generate" {
		t.Errorf("Name = %q, want image_generate", tool.Name)
	}
	if tool.Handler == nil {
		t.Fatal("Handler is nil")
	}
	params, ok := tool.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("Parameters.properties = %+v", tool.Parameters["properties"])
	}
	if _, ok := params["prompt"]; !ok {
		t.Error("Parameters.properties missing prompt")
	}
	if _, ok := params["size"]; !ok {
		t.Error("Parameters.properties missing size")
	}
	required, ok := tool.Parameters["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "prompt" {
		t.Errorf("Parameters.required = %+v, want [prompt]", tool.Parameters["required"])
	}
}

func TestImageToolHandler(t *testing.T) {
	r := &fakeRegistrar{}
	c := &Clients{
		GenSize: "512x512",
		Generate: func(ctx context.Context, prompt, size string) (string, error) {
			if prompt == "boom" {
				return "", errors.New("kaboom")
			}
			return "/tmp/shell3-media/img-abc.png", nil
		},
	}
	if err := RegisterImageTool(r, c); err != nil {
		t.Fatalf("RegisterImageTool: %v", err)
	}
	handler := r.tools[0].Handler

	// Success.
	out, err := handler(context.Background(), `{"prompt":"a cat"}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if !strings.Contains(out, "/tmp/shell3-media/img-abc.png") {
		t.Errorf("result missing path: %q", out)
	}
	if !strings.Contains(out, "send_media_telegram") {
		t.Errorf("result missing send_media_telegram hint: %q", out)
	}
	if !strings.Contains(out, `kind="photo"`) {
		t.Errorf("result missing kind=\"photo\" hint: %q", out)
	}

	// Empty prompt.
	out, err = handler(context.Background(), `{"prompt":"   "}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if out != "error: prompt is required" {
		t.Errorf("out = %q, want error: prompt is required", out)
	}

	// Generate error.
	out, err = handler(context.Background(), `{"prompt":"boom"}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if out != "error: kaboom" {
		t.Errorf("out = %q, want error: kaboom", out)
	}
}
