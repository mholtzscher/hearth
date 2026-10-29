package sqlite

import "fmt"

// storedDataError retains a decoding cause without letting its input-validation
// identities classify stored corruption as a caller or wire error. It deliberately
// does not implement Unwrap; Cause is for private diagnostics, not error policy.
type storedDataError struct {
	cause error
}

func (*storedDataError) Error() string { return "corrupt stored automation data" }

func (err *storedDataError) Cause() error { return err.cause }

func storedDataErrorf(format string, args ...any) error {
	return &storedDataError{cause: fmt.Errorf(format, args...)}
}
