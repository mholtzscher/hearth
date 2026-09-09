package automations //nolint:testpackage // Tests inspect repository hydration through the real SQLite boundary.

import (
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// This test protects list summary pages from hydrating steps and command
// evidence that the HTTP summary discards, and fails if ListAutomationRuns
// reads automation_run_steps or commands again.
func TestListAutomationRunsOmitsStepHydration(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	automation := createTestAutomation(t, repo)
	first := admitTestAutomation(t, repo, automation.ID, "first")
	interruptTestAutomation(t, repo, first.ID)
	second := admitTestAutomation(t, repo, automation.ID, "second")

	start := beginTestAutomationStep(t, repo, second.ID, 0)
	insertAutomationCommandEvidence(t, database, start, start.CorrelationID, "satisfied")

	var storedSteps int
	if err := database.QueryRowContext(
		t.Context(),
		`SELECT count(*) FROM automation_run_steps WHERE run_id = ?`,
		string(second.ID),
	).Scan(&storedSteps); err != nil {
		t.Fatal(err)
	}
	if storedSteps == 0 {
		t.Fatal("fixture has no stored steps to omit")
	}

	page, err := repo.ListAutomationRuns(t.Context(), AutomationRunListParams{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || len(page.Items) != 1 || page.Items[0].ID != second.ID {
		t.Fatalf("newest-first page: %+v %v", page, err)
	}
	listed := page.Items[0]
	if len(listed.Steps) != 0 {
		t.Fatalf("list hydrated %d steps discarded by run summaries", len(listed.Steps))
	}
	if listed.Snapshot.AutomationID != automation.ID ||
		listed.Snapshot.Revision != 1 ||
		listed.Snapshot.Definition.Name != automation.Definition.Name ||
		listed.Source != AutomationRunSourceManual ||
		listed.Status != AutomationRunStatusRunning ||
		!listed.StartedAt.Equal(second.StartedAt) {
		t.Fatalf("list dropped summary fields: %+v", listed)
	}

	cursor := page.Items[0]
	next, err := repo.ListAutomationRuns(t.Context(), AutomationRunListParams{
		BeforeStartedAt: &cursor.StartedAt,
		BeforeID:        &cursor.ID,
		Limit:           1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.HasMore || len(next.Items) != 1 || next.Items[0].ID != first.ID {
		t.Fatalf("cursor continuation: %+v %v", next, err)
	}
	if len(next.Items[0].Steps) != 0 {
		t.Fatalf("cursor page hydrated %d steps", len(next.Items[0].Steps))
	}

	detail, err := repo.GetAutomationRun(t.Context(), second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Steps) != 2 {
		t.Fatalf("detail lost step hydration: %+v", detail)
	}
	evidence := detail.Steps[0]
	if evidence.ReservedCommandID == nil || evidence.CommandID == nil ||
		evidence.CommandStatus == nil || *evidence.CommandStatus != devices.CommandStatusSatisfied ||
		evidence.Outcome == nil || *evidence.Outcome != devices.OutcomeObserved {
		t.Fatalf("detail lost command evidence: %+v", evidence)
	}
	if string(evidence.Definition.Parameters) != `{"value":9007199254740993}` {
		t.Fatalf("detail lost step intent: %s", evidence.Definition.Parameters)
	}
}

func TestListAutomationRunsDoesNotQueryEvidenceTables(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	automation := createTestAutomation(t, repo)
	run := admitTestAutomation(t, repo, automation.ID, "no-evidence-queries")

	// Make hydration queries fail in this disposable database, proving the list
	// does not merely read and then discard Step or Command evidence.
	for _, statement := range []string{
		`ALTER TABLE automation_run_steps RENAME TO unavailable_run_steps`,
		`ALTER TABLE commands RENAME TO unavailable_commands`,
	} {
		if _, err := database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	page, err := repo.ListAutomationRuns(t.Context(), AutomationRunListParams{Limit: 1})
	if err != nil {
		t.Fatalf("summary listing queried unavailable evidence tables: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != run.ID || page.HasMore {
		t.Fatalf("summary listing without evidence tables: %+v", page)
	}
}
