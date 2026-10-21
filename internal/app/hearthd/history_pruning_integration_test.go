//go:build integration

package hearthd //nolint:testpackage // Tests exercise package-private scheduling and lifecycle behavior.

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

// This test protects the shutdown join order and fails if SQLite closes before
// the retention worker returns, which would let a pruning transaction race the
// closed database.
func TestCoreShutdownJoinsHistoryPruneWorkerBeforeDatabaseClose(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	workerReturned := make(chan bool, 1)
	worker := lifecycle.StartWorker(ctx, func(workerContext context.Context) error {
		<-workerContext.Done()
		// The database must still be open when the worker returns: shutdown joins
		// the worker before closing SQLite.
		workerReturned <- database.PingContext(context.Background()) == nil
		return nil
	})
	shutdown := &coreShutdown{
		runContext:         ctx,
		logger:             slog.New(slog.DiscardHandler),
		database:           database,
		historyPruneWorker: worker,
	}
	if runErr := shutdown.run(); runErr != nil {
		t.Fatalf("shutdown returned %v", runErr)
	}
	select {
	case open := <-workerReturned:
		if !open {
			t.Fatal("database closed before the retention worker was joined")
		}
	default:
		t.Fatal("shutdown returned without joining the retention worker")
	}
	if pingErr := database.PingContext(ctx); pingErr == nil {
		t.Fatal("shutdown did not close the database")
	}
}
