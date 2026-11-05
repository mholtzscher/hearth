//nolint:goconst // JSON checks name a fixed schema inventory.
package dbload

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

const (
	validationSampleLimit = 64
	validationPageLimit   = 50
)

func validateOpenFixture(
	ctx context.Context,
	database *sql.DB,
	manifest Manifest,
) (map[string]int64, error) {
	counts, err := fixtureRowCounts(ctx, database)
	if err != nil {
		return nil, err
	}
	if err = compareFixtureCounts(manifest.RowCounts, counts); err != nil {
		return nil, err
	}
	for _, check := range []func(context.Context, *sql.DB) error{
		checkSQLiteIntegrity,
		checkJSONColumns,
		checkStateAnchors,
		checkReadProjection,
		checkAvailabilityHeads,
		checkRuntimeFences,
		checkUnknownEntityRejections,
	} {
		if err = check(ctx, database); err != nil {
			return nil, err
		}
	}
	if err = checkDomainDecoding(ctx, database); err != nil {
		return nil, err
	}
	if err = checkRepositoryDecoding(ctx, database, manifest); err != nil {
		return nil, err
	}
	return counts, nil
}

func compareFixtureCounts(recorded, actual map[string]int64) error {
	for _, table := range fixtureTableNames() {
		want, present := recorded[table]
		if !present {
			return fmt.Errorf("%w: manifest omits table %s", errFixtureIntegrity, table)
		}
		if actual[table] != want {
			return fmt.Errorf(
				"%w: %s has %d rows, manifest records %d",
				errFixtureIntegrity, table, actual[table], want,
			)
		}
	}
	return nil
}

func checkSQLiteIntegrity(ctx context.Context, database *sql.DB) error {
	var applicationTables int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version'").
		Scan(&applicationTables); err != nil {
		return err
	}
	if applicationTables != len(fixtureTableNames()) {
		return fmt.Errorf(
			"%w: runtime has %d application tables, fixture inventory has %d",
			errFixtureIntegrity,
			applicationTables,
			len(fixtureTableNames()),
		)
	}
	var result string
	if err := database.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("integrity check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("%w: %s", errFixtureIntegrity, result)
	}
	rows, err := database.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("foreign key check: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		return fmt.Errorf("%w: foreign key violation", errFixtureIntegrity)
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("foreign key check: %w", err)
	}
	return nil
}

//nolint:gosec // Table and column names come from a fixed schema inventory, never input.
func checkJSONColumns(ctx context.Context, database *sql.DB) error {
	checks := []struct{ table, column string }{
		{"adapter_function_catalogs", "functions_json"},
		{"adapter_function_invocations", "descriptor_json"},
		{"adapter_function_invocations", "arguments_json"},
		{"adapter_function_invocations", "result_json"},
		{"adapter_health_heads", "snapshot_json"},
		{"adapter_health_outbox", "snapshot_json"},
		{"agent_messages", "message_json"},
		{"automation_history", "condition_decision_json"},
		{"automation_history", "run_matched_trigger_ids_json"},
		{"automation_history", "skip_matched_triggers_json"},
		{"automation_history", "fact_value_json"},
		{"automation_step_executions", "evidence_json"},
		{"commands", "parameters_json"},
		{"dashboards", "definition_json"},
		{"device_adapter_health_projection", "snapshot_json"},
		{"device_facts_outbox", "value_json"},
		{"device_facts_outbox", "previous_value_json"},
		{"entities", "support_json"},
		{"observations", "state_value_json"},
	}
	for _, check := range checks {
		query := fmt.Sprintf(
			"SELECT COUNT(*) FROM %s WHERE %s IS NOT NULL AND json_valid(%s) = 0",
			check.table, check.column, check.column,
		)
		var invalid int64
		if err := database.QueryRowContext(ctx, query).Scan(&invalid); err != nil {
			return fmt.Errorf("validate %s.%s JSON: %w", check.table, check.column, err)
		}
		if invalid != 0 {
			return fmt.Errorf("%w: %s.%s has invalid JSON", errFixtureDomain, check.table, check.column)
		}
	}
	return nil
}

func checkStateAnchors(ctx context.Context, database *sql.DB) error {
	mismatched, err := fixtureCount(ctx, database, `
SELECT COUNT(*) FROM entity_states AS s
JOIN observations AS o
  ON o.observation_id = s.observation_id AND o.receive_order = s.receive_order
WHERE s.value_json <> o.state_value_json
   OR s.observed_at <> o.observed_at
   OR s.adapter_received_at <> o.adapter_received_at
   OR o.disposition = 'rejected'`)
	if err != nil {
		return err
	}
	if mismatched != 0 {
		return fmt.Errorf("%w: %d current State anchors disagree with their Observation", errFixtureDomain, mismatched)
	}
	missing, err := fixtureCount(ctx, database, `
SELECT COUNT(*) FROM entity_states AS s
LEFT JOIN observations AS o ON o.observation_id = s.observation_id
  AND o.receive_order = s.receive_order AND o.entity_id = s.entity_id
WHERE o.observation_id IS NULL`)
	if err != nil {
		return err
	}
	if missing != 0 {
		return fmt.Errorf("%w: %d current State anchors are missing", errFixtureDomain, missing)
	}
	newer, err := fixtureCount(ctx, database, `
SELECT COUNT(*) FROM entity_states AS s
WHERE EXISTS (
  SELECT 1 FROM observations AS o
  WHERE o.entity_id = s.entity_id AND o.receive_order > s.receive_order
    AND o.disposition IN ('applied','unchanged')
)`)
	if err != nil {
		return err
	}
	if newer != 0 {
		return fmt.Errorf("%w: %d current State anchors are not the latest evidence", errFixtureDomain, newer)
	}
	return nil
}

func checkReadProjection(ctx context.Context, database *sql.DB) error {
	entities, err := fixtureCount(ctx, database, "SELECT COUNT(*) FROM entities")
	if err != nil {
		return err
	}
	projected, err := fixtureCount(ctx, database, "SELECT COUNT(*) FROM entity_read_projection")
	if err != nil {
		return err
	}
	mapped, err := fixtureCount(ctx, database, "SELECT COUNT(*) FROM adapter_entity_mappings")
	if err != nil {
		return err
	}
	if entities != projected || entities != mapped {
		return fmt.Errorf(
			"%w: %d entities, %d projections, %d mappings",
			errFixtureDomain, entities, projected, mapped,
		)
	}
	stateEntities, err := fixtureCount(ctx, database, `
SELECT COUNT(*) FROM entities WHERE type_id IN ('hearth.power/v1','hearth.temperature/v1','hearth.relativehumidity/v1')`)
	if err != nil {
		return err
	}
	states, err := fixtureCount(ctx, database, "SELECT COUNT(*) FROM entity_states")
	if err != nil {
		return err
	}
	if stateEntities != states {
		return fmt.Errorf("%w: %d state Entities but %d current States", errFixtureDomain, stateEntities, states)
	}
	return nil
}

func checkAvailabilityHeads(ctx context.Context, database *sql.DB) error {
	entities, err := fixtureCount(ctx, database, "SELECT COUNT(*) FROM entities")
	if err != nil {
		return err
	}
	current, err := fixtureCount(ctx, database, "SELECT COUNT(*) FROM entity_availability_current")
	if err != nil {
		return err
	}
	effective, err := fixtureCount(ctx, database, "SELECT COUNT(*) FROM entity_availability_effective")
	if err != nil {
		return err
	}
	if entities != current || entities != effective {
		return fmt.Errorf(
			"%w: %d entities, %d current, %d effective availability rows",
			errFixtureDomain, entities, current, effective,
		)
	}
	mismatched, err := fixtureCount(ctx, database, `
SELECT COUNT(*) FROM entity_availability_current AS c
LEFT JOIN entity_availability_effective AS e ON e.entity_id = c.entity_id
LEFT JOIN health_transitions AS h ON h.receive_order = e.latest_transition_receive_order
WHERE h.receive_order IS NULL OR h.entity_id <> c.entity_id
  OR h.status <> e.status OR c.status <> e.status`)
	if err != nil {
		return err
	}
	if mismatched != 0 {
		return fmt.Errorf(
			"%w: %d availability heads disagree with their effective transition",
			errFixtureDomain,
			mismatched,
		)
	}
	return nil
}

func checkRuntimeFences(ctx context.Context, database *sql.DB) error {
	invalid, err := fixtureCount(ctx, database, `
SELECT COUNT(*) FROM adapter_instances AS i
LEFT JOIN adapter_runtimes AS r ON r.runtime_id = i.active_runtime_id
WHERE r.runtime_id IS NULL OR r.ended_at IS NOT NULL OR r.adapter_id <> i.adapter_id`)
	if err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("%w: %d Adapters without an active runtime fence", errFixtureDomain, invalid)
	}
	return nil
}

func checkUnknownEntityRejections(ctx context.Context, database *sql.DB) error {
	unknown, err := fixtureCount(ctx, database, `
SELECT COUNT(*) FROM observations AS o
LEFT JOIN entities AS e ON e.id = o.entity_id
WHERE o.rejection_code = 'unknown_entity' AND e.id IS NULL`)
	if err != nil {
		return err
	}
	if unknown == 0 {
		return fmt.Errorf("%w: no FK-free unknown-Entity rejection evidence", errFixtureDomain)
	}
	return nil
}

func fixtureCount(ctx context.Context, database *sql.DB, query string, args ...any) (int64, error) {
	var count int64
	if err := database.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("fixture check query: %w", err)
	}
	return count, nil
}

func checkDomainDecoding(ctx context.Context, database *sql.DB) error {
	if err := checkAutomationDefinitions(ctx, database); err != nil {
		return err
	}
	if err := checkDashboardDefinitions(ctx, database); err != nil {
		return err
	}
	if err := checkAgentMessages(ctx, database); err != nil {
		return err
	}
	if err := checkHealthSnapshots(ctx, database); err != nil {
		return err
	}
	return checkFunctionCatalogs(ctx, database)
}

func checkAutomationDefinitions(ctx context.Context, database *sql.DB) error {
	rows, err := database.QueryContext(ctx, `
SELECT r.definition_json FROM automation_definition_revisions AS r
JOIN automations AS a ON a.id = r.automation_id AND a.revision = r.revision
ORDER BY r.automation_id LIMIT ?`, validationSampleLimit)
	if err != nil {
		return fmt.Errorf("read automation definitions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		if _, err = automations.DecodeDefinition(json.RawMessage(raw)); err != nil {
			return fmt.Errorf("%w: stored automation definition: %w", errFixtureDomain, err)
		}
	}
	return rows.Err()
}

func checkDashboardDefinitions(ctx context.Context, database *sql.DB) error {
	rows, err := database.QueryContext(ctx,
		"SELECT definition_json FROM dashboards ORDER BY id LIMIT ?", validationSampleLimit)
	if err != nil {
		return fmt.Errorf("read dashboards: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		if _, err = dashboards.ParseDefinition(json.RawMessage(raw)); err != nil {
			return fmt.Errorf("%w: stored dashboard: %w", errFixtureDomain, err)
		}
	}
	return rows.Err()
}

func checkAgentMessages(ctx context.Context, database *sql.DB) error {
	rows, err := database.QueryContext(ctx,
		"SELECT role, message_json FROM agent_messages ORDER BY id LIMIT ?", validationSampleLimit)
	if err != nil {
		return fmt.Errorf("read agent messages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var role, raw string
		if err = rows.Scan(&role, &raw); err != nil {
			return err
		}
		var message schema.Message
		if err = json.Unmarshal([]byte(raw), &message); err != nil {
			return fmt.Errorf("%w: stored agent message: %w", errFixtureDomain, err)
		}
		if string(message.Role) != role {
			return fmt.Errorf("%w: agent message role disagrees with its column", errFixtureDomain)
		}
	}
	return rows.Err()
}

func checkHealthSnapshots(ctx context.Context, database *sql.DB) error {
	for _, query := range []string{
		"SELECT snapshot_json FROM adapter_health_heads ORDER BY adapter_id LIMIT ?",
		"SELECT snapshot_json FROM adapter_health_outbox ORDER BY enqueue_order LIMIT ?",
		"SELECT snapshot_json FROM device_adapter_health_projection ORDER BY adapter_id LIMIT ?",
	} {
		if err := decodeHealthSnapshots(ctx, database, query); err != nil {
			return err
		}
	}
	return nil
}

func decodeHealthSnapshots(ctx context.Context, database *sql.DB, query string) error {
	rows, err := database.QueryContext(ctx, query, validationSampleLimit)
	if err != nil {
		return fmt.Errorf("read health snapshots: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		if _, err = adapters.DecodeHealthSnapshot([]byte(raw)); err != nil {
			return fmt.Errorf("%w: stored health snapshot: %w", errFixtureDomain, err)
		}
	}
	return rows.Err()
}

func checkFunctionCatalogs(ctx context.Context, database *sql.DB) error {
	rows, err := database.QueryContext(ctx,
		"SELECT functions_json FROM adapter_function_catalogs ORDER BY adapter_id LIMIT ?",
		validationSampleLimit)
	if err != nil {
		return fmt.Errorf("read function catalogs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		var descriptors []adapters.Descriptor
		if err = json.Unmarshal([]byte(raw), &descriptors); err != nil || len(descriptors) == 0 {
			return fmt.Errorf("%w: stored function catalog: %w", errFixtureDomain, err)
		}
		for _, descriptor := range descriptors {
			if _, _, _, err = adapters.PrepareDescriptor(descriptor); err != nil {
				return fmt.Errorf("%w: stored function descriptor: %w", errFixtureDomain, err)
			}
		}
	}
	return rows.Err()
}
