package httpapi

import (
	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func automationRunBody(run automations.Run) (AutomationRunBody, error) {
	body := AutomationRunFields{
		ID:                string(run.ID),
		AutomationID:      string(run.AutomationID),
		AutomationName:    run.AutomationName,
		Revision:          run.Revision,
		Cause:             admissionCauseBody(run.Cause),
		MatchedTriggerIDs: make([]string, len(run.MatchedTriggerIDs)),
		StartedAt:         run.StartedAt,
		Snapshot:          automationDefinitionBody(run.Snapshot),
		ConditionDecision: conditionDecisionBody(run.ConditionDecision),
		Executions:        make([]AutomationExecutionBody, len(run.Executions)),
		DataEvidence:      run.DataEvidence,
	}
	if run.ValueInitialization != nil {
		var err error
		body.ValueInitialization, err = automations.EncodeRunValueInitialization(*run.ValueInitialization)
		if err != nil {
			return AutomationRunBody{}, err
		}
	}
	for index, triggerID := range run.MatchedTriggerIDs {
		body.MatchedTriggerIDs[index] = string(triggerID)
	}
	for index, execution := range run.Executions {
		encoded, err := automations.EncodePublicStepExecution(execution)
		if err != nil {
			return AutomationRunBody{}, err
		}
		body.Executions[index] = AutomationExecutionBody{encoded: encoded}
	}
	return runLifecycleBody(body, run.State), nil
}
