package acp

import (
	"context"
	"fmt"
	"os"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/weatherjean/shell3/internal/fsx"
)

// acpFS routes read/edit_file I/O back to the ACP client's editor via the
// negotiated fs capability, so reads see unsaved buffers and writes flow
// through the editor. Built only when the client advertised fs.readTextFile
// and fs.writeTextFile; otherwise the session keeps the OS backend.
//
// sessionID is a *string rather than acpsdk.SessionId because the ACP session
// ID is only known after rt.Session returns, but acpFS must be constructed
// before that call (to pass it in SessionOpts). The pointer is pre-allocated
// by NewSession/resumeAndRegister (idPtr), filled immediately after rt.Session
// returns, and dereferenced here at call time. No tool can run before the
// session is fully constructed, so *sessionID is always valid by the time
// ReadTextFile or WriteTextFile is invoked.
type acpFS struct {
	conn      *acpsdk.AgentSideConnection
	sessionID *string
}

// Compile-time check: acpFS must implement fsx.FileSystem.
var _ fsx.FileSystem = acpFS{}

// ReadTextFile delegates to the ACP client's fs/read_text_file method.
// Line/Limit are left nil — shell3 handles paging on its own side.
// A "not found" response is normalised to os.ErrNotExist via mapACPNotFound
// so edit_file's create-vs-edit detection behaves the same as the OS backend.
func (f acpFS) ReadTextFile(ctx context.Context, absPath string) (string, error) {
	resp, err := f.conn.ReadTextFile(ctx, acpsdk.ReadTextFileRequest{
		SessionId: acpsdk.SessionId(*f.sessionID),
		Path:      absPath,
	})
	if err != nil {
		return "", mapACPNotFound(err)
	}
	return resp.Content, nil
}

// WriteTextFile delegates to the ACP client's fs/write_text_file method.
func (f acpFS) WriteTextFile(ctx context.Context, absPath, content string) error {
	_, err := f.conn.WriteTextFile(ctx, acpsdk.WriteTextFileRequest{
		SessionId: acpsdk.SessionId(*f.sessionID),
		Path:      absPath,
		Content:   content,
	})
	return err
}

// mapACPNotFound normalises a client "file not found" ACP error to
// os.ErrNotExist so edit_file's create-vs-edit detection works across
// backends. The mapping is best-effort: ACP has no standard "not found"
// error code, so we match on the error text returned by RequestError.Error()
// (a JSON-marshalled object that includes the Data field). An ambiguous ACP
// error that does not contain "not found" is returned unchanged and will be
// treated as a hard failure rather than a missing-file signal.
func mapACPNotFound(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		return fmt.Errorf("%w: %v", os.ErrNotExist, err)
	}
	return err
}
