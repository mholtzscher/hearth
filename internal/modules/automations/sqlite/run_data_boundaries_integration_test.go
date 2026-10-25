//go:build integration

package sqlite_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestRunDataCanonicalNumericLiteralsOnAppendAndRead(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	definition := validDomainDefinition(t)
	definition.Variables = map[automations.VariableName]automations.ValueBinding{
		"decimal": literalBinding(`1.0`), "exponent": literalBinding(`1e3`),
	}
	record, err := repository.CreateAutomation(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.AdmitManualRun(
		t.Context(),
		automations.ManualRunInput{AutomationID: record.ID},
		stateSnapshotWith(),
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	run := *runEntry(result)
	initial := automations.RunDataRecord{RecordedAt: run.StartedAt,
		Body: automations.VariablesInitialized{Values: json.RawMessage(`{"decimal":1,"exponent":1000}`)}}
	if err = repository.RecordRunData(t.Context(), run.ID, initial); err != nil {
		t.Fatal(err)
	}
	if got := retainedStorageRun(t, repository, record, run); len(got.Data) != 1 {
		t.Fatalf("normalized literal not retained: %#v", got.Data)
	}
	mustExec(
		t,
		database,
		`UPDATE automation_run_data SET payload_json = '{"values":{"decimal":2,"exponent":1000}}' WHERE run_id = ?`,
		string(run.ID),
	)
	if _, err = repository.GetHistoryEntry(
		t.Context(),
		record.ID,
		string(run.ID),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("retained literal mismatch accepted: %v", err)
	}
}

func TestFunctionReservationCanonicalLiteralFields(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	definition := functionStorageDefinition(t)
	function := definition.Steps[0].Body.(automations.FunctionStep)
	function.Arguments = literalBinding(`{"n":1.0,"vendor":18446744073709551615}`)
	definition.Steps[0].Body = function
	record, run := admitStorageRun(t, repository, definition, time.Now().UTC())
	start := functionStorageStart(t, run)
	before := retainedStorageRun(t, repository, record, run)
	for _, values := range []string{
		`{"parameters":{"n":1.0,"vendor":18446744073709551615}}`,
		`{"arguments":{"n":1.0}}`,
		`{"arguments":{"n":1,"vendor":18446744073709551615}}`,
		`{"arguments":{"n":1.0,"vendor":18446744073709551614}}`,
	} {
		start.Inputs = storageInputs(run, start.StepID, values)
		if err := repository.RecordFunctionStart(
			t.Context(),
			start,
		); !errors.Is(
			err,
			automations.ErrInvalidAutomation,
		) {
			t.Fatalf("literal input mismatch accepted: %s: %v", values, err)
		}
		if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
			t.Fatal("rejected literal inputs left a reservation")
		}
	}
	start.Inputs = storageInputs(run, start.StepID, `{"arguments":{"n":1.0,"vendor":18446744073709551615}}`)
	if err := repository.RecordFunctionStart(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	_ = retainedStorageRun(t, repository, record, run)
	mustExec(
		t,
		database,
		`UPDATE automation_run_data SET payload_json = '{"values":{"arguments":{"n":1,"vendor":18446744073709551615}}}' WHERE run_id = ? AND sequence = 1`,
		string(run.ID),
	)
	if _, err := repository.GetHistoryEntry(
		t.Context(),
		record.ID,
		string(run.ID),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("retained opaque literal normalization accepted: %v", err)
	}
}

func TestRunDataCanonicalStateAgeOnAppendAndRead(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	definition := validDomainDefinition(t)
	entity := newEntityID(t)
	maxAge := int64(10)
	definition.Steps = []automations.Step{{ID: "read", Body: automations.ReadStateStep{
		EntityID: entity, MaxAgeSeconds: &maxAge, OnUnusable: automations.ReadStateNull,
	}}}
	record, run := admitStorageRun(t, repository, definition, time.Now().UTC())
	id := automations.StepID("read")
	reason := automations.ConditionUnknownEvidenceInFuture
	body := automations.StateCaptured{
		EntityID: entity,
		ReadAt:   run.StartedAt,
		Observation: &automations.ObservationEvidence{
			ObservationID: devices.ObservationID(newObservationIDString(t)),
			ObservedAt:    run.StartedAt.Add(-time.Minute),
		},
		UnusableReason: &reason,
		Output:         json.RawMessage(`null`),
	}
	data := automations.RunDataRecord{Sequence: 1, StepID: &id, RecordedAt: run.StartedAt, Body: body}
	before := retainedStorageRun(t, repository, record, run)
	if err := repository.RecordRunData(t.Context(), run.ID, data); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("incorrect age reason accepted: %v", err)
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
		t.Fatal("rejected State capture changed history")
	}
	reason = automations.ConditionUnknownEvidenceExpired
	if err := repository.RecordRunData(t.Context(), run.ID, data); err != nil {
		t.Fatal(err)
	}
	_ = retainedStorageRun(t, repository, record, run)
	mustExec(
		t,
		database,
		`UPDATE automation_run_data SET payload_json = json_set(payload_json, '$.unusable_reason', 'evidence_in_future') WHERE run_id = ? AND sequence = 1`,
		string(run.ID),
	)
	if _, err := repository.GetHistoryEntry(
		t.Context(),
		record.ID,
		string(run.ID),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("retained age mismatch accepted: %v", err)
	}
}

//nolint:gocognit // The fault matrix shares one unchanged-history oracle for both successful-provider terminal paths.
func TestFunctionOutputAndValueFailureCommitAtomically(t *testing.T) {
	t.Parallel()
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "output", true: "value failure"}[failure], func(t *testing.T) {
			t.Parallel()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			record, run := admitStorageRun(t, repository, functionStorageDefinition(t), time.Now().UTC())
			start := functionStorageStart(t, run)
			if err := repository.RecordFunctionStart(t.Context(), start); err != nil {
				t.Fatal(err)
			}
			completion := functionStorageCompletion(
				start,
				automations.FunctionSucceeded{CompletedAt: start.StartedAt, CatalogRevision: functionStorageCatalog},
			)
			if failure {
				completion.Data.Body = automations.RunDataFailed{
					Path:        "/steps/0/output",
					FailureCode: "expression_value_invalid",
				}
			}
			before := retainedStorageRun(t, repository, record, run)
			for _, fault := range []string{"missing data", "sequence", "ownership", "kind", "insert", "parent", "commit"} {
				if fault == "parent" && !failure {
					continue
				}
				bad := completion
				data := *completion.Data
				bad.Data = &data
				switch fault {
				case "missing data":
					bad.Data = nil
				case "sequence":
					bad.Data.Sequence++
				case "ownership":
					other := automations.StepID("second")
					bad.Data.StepID = &other
				case "kind":
					bad.Data.Body = automations.VariablesAssigned{Values: json.RawMessage(`{"x":1}`)}
				case "insert":
					mustExec(
						t,
						database,
						`CREATE TRIGGER reject_output BEFORE INSERT ON automation_run_data BEGIN SELECT RAISE(ABORT, 'output fault'); END`,
					)
				case "commit":
					mustExec(
						t,
						database,
						`CREATE TABLE commit_fault (run_id TEXT REFERENCES automation_history(id) DEFERRABLE INITIALLY DEFERRED)`,
					)
					mustExec(
						t,
						database,
						`CREATE TRIGGER reject_commit AFTER INSERT ON automation_run_data BEGIN INSERT INTO commit_fault VALUES ('missing'); END`,
					)
				case "parent":
					mustExec(
						t,
						database,
						`CREATE TRIGGER reject_parent BEFORE UPDATE ON automation_history BEGIN SELECT RAISE(ABORT, 'parent fault'); END`,
					)
				}
				err := repository.CompleteFunction(t.Context(), bad)
				if err == nil {
					t.Fatalf("%s accepted", fault)
				}
				if fault == "commit" && !strings.Contains(err.Error(), "transaction commit") {
					t.Fatalf("fault did not reach commit: %v", err)
				}
				mustExec(t, database, `DROP TRIGGER IF EXISTS reject_output`)
				mustExec(t, database, `DROP TRIGGER IF EXISTS reject_parent`)
				mustExec(t, database, `DROP TRIGGER IF EXISTS reject_commit`)
				if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
					t.Fatalf("%s left partial terminal evidence: %#v", fault, got)
				}
			}
			if err := repository.CompleteFunction(t.Context(), completion); err != nil {
				t.Fatal(err)
			}
			got := retainedStorageRun(t, repository, record, run)
			if len(got.Data) != 3 || automations.FunctionStateStatus(got.Functions[0].State) != "succeeded" {
				t.Fatalf("provider success/output not retained: %#v", got)
			}
			want := automations.RunRunning
			if failure {
				want = automations.RunFailed
			}
			if automations.RunStateStatus(got.State) != want {
				t.Fatalf("parent status = %s, want %s", automations.RunStateStatus(got.State), want)
			}
		})
	}
}

func TestRetainedFunctionOutputRejectsPhaseAndEnvelopeCorruption(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"output before inputs", "missing reservation", "orphan inputs", "duplicated envelope"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			record, run := admitStorageRun(t, repository, functionStorageDefinition(t), time.Now().UTC())
			start := functionStorageStart(t, run)
			if err := repository.RecordFunctionStart(t.Context(), start); err != nil {
				t.Fatal(err)
			}
			completion := functionStorageCompletion(
				start,
				automations.FunctionSucceeded{CompletedAt: start.StartedAt, CatalogRevision: functionStorageCatalog},
			)
			if err := repository.CompleteFunction(t.Context(), completion); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "output before inputs":
				mustExec(
					t,
					database,
					`UPDATE automation_run_data SET sequence = 99 WHERE run_id = ? AND sequence = 1`,
					string(run.ID),
				)
				mustExec(
					t,
					database,
					`UPDATE automation_run_data SET sequence = 1 WHERE run_id = ? AND sequence = 2`,
					string(run.ID),
				)
				mustExec(
					t,
					database,
					`UPDATE automation_run_data SET sequence = 2 WHERE run_id = ? AND sequence = 99`,
					string(run.ID),
				)
			case "missing reservation":
				mustExec(t, database, `DELETE FROM automation_run_functions WHERE run_id = ?`, string(run.ID))
			case "orphan inputs":
				mustExec(t, database, `DELETE FROM automation_run_functions WHERE run_id = ?`, string(run.ID))
				mustExec(
					t,
					database,
					`DELETE FROM automation_run_data WHERE run_id = ? AND sequence = 2`,
					string(run.ID),
				)
			case "duplicated envelope":
				mustExec(
					t,
					database,
					`UPDATE automation_run_data SET payload_json = json_set(payload_json, '$.sequence', 0) WHERE run_id = ?`,
					string(run.ID),
				)
			}
			if _, err := repository.GetHistoryEntry(
				t.Context(),
				record.ID,
				string(run.ID),
			); !errors.Is(
				err,
				automations.ErrInvalidAutomation,
			) {
				t.Fatalf("%s accepted: %v", fault, err)
			}
		})
	}
}

func TestRetainedFunctionValueFailureUsesCompletionValidation(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	record, run := admitStorageRun(t, repository, functionStorageDefinition(t), time.Now().UTC())
	start := functionStorageStart(t, run)
	if err := repository.RecordFunctionStart(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	completion := functionStorageCompletion(
		start,
		automations.FunctionSucceeded{CompletedAt: start.StartedAt, CatalogRevision: functionStorageCatalog},
	)
	completion.Data.Body = automations.RunDataFailed{Path: "/steps/0/output", FailureCode: "expression_value_invalid"}
	if err := repository.CompleteFunction(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	mustExec(
		t,
		database,
		`UPDATE automation_run_data SET payload_json=json_set(payload_json,'$.failure_code','expression_missing') WHERE run_id=? AND kind='failed'`,
		string(run.ID),
	)
	mustExec(
		t,
		database,
		`UPDATE automation_history SET run_failure_code='expression_missing' WHERE id=?`,
		string(run.ID),
	)
	if _, err := repository.GetHistoryEntry(
		t.Context(),
		record.ID,
		string(run.ID),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("impossible successful-Function failure accepted: %v", err)
	}
}

func TestCopiedFunctionOutputSurvivesInvocationPruning(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	functions := adapterssqlite.NewAdapterRepository(database)
	record, run := admitStorageRun(t, repository, functionStorageDefinition(t), time.Now().UTC())
	start := functionStorageStart(t, run)
	if err := repository.RecordFunctionStart(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	output := json.RawMessage(`{"precise":9007199254740993}`)
	// Synthetic provider success uses the real invocation table and owner pruning
	// API. No live Adapter runtime is needed to prove independent retention.
	mustExec(t, database, `INSERT INTO adapter_function_invocations
		(id, adapter_id, runtime_id, catalog_revision, function_key, descriptor_json, arguments_json, started_at, deadline, status, completed_at, result_json)
		VALUES (?, 'weather', 'run_01980000-0000-7000-8000-000000000001', ?, 'refresh',
		'{"key":"refresh","description":"Refresh","input_schema":{"type":"object"},"output_schema":{"type":"object"},"timeout_ms":1000}',
		'{}', ?, ?, 'succeeded', ?, ?)`, string(start.InvocationID), string(functionStorageCatalog), migrationTimestamp, migrationTimestamp, migrationTimestamp, string(output))
	completion := functionStorageCompletion(
		start,
		automations.FunctionSucceeded{CompletedAt: start.StartedAt, CatalogRevision: functionStorageCatalog},
	)
	completion.Data.Body = automations.FunctionOutputCaptured{Output: output}
	if err := repository.CompleteFunction(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	before := retainedStorageRun(t, repository, record, run)
	if deleted, err := functions.DeleteHistoryBefore(
		t.Context(),
		time.Now().UTC().Add(time.Hour),
		500,
	); err != nil ||
		deleted != 1 {
		t.Fatalf("invocation pruning = %d, %v", deleted, err)
	}
	if _, err := functions.GetInvocation(
		t.Context(),
		start.InvocationID,
	); !errors.Is(
		err,
		adapters.ErrInvocationNotFound,
	) {
		t.Fatalf("invocation survived pruning: %v", err)
	}
	got := retainedStorageRun(t, repository, record, run)
	if !reflect.DeepEqual(got, before) ||
		string(got.Data[2].Body.(automations.FunctionOutputCaptured).Output) != string(output) {
		t.Fatalf("copied output changed after independent pruning: %#v", got.Data)
	}
}
