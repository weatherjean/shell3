//go:build unix

package telegram

import (
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

// newFakeRuntime builds a real Runtime backed by a fakellm that always replies
// with replyText. It uses the public test seam in pkg/shell3.
func newFakeRuntime(t *testing.T, replyText string) (*shell3.Runtime, *shell3.Session) {
	t.Helper()
	rt := shell3test.NewRuntimeForTest(t, replyText)
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	return rt, sess
}
