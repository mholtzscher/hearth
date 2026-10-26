package httpapi

import (
	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func automationRunBody(run automations.Run) (AutomationRunBody, error) {
	projection, err := automations.ProjectStepExecutions(run.Snapshot, run.Executions)
	if err != nil {
		return AutomationRunBody{}, err
	}
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
		Steps:             make([]AutomationStepAttemptBody, len(projection.Steps)),
		BranchDecisions:   make([]AutomationBranchDecisionBody, len(projection.BranchDecisions)),
		Delays:            make([]AutomationDelayExecutionBody, len(projection.Delays)),
		Notifications:     make([]AutomationNotificationExecutionBody, len(projection.Notifications)),
		Functions:         make([]AutomationFunctionExecutionBody, len(projection.Functions)),
	}
	for index, triggerID := range run.MatchedTriggerIDs {
		body.MatchedTriggerIDs[index] = string(triggerID)
	}
	for index, step := range projection.Steps {
		body.Steps[index] = automationStepAttemptBody(step)
	}
	for index, decision := range projection.BranchDecisions {
		body.BranchDecisions[index] = branchDecisionBody(decision)
	}
	for index, delay := range projection.Delays {
		body.Delays[index] = delayExecutionBody(delay)
	}
	for index, execution := range projection.Notifications {
		body.Notifications[index] = notificationExecutionBody(execution)
	}
	for index, execution := range projection.Functions {
		body.Functions[index] = functionExecutionBody(execution)
	}
	return runLifecycleBody(body, run.State), nil
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
