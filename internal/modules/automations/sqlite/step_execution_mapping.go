package sqlite

import (
	"encoding/json"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

func executionParentFromRow(row dbsqlc.GetExecutionParentRow) (executionParent, error) {
	snapshot, err := automations.DecodeDefinition(json.RawMessage(row.DefinitionJson.String))
	if err != nil {
		return executionParent{}, err
	}
	matches, err := decodeTriggerIDs(json.RawMessage(row.RunMatchedTriggerIdsJson.String))
	if err != nil {
		return executionParent{}, err
	}
	parent := executionParent{snapshot: snapshot, matches: matches}
	switch row.DataEvidence {
	case automations.RunDataRecorded:
		parent.recorded = true
	case automations.RunDataLegacyUnavailable:
		if row.ValueInitializationJson.Valid {
			return executionParent{}, lifecycleCorruption("legacy Run has value evidence")
		}
	default:
		return executionParent{}, lifecycleCorruption("unknown value provenance")
	}
	if row.ValueInitializationJson.Valid {
		init, decodeErr := automations.DecodeRunValueInitializationWithPreparedSnapshot(
			json.RawMessage(row.ValueInitializationJson.String),
			snapshot,
		)
		if decodeErr != nil {
			return executionParent{}, decodeErr
		}
		parent.initialization = &init
	}
	return parent, nil
}

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
