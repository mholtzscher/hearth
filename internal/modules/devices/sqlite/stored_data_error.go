package sqlite

// storedDataError retains a private decoder cause without giving corrupt stored
// evidence the error identity of invalid caller input.
type storedDataError struct{ cause error }

func (*storedDataError) Error() string { return "corrupt stored device data" }

func (err *storedDataError) Cause() error { return err.cause }
