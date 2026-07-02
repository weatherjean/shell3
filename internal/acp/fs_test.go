package acp

import (
	"context"
	"errors"
	"os"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

// TestACPFSMapNotFound verifies that mapACPNotFound wraps a "not found" ACP
// error as os.ErrNotExist so edit_file's create-vs-edit detection works.
func TestACPFSMapNotFound(t *testing.T) {
	// NewInvalidRequest("file not found: /x").Error() produces JSON that
	// contains "not found" — confirmed by inspecting errors.go in the SDK.
	err := mapACPNotFound(acpsdk.NewInvalidRequest("file not found: /x"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want os.ErrNotExist, got %v", err)
	}
}

// TestACPFSMapNotFoundNil verifies that mapACPNotFound(nil) == nil.
func TestACPFSMapNotFoundNil(t *testing.T) {
	if err := mapACPNotFound(nil); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

// TestACPFSMapNotFoundOther verifies that non-"not found" errors pass through.
func TestACPFSMapNotFoundOther(t *testing.T) {
	orig := acpsdk.NewInternalError("something broke")
	got := mapACPNotFound(orig)
	if errors.Is(got, os.ErrNotExist) {
		t.Fatalf("want non-ErrNotExist, got %v", got)
	}
	if got != orig {
		t.Fatalf("want original error passed through, got %v", got)
	}
}

// TestACPFSInterfaceSatisfied is a compile-time check that acpFS implements
// fsx.FileSystem. The var _ declaration in fs.go guards this; this test exists
// to make it visible in test output and confirm the struct is constructable.
func TestACPFSInterfaceSatisfied(t *testing.T) {
	sid := "s1"
	f := acpFS{conn: nil, sessionID: &sid}
	_ = f
}

// TestInitializeCapturesFSCapability verifies that Initialize sets clientFS=true
// when the client advertises both fs.readTextFile and fs.writeTextFile.
func TestInitializeCapturesFSCapability(t *testing.T) {
	a := newACPAgent(nil, Options{})
	_, err := a.Initialize(context.Background(), acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientCapabilities: acpsdk.ClientCapabilities{
			Fs: acpsdk.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	got := a.clientFS
	a.mu.Unlock()
	if !got {
		t.Fatal("expected clientFS=true when both fs caps advertised")
	}
}

// TestInitializeCapturesFSCapabilityAbsent verifies that clientFS stays false
// when the client does not advertise both fs capabilities.
func TestInitializeCapturesFSCapabilityAbsent(t *testing.T) {
	cases := []struct {
		name string
		caps acpsdk.FileSystemCapabilities
	}{
		{"neither", acpsdk.FileSystemCapabilities{}},
		{"read_only", acpsdk.FileSystemCapabilities{ReadTextFile: true}},
		{"write_only", acpsdk.FileSystemCapabilities{WriteTextFile: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newACPAgent(nil, Options{})
			_, err := a.Initialize(context.Background(), acpsdk.InitializeRequest{
				ProtocolVersion:    acpsdk.ProtocolVersionNumber,
				ClientCapabilities: acpsdk.ClientCapabilities{Fs: tc.caps},
			})
			if err != nil {
				t.Fatal(err)
			}
			a.mu.Lock()
			got := a.clientFS
			a.mu.Unlock()
			if got {
				t.Fatalf("expected clientFS=false for caps %+v", tc.caps)
			}
		})
	}
}
