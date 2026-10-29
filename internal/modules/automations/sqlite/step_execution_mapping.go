package sqlite

import (
	"encoding/json"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

func executionFromRow(row dbsqlc.AutomationStepExecution) (automations.StepExecution, error) {
	id := automations.StepID(row.StepID) // The details decoder validates the retained identity.
	started, err := decodeAutomationTimestamp(row.StartedAt)
	if err != nil {
		return automations.StepExecution{}, storedDataErrorf("stored Step %q started_at: %w", row.StepID, err)
	}
	completed, err := parseNullableAutomationTimestamp(row.CompletedAt)
	if err != nil {
		return automations.StepExecution{}, storedDataErrorf("stored Step %q completed_at: %w", row.StepID, err)
	}
	details, err := automations.DecodeStepExecutionDetails(id, automations.StepExecutionEnvelope{
		Kind:         automations.StepKind(row.Kind),
		Phase:        row.Phase,
		StartedAt:    started,
		CompletedAt:  completed,
		EvidenceJSON: json.RawMessage(row.EvidenceJson),
	})
	if err != nil {
		err = storedDataErrorf("stored Step %q details: %w", row.StepID, err)
	}
	return automations.StepExecution{StepID: id, Sequence: int(row.Sequence), Details: details}, err
}
