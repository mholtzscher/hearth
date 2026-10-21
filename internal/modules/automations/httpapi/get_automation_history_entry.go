package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

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

// AutomationMatchedTriggersBody uses the same concrete Trigger DTOs as definitions.
type AutomationMatchedTriggersBody struct{ encoded json.RawMessage }

func (body AutomationMatchedTriggersBody) MarshalJSON() ([]byte, error) {
	return body.encoded.MarshalJSON()
}

// AutomationSkipBody is one retained Skip with owned Cause and matched Triggers.
type AutomationSkipBody struct {
	ID             string `json:"id"`
	AutomationID   string `json:"automation_id"`
	AutomationName string `json:"automation_name"`
	Revision       int64  `json:"revision"`

	Cause           AdmissionCauseBody            `json:"cause"`
	MatchedTriggers AutomationMatchedTriggersBody `json:"matched_triggers"`
	// Reason identifies why a matching automation did not start a Run.
	Reason string `json:"reason" enum:"automation_busy,stale_fact,conditions_false,conditions_unknown"`
	// ConditionDecision records how admission conditions were evaluated.
	ConditionDecision AutomationConditionDecisionBody `json:"condition_decision"`
	SkippedAt         time.Time                       `json:"skipped_at"`
}

func matchedTriggersBody(triggers []automations.Trigger) AutomationMatchedTriggersBody {
	raw, err := automations.EncodeMatchedTriggers(triggers)
	if err != nil {
		panic(fmt.Errorf("invalid retained matched Triggers: %w", err))
	}
	return AutomationMatchedTriggersBody{encoded: raw}
}

func automationSkipBody(skip automations.Skip) AutomationSkipBody {
	body := AutomationSkipBody{
		ID:                string(skip.ID),
		AutomationID:      string(skip.AutomationID),
		AutomationName:    skip.AutomationName,
		Revision:          skip.Revision,
		Cause:             admissionCauseBody(skip.Cause),
		MatchedTriggers:   matchedTriggersBody(skip.MatchedTriggers),
		Reason:            string(skip.Reason),
		ConditionDecision: conditionDecisionBody(skip.ConditionDecision),
		SkippedAt:         skip.SkippedAt,
	}
	return body
}

func historyEntryBody(entry automations.HistoryEntry) AutomationHistoryEntryBody {
	switch entry := entry.(type) {
	case automations.Run:
		return AutomationHistoryEntryBody{
			Variant: RunHistoryEntryBody{Kind: string(automations.HistoryRun), Run: automationRunBody(entry)},
		}
	case automations.Skip:
		return AutomationHistoryEntryBody{
			Variant: SkipHistoryEntryBody{Kind: string(automations.HistorySkip), Skip: automationSkipBody(entry)},
		}
	default:
		panic("invalid retained history entry")
	}
}

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

func (RunHistoryEntryBody) isAutomationHistoryEntryBody() {}

func (SkipHistoryEntryBody) isAutomationHistoryEntryBody() {}

func (body AutomationHistoryEntryBody) MarshalJSON() ([]byte, error) {
	switch body.Variant.(type) {
	case RunHistoryEntryBody, SkipHistoryEntryBody:
		return json.Marshal(body.Variant)
	default:
		return nil, fmt.Errorf("invalid history entry DTO %T", body.Variant)
	}
}
