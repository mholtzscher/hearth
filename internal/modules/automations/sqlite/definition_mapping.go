package sqlite

import (
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

// automationRecord decodes one stored row and rejects a malformed identity,
// revision, timestamp, or definition instead of exposing partially trusted data.
func automationRecord(row dbsqlc.Automation) (automations.AutomationRecord, error) {
	id, err := automations.ParseAutomationID(row.ID)
	if err != nil {
		return automations.AutomationRecord{}, fmt.Errorf("stored automation: %w", err)
	}
	if row.Revision < 1 {
		return automations.AutomationRecord{}, fmt.Errorf(
			"%w: stored automation %q revision %d", automations.ErrInvalidAutomation, id, row.Revision,
		)
	}
	definition, err := automations.DecodeAutomationDefinition(json.RawMessage(row.DefinitionJson))
	if err != nil {
		return automations.AutomationRecord{}, fmt.Errorf("stored automation %q: %w", id, err)
	}
	createdAt, err := decodeAutomationTimestamp(row.CreatedAt)
	if err != nil {
		return automations.AutomationRecord{}, fmt.Errorf("stored automation %q created_at: %w", id, err)
	}
	updatedAt, err := decodeAutomationTimestamp(row.UpdatedAt)
	if err != nil {
		return automations.AutomationRecord{}, fmt.Errorf("stored automation %q updated_at: %w", id, err)
	}
	return automations.AutomationRecord{
		ID:         id,
		Revision:   row.Revision,
		Definition: definition,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}, nil
}
