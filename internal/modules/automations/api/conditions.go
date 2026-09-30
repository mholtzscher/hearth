package api

import (
	"encoding/json"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// AutomationConditionBody is one recursive, flattened Condition node mirroring
// the strict persisted definition shape: an entity_state leaf carries its family
// fields directly, all/any carry children, and not carries one child.
type AutomationConditionBody struct {
	ID            string                    `json:"id"                        doc:"Node identifier, unique within the tree"`
	Kind          string                    `json:"kind"                                                                                enum:"entity_state,all,any,not"`
	EntityID      string                    `json:"entity_id,omitempty"`
	ValuePointer  *string                   `json:"value_pointer,omitempty"   doc:"RFC 6901 JSON Pointer into the selected State value"`
	Operator      string                    `json:"operator,omitempty"                                                                  enum:"eq,ne,lt,lte,gt,gte"`
	Operand       json.RawMessage           `json:"operand,omitempty"         doc:"Exactly one static JSON operand"`
	MaxAgeSeconds *int64                    `json:"max_age_seconds,omitempty"`
	Children      []AutomationConditionBody `json:"children,omitempty"        doc:"Nonempty for all/any"`
	Child         *AutomationConditionBody  `json:"child,omitempty"           doc:"Exactly one child for not"`
}

// AutomationConditionNodeResultBody is one evaluated node's retained evidence.
// SelectedValue is absent when nothing was selected and the bytes "null" for a
// selected JSON null, so missing and selected-null stay distinct.
type AutomationConditionNodeResultBody struct {
	ID            string          `json:"id"`
	Result        string          `json:"result"                   enum:"true,false,unknown"`
	UnknownReason *string         `json:"unknown_reason,omitempty" enum:"entity_missing,state_missing,evidence_in_future,evidence_expired,pointer_missing,type_mismatch"`
	SelectedValue json.RawMessage `json:"selected_value,omitempty"                                                                                                       doc:"Absent when nothing was selected; null for a selected JSON null"`
	ObservationID *string         `json:"observation_id,omitempty"`
	ObservedAt    *time.Time      `json:"observed_at,omitempty"`
}

// AutomationConditionEvaluationBody is one complete evaluation in definition pre-order.
type AutomationConditionEvaluationBody struct {
	EvaluatedAt time.Time                           `json:"evaluated_at"`
	Result      string                              `json:"result"       enum:"true,false,unknown"`
	Nodes       []AutomationConditionNodeResultBody `json:"nodes"`
}

// AutomationConditionDecisionBody is the retained admission explanation; Mode
// determines which of snapshot and evaluation are present.
type AutomationConditionDecisionBody struct {
	Mode            string                             `json:"mode"                 enum:"not_configured,not_evaluated,bypassed,evaluated"`
	BypassRequested bool                               `json:"bypass_requested"`
	Snapshot        *AutomationConditionBody           `json:"snapshot,omitempty"`
	Evaluation      *AutomationConditionEvaluationBody `json:"evaluation,omitempty"`
}

// conditionBody maps one optional normalized domain Condition tree to its
// transport form; a nil tree stays nil.
func conditionBody(condition *automations.Condition) *AutomationConditionBody {
	if condition == nil {
		return nil
	}
	body := conditionNodeBody(*condition)
	return &body
}

// conditionNodeBody maps one domain Condition node, emitting only its own family fields.
func conditionNodeBody(condition automations.Condition) AutomationConditionBody {
	body := AutomationConditionBody{ID: string(condition.ID), Kind: string(condition.Kind)}
	switch condition.Kind {
	case automations.ConditionEntityState:
		if condition.EntityState != nil {
			pointer := condition.EntityState.Pointer
			body.EntityID = string(condition.EntityState.EntityID)
			body.ValuePointer = &pointer
			body.Operator = string(condition.EntityState.Operator)
			body.Operand = append(json.RawMessage(nil), condition.EntityState.Operand...)
			if condition.EntityState.MaxAgeSeconds != nil {
				age := *condition.EntityState.MaxAgeSeconds
				body.MaxAgeSeconds = &age
			}
		}
	case automations.ConditionAll, automations.ConditionAny:
		body.Children = make([]AutomationConditionBody, 0, len(condition.Children))
		for _, child := range condition.Children {
			body.Children = append(body.Children, conditionNodeBody(child))
		}
	case automations.ConditionNot:
		body.Child = conditionBody(condition.Child)
	default:
	}
	return body
}

// conditionDecisionBody maps one retained decision to its transport form.
func conditionDecisionBody(decision automations.ConditionDecision) AutomationConditionDecisionBody {
	body := AutomationConditionDecisionBody{
		Mode:            string(decision.DecisionMode()),
		BypassRequested: decision.BypassRequested(),
		Snapshot:        conditionBody(decision.DecisionSnapshot()),
	}
	if evaluation := decision.DecisionEvaluation(); evaluation != nil {
		mapped := conditionEvaluationBody(*evaluation)
		body.Evaluation = &mapped
	}
	return body
}

// conditionEvaluationBody maps one evaluation, normalizing every retained time to UTC.
func conditionEvaluationBody(
	evaluation automations.ConditionEvaluation,
) AutomationConditionEvaluationBody {
	body := AutomationConditionEvaluationBody{
		EvaluatedAt: evaluation.EvaluatedAt.UTC(),
		Result:      string(evaluation.Result),
		Nodes:       make([]AutomationConditionNodeResultBody, 0, len(evaluation.Nodes)),
	}
	for _, node := range evaluation.Nodes {
		body.Nodes = append(body.Nodes, conditionNodeResultBody(node))
	}
	return body
}

// conditionNodeResultBody maps one evaluated node, preserving the missing versus
// selected-null distinction.
func conditionNodeResultBody(
	node automations.ConditionNodeResult,
) AutomationConditionNodeResultBody {
	body := AutomationConditionNodeResultBody{
		ID:            string(node.ID),
		Result:        string(node.Result),
		SelectedValue: append(json.RawMessage(nil), node.SelectedValue...),
	}
	if node.UnknownReason != nil {
		reason := string(*node.UnknownReason)
		body.UnknownReason = &reason
	}
	if node.ObservationID != nil {
		observationID := string(*node.ObservationID)
		body.ObservationID = &observationID
	}
	if node.ObservedAt != nil {
		observedAt := node.ObservedAt.UTC()
		body.ObservedAt = &observedAt
	}
	return body
}
