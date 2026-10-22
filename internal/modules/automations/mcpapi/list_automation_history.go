//nolint:goconst // Literal schema keywords and wire fields keep operation contracts reviewable.
package mcpapi

import (
	"context"
	"fmt"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/internal/apicursor"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerListAutomationHistory(server *mcptools.Server, handler *handler) {
	mcptools.Register(server, mcptools.Tool[listAutomationHistoryToolInput, HistoryPage]{
		Name:        "list_automation_history",
		Description: "List compact Run and Skip summaries; inspect evidence with get_automation_history_entry",
		Handler:     handler.listAutomationHistory,
	})
}

type HistorySummary struct {
	EntryID      string    `json:"entry_id"         minLength:"1"`
	AutomationID string    `json:"automation_id"    minLength:"1"`
	Kind         string    `json:"kind"                           enum:"run,skip"`
	Revision     int64     `json:"revision"                                                                                             minimum:"1"`
	RecordedAt   time.Time `json:"recorded_at"`
	Source       string    `json:"source"                         enum:"manual,schedule,device_fact,held_state"`
	Status       string    `json:"status,omitempty"               enum:"running,succeeded,failed,interrupted"`
	Reason       string    `json:"reason,omitempty"               enum:"automation_busy,stale_fact,conditions_false,conditions_unknown"`
}
type HistoryPage struct {
	Items      []HistorySummary `json:"items"                 nullable:"false"`
	NextCursor string           `json:"next_cursor,omitempty"                  minLength:"1"`
}

func (handler *handler) listAutomationHistory(
	ctx context.Context,
	input listAutomationHistoryToolInput,
) (HistoryPage, error) {
	id := automations.AutomationID(input.AutomationID)
	params := automations.ListHistoryParams{AutomationID: id, Limit: input.Limit.pageSize()}
	if input.Cursor != "" {
		recordedAt, entryID, err := apicursor.DecodeHistory(input.Cursor, id)
		if err != nil {
			return HistoryPage{}, &mcptools.ToolError{Code: "invalid_cursor", Message: "history cursor is invalid"}
		}
		params.BeforeRecordedAt, params.BeforeID = recordedAt, entryID
	}
	page, err := handler.automations.ListHistory(ctx, params)
	if err != nil {
		return HistoryPage{}, automationFailure(err, failureNone)
	}
	result := HistoryPage{Items: make([]HistorySummary, len(page.Items))}
	for i, summary := range page.Items {
		item := HistorySummary{
			EntryID:      summary.ID,
			AutomationID: string(summary.AutomationID),
			Revision:     summary.Revision,
			RecordedAt:   summary.RecordedAt,
			Source:       causeSource(summary.Cause),
		}
		switch body := summary.Body.(type) {
		case automations.RunHistorySummary:
			item.Kind, item.Status = string(automations.HistoryRun), string(body.Status)
		case automations.SkipHistorySummary:
			item.Kind, item.Reason = string(automations.HistorySkip), string(body.Reason)
		default:
			return HistoryPage{}, mcptools.InternalToolError(
				fmt.Errorf("invalid retained history summary %T", summary.Body),
			)
		}
		result.Items[i] = item
	}
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		result.NextCursor, err = apicursor.EncodeHistory(id, last.RecordedAt, last.ID)
		if err != nil {
			return HistoryPage{}, mcptools.InternalToolError(err)
		}
	}
	return result, nil
}

type listAutomationHistoryToolInput struct {
	AutomationID automationID `json:"automation_id"    doc:"Canonical Hearth Automation ID"`
	Limit        pageLimit    `json:"limit,omitempty"  doc:"Page size, 1 to 200; omitted means 50"`
	Cursor       string       `json:"cursor,omitempty" doc:"Opaque next_cursor from a previous history page"`
}

func (HistorySummary) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.OneOf = []*huma.Schema{
		{
			Properties: map[string]*huma.Schema{"kind": {Enum: []any{"run"}}},
			Required:   []string{"status"},
			Not:        &huma.Schema{Required: []string{"reason"}},
		},
		{
			Properties: map[string]*huma.Schema{"kind": {Enum: []any{"skip"}}},
			Required:   []string{"reason"},
			Not:        &huma.Schema{Required: []string{"status"}},
		},
	}
	return schema
}

func causeSource(cause automations.AdmissionCause) string {
	switch cause.(type) {
	case automations.ManualCause:
		return "manual"
	case automations.ScheduleCause:
		return "schedule"
	case automations.DeviceFactCause:
		return "device_fact"
	case automations.HeldStateCause:
		return "held_state"
	default:
		panic("invalid retained admission cause")
	}
}
