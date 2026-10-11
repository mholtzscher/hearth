package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// factFromRow decodes the copied Fact evidence that explains one outcome,
// returning nil when the row carries none.
func factFromRow(row dbsqlc.AutomationHistory) (automations.DeviceFact, error) {
	if !row.FactID.Valid {
		if row.FactFamily.Valid || row.FactEntityID.Valid || row.FactVariant.Valid ||
			row.FactCausationID.Valid || row.FactEmittedAt.Valid || row.FactValueJson.Valid ||
			row.FactPreviousValueJson.Valid {
			return nil, fmt.Errorf(
				"%w: stored history %q has orphan Fact evidence",
				automations.ErrInvalidAutomation,
				row.ID,
			)
		}
		return nil, nil //nolint:nilnil // Non-Fact causes carry no Fact evidence.
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
	var fact automations.DeviceFact
	switch automations.DeviceFactFamily(row.FactFamily.String) {
	case automations.DeviceFactObservation:
		if !row.FactValueJson.Valid {
			return nil, fmt.Errorf("%w: stored Observation Fact lacks value", automations.ErrInvalidAutomation)
		}
		var previous devices.Value
		if row.FactPreviousValueJson.Valid {
			previous = devices.Value(row.FactPreviousValueJson.String)
		}
		fact = automations.ObservationFact{
			FactID: devices.DeviceFactID(row.FactID.String), EntityID: devices.EntityID(row.FactEntityID.String),
			ObservationID: devices.ObservationID(row.FactCausationID.String),
			Disposition:   devices.ObservationDisposition(row.FactVariant.String),
			Value:         devices.Value(row.FactValueJson.String), PreviousValue: previous, EmittedAt: emittedAt,
		}
	case automations.DeviceFactEntityEvent:
		if row.FactValueJson.Valid || row.FactPreviousValueJson.Valid {
			return nil, fmt.Errorf(
				"%w: stored Entity Event carries Observation values",
				automations.ErrInvalidAutomation,
			)
		}
		fact = automations.EntityEventFact{
			FactID: devices.DeviceFactID(row.FactID.String), EntityID: devices.EntityID(row.FactEntityID.String),
			EventID: devices.EntityEventID(row.FactCausationID.String),
			Name:    devices.EntityEventName(row.FactVariant.String), EmittedAt: emittedAt,
		}
	default:
		return nil, fmt.Errorf("%w: stored Fact has unknown family", automations.ErrInvalidAutomation)
	}
	if err = automations.ValidateDeviceFact(fact); err != nil {
		return nil, fmt.Errorf("%w: stored history %q Fact: %w", automations.ErrInvalidAutomation, row.ID, err)
	}
	return fact, nil
}

// storedFact holds the nullable Fact evidence columns one history row carries.
type storedFact struct {
	id                sql.NullString
	family            sql.NullString
	entityID          sql.NullString
	variant           sql.NullString
	causationID       sql.NullString
	valueJSON         sql.NullString
	previousValueJSON sql.NullString
	emittedAt         sql.NullString
}

// storedFactColumns encodes the Fact cause as history columns; other causes leave every column NULL.
func storedFactColumns(cause automations.AdmissionCause) storedFact {
	var fact automations.DeviceFact
	switch cause := cause.(type) {
	case automations.DeviceFactCause:
		fact = cause.Fact
	case automations.ManualCause, automations.HeldStateCause, automations.ScheduleCause:
		return storedFact{}
	default:
		return storedFact{}
	}
	stored := storedFact{
		id:        sql.NullString{String: string(automations.FactID(fact)), Valid: true},
		family:    sql.NullString{String: string(automations.FactFamily(fact)), Valid: true},
		emittedAt: sql.NullString{String: encodeAutomationTimestamp(automations.FactEmittedAt(fact)), Valid: true},
	}
	switch fact := fact.(type) {
	case automations.ObservationFact:
		stored.entityID = sql.NullString{String: string(fact.EntityID), Valid: true}
		stored.variant = sql.NullString{String: string(fact.Disposition), Valid: true}
		stored.causationID = sql.NullString{String: string(fact.ObservationID), Valid: true}
		stored.valueJSON = sql.NullString{String: string(fact.Value), Valid: true}
		if fact.PreviousValue != nil {
			stored.previousValueJSON = sql.NullString{String: string(fact.PreviousValue), Valid: true}
		}
	case automations.EntityEventFact:
		stored.entityID = sql.NullString{String: string(fact.EntityID), Valid: true}
		stored.variant = sql.NullString{String: string(fact.Name), Valid: true}
		stored.causationID = sql.NullString{String: string(fact.EventID), Valid: true}
	default:
		return storedFact{}
	}
	return stored
}

func causeFromRow(row dbsqlc.AutomationHistory, source automations.RunSource) (automations.AdmissionCause, error) {
	fact, err := factFromRow(row)
	if err != nil {
		return nil, err
	}
	held, err := heldStateEvidenceFromRow(row)
	if err != nil {
		return nil, err
	}
	switch source {
	case automations.RunSourceManual:
		if fact == nil && held == nil {
			return automations.ManualCause{}, nil
		}
	case automations.RunSourceDeviceFact:
		if fact != nil && held == nil {
			return automations.DeviceFactCause{Fact: fact}, nil
		}
	case automations.RunSourceHeldState:
		if fact == nil && held != nil {
			return automations.HeldStateCause{Evidence: *held}, nil
		}
	case automations.RunSourceSchedule:
		if fact == nil && held == nil {
			return automations.ScheduleCause{}, nil
		}
	}
	return nil, fmt.Errorf(
		"%w: stored history %q source disagrees with evidence",
		automations.ErrInvalidAutomation,
		row.ID,
	)
}
