package sqlite

import (
	"database/sql"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

type storedInvocation struct{ automationID, runID, stepID sql.NullString }

func storedInvocationColumns(cause automations.AdmissionCause) storedInvocation {
	if value, ok := cause.(automations.InvocationCause); ok {
		return storedInvocation{
			automationID: sql.NullString{String: string(value.ParentAutomationID), Valid: true},
			runID:        sql.NullString{String: string(value.ParentRunID), Valid: true},
			stepID:       sql.NullString{String: string(value.ParentStepID), Valid: true},
		}
	}
	return storedInvocation{}
}

func invocationCauseFromRow(row historySummaryRow) (automations.InvocationCause, error) {
	if !row.ParentAutomationID.Valid || !row.ParentRunID.Valid || !row.ParentStepID.Valid {
		return automations.InvocationCause{}, storedDataErrorf("stored invocation %q has incomplete parent", row.ID)
	}
	value := automations.InvocationCause{
		ParentAutomationID: automations.AutomationID(row.ParentAutomationID.String),
		ParentRunID:        automations.RunID(row.ParentRunID.String),
		ParentStepID:       automations.StepID(row.ParentStepID.String),
	}
	if err := automations.ValidateAdmissionCause(value, nil); err != nil {
		return automations.InvocationCause{}, storedDataErrorf("stored invocation %q: %w", row.ID, err)
	}
	return value, nil
}
