package httpapi

import (
	"encoding/json"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func automationRunBody(run automations.Run) AutomationRunBody {
	body := AutomationRunFields{
		DataEvidence:      run.DataEvidence,
		Data:              make([]json.RawMessage, len(run.Data)),
		ID:                string(run.ID),
		AutomationID:      string(run.AutomationID),
		AutomationName:    run.AutomationName,
		Revision:          run.Revision,
		Cause:             admissionCauseBody(run.Cause),
		MatchedTriggerIDs: make([]string, len(run.MatchedTriggerIDs)),
		StartedAt:         run.StartedAt,
		Snapshot:          automationDefinitionBody(run.Snapshot),
		ConditionDecision: conditionDecisionBody(run.ConditionDecision),
		Steps:             make([]AutomationStepAttemptBody, len(run.Steps)),
		BranchDecisions:   make([]AutomationBranchDecisionBody, len(run.BranchDecisions)),
		Delays:            make([]AutomationDelayExecutionBody, len(run.Delays)),
		Notifications:     make([]AutomationNotificationExecutionBody, len(run.Notifications)),
		Functions:         make([]AutomationFunctionExecutionBody, len(run.Functions)),
	}
	for index, triggerID := range run.MatchedTriggerIDs {
		body.MatchedTriggerIDs[index] = string(triggerID)
	}
	for index, record := range run.Data {
		raw, err := automations.EncodeRunDataRecord(record)
		if err != nil {
			panic("invalid retained Run data record")
		}
		body.Data[index] = raw
	}
	for index, step := range run.Steps {
		body.Steps[index] = automationStepAttemptBody(step)
	}
	for index, decision := range run.BranchDecisions {
		body.BranchDecisions[index] = branchDecisionBody(decision)
	}
	for index, delay := range run.Delays {
		body.Delays[index] = delayExecutionBody(delay)
	}
	for index, execution := range run.Notifications {
		body.Notifications[index] = notificationExecutionBody(execution)
	}
	for index, execution := range run.Functions {
		body.Functions[index] = functionExecutionBody(execution)
	}
	return runLifecycleBody(body, run.State)
}

func notificationExecutionBody(execution automations.NotificationExecution) AutomationNotificationExecutionBody {
	body := AutomationNotificationExecutionBody{
		StepID: string(execution.StepID), Position: execution.Position,
		AttemptID: string(execution.AttemptID), Status: string(execution.Status),
		StartedAt: execution.StartedAt, CompletedAt: execution.CompletedAt,
		ReasonCode: execution.ReasonCode,
	}
	if execution.Outcome != nil {
		outcome := string(*execution.Outcome)
		body.Outcome = &outcome
	}
	return body
}
