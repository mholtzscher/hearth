//go:build integration

package dbload_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/dbload"
)

func TestVariantPreparationProvesBaselineSchemaAndLogicalEquality(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, "canonical")
	m, err := dbload.Seed(ctx, dir, dbload.Scenario{Name: "population-1", Dimension: "population", Scale: 1, Seed: 1})
	require.NoError(t, err)
	manifest := filepath.Join(dir, "manifest.json")
	prepared, err := dbload.PrepareVariant(
		ctx,
		dbload.Config{OutputDir: filepath.Join(root, "prepared"), Fixture: manifest},
		true,
	)
	require.NoError(t, err)
	require.Equal(t, m.FixtureHash, prepared.FixtureHash)
	require.Equal(t, m.SchemaHash, prepared.SchemaHash)
	require.Equal(t, m.MigrationVersion, prepared.MigrationVersion)
	_, err = dbload.ReadManifest(filepath.Join(root, "prepared", "manifest.json"))
	require.NoError(t, err)
	m.SchemaHash = "not-the-frozen-baseline-schema"
	require.NoError(t, dbload.WriteJSON(manifest, m))
	_, err = dbload.PrepareVariant(
		ctx,
		dbload.Config{OutputDir: filepath.Join(root, "rejected"), Fixture: manifest},
		true,
	)
	require.ErrorContains(t, err, "baseline schema")
}
