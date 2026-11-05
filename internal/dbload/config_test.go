package dbload_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/dbload"
)

func TestScenarioValidationPreservesIndependentDimensions(t *testing.T) {
	t.Parallel()
	for _, scenario := range dbload.DefaultScenarios() {
		require.NoError(t, dbload.ValidateScenario(scenario))
	}
	for _, scenario := range []dbload.Scenario{
		{Name: "bad", Dimension: "population", Scale: 1000000},
		{Name: "bad", Dimension: "population", Scale: 10, Relation: "devices"},
		{Name: "bad", Dimension: "fanout", Scale: 10, Relation: "arbitrary"},
		{Name: "bad", Dimension: "combined", Scale: 1000},
		{Name: "../escape", Dimension: "history", Scale: 10},
	} {
		require.Error(t, dbload.ValidateScenario(scenario))
	}
}

func TestRunRejectsInvalidInputsBeforeCreatingFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	out := filepath.Join(root, "new-run")
	_, err := dbload.Run(context.Background(), dbload.Config{OutputDir: out, Samples: -1})
	require.Error(t, err)
	_, statErr := os.Stat(out)
	require.True(t, os.IsNotExist(statErr))
	cfg := dbload.Config{OutputDir: out, OperationLimit: 2 * time.Second, CaseLimit: time.Second}.Defaults()
	require.Error(t, cfg.Validate())
}

func TestManifestRejectsUnownedDatabaseWithoutTouchingIt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "database.db")
	original := []byte("not a suite database; preserve me")
	require.NoError(t, os.WriteFile(path, original, 0o600))
	_, err := dbload.ReadManifest(filepath.Join(root, "manifest.json"))
	require.Error(t, err)
	actual, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, actual)
}
