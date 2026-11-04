//go:build integration

package sqlite_test //nolint:cyclop // One end-to-end persistence scenario checks the complete lifecycle.

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/dashboards/sqlite"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

func TestDashboardPersistenceAndRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	repo := sqlite.NewRepository(db)
	svc := dashboards.NewService(repo, nil)
	d, err := dashboards.ParseDefinition(
		json.RawMessage(
			`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"First","spec":{"root":"a","elements":{"a":{"type":"Note","props":{"text":"hello"}}}}}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.CreateDashboard(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 {
		t.Fatal(created.Revision)
	}
	d.Title = "Changed"
	saved, err := svc.SaveDashboard(
		ctx,
		dashboards.SaveDashboardParams{ID: created.ID, ExpectedRevision: 1, Definition: d},
	)
	if err != nil || saved.Revision != 2 {
		t.Fatalf("save: %+v %v", saved, err)
	}
	_, err = svc.SaveDashboard(ctx, dashboards.SaveDashboardParams{ID: created.ID, ExpectedRevision: 1, Definition: d})
	if !errors.Is(err, dashboards.ErrRevisionConflict) {
		t.Fatalf("conflict: %v", err)
	}
	page, err := svc.ListDashboards(ctx, dashboards.ListParams{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Title != "Changed" {
		t.Fatalf("list: %+v %v", page, err)
	}
	future := `{"schema_version":2,"catalog_version":"future/v2","title":"Future","spec":{"root":"missing","elements":{}},"extra":true}`
	if _, execErr := db.ExecContext(
		ctx,
		"UPDATE dashboards SET definition_json=? WHERE id=?",
		future,
		string(created.ID),
	); execErr != nil {
		t.Fatal(execErr)
	}
	got, err := svc.GetDashboard(ctx, created.ID)
	if err != nil || string(got.Definition) != future || got.Revision != 2 {
		t.Fatalf("read: %+v %v", got, err)
	}
	if deleteErr := svc.DeleteDashboard(ctx, created.ID, 1); !errors.Is(deleteErr, dashboards.ErrRevisionConflict) {
		t.Fatalf("delete conflict: %v", deleteErr)
	}
	if deleteErr := svc.DeleteDashboard(ctx, created.ID, 2); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	if _, getErr := svc.GetDashboard(ctx, created.ID); !errors.Is(getErr, dashboards.ErrNotFound) {
		t.Fatalf("deleted: %v", getErr)
	}
}

func TestMissingReferenceFailedSaveAndRestartAfterConversationDeletion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "restart.db")
	db := dbtest.OpenMigrated(t, path)
	svc := dashboards.NewService(sqlite.NewRepository(db), nil)
	d, err := dashboards.ParseDefinition(
		json.RawMessage(
			`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"Missing sensor","spec":{"root":"m","elements":{"m":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"ent_01900000-0000-7000-8000-000000000000","entity_type":"hearth.temperature/v1"}}}}}}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.CreateDashboard(ctx, d)
	if err != nil {
		t.Fatalf("save missing reference: %v", err)
	}
	if _, execErr := db.ExecContext(
		ctx,
		`CREATE TRIGGER reject_dashboard_update BEFORE UPDATE ON dashboards BEGIN SELECT RAISE(ABORT,'injected failure'); END`,
	); execErr != nil {
		t.Fatal(execErr)
	}
	d.Title = "Not committed"
	if _, saveErr := svc.SaveDashboard(
		ctx,
		dashboards.SaveDashboardParams{ID: created.ID, ExpectedRevision: 1, Definition: d},
	); saveErr == nil {
		t.Fatal("failed write succeeded")
	}
	got, err := svc.GetDashboard(ctx, created.ID)
	if err != nil || got.Revision != 1 || string(got.Definition) != string(created.Definition) {
		t.Fatalf("changed after failure: %+v %v", got, err)
	}
	if _, execErr := db.ExecContext(
		ctx,
		`INSERT INTO agent_conversations(id,created_at) VALUES ('aconv_fixture','2026-01-01T00:00:00Z')`,
	); execErr != nil {
		t.Fatal(execErr)
	}
	if _, execErr := db.ExecContext(ctx, `DELETE FROM agent_conversations`); execErr != nil {
		t.Fatal(execErr)
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	reopened, err := platformdb.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	got, err = dashboards.NewService(sqlite.NewRepository(reopened), nil).GetDashboard(ctx, created.ID)
	if err != nil || got.Revision != 1 || string(got.Definition) != string(created.Definition) {
		t.Fatalf("restart: %+v %v", got, err)
	}
}

func TestListAndReadPreserveInvalidStoredGraph(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "graph.db"))
	svc := dashboards.NewService(sqlite.NewRepository(db), nil)
	d, err := dashboards.ParseDefinition(
		json.RawMessage(
			`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"First","spec":{"root":"a","elements":{"a":{"type":"Note","props":{"text":"hello"}}}}}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.CreateDashboard(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"schema_version":2,"catalog_version":"future","title":"Recoverable","spec":[]}`
	if _, execErr := db.ExecContext(
		ctx,
		`UPDATE dashboards SET definition_json=? WHERE id=?`,
		raw,
		string(created.ID),
	); execErr != nil {
		t.Fatal(execErr)
	}
	page, err := svc.ListDashboards(ctx, dashboards.ListParams{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Title != "Recoverable" {
		t.Fatalf("list: %+v %v", page, err)
	}
	got, err := svc.GetDashboard(ctx, created.ID)
	if err != nil || string(got.Definition) != raw || got.Revision != 1 {
		t.Fatalf("read: %+v %v", got, err)
	}
}
