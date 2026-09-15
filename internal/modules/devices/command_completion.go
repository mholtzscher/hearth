package devices

// ValidCommandCompletion reports whether a completion describes a supported
// terminal write: a dispatched success or a failure with its matching code.
// Observation satisfaction and startup interruption have separate write paths.
func ValidCommandCompletion(completion CommandCompletion) bool {
	if completion.Status == CommandStatusDispatched {
		return !completion.CompletedAt.IsZero() && completion.FailureCode == ""
	}
	expected := map[CommandStatus]CommandFailureCode{ //nolint:exhaustive // Only terminal failure statuses have failure codes.
		CommandStatusRejected:          CommandFailureUpstreamRejected,
		CommandStatusAdapterUnhealthy:  CommandFailureAdapterUnhealthy,
		CommandStatusEntityUnavailable: CommandFailureEntityUnavailable,
		CommandStatusOutcomeTimeout:    CommandFailureOutcomeTimeout,
		CommandStatusInternalFailure:   CommandFailureInternalError,
	}
	return !completion.CompletedAt.IsZero() && expected[completion.Status] == completion.FailureCode &&
		completion.FailureCode != ""
}

// MatchesCommandCompletion reports whether a persisted Command already has the
// exact requested terminal outcome and completion time, making a retry a no-op.
func MatchesCommandCompletion(command CommandRecord, completion CommandCompletion) bool {
	return command.Status == completion.Status && commandFailureMatches(command, completion) &&
		command.CompletedAt != nil &&
		command.CompletedAt.Equal(completion.CompletedAt)
}

// commandFailureMatches treats a completion without a failure code as matching
// only a record with no failure code, rather than a pointer to an empty string.
func commandFailureMatches(command CommandRecord, completion CommandCompletion) bool {
	if completion.FailureCode == "" {
		return command.FailureCode == nil
	}
	return command.FailureCode != nil && *command.FailureCode == completion.FailureCode
}
