package agent

import (
	"context"
	"errors"
)

// These private markers retain the origin through Eino's graph wrapping. They
// carry no transport status, recovery policy, or raw client-facing detail.
type modelError struct{ err error }

func (modelErr modelError) Error() string { return modelErr.err.Error() }
func (modelErr modelError) Unwrap() error { return modelErr.err }

var errModelResponseInvalid = errors.New("agent model returned an empty response")

type toolCallError struct{ err error }

func (toolErr toolCallError) Error() string { return toolErr.err.Error() }
func (toolErr toolCallError) Unwrap() error { return toolErr.err }

type tracePersistError struct{ err error }

func (traceErr tracePersistError) Error() string { return traceErr.err.Error() }
func (traceErr tracePersistError) Unwrap() error { return traceErr.err }

// turnCancellationError establishes that the turn owner ended, rather than
// merely finding a context error returned by a dependency.
type turnCancellationError struct{ err error }

func (turnCancellationError) Error() string              { return "agent turn canceled" }
func (cancellation turnCancellationError) Unwrap() error { return cancellation.err }

func expectedTurnCancellation(ctx context.Context, err error) bool {
	ownerErr := ctx.Err()
	if ownerErr == nil {
		return false
	}
	// Unwrap follows only a single cause. Joined faults must remain diagnostic,
	// even when another branch contains the owner's cancellation.
	for err != nil {
		if err == ownerErr { //nolint:errorlint // Require the exact owner sentinel, not a matching error class.
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}

func graphFailureStage(err error) string {
	switch {
	case errors.As(err, &tracePersistError{}):
		return turnStagePersist
	case errors.As(err, &toolCallError{}):
		return turnStageTool
	case errors.As(err, &modelError{}):
		return turnStageModel
	default:
		return turnStageGraph
	}
}
