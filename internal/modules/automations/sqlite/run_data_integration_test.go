//go:build integration

package sqlite_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

//nolint:gocognit // One ordered real-SQLite journey proves reservation rollback, envelope storage, completion and retention.
func TestRunDataAtomicInputsPayloadStorageAndRetention(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	definition := validDomainDefinition(t)
	record, err := repository.CreateAutomation(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := repository.AdmitManualRun(
		t.Context(),
		automations.ManualRunInput{AutomationID: record.ID},
		stateSnapshotWith(),
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	run := *runEntry(admitted)
	initial := automations.RunDataRecord{
		Sequence:   0,
		RecordedAt: time.Now().UTC(),
		Body:       automations.VariablesInitialized{Values: json.RawMessage(`{}`)},
	}
	mustExec(
		t,
		database,
		`CREATE TRIGGER reject_data BEFORE INSERT ON automation_run_data BEGIN SELECT RAISE(ABORT,'injected'); END`,
	)
	if err = repository.RecordRunData(t.Context(), run.ID, initial); err == nil {
		t.Fatal("injected data failure accepted")
	}
	mustExec(t, database, `DROP TRIGGER reject_data`)
	if err = repository.RecordRunData(t.Context(), run.ID, initial); err != nil {
		t.Fatal(err)
	}
	if err = repository.RecordRunData(t.Context(), run.ID, initial); err == nil {
		t.Fatal("duplicate initialization accepted")
	}
	stepID := run.Steps[0].StepID
	inputs := automations.RunDataRecord{
		Sequence:   1,
		StepID:     &stepID,
		RecordedAt: time.Now().UTC(),
		Body:       automations.InputsResolved{Values: json.RawMessage(`{"parameters":{"value":true}}`)},
	}
	if err = repository.RecordRunData(t.Context(), run.ID, inputs); err == nil {
		t.Fatal("effect inputs bypassed reservation operation")
	}
	start := automations.StepStart{
		RunID:         run.ID,
		Position:      0,
		CommandID:     devices.CommandID(newCommandIDString(t)),
		CorrelationID: devices.CorrelationID(newCorrelationIDString(t)),
		Inputs:        inputs,
	}
	mustExec(
		t,
		database,
		`CREATE TRIGGER reject_data BEFORE INSERT ON automation_run_data BEGIN SELECT RAISE(ABORT,'injected'); END`,
	)
	if err = repository.MarkStepRunning(t.Context(), start); err == nil {
		t.Fatal("injected input failure accepted")
	}
	retained := retainedStorageRun(t, repository, record, run)
	if len(retained.Data) != 1 ||
		automations.StepAttemptStatus(retained.Steps[0].State) != automations.StepNotAttempted {
		t.Fatal("failed reservation left partial evidence")
	}
	mustExec(t, database, `DROP TRIGGER reject_data`)
	if err = repository.MarkStepRunning(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err = database.QueryRowContext(t.Context(), `SELECT payload_json FROM automation_run_data WHERE run_id=? AND sequence=1`, string(run.ID)).
		Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal([]byte(payload), &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"kind", "sequence", "step_id", "recorded_at"} {
		if _, exists := fields[key]; exists {
			t.Fatalf("duplicate envelope: %s", payload)
		}
	}
	if err = repository.CompleteStep(
		t.Context(),
		automations.StepCompletion{
			RunID:    run.ID,
			Position: 0,
			Outcome:  automations.DispatchedStep{VerifiedCommandID: start.CommandID},
		},
	); err != nil {
		t.Fatal(err)
	}
	if err = repository.CompleteRun(
		t.Context(),
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	); err != nil {
		t.Fatal(err)
	}
	retained = retainedStorageRun(t, repository, record, run)
	if retained.DataEvidence != "recorded" || len(retained.Data) != 2 {
		t.Fatal("missing retained exact journal")
	}
	if _, err = repository.DeleteHistoryBefore(t.Context(), time.Now().UTC().Add(time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = database.QueryRowContext(t.Context(), `SELECT count(*) FROM automation_run_data WHERE run_id=?`, string(run.ID)).
		Scan(&count); err != nil ||
		count != 0 {
		t.Fatal("retention did not cascade")
	}
}

func TestRunDataRejectsOwnershipSequenceLiteralMismatchAndTerminalWrites(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	definition, err := automations.DecodeDefinition(
		json.RawMessage(
			`{"name":"values","enabled":true,"triggers":[{"id":"clock","kind":"cron","expression":"* * * * *"}],"variables":{"x":{"literal":9007199254740993}},"steps":[{"id":"assign","kind":"set_variables","assignments":{"x":{"expr":"vars.x + 1"}}}]}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	record, err := repository.CreateAutomation(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := repository.AdmitManualRun(
		t.Context(),
		automations.ManualRunInput{AutomationID: record.ID},
		stateSnapshotWith(),
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	run := *runEntry(admitted)
	initial := automations.RunDataRecord{
		Sequence:   0,
		RecordedAt: time.Now().UTC(),
		Body:       automations.VariablesInitialized{Values: json.RawMessage(`{"x":9007199254740993}`)},
	}
	wrong := initial
	wrong.Body = automations.VariablesInitialized{Values: json.RawMessage(`{"x":9007199254740992}`)}
	if err = repository.RecordRunData(t.Context(), run.ID, wrong); err == nil {
		t.Fatal("rounded literal accepted")
	}
	if err = repository.RecordRunData(t.Context(), run.ID, initial); err != nil {
		t.Fatal(err)
	}
	id := automations.StepID("assign")
	assignment := automations.RunDataRecord{
		Sequence:   1,
		StepID:     &id,
		RecordedAt: time.Now().UTC(),
		Body:       automations.VariablesAssigned{Values: json.RawMessage(`{"x":9007199254740994}`)},
	}
	for name, mutate := range map[string]func(*automations.RunDataRecord){
		"sequence":     func(r *automations.RunDataRecord) { r.Sequence = 2 },
		"unknown Step": func(r *automations.RunDataRecord) { unknown := automations.StepID("other"); r.StepID = &unknown },
		"wrong kind": func(r *automations.RunDataRecord) {
			r.Body = automations.FunctionOutputCaptured{Output: json.RawMessage(`1`)}
		},
		"wrong names": func(r *automations.RunDataRecord) {
			r.Body = automations.VariablesAssigned{Values: json.RawMessage(`{"y":1}`)}
		},
	} {
		bad := assignment
		mutate(&bad)
		if err = repository.RecordRunData(t.Context(), run.ID, bad); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	if err = repository.RecordRunData(t.Context(), run.ID, assignment); err != nil {
		t.Fatal(err)
	}
	if err = repository.RecordRunData(t.Context(), run.ID, assignment); err == nil {
		t.Fatal("duplicate assignment accepted")
	}
	if err = repository.CompleteRun(
		t.Context(),
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	); err != nil {
		t.Fatal(err)
	}
	retained := retainedStorageRun(t, repository, record, run)
	raw, err := automations.EncodeRunDataRecord(retained.Data[0])
	if err != nil || !strings.Contains(string(raw), "9007199254740993") {
		t.Fatalf("exact evidence lost: %s %v", raw, err)
	}
	assignment.Sequence = 2
	if err = repository.RecordRunData(t.Context(), run.ID, assignment); err == nil {
		t.Fatal("terminal parent accepted data")
	}
}

//nolint:gocognit,nestif // Capacity exhaustion must preserve enough space for a real atomic terminal failure.
func TestRunDataBudgetPreservesFailureCapacity(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	large, _ := json.Marshal(strings.Repeat("x", 65400))
	steps := make([]string, 32)
	for i := range steps {
		steps[i] = fmt.Sprintf(`{"id":"s%d","kind":"set_variables","assignments":{"x":{"expr":"vars.x"}}}`, i)
	}
	raw := `{"name":"capacity","enabled":true,"triggers":[{"id":"clock","kind":"cron","expression":"* * * * *"}],"variables":{"x":{"literal":` + string(
		large,
	) + `}},"steps":[` + strings.Join(
		steps,
		",",
	) + `]}`
	definition, err := automations.DecodeDefinition(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	record, err := repository.CreateAutomation(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := repository.AdmitManualRun(
		t.Context(),
		automations.ManualRunInput{AutomationID: record.ID},
		stateSnapshotWith(),
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	run := *runEntry(admitted)
	values := json.RawMessage(`{"x":` + string(large) + `}`)
	if err = repository.RecordRunData(
		t.Context(),
		run.ID,
		automations.RunDataRecord{
			Sequence:   0,
			RecordedAt: time.Now().UTC(),
			Body:       automations.VariablesInitialized{Values: values},
		},
	); err != nil {
		t.Fatal(err)
	}
	failedAt := -1
	for i := range 32 {
		id := automations.StepID(fmt.Sprintf("s%d", i))
		err = repository.RecordRunData(
			t.Context(),
			run.ID,
			automations.RunDataRecord{
				Sequence:   i + 1,
				StepID:     &id,
				RecordedAt: time.Now().UTC(),
				Body:       automations.VariablesAssigned{Values: values},
			},
		)
		if err != nil {
			failure, ok := errors.AsType[*exprvalue.Failure](err)
			if !ok || failure.Code != "run_data_limit" {
				t.Fatalf("capacity error: %v", err)
			}
			failedAt = i
			// Payload-only accounting would admit this candidate. The public
			// envelope must be charged even though SQL stores it separately.
			var payloadBytes int
			if scanErr := database.QueryRow(`SELECT coalesce(sum(length(CAST(payload_json AS BLOB))), 0) FROM automation_run_data WHERE run_id = ?`, string(run.ID)).
				Scan(&payloadBytes); scanErr != nil {
				t.Fatal(scanErr)
			}
			candidatePayload, marshalErr := json.Marshal(map[string]json.RawMessage{"values": values})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if payloadBytes+len(candidatePayload) > automations.RunDataMaxBytes-automations.RunDataFailureReserveBytes {
				t.Fatal("fixture does not distinguish envelope-inclusive from payload-only accounting")
			}
			failureRecord := automations.RunDataRecord{
				Sequence:   i + 1,
				StepID:     &id,
				RecordedAt: time.Now().UTC(),
				Body: automations.RunDataFailed{
					Path:        fmt.Sprintf("/steps/%d/assignments/x", i),
					FailureCode: "run_data_limit",
				},
			}
			if err = repository.FailRunData(t.Context(), run.ID, failureRecord); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if failedAt < 0 {
		t.Fatal("aggregate budget not enforced")
	}
	retained := retainedStorageRun(t, repository, record, run)
	if automations.RunStateStatus(retained.State) != automations.RunFailed || len(retained.Data) != failedAt+2 {
		t.Fatal("capacity failure was not retained atomically")
	}
}
