package dbload_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/dbload"
)

func TestCatalogMatchesProductionNamedSourcesAndHandwrittenSnapshot(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	coverage, queries, err := dbload.Catalog(root)
	require.NoError(t, err)
	files, err := filepath.Glob(filepath.Join(root, "internal/modules/*/sqlite/dbqueries/*.sql"))
	require.NoError(t, err)
	expected := map[string]bool{"devices/GetEntityStateSnapshot": true}
	for _, path := range files {
		owner := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(path))))
		raw, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "-- name:") {
				fields := strings.Fields(line)
				expected[owner+"/"+fields[2]] = true
			}
		}
	}
	require.Len(t, queries, len(expected))
	require.Len(t, coverage, len(expected))
	for _, item := range coverage {
		key := item.Owner + "/" + item.Name
		require.True(t, expected[key], key)
		require.NotEmpty(t, item.Hash)
		require.NotEmpty(t, queries[key])
		require.Equal(t, "uncovered", item.Status)
		// The diagnostic text is generated SQLite SQL, not SQLC macros.
		require.NotContains(t, queries[key], "sqlc.arg(")
		require.NotContains(t, queries[key], "sqlc.narg(")
	}
}
