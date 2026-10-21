package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func registerGetHistoryEntry(api huma.API, handler *handler) {
	huma.Register(api, operation("get-automation-history-entry", http.MethodGet,
		"/automations/{automation_id}/history/{entry_id}", "Get one Automation history entry"), handler.getHistoryEntry)
}

// GetHistoryEntryInput addresses one retained Run or Skip.
type GetHistoryEntryInput struct {
	AutomationID string `path:"automation_id" doc:"Canonical Hearth Automation ID"`
	EntryID      string `path:"entry_id"      doc:"Canonical Hearth Automation Run or Skip ID"`
}

// AutomationHistoryEntryOutput is one history detail.
type AutomationHistoryEntryOutput struct {
	Body AutomationHistoryEntryBody
}

func (handler *handler) getHistoryEntry(
	ctx context.Context,
	input *GetHistoryEntryInput,
) (*AutomationHistoryEntryOutput, error) {
	id, err := automations.ParseAutomationID(input.AutomationID)
	if err != nil {
		return nil, newProblem(http.StatusBadRequest, "invalid_automation_id", "automation_id is not canonical")
	}
	if !validEntryID(input.EntryID) {
		return nil, newProblem(http.StatusBadRequest, "invalid_entry_id", "entry_id is not a canonical history ID")
	}
	entry, err := handler.automations.GetHistoryEntry(ctx, id, input.EntryID)
	if err != nil {
		return nil, mapDomainError(err)
	}
	return &AutomationHistoryEntryOutput{Body: historyEntryBody(entry)}, nil
}
func validEntryID(value string) bool {
	if _, err := automations.ParseRunID(value); err == nil {
		return true
	}
	if _, err := automations.ParseSkipID(value); err == nil {
		return true
	}
	return false
}
