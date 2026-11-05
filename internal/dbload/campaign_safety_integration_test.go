//go:build integration

package dbload_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/dbload"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

func TestExplorationDeletesOnlyItsOwnedCanonicalImage(t *testing.T) {
	// Chdir is intentional: a relative manifest filename must never name a CWD file.
	source, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	cwd := t.TempDir()
	t.Chdir(cwd)
	decoy := filepath.Join(cwd, "fixture.db")
	sentinel := []byte("unrelated fixture; do not delete")
	require.NoError(t, os.WriteFile(decoy, sentinel, 0o600))
	out := filepath.Join(t.TempDir(), "campaign")
	data, err := dbload.Explore(
		context.Background(),
		dbload.Config{
			OutputDir:  out,
			SourceRoot: source,
			Cases:      []string{"devices.get-entity"},
			Samples:    1,
			Blocks:     1,
			Budget:     30 * time.Second,
		},
		[]dbload.Scenario{{Name: "population-10", Dimension: "population", Scale: 10, Seed: 1, Concurrency: 1}},
	)
	require.NoError(t, err)
	require.Len(t, data.Manifests, 1)
	_, err = os.Stat(filepath.Join(out, "fixtures", "population-10", "fixture.db"))
	require.True(t, os.IsNotExist(err), "nonretained canonical image must actually be removed")
	actual, err := os.ReadFile(decoy)
	require.NoError(t, err)
	require.Equal(t, sentinel, actual)
}

func TestTimedQueryDeclarationsReferToRealProductionSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "seed")
	m, err := dbload.Seed(ctx, dir, dbload.Scenario{Name: "population-1", Dimension: "population", Scale: 1, Seed: 1})
	require.NoError(t, err)
	database, err := platformdb.Open(ctx, filepath.Join(dir, m.Database))
	require.NoError(t, err)
	defer database.Close()
	workloads, err := dbload.Workloads(ctx, database, m)
	require.NoError(t, err)
	_, queries, err := dbload.Catalog(filepath.Join("..", ".."))
	require.NoError(t, err)
	for _, workload := range workloads {
		for _, query := range workload.Queries {
			if _, ok := queries[query]; !ok {
				t.Errorf("%s declares nonexistent query %s", workload.Name, query)
			}
		}
	}
}
