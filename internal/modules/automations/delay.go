package automations

import "fmt"

// DelayStep waits for a fixed elapsed duration from 1 millisecond through 24 hours.
type DelayStep struct {
	DurationMS int64
}

func normalizeDelayStep(id StepID, input DelayStep) (Step, error) {
	if input.DurationMS < 1 || input.DurationMS > 86400000 {
		return Step{}, definitionIssue("/steps", "Delay duration_ms must be 1 to 86400000")
	}
	return Step{ID: id, Body: input}, nil
}

// DelayDurationWithPreparedSnapshot resolves a delay using an unchanged prepared snapshot.
// The caller must prepare arbitrary definitions before using this lookup.
func DelayDurationWithPreparedSnapshot(snapshot Definition, id StepID) (int64, error) {
	step := findSnapshotStep(snapshot.Steps, id)
	if step == nil {
		return 0, fmt.Errorf("%w: snapshot Step %q is not a delay", ErrInvalidAutomation, id)
	}
	body, ok := step.Body.(DelayStep)
	if !ok {
		return 0, fmt.Errorf("%w: snapshot Step %q is not a delay", ErrInvalidAutomation, id)
	}
	return body.DurationMS, nil
}
