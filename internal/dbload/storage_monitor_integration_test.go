//go:build integration

package dbload //nolint:testpackage // Exercise the private sampled disk monitor during disposable-file removal.

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiskMonitorToleratesConcurrentDisposableFileRemoval(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	var writer sync.WaitGroup
	writer.Go(func() {
		for range 100 {
			//nolint:usetesting // Files must disappear during the monitor walk, not at test cleanup.
			dir, err := os.MkdirTemp(root, "block-")
			if err != nil {
				return
			}
			_ = os.WriteFile(filepath.Join(dir, "fixture.db-wal"), []byte("disposable"), 0o600)
			_ = os.RemoveAll(dir)
		}
	})
	defer writer.Wait()
	for range 100 {
		size, err := diskBytes(root)
		require.NoError(t, err)
		require.GreaterOrEqual(t, size, int64(0))
	}
	_, err := diskBytes(filepath.Join(root, "missing-root"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
