package api

import (
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// AutomationHistoryEntryBody contains exactly one Run or Skip detail.
type AutomationHistoryEntryBody struct{ Variant AutomationHistoryEntryVariant }

//sumtype:decl
type AutomationHistoryEntryVariant interface{ isAutomationHistoryEntryBody() }

type RunHistoryEntryBody struct {
	Kind string            `json:"kind" enum:"run"`
	Run  AutomationRunBody `json:"run"`
}

type SkipHistoryEntryBody struct {
	Kind string             `json:"kind" enum:"skip"`
	Skip AutomationSkipBody `json:"skip"`
}

func (RunHistoryEntryBody) isAutomationHistoryEntryBody()  {}
func (SkipHistoryEntryBody) isAutomationHistoryEntryBody() {}

func (body AutomationHistoryEntryBody) MarshalJSON() ([]byte, error) {
	switch body.Variant.(type) {
	case RunHistoryEntryBody, SkipHistoryEntryBody:
		return json.Marshal(body.Variant)
	default:
		return nil, fmt.Errorf("invalid history entry DTO %T", body.Variant)
	}
}

// AutomationHistorySummaryBody contains a concrete Run or Skip summary.
// Condition labels are flattened only after selecting a concrete summary mode.
type AutomationHistorySummaryBody struct {
	Variant AutomationHistorySummaryVariant
}

//sumtype:decl
type AutomationHistorySummaryVariant interface{ isAutomationHistorySummaryBody() }

func (body AutomationHistorySummaryBody) MarshalJSON() ([]byte, error) {
	switch body.Variant.(type) {
	case UnevaluatedRunHistorySummaryBody, EvaluatedRunHistorySummaryBody,
		UnevaluatedSkipHistorySummaryBody, EvaluatedSkipHistorySummaryBody:
		return json.Marshal(body.Variant)
	default:
		return nil, fmt.Errorf("invalid history summary DTO %T", body.Variant)
	}
}

type RunHistorySummaryFields struct {
	AutomationHistorySummaryFields

	Kind   string `json:"kind"   enum:"run"`
	Status string `json:"status" enum:"running,succeeded,failed,interrupted"`
}

type SkipHistorySummaryFields struct {
	AutomationHistorySummaryFields

	Kind   string `json:"kind"   enum:"skip"`
	Reason string `json:"reason" enum:"automation_busy,stale_fact,conditions_false,conditions_unknown"`
}

type UnevaluatedConditionSummaryFields struct {
	ConditionMode   string `json:"condition_mode"   enum:"not_configured,not_evaluated,bypassed"`
	BypassRequested bool   `json:"bypass_requested"`
}

type EvaluatedConditionSummaryFields struct {
	ConditionMode   string `json:"condition_mode"   enum:"evaluated"`
	BypassRequested bool   `json:"bypass_requested"`
	ConditionResult string `json:"condition_result" enum:"true,false,unknown"`
}

type UnevaluatedRunHistorySummaryBody struct {
	RunHistorySummaryFields
	UnevaluatedConditionSummaryFields
}

type EvaluatedRunHistorySummaryBody struct {
	RunHistorySummaryFields
	EvaluatedConditionSummaryFields
}

type UnevaluatedSkipHistorySummaryBody struct {
	SkipHistorySummaryFields
	UnevaluatedConditionSummaryFields
}

type EvaluatedSkipHistorySummaryBody struct {
	SkipHistorySummaryFields
	EvaluatedConditionSummaryFields
}

func (UnevaluatedRunHistorySummaryBody) isAutomationHistorySummaryBody()  {}
func (EvaluatedRunHistorySummaryBody) isAutomationHistorySummaryBody()    {}
func (UnevaluatedSkipHistorySummaryBody) isAutomationHistorySummaryBody() {}
func (EvaluatedSkipHistorySummaryBody) isAutomationHistorySummaryBody()   {}

func historySummaryVariantBody(
	fields AutomationHistorySummaryFields,
	history automations.HistorySummaryBody,
	condition automations.ConditionDecisionSummary,
) AutomationHistorySummaryBody {
	// Each concrete DTO contains only its own history and Condition fields.
	var dto AutomationHistorySummaryVariant
	switch condition := condition.(type) {
	case automations.NotConfiguredSummary:
		dto = unevaluatedHistorySummaryBody(
			fields,
			history,
			UnevaluatedConditionSummaryFields{ConditionMode: "not_configured"},
		)
	case automations.NotEvaluatedSummary:
		dto = unevaluatedHistorySummaryBody(
			fields,
			history,
			UnevaluatedConditionSummaryFields{ConditionMode: "not_evaluated"},
		)
	case automations.BypassedSummary:
		dto = unevaluatedHistorySummaryBody(
			fields,
			history,
			UnevaluatedConditionSummaryFields{ConditionMode: "bypassed", BypassRequested: true},
		)
	case automations.EvaluatedSummary:
		dto = evaluatedHistorySummaryBody(fields, history, EvaluatedConditionSummaryFields{
			ConditionMode: "evaluated", ConditionResult: string(condition.Result),
		})
	default:
		panic("invalid retained Condition summary")
	}
	return AutomationHistorySummaryBody{Variant: dto}
}

func unevaluatedHistorySummaryBody(
	fields AutomationHistorySummaryFields,
	history automations.HistorySummaryBody,
	condition UnevaluatedConditionSummaryFields,
) AutomationHistorySummaryVariant {
	switch history := history.(type) {
	case automations.RunHistorySummary:
		return UnevaluatedRunHistorySummaryBody{
			AutomationHistorySummaryFields:    fields,
			Kind:                              string(automations.HistoryRun),
			Status:                            string(history.Status),
			UnevaluatedConditionSummaryFields: condition,
		}
	case automations.SkipHistorySummary:
		return UnevaluatedSkipHistorySummaryBody{
			AutomationHistorySummaryFields:    fields,
			Kind:                              string(automations.HistorySkip),
			Reason:                            string(history.Reason),
			UnevaluatedConditionSummaryFields: condition,
		}
	default:
		panic("invalid retained history summary")
	}
}

func evaluatedHistorySummaryBody(
	fields AutomationHistorySummaryFields,
	history automations.HistorySummaryBody,
	condition EvaluatedConditionSummaryFields,
) AutomationHistorySummaryVariant {
	switch history := history.(type) {
	case automations.RunHistorySummary:
		return EvaluatedRunHistorySummaryBody{
			AutomationHistorySummaryFields:  fields,
			Kind:                            string(automations.HistoryRun),
			Status:                          string(history.Status),
			EvaluatedConditionSummaryFields: condition,
		}
	case automations.SkipHistorySummary:
		return EvaluatedSkipHistorySummaryBody{
			AutomationHistorySummaryFields:  fields,
			Kind:                            string(automations.HistorySkip),
			Reason:                          string(history.Reason),
			EvaluatedConditionSummaryFields: condition,
		}
	default:
		panic("invalid retained history summary")
	}
}
