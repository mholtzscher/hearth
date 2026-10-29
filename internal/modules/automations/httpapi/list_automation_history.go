package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/internal/apicursor"
)

//nolint:goconst // Operation metadata stays literal at registration.
func registerListHistory(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-automation-history",
		Method:      http.MethodGet,
		Path:        "/automations/{automation_id}/history",
		Summary:     "List Automation history",
		Tags:        []string{"Automations"},
		Responses: problemResponses(api,
			http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusInternalServerError),
	}, handler.listHistory)
}

// ListHistoryInput pages newest-first history for one Automation.
type ListHistoryInput struct {
	AutomationID string `path:"automation_id" doc:"Canonical Hearth Automation ID"`
	Limit        int    `                                                          query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor       string `                                                          query:"cursor"`
}

// AutomationHistoryCollectionOutput is a history page.
type AutomationHistoryCollectionOutput struct {
	Body AutomationHistoryCollectionBody
}

func (handler *handler) listHistory(
	ctx context.Context,
	input *ListHistoryInput,
) (*AutomationHistoryCollectionOutput, error) {
	id, err := automations.ParseAutomationID(input.AutomationID)
	if err != nil {
		return nil, newProblem(http.StatusBadRequest, "invalid_automation_id", "automation_id is not canonical")
	}
	params := automations.ListHistoryParams{AutomationID: id, Limit: input.Limit}
	if input.Cursor != "" {
		recordedAt, entryID, cursorErr := apicursor.DecodeHistory(input.Cursor, id)
		if cursorErr != nil {
			return nil, newProblem(http.StatusBadRequest, "invalid_cursor", "history cursor is invalid")
		}
		params.BeforeRecordedAt = recordedAt
		params.BeforeID = entryID
	}
	page, err := handler.automations.ListHistory(ctx, params)
	if err != nil {
		return nil, handler.mapError(ctx, "list-automation-history", err)
	}
	output := &AutomationHistoryCollectionOutput{}
	output.Body.Items = make([]AutomationHistorySummaryBody, len(page.Items))
	for index, summary := range page.Items {
		output.Body.Items[index] = historySummaryBody(summary)
	}
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		cursor, cursorErr := apicursor.EncodeHistory(id, last.RecordedAt, last.ID)
		if cursorErr != nil {
			handler.logInternalFailure(ctx, "list-automation-history", "mapping", cursorErr)
			return nil, huma.Error500InternalServerError("internal error")
		}
		output.Body.NextCursor = &cursor
	}
	return output, nil
}

// AutomationHistorySummaryFields are the shared lightweight history projection.
type AutomationHistorySummaryFields struct {
	ID             string             `json:"id"`
	AutomationID   string             `json:"automation_id"`
	AutomationName string             `json:"automation_name"`
	Revision       int64              `json:"revision"`
	RecordedAt     time.Time          `json:"recorded_at"`
	Cause          AdmissionCauseBody `json:"cause"`
}

// AutomationHistoryCollectionBody is one newest-first history page.
type AutomationHistoryCollectionBody struct {
	Items      []AutomationHistorySummaryBody `json:"items"`
	NextCursor *string                        `json:"next_cursor,omitempty"`
}

func historySummaryBody(summary automations.HistorySummary) AutomationHistorySummaryBody {
	body := AutomationHistorySummaryFields{
		ID:             summary.ID,
		AutomationID:   string(summary.AutomationID),
		AutomationName: summary.AutomationName,
		Revision:       summary.Revision,
		RecordedAt:     summary.RecordedAt,
		Cause:          admissionCauseBody(summary.Cause),
	}
	return historySummaryVariantBody(body, summary.Body, summary.ConditionSummary)
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

func (UnevaluatedRunHistorySummaryBody) isAutomationHistorySummaryBody() {}

func (EvaluatedRunHistorySummaryBody) isAutomationHistorySummaryBody() {}

func (UnevaluatedSkipHistorySummaryBody) isAutomationHistorySummaryBody() {}

func (EvaluatedSkipHistorySummaryBody) isAutomationHistorySummaryBody() {}

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
