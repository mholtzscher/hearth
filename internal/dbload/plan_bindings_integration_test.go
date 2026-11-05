//go:build integration

package dbload_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/dbload"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

func TestDiagnosticBindingsExplainTheExactProductionStatements(t *testing.T) {
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
	for _, w := range workloads {
		for name, args := range w.PlanArgs {
			sqlText, ok := queries[name]
			require.True(t, ok, name)
			rows, queryErr := database.QueryContext(ctx, "EXPLAIN QUERY PLAN "+sqlText, args...)
			require.NoError(t, queryErr, w.Name+" "+name)
			got := false
			for rows.Next() {
				var id, parent, unused int
				var detail string
				require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
				require.NotEmpty(t, detail)
				got = true
			}
			require.NoError(t, rows.Err())
			require.NoError(t, rows.Close())
			require.True(t, got, "expected a real planner result")
		}
	}
}
