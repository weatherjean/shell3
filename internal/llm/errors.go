package llm

// StatusError carries the provider's HTTP status code alongside the underlying
// error, so consumers branch on the code with errors.As instead of matching
// substrings of the rendered message (which vary by provider and proxy).
type StatusError struct {
	Code int // HTTP status, e.g. 400
	Err  error
}

func (e *StatusError) Error() string { return e.Err.Error() }
func (e *StatusError) Unwrap() error { return e.Err }
