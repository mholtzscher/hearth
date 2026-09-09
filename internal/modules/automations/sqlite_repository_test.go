package automations //nolint:testpackage // Tests inspect parser masks and inject repository clocks/failures.

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

func testAutomationRepository(t *testing.T) (*SQLiteRepository, *sql.DB) {
	t.Helper()
	database, err := platformdb.Open(t.Context(), filepath.Join(t.TempDir(), "automations.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if err = platformdb.Migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	repo := NewSQLiteRepository(database)
	repo.now = func() time.Time { return time.Date(2026, time.June, 1, 19, 0, 0, 123, time.UTC) }
	return repo, database
}
func testAutomationDefinition() AutomationDefinition {
	return AutomationDefinition{
		Name:    "Evening lights",
		Enabled: false,
		Triggers: []AutomationTrigger{
			{ID: "daily", Kind: AutomationTriggerKindCron, Expression: "0 19 * * *"},
			{ID: "also-daily", Kind: AutomationTriggerKindCron, Expression: "0 19 * * *"},
		},
		Steps: []AutomationStep{
			{
				EntityID:      "ent_01900000-0000-7000-8000-000000000001",
				OperationName: "set",
				Parameters:    devices.CommandParameters(`{"value":9007199254740993}`),
			},
			{
				EntityID:      "ent_01900000-0000-7000-8000-000000000001",
				OperationName: "set",
				Parameters:    devices.CommandParameters(`{"value":false}`),
			},
		},
	}
}
func createTestAutomation(t *testing.T, repo *SQLiteRepository) AutomationRecord {
	t.Helper()
	record, err := repo.CreateAutomation(t.Context(), testAutomationDefinition())
	if err != nil {
		t.Fatal(err)
	}
	return record
}
func admitTestAutomation(t *testing.T, repo *SQLiteRepository, id AutomationID, key string) AutomationRunRecord {
	t.Helper()
	admission, err := repo.AdmitManualRun(
		t.Context(),
		AutomationManualAdmission{
			Request:  AutomationManualRequest{AutomationID: id, IdempotencyKey: key},
			Timezone: "America/New_York",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return admission.Run
}
func beginTestAutomationStep(t *testing.T, repo *SQLiteRepository, id AutomationRunID, index int) AutomationStepStart {
	t.Helper()
	command, err := devices.NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	correlation, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	input := AutomationStepStart{RunID: id, Index: index, CommandID: command, CorrelationID: correlation}
	if err = repo.BeginAutomationStep(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	return input
}
func interruptTestAutomation(t *testing.T, repo *SQLiteRepository, id AutomationRunID) {
	t.Helper()
	code := AutomationFailureCoreStopping
	if err := repo.CompleteAutomationRun(
		t.Context(),
		AutomationRunCompletion{RunID: id, Status: AutomationRunStatusInterrupted, FailureCode: &code},
	); err != nil {
		t.Fatal(err)
	}
}

func TestAutomationConcurrentAdmission(t *testing.T) {
	t.Parallel()
	repo, _ := testAutomationRepository(t)
	automation := createTestAutomation(t, repo)
	independent := createTestAutomation(t, repo)
	const callers = 12
	gate := make(chan struct{})
	admissions := make(chan AutomationAdmission, callers)
	failures := make(chan error, callers)
	var workers sync.WaitGroup
	for range callers {
		workers.Go(func() {
			<-gate
			admission, err := repo.AdmitManualRun(
				t.Context(),
				AutomationManualAdmission{
					Request:  AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: "same-key"},
					Timezone: "UTC",
				},
			)
			if err != nil {
				failures <- err
				return
			}
			admissions <- admission
		})
	}
	close(gate)
	workers.Wait()
	close(admissions)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var id AutomationRunID
	fresh := 0
	count := 0
	for admission := range admissions {
		count++
		if id == "" {
			id = admission.Run.ID
		}
		if admission.Run.ID != id {
			t.Fatal("same key produced different runs")
		}
		if !admission.Reused {
			fresh++
		}
	}
	if fresh != 1 || count != callers {
		t.Fatalf("fresh=%d total=%d", fresh, count)
	}
	_, err := repo.AdmitManualRun(
		t.Context(),
		AutomationManualAdmission{
			Request:  AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: "other-key"},
			Timezone: "UTC",
		},
	)
	if !errors.Is(err, ErrAutomationRunActive) {
		t.Fatalf("overlap: %v", err)
	}
	if other := admitTestAutomation(t, repo, independent.ID, "other-key"); other.ID == id {
		t.Fatal("independent automation did not proceed")
	}
	if err = repo.DeleteAutomation(t.Context(), automation.ID, 1); !errors.Is(err, ErrAutomationRunActive) {
		t.Fatalf("delete active: %v", err)
	}
}

func TestAutomationAdmissionRollback(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	automation := createTestAutomation(t, repo)
	if _, err := database.ExecContext(
		t.Context(),
		`CREATE TRIGGER fail_second_step BEFORE INSERT ON automation_run_steps WHEN NEW.step_index = 1 BEGIN SELECT RAISE(ABORT, 'injected failure'); END`,
	); err != nil {
		t.Fatal(err)
	}
	_, err := repo.AdmitManualRun(
		t.Context(),
		AutomationManualAdmission{
			Request:  AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: "rollback-key"},
			Timezone: "UTC",
		},
	)
	if err == nil {
		t.Fatal("expected write failure")
	}
	var runs, steps int
	if err = database.QueryRowContext(t.Context(), `SELECT count(*) FROM automation_runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRowContext(t.Context(), `SELECT count(*) FROM automation_run_steps`).
		Scan(&steps); err != nil {
		t.Fatal(err)
	}
	if runs != 0 || steps != 0 {
		t.Fatalf("partial admission: runs=%d steps=%d", runs, steps)
	}
	if _, err = database.ExecContext(t.Context(), `DROP TRIGGER fail_second_step`); err != nil {
		t.Fatal(err)
	}
	admitTestAutomation(t, repo, automation.ID, "rollback-key")
}

//nolint:gocognit // Keep the complete contract scenario and independent assertions together.
func TestAutomationSnapshotsRevisionKeysAndPruning(t *testing.T) {
	t.Parallel()
	repo, _ := testAutomationRepository(t)
	automation := createTestAutomation(t, repo)
	run := admitTestAutomation(t, repo, automation.ID, "retained")
	if run.Snapshot.Revision != 1 || run.Snapshot.Timezone != "America/New_York" || run.Snapshot.Definition.Enabled ||
		run.MatchedTriggerIDs == nil ||
		len(run.MatchedTriggerIDs) != 0 {
		t.Fatalf("manual snapshot: %+v", run)
	}
	replacement := testAutomationDefinition()
	replacement.Name = "Replacement"
	replacement.Enabled = true
	replacement.Triggers[0], replacement.Triggers[1] = replacement.Triggers[1], replacement.Triggers[0]
	replacement.Steps[0].Parameters = devices.CommandParameters(`{"value":true}`)
	updated, err := repo.UpdateAutomation(
		t.Context(),
		AutomationUpdate{ID: automation.ID, ExpectedRevision: 1, Definition: replacement},
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 {
		t.Fatal("revision not incremented")
	}
	_, err = repo.UpdateAutomation(
		t.Context(),
		AutomationUpdate{ID: automation.ID, ExpectedRevision: 1, Definition: testAutomationDefinition()},
	)
	if !errors.Is(err, ErrAutomationRevisionConflict) {
		t.Fatalf("stale update: %v", err)
	}
	if err = repo.DeleteAutomation(t.Context(), automation.ID, 1); !errors.Is(err, ErrAutomationRevisionConflict) {
		t.Fatalf("stale delete: %v", err)
	}
	run.Snapshot.Definition.Steps[0].Parameters[0] = '!'
	run.Snapshot.Definition.Triggers[0].ID = "mutated"
	stored, err := repo.GetAutomationRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Snapshot.Definition.Name != "Evening lights" || stored.Snapshot.Definition.Triggers[0].ID != "daily" ||
		string(stored.Snapshot.Definition.Steps[0].Parameters) != `{"value":9007199254740993}` {
		t.Fatalf("snapshot changed: %+v", stored.Snapshot)
	}
	interruptTestAutomation(t, repo, run.ID)
	if err = repo.DeleteAutomation(t.Context(), automation.ID, 2); err != nil {
		t.Fatal(err)
	}
	reused, err := repo.AdmitManualRun(
		t.Context(),
		AutomationManualAdmission{
			Request:  AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: "retained"},
			Timezone: "UTC",
		},
	)
	if err != nil || !reused.Reused || reused.Run.ID != run.ID {
		t.Fatalf("deleted retry: %+v %v", reused, err)
	}
	count, err := repo.PruneAutomationHistory(t.Context(), repo.now())
	if err != nil || count != 0 {
		t.Fatalf("cutoff equality: %d %v", count, err)
	}
	count, err = repo.PruneAutomationHistory(t.Context(), repo.now().Add(time.Nanosecond))
	if err != nil || count != 1 {
		t.Fatalf("strictly older pruning: %d %v", count, err)
	}
	if _, err = repo.GetAutomationRun(t.Context(), run.ID); !errors.Is(err, ErrAutomationRunNotFound) {
		t.Fatalf("pruned run: %v", err)
	}
	if _, err = repo.AdmitManualRun(
		t.Context(),
		AutomationManualAdmission{
			Request:  AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: "retained"},
			Timezone: "UTC",
		},
	); !errors.Is(
		err,
		ErrAutomationNotFound,
	) {
		t.Fatalf("pruned key of deleted definition: %v", err)
	}
	alive := createTestAutomation(t, repo)
	first := admitTestAutomation(t, repo, alive.ID, "recyclable")
	interruptTestAutomation(t, repo, first.ID)
	if _, err = repo.PruneAutomationHistory(t.Context(), repo.now().Add(time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	second := admitTestAutomation(t, repo, alive.ID, "recyclable")
	if second.ID == first.ID {
		t.Fatal("pruned key was retained")
	}
	if count, err = repo.PruneAutomationHistory(t.Context(), repo.now().Add(time.Hour)); err != nil || count != 0 {
		t.Fatalf("active run pruned: %d %v", count, err)
	}
}

func TestAutomationOrderedTransitionsAndFailureRollback(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	automation := createTestAutomation(t, repo)
	run := admitTestAutomation(t, repo, automation.ID, "steps")
	command, err := devices.NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	correlation, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.BeginAutomationStep(
		t.Context(),
		AutomationStepStart{RunID: run.ID, Index: 1, CommandID: command, CorrelationID: correlation},
	); !errors.Is(
		err,
		ErrAutomationTransitionConflict,
	) {
		t.Fatalf("out of order begin: %v", err)
	}
	first := beginTestAutomationStep(t, repo, run.ID, 0)
	stored, err := repo.GetAutomationRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Steps[0].ReservedCommandID == nil || *stored.Steps[0].ReservedCommandID != first.CommandID ||
		stored.Steps[0].StartedAt == nil {
		t.Fatal("intent was not durable")
	}
	if err = repo.BeginAutomationStep(t.Context(), first); !errors.Is(err, ErrAutomationTransitionConflict) {
		t.Fatalf("repeat begin: %v", err)
	}
	if _, err = database.ExecContext(
		t.Context(),
		`CREATE TRIGGER fail_run_completion BEFORE UPDATE ON automation_runs WHEN NEW.status = 'failed' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`,
	); err != nil {
		t.Fatal(err)
	}
	code := AutomationFailureInvalidCommand
	completion := AutomationStepCompletion{
		RunID:              run.ID,
		Index:              0,
		Status:             AutomationStepStatusFailed,
		FailureCode:        &code,
		PrecreationFailure: true,
	}
	if err = repo.CompleteAutomationStep(t.Context(), completion); err == nil {
		t.Fatal("expected terminal persistence failure")
	}
	stored, err = repo.GetAutomationRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != AutomationRunStatusRunning || stored.Steps[0].Status != AutomationStepStatusRunning ||
		stored.Steps[1].Status != AutomationStepStatusPending {
		t.Fatal("partial failure transaction")
	}
	if _, err = database.ExecContext(t.Context(), `DROP TRIGGER fail_run_completion`); err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteAutomationStep(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	stored, err = repo.GetAutomationRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != AutomationRunStatusFailed || stored.FailureCode == nil || *stored.FailureCode != code ||
		stored.Steps[1].Status != AutomationStepStatusNotAttempted ||
		stored.Steps[1].ReservedCommandID != nil ||
		stored.Steps[1].StartedAt != nil {
		t.Fatalf("failure state: %+v", stored)
	}
	if err = repo.CompleteAutomationStep(t.Context(), completion); !errors.Is(err, ErrAutomationTransitionConflict) {
		t.Fatalf("terminal overwrite: %v", err)
	}
}

func TestAutomationHistoryPagination(t *testing.T) {
	t.Parallel()
	repo, _ := testAutomationRepository(t)
	automation := createTestAutomation(t, repo)
	other := createTestAutomation(t, repo)
	first := admitTestAutomation(t, repo, automation.ID, "first")
	interruptTestAutomation(t, repo, first.ID)
	second := admitTestAutomation(t, repo, automation.ID, "second")
	interruptTestAutomation(t, repo, second.ID)
	unrelated := admitTestAutomation(t, repo, other.ID, "unrelated")
	definitions, err := repo.ListAutomations(t.Context(), AutomationListParams{Limit: 1})
	if err != nil || len(definitions.Items) != 1 || !definitions.HasMore {
		t.Fatalf("definitions page: %+v %v", definitions, err)
	}
	after := definitions.Items[0].ID
	last, err := repo.ListAutomations(t.Context(), AutomationListParams{AfterID: &after, Limit: 1})
	if err != nil || len(last.Items) != 1 || last.HasMore {
		t.Fatalf("last definitions page: %+v %v", last, err)
	}
	page, err := repo.ListAutomationRuns(t.Context(), AutomationRunListParams{AutomationID: &automation.ID, Limit: 1})
	if err != nil || len(page.Items) != 1 || !page.HasMore || page.Items[0].ID != second.ID {
		t.Fatalf("first history page: %+v %v", page, err)
	}
	if err = repo.DeleteAutomation(t.Context(), automation.ID, 1); err != nil {
		t.Fatal(err)
	}
	position := page.Items[0]
	page, err = repo.ListAutomationRuns(
		t.Context(),
		AutomationRunListParams{
			AutomationID:    &automation.ID,
			BeforeStartedAt: &position.StartedAt,
			BeforeID:        &position.ID,
			Limit:           1,
		},
	)
	if err != nil || len(page.Items) != 1 || page.HasMore || page.Items[0].ID != first.ID {
		t.Fatalf("deleted filtered continuation: %+v %v", page, err)
	}
	all, err := repo.ListAutomationRuns(t.Context(), AutomationRunListParams{})
	if err != nil || len(all.Items) != 3 || all.Items[0].ID != unrelated.ID {
		t.Fatalf("all history: %+v %v", all, err)
	}
}

// insertAutomationCommandEvidence uses the real command table without dispatch.
func insertAutomationCommandEvidence(
	t *testing.T,
	database *sql.DB,
	start AutomationStepStart,
	correlation devices.CorrelationID,
	status string,
) {
	t.Helper()
	ctx := t.Context()
	timestamp := automationTime(time.Date(2026, time.June, 1, 19, 0, 0, 0, time.UTC))
	statements := []string{
		`INSERT OR IGNORE INTO devices(id,kind,name,created_at,updated_at) VALUES ('dev_01900000-0000-7000-8000-000000000001','light','Light','` + timestamp + `','` + timestamp + `')`,
		`INSERT OR IGNORE INTO entities(id,device_id,name,type_id,support_json,created_at,updated_at) VALUES ('ent_01900000-0000-7000-8000-000000000001','dev_01900000-0000-7000-8000-000000000001','Power','hearth.power/v1','{}','` + timestamp + `','` + timestamp + `')`,
	}
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	var completed, observation any
	if status != "requested" {
		completed = timestamp
	}
	if status == "satisfied" {
		observation = "obs_" + string(start.CommandID)[4:]
	}
	_, err := database.ExecContext(
		ctx,
		`INSERT INTO commands(id,entity_id,adapter_id,operation,parameters_json,correlation_id,status,requested_at,deadline_at,completed_at,outcome_observation_id) VALUES (?,'ent_01900000-0000-7000-8000-000000000001','test','set','{}',?,?,?,?,?,?)`,
		string(start.CommandID),
		string(correlation),
		status,
		timestamp,
		timestamp,
		completed,
		observation,
	)
	if err != nil {
		t.Fatal(err)
	}
}

//nolint:gocognit // Keep the complete contract scenario and independent assertions together.
func TestAutomationRecoveryOwnershipAndPrecreationExclusions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, status string
		foreign      bool
		failure      *string
	}{
		{
			name: "before creation",
		},
		{name: "owned nonterminal", status: "requested"},
		{name: "owned observed", status: "satisfied"},
		{name: "owned dispatched", status: "dispatched"},
		{
			name:    "foreign observed",
			status:  "satisfied",
			foreign: true,
		},
		{name: "foreign dispatched", status: "dispatched", foreign: true},
	}
	for _, code := range []string{AutomationFailureCommandIDConflict, AutomationFailureInvalidCommand, AutomationFailureEntityNotFound, AutomationFailureInternalError} {
		cases = append(cases, struct {
			name, status string
			foreign      bool
			failure      *string
		}{name: code, status: "dispatched", failure: &code})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo, database := testAutomationRepository(t)
			automation := createTestAutomation(t, repo)
			run := admitTestAutomation(t, repo, automation.ID, "recovery")
			start := beginTestAutomationStep(t, repo, run.ID, 0)
			correlation := start.CorrelationID
			if test.foreign {
				var err error
				correlation, err = devices.NewCorrelationID()
				if err != nil {
					t.Fatal(err)
				}
			}
			if test.status != "" {
				insertAutomationCommandEvidence(t, database, start, correlation, test.status)
			}
			if test.failure != nil {
				if err := repo.CompleteAutomationStep(
					t.Context(),
					AutomationStepCompletion{
						RunID:              run.ID,
						Index:              0,
						Status:             AutomationStepStatusFailed,
						FailureCode:        test.failure,
						PrecreationFailure: true,
					},
				); err != nil {
					t.Fatal(err)
				}
			}
			if err := repo.InterruptAutomationRuns(t.Context()); err != nil {
				t.Fatal(err)
			}
			got, err := repo.GetAutomationRun(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if test.failure == nil {
				if got.Status != AutomationRunStatusInterrupted || got.FailureCode == nil ||
					*got.FailureCode != AutomationFailureCoreRestarted ||
					got.Steps[0].Status != AutomationStepStatusInterrupted {
					t.Fatalf("recovery: %+v", got)
				}
			} else if got.Status != AutomationRunStatusFailed {
				t.Fatal("recovery overwrote terminal run")
			}
			if got.Steps[1].Status != AutomationStepStatusNotAttempted || got.Steps[1].ReservedCommandID != nil {
				t.Fatal("pending step resumed")
			}
			owned := test.status != "" && !test.foreign && test.failure == nil
			if (got.Steps[0].CommandID != nil) != owned || (got.Steps[0].CommandStatus != nil) != owned {
				t.Fatalf("ownership evidence: %+v", got.Steps[0])
			}
			success := owned && test.status != "requested"
			if (got.Steps[0].Outcome != nil) != success {
				t.Fatalf("outcome attribution: %+v", got.Steps[0])
			}
			if success {
				expected := devices.OutcomeDispatched
				if test.status == "satisfied" {
					expected = devices.OutcomeObserved
				}
				if *got.Steps[0].Outcome != expected {
					t.Fatal("wrong outcome")
				}
			}
			if err = repo.InterruptAutomationRuns(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

//nolint:gocognit // Keep the complete contract scenario and independent assertions together.
func TestAutomationAdmissionEditAndDeleteRaces(t *testing.T) {
	t.Parallel()
	for _, deletion := range []bool{false, true} {
		t.Run(map[bool]string{false: "edit", true: "delete"}[deletion], func(t *testing.T) {
			t.Parallel()
			repo, _ := testAutomationRepository(t)
			automation := createTestAutomation(t, repo)
			gate := make(chan struct{})
			var admission AutomationAdmission
			var admitErr, writeErr error
			var workers sync.WaitGroup
			workers.Go(func() {
				<-gate
				admission, admitErr = repo.AdmitManualRun(
					t.Context(),
					AutomationManualAdmission{
						Request:  AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: "race"},
						Timezone: "UTC",
					},
				)
			})
			workers.Go(func() {
				<-gate
				if deletion {
					writeErr = repo.DeleteAutomation(t.Context(), automation.ID, 1)
					return
				}
				definition := testAutomationDefinition()
				definition.Name = "New"
				definition.Steps[0].Parameters = devices.CommandParameters(`{"value":true}`)
				_, writeErr = repo.UpdateAutomation(
					t.Context(),
					AutomationUpdate{ID: automation.ID, ExpectedRevision: 1, Definition: definition},
				)
			})
			close(gate)
			workers.Wait()
			if deletion {
				if admitErr == nil {
					if !errors.Is(writeErr, ErrAutomationRunActive) {
						t.Fatalf("admission won but delete succeeded: %v", writeErr)
					}
				} else if !errors.Is(admitErr, ErrAutomationNotFound) || writeErr != nil {
					t.Fatalf("delete race: %v %v", admitErr, writeErr)
				}
				return
			}
			if admitErr != nil || writeErr != nil {
				t.Fatal(admitErr, writeErr)
			}
			snapshot := admission.Run.Snapshot
			if snapshot.Revision == 1 {
				if snapshot.Definition.Name != "Evening lights" ||
					string(snapshot.Definition.Steps[0].Parameters) != `{"value":9007199254740993}` {
					t.Fatal("mixed revision 1")
				}
			} else if snapshot.Revision != 2 || snapshot.Definition.Name != "New" || string(snapshot.Definition.Steps[0].Parameters) != `{"value":true}` {
				t.Fatal("mixed revision 2")
			}
		})
	}
}

func TestAutomationRunProvenance(t *testing.T) {
	t.Parallel()
	minute := time.Date(2026, time.June, 1, 19, 0, 0, 0, time.UTC)
	key := "key"
	run := AutomationRunRecord{
		Snapshot:          AutomationRunSnapshot{Definition: testAutomationDefinition()},
		Source:            AutomationRunSourceScheduled,
		ScheduledAt:       &minute,
		MatchedTriggerIDs: []AutomationTriggerID{"daily", "also-daily"},
	}
	if err := ValidateAutomationRunProvenance(run, nil); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]AutomationTriggerID{nil, {}, {"missing"}, {"daily", "daily"}, {"also-daily", "daily"}} {
		run.MatchedTriggerIDs = ids
		if err := ValidateAutomationRunProvenance(run, nil); !errors.Is(err, ErrInvalidAutomation) {
			t.Fatalf("bad matches %v: %v", ids, err)
		}
	}
	run.MatchedTriggerIDs = []AutomationTriggerID{"daily"}
	if err := ValidateAutomationRunProvenance(run, &key); err == nil {
		t.Fatal("scheduled key accepted")
	}
	run.Source = AutomationRunSourceManual
	run.ScheduledAt = nil
	if err := ValidateAutomationRunProvenance(run, &key); err == nil {
		t.Fatal("manual matches accepted")
	}
	run.MatchedTriggerIDs = []AutomationTriggerID{}
	if err := ValidateAutomationRunProvenance(run, &key); err != nil {
		t.Fatal(err)
	}
}

func TestAutomationSuccessfulSequenceAndIntentRollback(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	automation := createTestAutomation(t, repo)
	run := admitTestAutomation(t, repo, automation.ID, "success")
	if err := repo.CompleteAutomationRun(
		t.Context(),
		AutomationRunCompletion{RunID: run.ID, Status: AutomationRunStatusSucceeded},
	); !errors.Is(
		err,
		ErrAutomationTransitionConflict,
	) {
		t.Fatalf("pending run succeeded: %v", err)
	}
	first := beginTestAutomationStep(t, repo, run.ID, 0)
	insertAutomationCommandEvidence(t, database, first, first.CorrelationID, "satisfied")
	observed := devices.OutcomeObserved
	if err := repo.CompleteAutomationStep(
		t.Context(),
		AutomationStepCompletion{RunID: run.ID, Index: 0, Status: AutomationStepStatusSatisfied, Outcome: &observed},
	); err != nil {
		t.Fatal(err)
	}
	duplicate := first
	duplicate.Index = 1
	if err := repo.BeginAutomationStep(t.Context(), duplicate); err == nil {
		t.Fatal("reused command reservation accepted")
	}
	fresh, err := devices.NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	duplicate.CommandID = fresh
	if err = repo.BeginAutomationStep(t.Context(), duplicate); err == nil {
		t.Fatal("reused correlation reservation accepted")
	}
	stored, err := repo.GetAutomationRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Steps[1].Status != AutomationStepStatusPending || stored.Steps[1].ReservedCommandID != nil ||
		stored.Steps[1].StartedAt != nil {
		t.Fatal("failed intent left partial reservation")
	}
	second := beginTestAutomationStep(t, repo, run.ID, 1)
	insertAutomationCommandEvidence(t, database, second, second.CorrelationID, "dispatched")
	dispatched := devices.OutcomeDispatched
	if err = repo.CompleteAutomationStep(
		t.Context(),
		AutomationStepCompletion{RunID: run.ID, Index: 1, Status: AutomationStepStatusDispatched, Outcome: &dispatched},
	); err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteAutomationRun(
		t.Context(),
		AutomationRunCompletion{RunID: run.ID, Status: AutomationRunStatusSucceeded},
	); err != nil {
		t.Fatal(err)
	}
	stored, err = repo.GetAutomationRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != AutomationRunStatusSucceeded || stored.CompletedAt == nil || stored.FailureCode != nil ||
		stored.Steps[0].Outcome == nil ||
		*stored.Steps[0].Outcome != observed ||
		stored.Steps[1].Outcome == nil ||
		*stored.Steps[1].Outcome != dispatched {
		t.Fatalf("successful sequence: %+v", stored)
	}
	if err = repo.InterruptAutomationRuns(t.Context()); err != nil {
		t.Fatal(err)
	}
	stored, err = repo.GetAutomationRun(t.Context(), run.ID)
	if err != nil || stored.Status != AutomationRunStatusSucceeded {
		t.Fatalf("recovery changed success: %v", err)
	}
}

func TestAutomationSQLiteStorageConstraints(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	automation := createTestAutomation(t, repo)
	run := admitTestAutomation(t, repo, automation.ID, "constraints")
	invalidUpdates := []string{
		`UPDATE automations SET revision = 0`,
		`UPDATE automations SET enabled = 2`,
		`UPDATE automations SET triggers_json = 'invalid json'`,
		`UPDATE automation_runs SET source = 'unknown'`,
		`UPDATE automation_runs SET status = 'unknown'`,
		`UPDATE automation_runs SET status = 'succeeded'`,
		`UPDATE automation_runs SET completed_at = '2026-06-01T19:00:00.000000000Z'`,
		`UPDATE automation_runs SET matched_trigger_ids_json = '["daily"]'`,
		`UPDATE automation_runs SET matched_trigger_ids_json = '{}'`,
		`UPDATE automation_runs SET idempotency_key = NULL`,
		`UPDATE automation_runs SET idempotency_key = 'white space'`,
		`UPDATE automation_runs SET scheduled_at = '2026-06-01T19:00:00.000000000Z'`,
		`UPDATE automation_runs SET source = 'scheduled', idempotency_key = NULL`,
		`UPDATE automation_run_steps SET reserved_command_id = 'cmd_x' WHERE step_index = 0`,
		`UPDATE automation_run_steps SET reserved_command_id = 'cmd_x', reserved_correlation_id = 'cor_x' WHERE step_index = 0`,
		`UPDATE automation_run_steps SET status = 'satisfied', reserved_command_id = 'cmd_x', reserved_correlation_id = 'cor_x', started_at = 'now', completed_at = 'now' WHERE step_index = 0`,
	}
	for _, statement := range invalidUpdates {
		if _, err := database.ExecContext(t.Context(), statement); err == nil {
			t.Fatalf("accepted invalid storage: %s", statement)
		}
	}
	got, err := repo.GetAutomationRun(t.Context(), run.ID)
	if err != nil || got.Status != AutomationRunStatusRunning || got.Steps[0].Status != AutomationStepStatusPending {
		t.Fatalf("failed CHECK changed storage: %+v %v", got, err)
	}
}

func TestAutomationAdmissionCommitRollback(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	automation := createTestAutomation(t, repo)
	// The deferred FK permits all inserts, then fails COMMIT rather than a statement.
	for _, statement := range []string{
		`CREATE TABLE commit_fault_parent (id TEXT PRIMARY KEY)`,
		`CREATE TABLE commit_fault_child (parent_id TEXT REFERENCES commit_fault_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
		`CREATE TRIGGER fail_admission_commit AFTER INSERT ON automation_run_steps WHEN NEW.step_index = 1 BEGIN INSERT INTO commit_fault_child(parent_id) VALUES ('missing-parent'); END`,
	} {
		if _, err := database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	_, err := repo.AdmitManualRun(
		t.Context(),
		AutomationManualAdmission{
			Request:  AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: "commit-key"},
			Timezone: "UTC",
		},
	)
	if err == nil {
		t.Fatal("deferred commit failure was ignored")
	}
	var runs, steps, faults int
	if err = database.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM automation_runs), (SELECT count(*) FROM automation_run_steps), (SELECT count(*) FROM commit_fault_child)`).
		Scan(&runs, &steps, &faults); err != nil {
		t.Fatal(err)
	}
	if runs != 0 || steps != 0 || faults != 0 {
		t.Fatalf("failed commit left rows: runs=%d steps=%d faults=%d", runs, steps, faults)
	}
	if _, err = database.ExecContext(t.Context(), `DROP TRIGGER fail_admission_commit`); err != nil {
		t.Fatal(err)
	}
	admission, err := repo.AdmitManualRun(
		t.Context(),
		AutomationManualAdmission{
			Request:  AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: "commit-key"},
			Timezone: "UTC",
		},
	)
	if err != nil || admission.Reused {
		t.Fatalf("failed commit retained key or claim: %+v %v", admission, err)
	}
}

// insertAutomationTerminalFailureEvidence stores a terminal failed command with an
// explicit failure code, unlike insertAutomationCommandEvidence which only covers
// successful or pending statuses.
func insertAutomationTerminalFailureEvidence(
	t *testing.T,
	database *sql.DB,
	start AutomationStepStart,
	correlation devices.CorrelationID,
	status, failureCode string,
) {
	t.Helper()
	ctx := t.Context()
	timestamp := automationTime(time.Date(2026, time.June, 1, 19, 0, 0, 0, time.UTC))
	for _, statement := range []string{
		`INSERT OR IGNORE INTO devices(id,kind,name,created_at,updated_at) VALUES ('dev_01900000-0000-7000-8000-000000000001','light','Light','` + timestamp + `','` + timestamp + `')`,
		`INSERT OR IGNORE INTO entities(id,device_id,name,type_id,support_json,created_at,updated_at) VALUES ('ent_01900000-0000-7000-8000-000000000001','dev_01900000-0000-7000-8000-000000000001','Power','hearth.power/v1','{}','` + timestamp + `','` + timestamp + `')`,
	} {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	_, err := database.ExecContext(
		ctx,
		`INSERT INTO commands(id,entity_id,adapter_id,operation,parameters_json,correlation_id,status,requested_at,deadline_at,completed_at,failure_code) VALUES (?,'ent_01900000-0000-7000-8000-000000000001','test','set','{}',?,?,?,?,?,?)`,
		string(start.CommandID),
		string(correlation),
		status,
		timestamp,
		timestamp,
		timestamp,
		failureCode,
	)
	if err != nil {
		t.Fatal(err)
	}
}

// This test protects owned terminal internal_error visibility and fails if
// ownership falls back to matching the failure code: the copied code must keep
// its command evidence in history and recovery while the run still fails.
func TestAutomationOwnedInternalErrorVisible(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	code := AutomationFailureInternalError
	automation := createTestAutomation(t, repo)
	run := admitTestAutomation(t, repo, automation.ID, "owned")
	start := beginTestAutomationStep(t, repo, run.ID, 0)
	insertAutomationTerminalFailureEvidence(t, database, start, start.CorrelationID, "internal_failure", code)
	completion := AutomationStepCompletion{
		RunID:       run.ID,
		Index:       0,
		Status:      AutomationStepStatusFailed,
		FailureCode: &code,
	}
	if err := repo.CompleteAutomationStep(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetAutomationRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertAutomationOwnedInternalError(t, stored, start, code)
	if err = repo.InterruptAutomationRuns(t.Context()); err != nil {
		t.Fatal(err)
	}
	recovered, err := repo.GetAutomationRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertAutomationOwnedInternalError(t, recovered, start, code)
}

func assertAutomationOwnedInternalError(
	t *testing.T,
	run AutomationRunRecord,
	start AutomationStepStart,
	code string,
) {
	t.Helper()
	step := run.Steps[0]
	if step.PrecreationFailure || step.FailureCode == nil || *step.FailureCode != code {
		t.Fatalf("owned failure marker/code not preserved: %+v", step)
	}
	if step.CommandID == nil || *step.CommandID != start.CommandID ||
		step.CommandStatus == nil || *step.CommandStatus != devices.CommandStatusInternalFailure ||
		step.Outcome != nil {
		t.Fatalf("owned terminal internal_error hidden: %+v", step)
	}
	if run.Status != AutomationRunStatusFailed || run.FailureCode == nil || *run.FailureCode != code {
		t.Fatalf("owned run failure not preserved: %+v", run)
	}
}

// This test protects the confirmed pre-creation failure marker and fails if a
// confirmed pre-creation internal_error adopts a command with identical reserved
// identities appearing later, in history or recovery.
func TestAutomationPrecreationInternalErrorExcludesLaterCommand(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	code := AutomationFailureInternalError
	automation := createTestAutomation(t, repo)
	run := admitTestAutomation(t, repo, automation.ID, "precreation")
	start := beginTestAutomationStep(t, repo, run.ID, 0)
	completion := AutomationStepCompletion{
		RunID:              run.ID,
		Index:              0,
		Status:             AutomationStepStatusFailed,
		FailureCode:        &code,
		PrecreationFailure: true,
	}
	if err := repo.CompleteAutomationStep(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	insertAutomationTerminalFailureEvidence(t, database, start, start.CorrelationID, "internal_failure", code)
	stored, err := repo.GetAutomationRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertAutomationPrecreationExcluded(t, stored, code)
	if err = repo.InterruptAutomationRuns(t.Context()); err != nil {
		t.Fatal(err)
	}
	recovered, err := repo.GetAutomationRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertAutomationPrecreationExcluded(t, recovered, code)
}

func assertAutomationPrecreationExcluded(t *testing.T, run AutomationRunRecord, code string) {
	t.Helper()
	step := run.Steps[0]
	if !step.PrecreationFailure || step.FailureCode == nil || *step.FailureCode != code {
		t.Fatalf("pre-creation failure marker/code not preserved: %+v", step)
	}
	if step.CommandID != nil || step.CommandStatus != nil || step.Outcome != nil {
		t.Fatalf("confirmed pre-creation internal_error adopted later command: %+v", step)
	}
	if run.Status != AutomationRunStatusFailed {
		t.Fatalf("recovery overwrote terminal pre-creation run: %+v", run)
	}
}
