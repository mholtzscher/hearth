package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const forecastNeutralMigrationVersion = 32

// Frozen names for migration 32. Numeric values retain their canonical units.
var forecastNeutralFields = [][2]string{ //nolint:gochecknoglobals // Frozen migration mapping.
	{"temperature_c", "temperature"},
	{"apparent_temperature_c", "apparent_temperature"},
	{"temperature_min_c", "temperature_min"},
	{"temperature_max_c", "temperature_max"},
	{"precipitation_probability_percent", "precipitation_probability"},
	{"precipitation_rate_mmh", "precipitation_rate"},
	{"humidity_percent", "humidity"},
	{"pressure_hpa", "pressure"},
	{"wind_speed_mps", "wind_speed"},
	{"wind_gust_mps", "wind_gust"},
	{"wind_direction_degrees", "wind_direction"},
	{"cloud_cover_percent", "cloud_cover"},
}

// neutralForecast preserves numeric tokens and leaves already-neutral JSON untouched.
//
//nolint:gocognit // Preserve each period's original data and detect collisions.
func neutralForecast(
	raw string,
) (string, error) {
	if strings.TrimSpace(raw) == "null" {
		return raw, nil
	}
	object, err := valueObject(json.RawMessage(raw))
	if err != nil {
		return "", err
	}
	periods, err := valueArray(object["periods"])
	if err != nil {
		return "", fmt.Errorf("forecast periods: %w", err)
	}
	changed := false
	for i, rawPeriod := range periods {
		period, periodErr := valueObject(rawPeriod)
		if periodErr != nil {
			return "", fmt.Errorf("forecast period %d: %w", i, periodErr)
		}
		periodChanged := false
		for _, names := range forecastNeutralFields {
			value, found := period[names[0]]
			if !found {
				continue
			}
			if _, collision := period[names[1]]; collision {
				return "", fmt.Errorf("forecast period %d contains both %s and %s", i, names[0], names[1])
			}
			period[names[1]] = value
			delete(period, names[0])
			periodChanged = true
		}
		if periodChanged {
			periods[i], err = json.Marshal(period)
			if err != nil {
				return "", err
			}
			changed = true
		}
	}
	if !changed {
		return raw, nil
	}
	object["periods"], err = json.Marshal(periods)
	if err != nil {
		return "", err
	}
	result, err := json.Marshal(object)
	return string(result), err
}

type forecastStateRow struct {
	entity, observation, value string
	order                      int64
	backing                    sql.NullString
}

func migrateForecastNeutral(ctx context.Context, tx *sql.Tx) error {
	if err := migrateForecastStates(ctx, tx); err != nil {
		return err
	}
	return migrateForecastFacts(ctx, tx)
}

func migrateForecastStates(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(
		ctx,
		`SELECT s.entity_id, s.observation_id, s.receive_order, s.value_json, o.state_value_json
 FROM entity_states s JOIN entities e ON e.id=s.entity_id
 LEFT JOIN observations o ON o.observation_id=s.observation_id AND o.receive_order=s.receive_order
 WHERE e.type_id IN ('hearth.hourlyforecast/v1','hearth.dailyforecast/v1') ORDER BY s.receive_order`,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	var states []forecastStateRow
	for rows.Next() {
		var state forecastStateRow
		if err = rows.Scan(&state.entity, &state.observation, &state.order, &state.value, &state.backing); err != nil {
			_ = rows.Close()
			return err
		}
		states = append(states, state)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, state := range states {
		if !state.backing.Valid || state.backing.String != state.value {
			return fmt.Errorf(
				"forecast state %s does not match backing observation %s at receive order %d",
				state.entity,
				state.observation,
				state.order,
			)
		}
		converted, convertErr := neutralForecast(state.value)
		if convertErr != nil {
			return fmt.Errorf("forecast state %s: %w", state.entity, convertErr)
		}
		if converted == state.value {
			continue
		}
		if _, err = tx.ExecContext(
			ctx,
			`UPDATE entity_states SET value_json=? WHERE entity_id=?`,
			converted,
			state.entity,
		); err != nil {
			return err
		}
		if _, err = tx.ExecContext(
			ctx,
			`UPDATE observations SET state_value_json=? WHERE observation_id=? AND receive_order=?`,
			converted,
			state.observation,
			state.order,
		); err != nil {
			return err
		}
	}
	return nil
}

type forecastFactRow struct {
	order    int64
	source   string
	value    string
	previous sql.NullString
	backing  sql.NullString
}

func migrateForecastFacts(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(
		ctx,
		`SELECT f.enqueue_order, f.source_id, f.value_json, f.previous_value_json, o.state_value_json
 FROM device_facts_outbox f JOIN entities e ON e.id=f.entity_id
 LEFT JOIN observations o ON o.observation_id=f.source_id
 WHERE f.family='observation' AND e.type_id IN ('hearth.hourlyforecast/v1','hearth.dailyforecast/v1')`,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	var facts []forecastFactRow
	for rows.Next() {
		var fact forecastFactRow
		if err = rows.Scan(&fact.order, &fact.source, &fact.value, &fact.previous, &fact.backing); err != nil {
			_ = rows.Close()
			return err
		}
		facts = append(facts, fact)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, fact := range facts {
		if err = migrateForecastFact(ctx, tx, fact); err != nil {
			return fmt.Errorf("forecast fact %d: %w", fact.order, err)
		}
	}
	return nil
}

func migrateForecastFact(ctx context.Context, tx *sql.Tx, fact forecastFactRow) error {
	value, err := neutralForecast(fact.value)
	if err != nil {
		return err
	}
	previous := fact.previous
	if previous.Valid {
		previous.String, err = neutralForecast(previous.String)
		if err != nil {
			return err
		}
	}
	if value != fact.value || previous != fact.previous {
		if _, err = tx.ExecContext(
			ctx,
			`UPDATE device_facts_outbox SET value_json=?, previous_value_json=? WHERE enqueue_order=?`,
			value,
			previous,
			fact.order,
		); err != nil {
			return err
		}
	}
	if fact.backing.Valid {
		backing, convertErr := neutralForecast(fact.backing.String)
		if convertErr != nil {
			return convertErr
		}
		if backing != fact.backing.String {
			_, err = tx.ExecContext(
				ctx,
				`UPDATE observations SET state_value_json=? WHERE observation_id=?`,
				backing,
				fact.source,
			)
		}
	}
	return err
}

func refuseForecastNeutralDown(context.Context, *sql.Tx) error {
	return errors.New(
		"restore the pre-upgrade backup with its matching binary; forecast names have no lossless Down migration",
	)
}
