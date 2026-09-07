package devices //nolint:testpackage // Tests exercise the outcome constructor invariant.

import (
	"testing"
)

func TestNewCommandResultEnforcesOutcomeEvidenceInvariant(t *testing.T) {
	t.Parallel()
	observationID := commandTestObservationID
	value := Value(`true`)
	otherOutcome := OutcomeKind("satisfied")

	tests := []struct {
		name          string
		outcome       OutcomeKind
		observationID *ObservationID
		value         *Value
		wantErr       bool
	}{
		{"observed with evidence", OutcomeObserved, &observationID, &value, false},
		{"observed without observation", OutcomeObserved, nil, &value, true},
		{"observed without value", OutcomeObserved, &observationID, nil, true},
		{"observed without evidence", OutcomeObserved, nil, nil, true},
		{"dispatched without evidence", OutcomeDispatched, nil, nil, false},
		{"dispatched with observation", OutcomeDispatched, &observationID, nil, true},
		{"dispatched with value", OutcomeDispatched, nil, &value, true},
		{"dispatched with evidence", OutcomeDispatched, &observationID, &value, true},
		{"unknown outcome without evidence", otherOutcome, nil, nil, true},
		{"unknown outcome with evidence", otherOutcome, &observationID, &value, true},
		{"empty outcome", OutcomeKind(""), nil, nil, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := NewCommandResult(commandTestID, test.outcome, test.observationID, test.value)
			if test.wantErr {
				if err == nil {
					t.Fatalf("result = %#v, want constructor error", result)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.CommandID != commandTestID || result.Outcome != test.outcome {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}
