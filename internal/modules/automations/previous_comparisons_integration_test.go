//go:build integration

package automations_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestUnmatchedObservationDoesNotCreateReceiptOrHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	entityID := newEntityID(t)
	service, _ := newRuntimeService(t, newScriptedDevices(), runtimeTestDependencies())
	definition := runtimeDefinitionFor(t, entityID)
	observationBody := definition.Triggers[0].Body.(automations.ObservationTrigger)
	observationBody.PreviousComparisons = []automations.ObservationComparison{{
		Pointer: "/temperature", Operator: automations.ComparisonGreaterThan, Operand: json.RawMessage("30"),
	}}
	definition.Triggers[0].Body = observationBody

	record := createRuntimeAutomation(t, service, definition)
	fact := newObservationFact(t, entityID, runtimeTestNow)

	outcome, err := service.ReceiveDeviceFact(ctx, fact)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.MatchedAutomations != 0 || outcome.StartedRuns != 0 || outcome.RecordedSkips != 0 {
		t.Fatalf("unmatched fact outcome = %#v, want no admission", outcome)
	}
	if history := listHistory(t, service, record.ID); len(history) != 0 {
		t.Fatalf("unmatched fact created history: %#v", history)
	}
	observationBody2 :=

		// The same Fact must remain admissible after a definition change makes it match;
		// an earlier unmatched receipt would classify this delivery as a duplicate.
		definition.Triggers[0].Body.(automations.ObservationTrigger)
	observationBody2.PreviousComparisons = nil
	definition.Triggers[0].Body = observationBody2
	if _, replaceErr := service.ReplaceAutomation(ctx, record.ID, record.Revision, definition); replaceErr != nil {
		t.Fatal(replaceErr)
	}
	outcome, err = service.ReceiveDeviceFact(ctx, fact)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.MatchedAutomations != 1 || outcome.StartedRuns != 1 || outcome.DuplicateOutcomes != 0 {
		t.Fatalf("same fact after becoming eligible = %#v, want a new admission", outcome)
	}
	waitForRuns(t, service)
	if history := listHistory(t, service, record.ID); len(history) != 1 {
		t.Fatalf("history after newly matching delivery = %#v, want one entry", history)
	}
}
