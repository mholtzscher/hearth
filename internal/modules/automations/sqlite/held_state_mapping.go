package sqlite

import (
	"database/sql"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

type storedHeldState struct {
	triggerID sql.NullString
	startedAt sql.NullString
	dueAt     sql.NullString
}

func storedHeldStateColumns(evidence *automations.HeldStateEvidence) storedHeldState {
	if evidence == nil {
		return storedHeldState{}
	}
	return storedHeldState{
		triggerID: sql.NullString{String: string(evidence.TriggerID), Valid: true},
		startedAt: sql.NullString{String: encodeAutomationTimestamp(evidence.StartedAt), Valid: true},
		dueAt:     sql.NullString{String: encodeAutomationTimestamp(evidence.DueAt), Valid: true},
	}
}

func storedCauseHeldStateColumns(cause automations.AdmissionCause) storedHeldState {
	switch cause := cause.(type) {
	case automations.HeldStateCause:
		return storedHeldStateColumns(&cause.Evidence)
	case automations.ManualCause, automations.DeviceFactCause, automations.ScheduleCause:
		return storedHeldState{}
	default:
		return storedHeldState{}
	}
}

func heldStateEvidenceFromHoldRow(hold dbsqlc.AutomationHold) (automations.HeldStateEvidence, error) {
	started, err := decodeAutomationTimestamp(hold.StartedAt.String)
	if err != nil {
		return automations.HeldStateEvidence{}, err
	}
	due, err := decodeAutomationTimestamp(hold.DueAt.String)
	if err != nil {
		return automations.HeldStateEvidence{}, err
	}
	return automations.HeldStateEvidence{
		TriggerID: automations.TriggerID(hold.TriggerID),
		StartedAt: started,
		DueAt:     due,
	}, nil
}
