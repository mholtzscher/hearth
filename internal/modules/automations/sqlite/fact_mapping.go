package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// factSummaryFromRow decodes the copied Fact summary that explains one outcome,
// returning nil when the row carries none.
func factSummaryFromRow(row dbsqlc.AutomationHistory) (*automations.DeviceFactSummary, error) {
	if !row.FactID.Valid {
		return nil, nil //nolint:nilnil // An absent Fact summary is the manual-Run case.
	}
	if !row.FactFamily.Valid || !row.FactEntityID.Valid || !row.FactVariant.Valid ||
		!row.FactCausationID.Valid || !row.FactEmittedAt.Valid {
		return nil, fmt.Errorf(
			"%w: stored history %q has a partial Fact summary", automations.ErrInvalidAutomation, row.ID,
		)
	}
	emittedAt, err := decodeAutomationTimestamp(row.FactEmittedAt.String)
	if err != nil {
		return nil, fmt.Errorf("stored history %q fact_emitted_at: %w", row.ID, err)
	}
	summary := &automations.DeviceFactSummary{
		FactID:      devices.DeviceFactID(row.FactID.String),
		Family:      automations.DeviceFactFamily(row.FactFamily.String),
		EntityID:    devices.EntityID(row.FactEntityID.String),
		Variant:     row.FactVariant.String,
		CausationID: row.FactCausationID.String,
		EmittedAt:   emittedAt,
	}
	if row.FactValueJson.Valid {
		summary.ObservationValue = devices.Value(row.FactValueJson.String)
	}
	if err = automations.ValidateDeviceFactSummary(*summary); err != nil {
		return nil, err
	}
	return summary, nil
}

// storedFact holds the nullable Fact evidence columns one history row carries.
type storedFact struct {
	id          sql.NullString
	family      sql.NullString
	entityID    sql.NullString
	variant     sql.NullString
	causationID sql.NullString
	valueJSON   sql.NullString
	emittedAt   sql.NullString
}

// storedFactColumns encodes a Fact summary as history columns; a nil summary leaves every column NULL.
func storedFactColumns(summary *automations.DeviceFactSummary) storedFact {
	if summary == nil {
		return storedFact{}
	}
	stored := storedFact{
		id:          sql.NullString{String: string(summary.FactID), Valid: true},
		family:      sql.NullString{String: string(summary.Family), Valid: true},
		entityID:    sql.NullString{String: string(summary.EntityID), Valid: true},
		variant:     sql.NullString{String: summary.Variant, Valid: true},
		causationID: sql.NullString{String: summary.CausationID, Valid: true},
		emittedAt:   sql.NullString{String: encodeAutomationTimestamp(summary.EmittedAt), Valid: true},
	}
	if summary.ObservationValue != nil {
		stored.valueJSON = sql.NullString{String: string(summary.ObservationValue), Valid: true}
	}
	return stored
}
