//go:build integration

package sqlite //nolint:testpackage // Exercise the transaction owner and its private retained causes.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

func TestTransactionRetainsPrimaryAndRollbackEvidence(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"ordinary conflict", "context rollback", "canceled action", "SQLite rollback", "commit"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			exerciseTransactionRollback(t, mode)
		})
	}
}

func exerciseTransactionRollback(t *testing.T, mode string) {
	t.Helper()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "transaction.db"))
	repo := NewAutomationRepository(database, automations.Dependencies{})
	if mode == "SQLite rollback" {
		// Synthetic SQLite failure ends the real transaction before the
		// helper's explicit cleanup, without a fake driver or fault seam.
		if _, err := database.ExecContext(
			t.Context(),
			`CREATE TRIGGER force_rollback BEFORE INSERT ON automation_definition_revisions BEGIN SELECT RAISE(ROLLBACK, 'SENSITIVE-action'); END`,
		); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var primary error
	calls := 0
	err := repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		calls++
		if primary = insertTransactionRevision(ctx, q); primary != nil {
			return primary
		}
		primary = transactionActionFailure(ctx, t, database, cancel, mode)
		return primary
	})
	wantRows := 0
	if mode == "commit" {
		wantRows = 1
	}
	assertTransactionRows(t, database, wantRows)
	if calls != 1 {
		t.Fatalf("action calls=%d", calls)
	}
	switch mode {
	case "commit":
		if err != nil {
			t.Fatal(err)
		}
	case "ordinary conflict":
		//nolint:errorlint // Require the unchanged action error.
		if err != primary ||
			!errors.Is(err, automations.ErrRevisionConflict) {
			t.Fatalf("ordinary conflict changed: %v", err)
		}
	case "context rollback", "canceled action", "SQLite rollback":
		assertTransactionRollbackEvidence(t, err, primary, mode != "SQLite rollback")
		assertRollbackProtocolFailures(t, err)
	}
}

func transactionActionFailure(
	ctx context.Context,
	t *testing.T,
	database *sql.DB,
	cancel context.CancelFunc,
	mode string,
) error {
	t.Helper()
	switch mode {
	case "ordinary conflict":
		return fmt.Errorf("SENSITIVE-conflict: %w", automations.ErrRevisionConflict)
	case "context rollback", "canceled action":
		cancel()
		waitForAutomaticRollback(t, database)
		if mode == "canceled action" {
			return ctx.Err()
		}
		return automations.ErrRevisionConflict
	default:
		return nil
	}
}

func insertTransactionRevision(ctx context.Context, q *dbsqlc.Queries) error {
	return q.CreateDefinitionRevision(ctx, dbsqlc.CreateDefinitionRevisionParams{
		AutomationID:   "aut_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Revision:       1,
		DefinitionJson: `{"name":"Synthetic","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[{"id":"wait","kind":"delay","duration_ms":1}]}`,
	})
}

func waitForAutomaticRollback(t *testing.T, database *sql.DB) {
	t.Helper()
	// Production's single connection cannot be acquired until database/sql has
	// completed its automatic rollback. The wait has an independent bounded context.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
}

func assertTransactionRows(t *testing.T, database *sql.DB, want int) {
	t.Helper()
	var rows int
	if err := database.QueryRowContext(t.Context(), `SELECT count(*) FROM automation_definition_revisions`).
		Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != want {
		t.Fatalf("revision rows=%d want=%d", rows, want)
	}
}

func assertTransactionRollbackEvidence(t *testing.T, err, primary error, txDone bool) {
	t.Helper()
	failure, ok := errors.AsType[*transactionRollbackError](err)
	if !ok {
		t.Fatalf("lost private primary evidence: %T %v", err, err)
	}
	//nolint:errorlint // Retain the exact error values, not just matching classes.
	if failure.Cause() != primary ||
		failure.rollback == nil {
		t.Fatal("primary or cleanup identity was lost")
	}
	if failure.Error() != "automation transaction rollback failed" {
		t.Fatalf("unsafe wrapper text: %s", failure.Error())
	}
	if errors.Is(err, primary) || errors.Is(err, automations.ErrRevisionConflict) || errors.Is(err, context.Canceled) {
		t.Fatal("primary changed cleanup precedence")
	}
	if !errors.Is(err, failure.rollback) || errors.Is(err, sql.ErrTxDone) != txDone {
		t.Fatal("cleanup matching changed")
	}
}
