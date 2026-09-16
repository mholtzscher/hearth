package sqlite

import (
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

// automationRecord decodes one stored row, rejecting a malformed identity,
// revision, timestamp, or definition.
func automationRecord(row dbsqlc.Automation) (automations.Record, error) {
	id, err := automations.ParseAutomationID(row.ID)
	if err != nil {
		return automations.Record{}, fmt.Errorf("stored automation: %w", err)
	}
	if row.Revision < 1 {
		return automations.Record{}, fmt.Errorf(
			"%w: stored automation %q revision %d", automations.ErrInvalidAutomation, id, row.Revision,
		)
	}
	definition, err := automations.DecodeDefinition(json.RawMessage(row.DefinitionJson))
	if err != nil {
		return automations.Record{}, fmt.Errorf("stored automation %q: %w", id, err)
	}
	createdAt, err := decodeAutomationTimestamp(row.CreatedAt)
	if err != nil {
		return automations.Record{}, fmt.Errorf("stored automation %q created_at: %w", id, err)
	}
	updatedAt, err := decodeAutomationTimestamp(row.UpdatedAt)
	if err != nil {
		return automations.Record{}, fmt.Errorf("stored automation %q updated_at: %w", id, err)
	}
	return automations.Record{
		ID:         id,
		Revision:   row.Revision,
		Definition: definition,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}, nil
}
