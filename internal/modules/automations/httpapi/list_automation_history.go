package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/internal/apicursor"
)

func registerListHistory(api huma.API, handler *handler) {
	huma.Register(api, operation("list-automation-history", http.MethodGet,
		"/automations/{automation_id}/history", "List Automation history"), handler.listHistory)
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
		return nil, mapDomainError(err)
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
			return nil, huma.Error500InternalServerError("internal error")
		}
		output.Body.NextCursor = &cursor
	}
	return output, nil
}
