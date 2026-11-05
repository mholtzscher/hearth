//go:build integration

package dbload_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/dbload"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

func TestProductionWorkloadsConsumeValidFixtureAndPreserveCanonicalImage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	m, err := dbload.Seed(
		ctx,
		seedDir,
		dbload.Scenario{Name: "population-1", Dimension: "population", Scale: 1, Concurrency: 1, Seed: 1},
	)
	require.NoError(t, err)
	manifest := filepath.Join(seedDir, "manifest.json")
	require.NoError(t, dbload.WriteJSON(manifest, m))
	data, err := dbload.Run(
		ctx,
		dbload.Config{
			OutputDir:   filepath.Join(root, "run"),
			Fixture:     manifest,
			SourceRoot:  filepath.Join("..", ".."),
			Samples:     2,
			Blocks:      1,
			Concurrency: 1,
			CaseLimit:   10 * time.Second,
		},
	)
	assertCorrectExecution(t, data, err)
	_, err = dbload.ReadManifest(manifest)
	require.NoError(t, err, "all mutation blocks use disposable copies, never the canonical image")
}

func assertCorrectExecution(t *testing.T, data dbload.ReportData, runErr error) {
	t.Helper()
	require.NotEmpty(t, data.Blocks)
	for _, block := range data.Blocks {
		require.Contains(t, []string{"complete", "invalid-load"}, block.Status, block.Case)
		require.Zero(t, block.Errors, block.Case)
		require.Zero(t, block.Timeouts, block.Case)
		require.Equal(t, block.Attempted, block.Completed, block.Case)
		require.Positive(t, block.Completed, block.Case)
	}
	// Correctness checks may run beside the full repository gate. Only the strict
	// measurement-load rejection is acceptable; storage, execution, and budget
	// failures still fail this test.
	if runErr != nil {
		for line := range strings.SplitSeq(runErr.Error(), "\n") {
			require.Regexp(t, `^block invalid-load: [0-9]+/[0-9]+ completed; 0 errors, 0 timeouts$`, line)
		}
	}
}

func TestFixtureValidationRejectsTwoIndividuallyValidButMismatchedStateLinks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "seed")
	m, err := dbload.Seed(ctx, dir, dbload.Scenario{Name: "population-1", Dimension: "population", Scale: 1, Seed: 1})
	require.NoError(t, err)
	path := filepath.Join(dir, m.Database)
	database, err := platformdb.Open(ctx, path)
	require.NoError(t, err)
	defer database.Close()
	_, err = database.ExecContext(
		ctx,
		`UPDATE entity_states SET receive_order=(SELECT receive_order FROM observations WHERE entity_id=? AND disposition IN ('applied','unchanged') AND receive_order NOT IN (SELECT receive_order FROM entity_states) ORDER BY receive_order DESC LIMIT 1)
WHERE entity_id=(SELECT entity_id FROM entity_states WHERE entity_id<>? ORDER BY entity_id DESC LIMIT 1)`,
		m.EntityID,
		m.EntityID,
	)
	// The two independent foreign keys remain valid; their relationship is corrupt.
	require.NoError(t, err)
	require.NoError(t, database.Close())
	_, err = dbload.ValidateFixture(ctx, path)
	require.Error(t, err)
	require.ErrorContains(t, err, "State anchors")
}
