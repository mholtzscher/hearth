//go:build integration

package dbload //nolint:testpackage // Integration tests verify unexported fixture invariants.

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

func baselineScenario() Scenario {
	return Scenario{Name: "population-1", Dimension: "population", Scale: 1, Concurrency: 1, Seed: 7}
}

func TestSeedPopulationBaseline(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "fixture")
	manifest, err := Seed(t.Context(), directory, baselineScenario())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, fixtureDatabaseName)
	counts, err := ValidateFixture(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.RowCounts) != len(fixtureTableNames()) {
		t.Fatalf("manifest records %d tables, want %d", len(manifest.RowCounts), len(fixtureTableNames()))
	}
	for _, table := range fixtureTableNames() {
		if counts[table] <= 0 || manifest.RowCounts[table] != counts[table] {
			t.Fatalf("table %s counts = %d, manifest = %d", table, counts[table], manifest.RowCounts[table])
		}
	}
	assertManifestTargets(t, manifest)
	if manifest.HistoryRows != baselineStateHistory {
		t.Fatalf("HistoryRows = %d, want %d", manifest.HistoryRows, baselineStateHistory)
	}
	assertBaselineQueries(t, path, manifest)
}

func assertManifestTargets(t *testing.T, manifest Manifest) {
	t.Helper()
	for _, target := range []struct{ prefix, value string }{
		{"ent_", manifest.EntityID}, {"ent_", manifest.EventEntityID}, {"dev_", manifest.DeviceID},
		{"run_", manifest.RuntimeID}, {"aut_", manifest.AutomationID},
		{"aconv_", manifest.ConversationID}, {"ndst_", manifest.DestinationID},
		{"dash_", manifest.DashboardID},
	} {
		if len(target.value) <= len(target.prefix) || target.value[:len(target.prefix)] != target.prefix {
			t.Fatalf("target %q does not carry prefix %s", target.value, target.prefix)
		}
	}
	parsed, err := uuid.Parse(manifest.EntityID[len("ent_"):])
	if err != nil || parsed.Version() != 7 {
		t.Fatalf("EntityID %q is not a canonical UUIDv7: %v", manifest.EntityID, err)
	}
	if manifest.AdapterID == "" || manifest.FunctionName == "" {
		t.Fatal("manifest omits an Adapter or Function target")
	}
}

func assertBaselineQueries(t *testing.T, path string, manifest Manifest) {
	t.Helper()
	database, err := platformdb.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	var entityType string
	if err = database.QueryRowContext(t.Context(),
		"SELECT type_id FROM entities WHERE id = ?", manifest.EntityID).Scan(&entityType); err != nil {
		t.Fatal(err)
	}
	if entityType != "hearth.power/v1" {
		t.Fatalf("target Entity type = %s, want a power Entity", entityType)
	}
	assertCount(t.Context(), t, database, `
SELECT COUNT(*) FROM observations AS o
LEFT JOIN entities AS e ON e.id = o.entity_id
WHERE o.rejection_code = 'unknown_entity' AND e.id IS NULL`, unknownRejectedObservations)
	assertCount(t.Context(), t, database,
		"SELECT COUNT(*) FROM entity_read_projection", manifest.RowCounts["entities"])
	assertCount(t.Context(), t, database, `
SELECT COUNT(*) FROM entity_states AS s
WHERE EXISTS (
  SELECT 1 FROM observations AS o
  WHERE o.entity_id = s.entity_id AND o.receive_order > s.receive_order
)`, 0)
}

func assertCount(ctx context.Context, t *testing.T, database *sql.DB, query string, want int64) {
	t.Helper()
	var got int64
	if err := database.QueryRowContext(ctx, query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("query returned %d, want %d: %s", got, want, query)
	}
}

func TestSeedIsDeterministic(t *testing.T) {
	t.Parallel()
	first, err := Seed(t.Context(), filepath.Join(t.TempDir(), "first"), baselineScenario())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Seed(t.Context(), filepath.Join(t.TempDir(), "second"), baselineScenario())
	if err != nil {
		t.Fatal(err)
	}
	if first.FixtureHash == "" || first.FixtureHash != second.FixtureHash {
		t.Fatalf("fixture hashes differ: %s and %s", first.FixtureHash, second.FixtureHash)
	}
	if first.PhysicalHash == "" {
		t.Fatal("physical hash is empty")
	}
}

func TestSeedRefusesExistingOrUnownedDirectories(t *testing.T) {
	t.Parallel()
	existing := t.TempDir()
	if _, err := Seed(t.Context(), existing, baselineScenario()); err == nil {
		t.Fatal("Seed accepted an existing directory")
	}
	directory := filepath.Join(t.TempDir(), "fixture")
	if _, err := Seed(t.Context(), directory, baselineScenario()); err != nil {
		t.Fatal(err)
	}
	if _, err := Seed(t.Context(), directory, baselineScenario()); err == nil {
		t.Fatal("Seed accepted a second fixture in the same directory")
	}
}

func TestValidateFixtureRefusesUnownedPaths(t *testing.T) {
	t.Parallel()
	foreign := filepath.Join(t.TempDir(), "live.db")
	if _, err := ValidateFixture(t.Context(), foreign); err == nil {
		t.Fatal("ValidateFixture accepted a path outside a marked suite directory")
	}
	marked := filepath.Join(t.TempDir(), "marked")
	if err := createOwnedDirectory(marked); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateFixture(t.Context(), filepath.Join(marked, fixtureDatabaseName)); err == nil {
		t.Fatal("ValidateFixture accepted a marked directory without a manifest")
	}
}

func TestValidateFixtureDetectsTamperedState(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "fixture")
	manifest, err := Seed(t.Context(), directory, baselineScenario())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, fixtureDatabaseName)
	database, err := platformdb.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.ExecContext(t.Context(),
		"UPDATE entity_states SET value_json = 'true' WHERE entity_id = ?", manifest.EntityID); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = ValidateFixture(t.Context(), path); err == nil {
		t.Fatal("ValidateFixture accepted a State anchor that disagrees with its Observation")
	}
}

func TestSeedFanoutMessagesBaseline(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "fixture")
	scenario := Scenario{Name: "fanout-messages-1", Dimension: "fanout", Scale: 1, Relation: "messages", Seed: 7}
	manifest, err := Seed(t.Context(), directory, scenario)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Scenario.Relation != "messages" || manifest.RowCounts["agent_messages"] != 320 {
		t.Fatalf("fan-out manifest = %+v", manifest.Scenario)
	}
	if _, err = ValidateFixture(t.Context(), filepath.Join(directory, fixtureDatabaseName)); err != nil {
		t.Fatal(err)
	}
}
