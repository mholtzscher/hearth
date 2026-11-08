//nolint:goconst // Table names are a fixed schema inventory repeated in checks.
package dbload

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strconv"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// Seed creates a deterministic, migrated fixture database inside a new
// suite-owned output directory and returns its manifest.
func Seed(ctx context.Context, outputDir string, scenario Scenario) (Manifest, error) {
	plan, err := planFor(scenario)
	if err != nil {
		return Manifest{}, err
	}
	if err = createOwnedDirectory(outputDir); err != nil {
		return Manifest{}, fmt.Errorf("create fixture directory: %w", err)
	}
	guard := Config{OutputDir: outputDir, ResourceRoot: outputDir}.Defaults()
	ctx, stop, err := withResourceMonitor(ctx, guard)
	if err != nil {
		return Manifest{}, err
	}
	defer stop()
	manifest, seedErr := seedOwnedDirectory(ctx, outputDir, plan)
	if seedErr != nil {
		_ = os.RemoveAll(outputDir)
		return Manifest{}, seedErr
	}
	return manifest, nil
}

func seedOwnedDirectory(ctx context.Context, outputDir string, plan fixturePlan) (Manifest, error) {
	path := filepath.Join(outputDir, fixtureDatabaseName)
	database, err := platformdb.Open(ctx, path)
	if err != nil {
		return Manifest{}, fmt.Errorf("open fixture database: %w", err)
	}
	// Seed raw evidence at the pre-rollup schema, then exercise the production
	// backfill so fixtures include genuine derived cells, not empty stand-ins.
	const sourceFixtureVersion = 29
	provider, err := platformdb.NewMigrationProvider(database)
	if err == nil {
		_, err = provider.UpTo(ctx, sourceFixtureVersion)
	}
	if err != nil {
		_ = database.Close()
		return Manifest{}, fmt.Errorf("migrate fixture database: %w", err)
	}
	targets, err := insertFixture(ctx, database, plan)
	if err != nil {
		_ = database.Close()
		return Manifest{}, err
	}
	if err = platformdb.Migrate(ctx, database); err != nil {
		_ = database.Close()
		return Manifest{}, fmt.Errorf("backfill fixture rollups: %w", err)
	}
	counts, err := fixtureRowCounts(ctx, database)
	if err != nil {
		_ = database.Close()
		return Manifest{}, err
	}
	logicalHash, err := logicalFixtureHash(ctx, database)
	if err != nil {
		_ = database.Close()
		return Manifest{}, err
	}
	schemaHash, version, err := schemaIdentity(ctx, database)
	if err != nil {
		_ = database.Close()
		return Manifest{}, err
	}
	if err = closeFixtureDatabase(ctx, database); err != nil {
		return Manifest{}, err
	}
	physicalHash, err := hashFile(path)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{
		FormatVersion: FormatVersion,
		SuiteRevision: fixtureSuiteRevision,
		Scenario:      plan.scenario,
		Clock:         fixtureClock,
		RowCounts:     counts,
		FixtureHash:   logicalHash,
		PhysicalHash:  physicalHash,
		SchemaHash:    schemaHash, MigrationVersion: version,
		Database:       fixtureDatabaseName,
		EntityID:       targets.EntityID,
		EventEntityID:  targets.EventEntityID,
		DeviceID:       targets.DeviceID,
		AdapterID:      targets.AdapterID,
		RuntimeID:      targets.RuntimeID,
		AutomationID:   targets.AutomationID,
		ConversationID: targets.ConversationID,
		DestinationID:  targets.DestinationID,
		FunctionName:   targets.FunctionName,
		DashboardID:    targets.DashboardID,
		HistoryRows:    plan.historyRows(),
		Limits:         plan.limits(),
	}
	if err = writeManifest(outputDir, manifest); err != nil {
		return Manifest{}, err
	}
	if _, err = ValidateFixture(ctx, path); err != nil {
		return Manifest{}, fmt.Errorf("validate generated fixture: %w", err)
	}
	return manifest, nil
}

func insertFixture(ctx context.Context, database *sql.DB, plan fixturePlan) (fixtureTargets, error) {
	if err := insertAdapters(ctx, database, plan); err != nil {
		return fixtureTargets{}, err
	}
	if err := insertDevices(ctx, database, plan); err != nil {
		return fixtureTargets{}, err
	}
	if err := insertObservations(ctx, database, plan); err != nil {
		return fixtureTargets{}, err
	}
	if err := insertCommandsAndEvents(ctx, database, plan); err != nil {
		return fixtureTargets{}, err
	}
	if err := insertHealth(ctx, database, plan); err != nil {
		return fixtureTargets{}, err
	}
	if err := insertFacts(ctx, database, plan); err != nil {
		return fixtureTargets{}, err
	}
	automationID, err := insertAutomations(ctx, database, plan)
	if err != nil {
		return fixtureTargets{}, err
	}
	if err = insertAgent(ctx, database, plan); err != nil {
		return fixtureTargets{}, err
	}
	if err = insertNotifications(ctx, database, plan); err != nil {
		return fixtureTargets{}, err
	}
	if err = insertFunctions(ctx, database, plan); err != nil {
		return fixtureTargets{}, err
	}
	if err = insertDashboards(ctx, database, plan); err != nil {
		return fixtureTargets{}, err
	}
	return fixtureTargets{
		AdapterID:      adapterIDFor(0),
		RuntimeID:      runtimeIDFor(0),
		DeviceID:       deviceIDFor(0),
		EntityID:       entityIDFor(0, rolePower),
		EventEntityID:  entityIDFor(0, roleEvent),
		AutomationID:   automationID,
		ConversationID: fixtureID("aconv_", 0),
		DestinationID:  fixtureID("ndst_", 0),
		FunctionName:   fixtureFunctionName,
		DashboardID:    fixtureID("dash_", 0),
	}, nil
}

func closeFixtureDatabase(ctx context.Context, database *sql.DB) error {
	if _, err := database.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = database.Close()
		return fmt.Errorf("checkpoint fixture database: %w", err)
	}
	if err := database.Close(); err != nil {
		return fmt.Errorf("close fixture database: %w", err)
	}
	return nil
}

func writeManifest(outputDir string, manifest Manifest) error {
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode fixture manifest: %w", err)
	}
	encoded = append(encoded, '\n')
	if err = os.WriteFile(filepath.Join(outputDir, fixtureManifestName), encoded, 0o600); err != nil {
		return fmt.Errorf("write fixture manifest: %w", err)
	}
	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open fixture image: %w", err)
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	if _, err = io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("hash fixture image: %w", err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func fixtureTableNames() []string {
	return []string{
		"adapter_bindings",
		"adapter_entity_mappings",
		"adapter_function_catalogs",
		"adapter_function_invocations",
		"adapter_health_heads",
		"adapter_health_outbox",
		"adapter_instances",
		"adapter_runtimes",
		"agent_conversations",
		"agent_messages",
		"automation_definition_revisions",
		"automation_fact_receipts",
		"automation_history",
		"automation_holds",
		"automation_schedule_watermarks",
		"automation_step_executions",
		"automations",
		"commands",
		"dashboards",
		"device_adapter_health_projection",
		"device_facts_outbox",
		"devices",
		"entities",
		"entity_availability_current",
		"entity_availability_effective",
		"entity_availability_receipts",
		"entity_events",
		"entity_states",
		"health_transitions",
		"notification_attempts",
		"notification_destinations",
		"observations",
		"observation_rollups",
	}
}

func fixtureRowCounts(ctx context.Context, database *sql.DB) (map[string]int64, error) {
	counts := make(map[string]int64, len(fixtureTableNames()))
	for _, table := range fixtureTableNames() {
		var count int64
		if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			return nil, fmt.Errorf("count %s: %w", table, err)
		}
		counts[table] = count
	}
	return counts, nil
}

func logicalFixtureHash(ctx context.Context, database *sql.DB) (string, error) {
	digest := sha256.New()
	for _, table := range fixtureTableNames() {
		// Rollups are derived data and intentionally differ after migration.
		// Keep source-evidence identity comparable to frozen migration-29 fixtures;
		// row-count/integrity checks still cover the new table independently.
		if table == "observation_rollups" {
			continue
		}
		if err := hashFixtureTable(ctx, database, digest, table); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func hashFixtureTable(ctx context.Context, database *sql.DB, digest hash.Hash, table string) error {
	//nolint:unqueryvet,gosec // One canonical logical dump hashes every column of a fixed schema inventory.
	rows, err := database.QueryContext(ctx, "SELECT * FROM "+table+" ORDER BY rowid")
	if err != nil {
		return fmt.Errorf("read %s for fixture hash: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("columns for %s: %w", table, err)
	}
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for index := range targets {
			targets[index] = &values[index]
		}
		if err = rows.Scan(targets...); err != nil {
			return fmt.Errorf("scan %s for fixture hash: %w", table, err)
		}
		writeHashRow(digest, table, columns, values)
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("read %s for fixture hash: %w", table, err)
	}
	return nil
}

func writeHashRow(digest hash.Hash, table string, columns []string, values []any) {
	writeHashField(digest, "table", table)
	for index, column := range columns {
		writeHashField(digest, column, values[index])
	}
}

func writeHashField(digest hash.Hash, name string, value any) {
	_, _ = io.WriteString(digest, name)
	_, _ = io.WriteString(digest, "=")
	switch typed := value.(type) {
	case nil:
		_, _ = io.WriteString(digest, "null;")
	case int64:
		_, _ = io.WriteString(digest, "int:"+strconv.FormatInt(typed, 10)+";")
	case float64:
		_, _ = io.WriteString(digest, "float:"+strconv.FormatFloat(typed, 'g', -1, 64)+";")
	case string:
		_, _ = io.WriteString(digest, "text:"+strconv.Itoa(len(typed))+":"+typed+";")
	case []byte:
		_, _ = io.WriteString(digest, "blob:"+hex.EncodeToString(typed)+";")
	default:
		_, _ = io.WriteString(digest, fmt.Sprintf("other:%v;", typed))
	}
}

// ValidateFixture checks one suite-owned fixture image and returns its table
// counts. It refuses a path outside a marked suite directory or without a
// matching manifest, so a live or arbitrary database cannot be opened here.
func ValidateFixture(ctx context.Context, path string) (map[string]int64, error) {
	directory := filepath.Dir(path)
	if err := requireOwnedDirectory(directory); err != nil {
		return nil, err
	}
	manifest, err := readFixtureManifest(directory, path)
	if err != nil {
		return nil, err
	}
	database, err := platformdb.Open(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("open fixture database: %w", err)
	}
	defer func() { _ = database.Close() }()
	return validateOpenFixture(ctx, database, manifest)
}

func readFixtureManifest(directory, path string) (Manifest, error) {
	encoded, err := os.ReadFile(filepath.Join(directory, fixtureManifestName))
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", errFixtureUnmarked, err)
	}
	var manifest Manifest
	if err = json.Unmarshal(encoded, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", errFixtureUnmarked, err)
	}
	if manifest.FormatVersion != FormatVersion || manifest.Database != filepath.Base(path) ||
		filepath.Clean(path) != filepath.Join(directory, manifest.Database) {
		return Manifest{}, errFixtureUnmarked
	}
	return manifest, nil
}
