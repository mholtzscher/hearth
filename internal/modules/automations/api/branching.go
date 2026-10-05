package api

import (
	"encoding/json"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// AutomationBranchConditionBody adds Trigger matching only to branch predicates.
type AutomationBranchConditionBody struct {
	ID            string                          `json:"id"`
	Kind          string                          `json:"kind"                      enum:"entity_state,trigger,all,any,not"`
	EntityID      string                          `json:"entity_id,omitempty"`
	ValuePointer  *string                         `json:"value_pointer,omitempty"`
	Operator      string                          `json:"operator,omitempty"        enum:"eq,ne,lt,lte,gt,gte"`
	Operand       json.RawMessage                 `json:"operand,omitempty"`
	MaxAgeSeconds *int64                          `json:"max_age_seconds,omitempty"`
	TriggerIDs    []string                        `json:"trigger_ids,omitempty"`
	Children      []AutomationBranchConditionBody `json:"children,omitempty"`
	Child         *AutomationBranchConditionBody  `json:"child,omitempty"`
}

// AutomationChooseBranchBody identifies one ordered alternative.
type AutomationChooseBranchBody struct {
	ID         string                        `json:"id"`
	Conditions AutomationBranchConditionBody `json:"conditions"`
	Steps      []AutomationStepBody          `json:"steps"`
}

// AutomationTriggerConditionEvidenceBody records the configured-order intersection.
type AutomationTriggerConditionEvidenceBody struct {
	MatchedTriggerIDs []string `json:"matched_trigger_ids"`
}

// AutomationBranchConditionEvaluationBody addresses a root within its branch Step.
type AutomationBranchConditionEvaluationBody struct {
	BranchID   *string                           `json:"branch_id,omitempty"`
	Evaluation AutomationConditionEvaluationBody `json:"evaluation"`
}

// AutomationBranchDecisionBody records selection, not command completion.
type AutomationBranchDecisionBody struct {
	Position         int                                       `json:"position"`
	StepID           string                                    `json:"step_id"`
	Kind             string                                    `json:"kind"                         enum:"if,choose"`
	EvaluatedAt      time.Time                                 `json:"evaluated_at"`
	Outcome          string                                    `json:"outcome"                      enum:"then,else,branch,default,no_match,unknown,error"`
	SelectedBranchID *string                                   `json:"selected_branch_id,omitempty"`
	Evaluations      []AutomationBranchConditionEvaluationBody `json:"evaluations"`
	FailureCode      *string                                   `json:"failure_code,omitempty"`
}

func automationSequenceBody(steps []automations.Step) []AutomationStepBody {
	if steps == nil {
		return nil
	}
	body := make([]AutomationStepBody, len(steps))
	for index, step := range steps {
		mapped := AutomationStepBody{ID: string(step.ID)}
		switch step.Kind {
		case "", automations.StepKindCommand:
			mapped.EntityID = string(step.EntityID)
			mapped.Operation = string(step.OperationName)
			mapped.Parameters = append(json.RawMessage(nil), step.Parameters...)
		case automations.StepKindIf:
			mapped.Kind = string(step.Kind)
			condition := branchConditionBody(step.If.Conditions)
			mapped.Conditions = &condition
			mapped.Then = automationSequenceBody(step.If.Then)
			mapped.Else = automationSequenceBody(step.If.Else)
		case automations.StepKindChoose:
			mapped.Kind = string(step.Kind)
			mapped.Branches = make([]AutomationChooseBranchBody, len(step.Choose.Branches))
			for branchIndex, branch := range step.Choose.Branches {
				mapped.Branches[branchIndex] = AutomationChooseBranchBody{
					ID: string(branch.ID), Conditions: branchConditionBody(branch.Conditions),
					Steps: automationSequenceBody(branch.Steps),
				}
			}
			mapped.Default = automationSequenceBody(step.Choose.Default)
		case automations.StepKindDelay:
			mapped.Kind = string(step.Kind)
			duration := step.Delay.DurationMS
			mapped.DurationMS = &duration
		}
		body[index] = mapped
	}
	return body
}

func branchConditionBody(condition automations.Condition) AutomationBranchConditionBody {
	body := AutomationBranchConditionBody{ID: string(condition.ID), Kind: string(condition.Kind)}
	switch condition.Kind {
	case automations.ConditionEntityState:
		state := conditionNodeBody(condition)
		body.EntityID, body.ValuePointer, body.Operator = state.EntityID, state.ValuePointer, state.Operator
		body.Operand, body.MaxAgeSeconds = state.Operand, state.MaxAgeSeconds
	case automations.ConditionTrigger:
		body.TriggerIDs = make([]string, len(condition.Trigger.TriggerIDs))
		for index, id := range condition.Trigger.TriggerIDs {
			body.TriggerIDs[index] = string(id)
		}
	case automations.ConditionAll, automations.ConditionAny:
		body.Children = make([]AutomationBranchConditionBody, len(condition.Children))
		for index, child := range condition.Children {
			body.Children[index] = branchConditionBody(child)
		}
	case automations.ConditionNot:
		child := branchConditionBody(*condition.Child)
		body.Child = &child
	}
	return body
}

func branchDecisionBody(decision automations.BranchDecision) AutomationBranchDecisionBody {
	body := AutomationBranchDecisionBody{
		Position: decision.Position, StepID: string(decision.StepID), Kind: string(decision.Kind),
		EvaluatedAt: decision.EvaluatedAt.UTC(), Outcome: string(decision.Outcome), FailureCode: decision.FailureCode,
		Evaluations: make([]AutomationBranchConditionEvaluationBody, len(decision.Evaluations)),
	}
	if decision.SelectedBranchID != nil {
		id := string(*decision.SelectedBranchID)
		body.SelectedBranchID = &id
	}
	for index, evaluation := range decision.Evaluations {
		mapped := AutomationBranchConditionEvaluationBody{Evaluation: conditionEvaluationBody(evaluation.Evaluation)}
		if evaluation.BranchID != nil {
			id := string(*evaluation.BranchID)
			mapped.BranchID = &id
		}
		body.Evaluations[index] = mapped
	}
	return body
}
