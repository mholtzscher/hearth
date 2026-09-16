package config

import (
	"errors"
	"fmt"
)

// InvalidError reports a configuration that was read successfully but failed
// static validation. Reason names the offending field, Device, or Entity and
// never repeats the configuration path or a configured value, so process
// records can publish it; Error keeps the path for direct callers.
type InvalidError struct {
	Path   string
	Reason string
	err    error
}

// Invalid classifies one static validation failure for the configuration at path.
func Invalid(path string, err error) error {
	return &InvalidError{Path: path, Reason: err.Error(), err: err}
}

func (err *InvalidError) Error() string {
	return fmt.Sprintf("validate config %q: %s", err.Path, err.Reason)
}

// Unwrap returns the underlying validation failure so [errors.Is] and
// [errors.As] keep matching it.
func (err *InvalidError) Unwrap() error { return err.err }

// Reason returns the safe classification of one configuration failure for a
// process record: the path-free validation reason when the configuration was
// read but is semantically invalid, and a fixed unreadable classification
// otherwise. A decode failure stays unreported because its text can echo a
// configured value.
func Reason(err error) string {
	invalid, ok := errors.AsType[*InvalidError](err)
	if !ok {
		return "configuration could not be read or decoded"
	}
	return invalid.Reason
}
