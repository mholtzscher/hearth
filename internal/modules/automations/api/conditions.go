package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// StartAutomationRunBody carries only the optional Condition bypass request for
// a manual Run. An omitted request body leaves the input Body nil, which applies
// Conditions exactly like an explicit false; a present body must be one strict
// JSON object. Unknown members, JSON null, non-boolean bypass values, and
// trailing JSON are rejected before admission.
type StartAutomationRunBody struct {
	BypassConditions bool `json:"bypass_conditions,omitempty"`
}

// UnmarshalJSON enforces the strict optional manual bypass body. Huma owns body
// size, schema validation, and error transport; this decoder closes the shapes a
// generated object schema accepts but the product contract forbids, and it always
// returns a fixed, payload-free error so a rejected body never echoes values.
func (body *StartAutomationRunBody) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var members map[string]json.RawMessage
	if err := decoder.Decode(&members); err != nil {
		return errors.New("manual run body must be one JSON object")
	}
	if members == nil {
		return errors.New("manual run body must be a JSON object, not null")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("manual run body must contain exactly one JSON object")
	}
	for member := range members {
		if member != "bypass_conditions" {
			return errors.New("manual run body has an unknown member")
		}
	}
	bypass := false
	if operand, present := members["bypass_conditions"]; present {
		switch strings.TrimSpace(string(operand)) {
		case "true":
			bypass = true
		case "false":
			bypass = false
		default:
			return errors.New("bypass_conditions must be a JSON boolean")
		}
	}
	// Assign only after every member is validated, so a rejected body never
	// leaves a partially-applied bypass behind.
	*body = StartAutomationRunBody{BypassConditions: bypass}
	return nil
}

// AutomationConditionBody is one recursive, flattened Condition node. It mirrors
// the strict persisted definition shape: an entity_state leaf carries its family
// fields directly, all/any carry children, and not carries one child. Every
// family-inapplicable field stays absent rather than emitting a nullable
// placeholder, and the empty pointer of a leaf is explicit.
type AutomationConditionBody struct {
	ID            string                    `json:"id"                        doc:"Node identifier, unique within the tree"`
	Kind          string                    `json:"kind"                                                                                enum:"entity_state,all,any,not"`
	EntityID      string                    `json:"entity_id,omitempty"`
	Pointer       *string                   `json:"pointer,omitempty"         doc:"RFC 6901 JSON Pointer into the selected State value"`
	Operator      string                    `json:"operator,omitempty"                                                                  enum:"eq,ne,lt,lte,gt,gte"`
	Operand       json.RawMessage           `json:"operand,omitempty"         doc:"Exactly one static JSON operand"`
	MaxAgeSeconds *int64                    `json:"max_age_seconds,omitempty"`
	Children      []AutomationConditionBody `json:"children,omitempty"        doc:"Nonempty for all/any"`
	Child         *AutomationConditionBody  `json:"child,omitempty"           doc:"Exactly one child for not"`
}

// AutomationConditionNodeResultBody is one evaluated node's retained evidence.
// SelectedValue is absent when the pointer selected nothing and the JSON bytes
// "null" when it selected a JSON null, so missing and selected-null stay
// distinct. ObservationID and ObservedAt are present together.
type AutomationConditionNodeResultBody struct {
	ID            string          `json:"id"`
	Result        string          `json:"result"                   enum:"true,false,unknown"`
	UnknownReason *string         `json:"unknown_reason,omitempty" enum:"entity_missing,state_missing,evidence_in_future,evidence_expired,pointer_missing,type_mismatch"`
	SelectedValue json.RawMessage `json:"selected_value,omitempty"                                                                                                       doc:"Absent when nothing was selected; null for a selected JSON null"`
	ObservationID *string         `json:"observation_id,omitempty"`
	ObservedAt    *time.Time      `json:"observed_at,omitempty"`
}

// AutomationConditionEvaluationBody is one complete evaluation in definition
// pre-order. It appears only for the evaluated decision mode.
type AutomationConditionEvaluationBody struct {
	EvaluatedAt time.Time                           `json:"evaluated_at"`
	Result      string                              `json:"result"       enum:"true,false,unknown"`
	Nodes       []AutomationConditionNodeResultBody `json:"nodes"`
}

// AutomationConditionDecisionBody is the retained admission explanation. Mode
// determines presence: not_configured carries neither snapshot nor evaluation,
// not_evaluated and bypassed carry a snapshot only, and evaluated carries both.
type AutomationConditionDecisionBody struct {
	Mode            string                             `json:"mode"                 enum:"not_configured,not_evaluated,bypassed,evaluated"`
	BypassRequested bool                               `json:"bypass_requested"`
	Snapshot        *AutomationConditionBody           `json:"snapshot,omitempty"`
	Evaluation      *AutomationConditionEvaluationBody `json:"evaluation,omitempty"`
}

// conditionBody maps one optional normalized domain Condition tree to its
// transport form. A nil tree stays nil so an unconditioned decision emits no
// fabricated snapshot.
func conditionBody(condition *automations.Condition) *AutomationConditionBody {
	if condition == nil {
		return nil
	}
	body := conditionNodeBody(*condition)
	return &body
}

// conditionNodeBody maps one domain Condition node, emitting only the fields of
// its own family.
func conditionNodeBody(condition automations.Condition) AutomationConditionBody {
	body := AutomationConditionBody{ID: string(condition.ID), Kind: string(condition.Kind)}
	switch condition.Kind {
	case automations.ConditionEntityState:
		if condition.EntityState != nil {
			pointer := condition.EntityState.Pointer
			body.EntityID = string(condition.EntityState.EntityID)
			body.Pointer = &pointer
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
		Mode:            string(decision.Mode),
		BypassRequested: decision.BypassRequested,
		Snapshot:        conditionBody(decision.Snapshot),
	}
	if decision.Evaluation != nil {
		evaluation := conditionEvaluationBody(*decision.Evaluation)
		body.Evaluation = &evaluation
	}
	return body
}

// conditionEvaluationBody maps one evaluation, normalizing every retained time
// to UTC so the RFC3339Nano rendering is canonical.
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
// selected-null distinction and pairing Observation identity with its time.
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
