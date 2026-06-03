package patchwidgets

// Reason explains why a widget returned. ReasonOK accompanies a successful
// submission; the others mean the widget was dismissed without a value.
type Reason string

const (
	ReasonOK      Reason = "ok"
	ReasonCancel  Reason = "cancel"  // user pressed Esc / Ctrl+C
	ReasonTimeout Reason = "timeout" // TimeoutSeconds elapsed
	ReasonEOF     Reason = "eof"     // /dev/tty closed mid-read
)

// Result is what each widget returns. OK is true iff the user submitted a
// value; in that case Value is set (and Index, for [Pick]). Otherwise OK
// is false and Reason explains why.
type Result struct {
	OK     bool   `json:"ok"`
	Value  any    `json:"value,omitempty"`
	Index  *int   `json:"index,omitempty"`
	Reason Reason `json:"reason,omitempty"`
}

// ExitCode maps a Result to a conventional Unix exit code:
//
//   - 0   ok
//   - 1   negative answer (Confirm "no") — value is bool false
//   - 2   timeout
//   - 130 cancel / EOF (matches the SIGINT convention)
func (r Result) ExitCode() int {
	if r.OK {
		if b, ok := r.Value.(bool); ok && !b {
			return 1
		}
		return 0
	}
	switch r.Reason {
	case ReasonTimeout:
		return 2
	default:
		return 130
	}
}

func okResult(value any) Result { return Result{OK: true, Value: value, Reason: ReasonOK} }
func okIndex(value any, i int) Result {
	return Result{OK: true, Value: value, Index: &i, Reason: ReasonOK}
}
func cancelResult() Result  { return Result{Reason: ReasonCancel} }
func timeoutResult() Result { return Result{Reason: ReasonTimeout} }
func eofResult() Result     { return Result{Reason: ReasonEOF} }
