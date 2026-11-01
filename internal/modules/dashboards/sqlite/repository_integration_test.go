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
