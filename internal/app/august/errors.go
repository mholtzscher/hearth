package august

import "errors"

const (
	failureCredentialsRead    = "credentials_file_unreadable"
	failureCredentialsInvalid = "credentials_file_invalid"
	failureCommandsBlocked    = "commands_blocked"
)

type startupError struct {
	code  string
	cause error
}

func (e *startupError) Error() string { return "August companion: " + e.code }
func (e *startupError) Unwrap() error { return e.cause }

// FailureCode returns a fixed process diagnostic, never dependency error text.
func FailureCode(err error) string {
	if failure, ok := errors.AsType[*startupError](err); ok {
		return failure.code
	}
	var coded interface{ SafeCode() string }
	if errors.As(err, &coded) {
		switch code := coded.SafeCode(); code {
		case "auth_rejected",
			"validation_required",
			"session_expired",
			"throttled",
			"provider_unavailable",
			"invalid_response",
			"upstream_rejected",
			"verification_input_invalid",
			"verification_input_unreadable",
			"verification_rejected":
			return code
		}
	}
	return "run_failed"
}
