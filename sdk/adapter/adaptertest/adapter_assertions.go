// Package adaptertest provides the shared handwritten assertions for generated
// sdk/adapter Entity-type facade conformance tests: adapter validation-error
// classification and adapter timestamp parsing. It imports sdk/adapter, so
// generated facade tests get one definition of the facade rejection contract
// instead of a private copy per package.
package adaptertest

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// RequireValidationError fails t unless err is or wraps an
// *adapter.ValidationError, naming the action under test. Generated facade
// tests call it after every rejection so a plain error cannot masquerade as an
// adapter validation error.
func RequireValidationError(t *testing.T, err error, action string) {
	t.Helper()
	if !isValidationError(err) {
		t.Fatalf("%s: expected adapter validation error, got %v", action, err)
	}
}

// ParseAdapterTime parses an adapter timestamp field and fails t unless raw is
// RFC3339Nano text carrying a UTC offset. Generated facade tests use it to
// prove observations echo adapter times in UTC.
func ParseAdapterTime(t *testing.T, field, raw string) time.Time {
	t.Helper()
	parsed, err := parseAdapterTime(field, raw)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return parsed
}

// isValidationError reports whether err is or wraps an *adapter.ValidationError.
func isValidationError(err error) bool {
	var validationErr *adapter.ValidationError
	return errors.As(err, &validationErr)
}

// parseAdapterTime parses raw as RFC3339Nano and rejects any offset other than
// UTC, naming field in the error so a failing assertion identifies its column.
func parseAdapterTime(field, raw string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %q is not RFC3339Nano: %w", field, raw, err)
	}
	if _, offset := parsed.Zone(); offset != 0 {
		return time.Time{}, fmt.Errorf("%s %q is not formatted as UTC", field, raw)
	}
	return parsed, nil
}
