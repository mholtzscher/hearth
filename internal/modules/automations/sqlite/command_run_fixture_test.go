//go:build integration

package sqlite_test

import (
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
)

// finishStorageCommandRun records the complete flat Command path used by admission fixtures.
func finishStorageCommandRun(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	run automations.Run,
	at time.Time,
) error {
	t.Helper()
	if _, ok := fixtureSnapshots.Load(run.ID); !ok {
		initializeStorageRun(t, repository, run)
	}
	for _, step := range run.Snapshot.Steps {
		if _, command := step.Body.(automations.CommandStep); !command {
			t.Fatalf("flat Command fixture contains %T", step.Body)
		}
		if err := completeStorageCommand(t.Context(), t, repository, run.ID, step.ID, at); err != nil {
			return err
		}
	}
	return repository.CompleteRun(
		t.Context(),
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	)
}
