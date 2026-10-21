//go:build integration

package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
)

const functionRevision = adapters.CatalogID("afc_01890f47-7a6b-7c4d-8e9f-0123456789ab")
const nextFunctionRevision = adapters.CatalogID("afc_01890f47-7a6b-7c4d-8e9f-0123456789ac")

func TestFunctionCatalogRegistrationIsImmutableFencedAndRuntimeScoped(t *testing.T) {
	t.Parallel()
	database, repository, catalog := functionFixture(t)
	ctx := context.Background()
	page, err := repository.ListFunctions(ctx, "bridge", adapters.PageInput{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Descriptor.Key != "inspect" || !page.Items[0].Callable ||
		page.NextCursor == "" {
		t.Fatalf("first catalog page=%+v err=%v", page, err)
	}
	if err = repository.RegisterCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	catalog.Functions[0].Description = "changed"
	if err = repository.RegisterCatalog(ctx, catalog); !errors.Is(err, adapters.ErrCatalogConflict) {
		t.Fatalf("changed catalog: %v", err)
	}
	catalog.Functions[0].Description = "Inspect"
	catalog.ID = nextFunctionRevision
	if err = repository.RegisterCatalog(ctx, catalog); !errors.Is(err, adapters.ErrCatalogConflict) {
		t.Fatalf("new revision in same runtime: %v", err)
	}
	at := time.Now().UTC()
	if err = repository.ReleaseAdapterRuntime(
		ctx,
		adapters.ReleaseRuntimeWrite{AdapterID: "bridge", RuntimeID: testRuntime, ReleasedAt: at},
	); err != nil {
		t.Fatal(err)
	}
	function, err := repository.GetFunction(ctx, "bridge", "inspect")
	if err != nil || function.Callable {
		t.Fatalf("offline catalog=%+v err=%v", function, err)
	}
	if err = repository.RegisterCatalog(ctx, catalog); !errors.Is(err, adapters.ErrRuntimeFenced) {
		t.Fatalf("stale catalog: %v", err)
	}
	claim := claimWrite(at.Add(time.Millisecond))
	claim.RuntimeID = replacementRuntime
	if err = repository.ClaimAdapterRuntime(ctx, claim); err != nil {
		t.Fatal(err)
	}
	function, err = repository.GetFunction(ctx, "bridge", "inspect")
	if err != nil || function.Callable {
		t.Fatalf("replacement inherited old catalog: %+v %v", function, err)
	}
	catalog.RuntimeID = replacementRuntime
	catalog.ID = functionRevision
	if err = repository.RegisterCatalog(ctx, catalog); !errors.Is(err, adapters.ErrCatalogConflict) {
		t.Fatalf("reused previous id: %v", err)
	}
	catalog.ID = nextFunctionRevision
	catalog.Functions = catalog.Functions[1:]
	if err = repository.RegisterCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.GetFunction(ctx, "bridge", "inspect"); !errors.Is(err, adapters.ErrFunctionNotFound) {
		t.Fatalf("removed function: %v", err)
	}
	if _, err = repository.ListFunctions(
		ctx,
		"bridge",
		adapters.PageInput{Limit: 1, Cursor: page.NextCursor},
	); !errors.Is(
		err,
		adapters.ErrCatalogChanged,
	) {
		t.Fatalf("replaced cursor: %v", err)
	}
	if countRows(t, database, "adapter_function_catalogs") != 1 {
		t.Fatal("catalog retries created extra rows")
	}
}

func TestFunctionAdmissionRejectsForgedPreparationAndChangingFacts(t *testing.T) {
	t.Parallel()
	database, repository, _ := functionFixture(t)
	ctx := context.Background()
	function, err := repository.GetFunction(ctx, "bridge", "inspect")
	if err != nil {
		t.Fatal(err)
	}
	input := functionInput()
	function.Descriptor.InputSchema = json.RawMessage(`{"type":"object"}`)
	input.Arguments = json.RawMessage(`{"unadvertised":true}`)
	forged, err := adapters.PrepareInvocation(function, input, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.Admit(ctx, forged); !errors.Is(err, adapters.ErrCatalogChanged) {
		t.Fatalf("forged weaker descriptor: %v", err)
	}
	if _, err = repository.Admit(
		ctx,
		adapters.PreparedInvocation{},
	); !errors.Is(
		err,
		adapters.ErrInvalidFunctionInput,
	) {
		t.Fatalf("zero preparation: %v", err)
	}
	prepared := functionPreparation(t, repository, time.Now().UTC())
	if err = repository.ReleaseAdapterRuntime(
		ctx,
		adapters.ReleaseRuntimeWrite{AdapterID: "bridge", RuntimeID: testRuntime, ReleasedAt: time.Now().UTC()},
	); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.Admit(ctx, prepared); !errors.Is(err, adapters.ErrAdapterUnavailable) {
		t.Fatalf("released runtime: %v", err)
	}
	if countRows(t, database, "adapter_function_invocations") != 0 {
		t.Fatal("ineligible admission left running rows")
	}
}

func TestFunctionEvidenceIsPreciseImmutableAndIndependentOfCatalogReplacement(t *testing.T) {
	t.Parallel()
	_, repository, _ := functionFixture(t)
	ctx := context.Background()
	prepared := functionPreparation(t, repository, time.Now().UTC())
	invocation, err := repository.Admit(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if invocation.Completion != nil || invocation.Route.RuntimeID != testRuntime ||
		string(invocation.Arguments) != `{"n":9007199254740993}` {
		t.Fatalf("running=%+v", invocation)
	}
	at := invocation.StartedAt.Add(time.Millisecond)
	if _, err = repository.Complete(
		ctx,
		invocation.ID,
		adapters.Succeeded{Result: json.RawMessage(`{"n":"wrong"}`)},
		at,
	); !errors.Is(
		err,
		adapters.ErrInvalidFunctionInput,
	) {
		t.Fatalf("invalid successful output: %v", err)
	}
	terminal, err := repository.Complete(
		ctx,
		invocation.ID,
		adapters.Succeeded{Result: json.RawMessage(` {"n":9007199254740993} `)},
		at,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := terminal.Completion.Evidence.(adapters.Succeeded); string(got.Result) != `{"n":9007199254740993}` {
		t.Fatalf("result=%s", got.Result)
	}
	first, err := repository.Complete(
		ctx,
		invocation.ID,
		adapters.Unknown{ReasonCode: "transport_error"},
		at.Add(time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := first.Completion.Evidence.(adapters.Succeeded); !ok || !first.Completion.At.Equal(at) {
		t.Fatalf("terminal overwritten: %+v", first)
	}
	if err = repository.ReleaseAdapterRuntime(
		ctx,
		adapters.ReleaseRuntimeWrite{AdapterID: "bridge", RuntimeID: testRuntime, ReleasedAt: time.Now().UTC()},
	); err != nil {
		t.Fatal(err)
	}
	claim := claimWrite(time.Now().UTC())
	claim.RuntimeID = replacementRuntime
	if err = repository.ClaimAdapterRuntime(ctx, claim); err != nil {
		t.Fatal(err)
	}
	catalog := functionCatalog()
	catalog.RuntimeID = replacementRuntime
	catalog.ID = nextFunctionRevision
	catalog.Functions = catalog.Functions[1:]
	if err = repository.RegisterCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	retained, err := repository.GetInvocation(ctx, invocation.ID)
	if err != nil || retained.Function.Key != "inspect" || retained.CatalogRevision != functionRevision ||
		retained.Route.RuntimeID != testRuntime {
		t.Fatalf("retained snapshot=%+v err=%v", retained, err)
	}
	page, err := repository.ListInvocations(ctx, adapters.InvocationFilter{AdapterID: "bridge", FunctionKey: "inspect"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != invocation.ID {
		t.Fatalf("removed key history=%+v err=%v", page, err)
	}
}

func TestFunctionRecoveryRetentionAndScopedHistoryCursors(t *testing.T) {
	t.Parallel()
	_, repository, _ := functionFixture(t)
	ctx := context.Background()
	start := time.Now().UTC()
	first, err := repository.Admit(ctx, functionPreparation(t, repository, start))
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.Admit(ctx, functionPreparation(t, repository, start))
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.Complete(
		ctx,
		first.ID,
		adapters.NotExecuted{ReasonCode: "adapter_busy"},
		start.Add(time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	filter := adapters.InvocationFilter{AdapterID: "bridge", Limit: 1}
	page, err := repository.ListInvocations(ctx, filter)
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("page=%+v %v", page, err)
	}
	filter.Cursor = page.NextCursor
	next, err := repository.ListInvocations(ctx, filter)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID || next.NextCursor != "" {
		t.Fatalf("next=%+v %v", next, err)
	}
	filter.FunctionKey = "inspect"
	if _, err = repository.ListInvocations(ctx, filter); !errors.Is(err, adapters.ErrInvalidFunctionInput) {
		t.Fatalf("filter scope: %v", err)
	}
	const retention = 30 * 24 * time.Hour
	deleted, err := repository.DeleteHistoryBefore(ctx, start.Add(retention+time.Hour), 500)
	if err != nil || deleted != 1 {
		t.Fatalf("prune deleted=%d error=%v", deleted, err)
	}
	if _, err = repository.GetInvocation(ctx, second.ID); err != nil {
		t.Fatalf("running record pruned: %v", err)
	}
	if err = repository.Recover(ctx, start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	recovered, err := repository.GetInvocation(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome, ok := recovered.Completion.Evidence.(adapters.Unknown); !ok || outcome.ReasonCode != "core_restarted" {
		t.Fatalf("recovered=%+v", recovered)
	}
	if err = repository.Recover(ctx, start.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	again, err := repository.GetInvocation(ctx, second.ID)
	if err != nil || !again.Completion.At.Equal(recovered.Completion.At) {
		t.Fatalf("recovery rewrote terminal evidence: %+v %v", again, err)
	}
}

func TestFunctionRecoveryRejectsBackwardTimeWithoutMakingHistoryUnreadable(t *testing.T) {
	t.Parallel()
	_, repository, _ := functionFixture(t)
	ctx := context.Background()
	start := time.Now().UTC()
	invocation, err := repository.Admit(ctx, functionPreparation(t, repository, start))
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.Recover(ctx, start.Add(-time.Second)); !errors.Is(err, adapters.ErrInvalidFunctionInput) {
		t.Fatalf("backward recovery=%v", err)
	}
	unchanged, err := repository.GetInvocation(ctx, invocation.ID)
	if err != nil || unchanged.Completion != nil {
		t.Fatalf("backward recovery changed evidence: %+v %v", unchanged, err)
	}
	if err = repository.Recover(ctx, start); err != nil {
		t.Fatal(err)
	}
	recovered, err := repository.GetInvocation(ctx, invocation.ID)
	if err != nil || recovered.Completion == nil || !recovered.Completion.At.Equal(start) {
		t.Fatalf("recovery=%+v %v", recovered, err)
	}
}

func TestFunctionRepositoryValidatesInputsAndIsolatesCorruptCatalogs(t *testing.T) {
	t.Parallel()
	database, repository, catalog := functionFixture(t)
	ctx := context.Background()
	invalid := catalog
	invalid.Functions = append([]adapters.Descriptor(nil), catalog.Functions...)
	invalid.Functions[0].TimeoutMS = 0
	if err := repository.RegisterCatalog(ctx, invalid); !errors.Is(err, adapters.ErrInvalidFunctionInput) {
		t.Fatalf("invalid direct registration: %v", err)
	}
	page, err := repository.ListFunctions(ctx, "unknown", adapters.PageInput{})
	if !errors.Is(err, adapters.ErrAdapterNotFound) {
		t.Fatalf("unknown adapter=%+v %v", page, err)
	}
	if _, err = repository.ListInvocations(
		ctx,
		adapters.InvocationFilter{AdapterID: "bridge", FunctionKey: "removed"},
	); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.DeleteHistoryBefore(
		ctx,
		time.Time{},
		500,
	); !errors.Is(
		err,
		adapters.ErrInvalidFunctionInput,
	) {
		t.Fatalf("invalid prune time: %v", err)
	}
	if _, err = repository.DeleteHistoryBefore(
		ctx,
		time.Now().UTC(),
		501,
	); !errors.Is(
		err,
		adapters.ErrInvalidFunctionInput,
	) {
		t.Fatalf("invalid prune batch: %v", err)
	}
	mustExec(t, database, `UPDATE adapter_function_catalogs SET functions_json='[]' WHERE adapter_id='bridge'`)
	if _, err = repository.GetFunction(ctx, "bridge", "inspect"); !errors.Is(err, adapters.ErrFunctionPersistence) {
		t.Fatalf("corrupt catalog became empty/permissive: %v", err)
	}
}

func TestFunctionWriteFaultsPreventDispatchWithoutPoisoningAdmission(t *testing.T) {
	t.Parallel()
	database, repository, _ := functionFixture(t)
	ctx := context.Background()
	invoker := &recordFunctionInvoker{}
	service, err := adapters.NewFunctionService(ctx, repository, invoker)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(
		t,
		database,
		`CREATE TRIGGER fail_function_admit BEFORE INSERT ON adapter_function_invocations BEGIN SELECT RAISE(ABORT,'test'); END`,
	)
	if _, err = service.Invoke(ctx, functionInput()); err == nil || invoker.calls != 0 {
		t.Fatalf("admission fault: calls=%d err=%v", invoker.calls, err)
	}
	mustExec(t, database, `DROP TRIGGER fail_function_admit`)
	mustExec(
		t,
		database,
		`CREATE TRIGGER fail_function_complete BEFORE UPDATE ON adapter_function_invocations BEGIN SELECT RAISE(ABORT,'test'); END`,
	)
	_, err = service.Invoke(ctx, functionInput())
	var known *adapters.InvocationError
	if !errors.As(err, &known) || known.ID == "" || invoker.calls != 1 || !service.AdmissionOpen() {
		t.Fatalf("completion fault: calls=%d err=%v", invoker.calls, err)
	}
	retained, readErr := repository.GetInvocation(ctx, known.ID)
	if readErr != nil || retained.Completion != nil {
		t.Fatalf("invented terminal=%+v err=%v", retained, readErr)
	}
	mustExec(t, database, `DROP TRIGGER fail_function_complete`)
	terminal, err := service.Invoke(ctx, functionInput())
	if err != nil || terminal.Completion == nil || invoker.calls != 2 {
		t.Fatalf("later independent invocation=%+v err=%v", terminal, err)
	}
	service.StopAdmission()
	if _, err = service.Invoke(ctx, functionInput()); !errors.Is(err, adapters.ErrFunctionAdmissionClosed) {
		t.Fatalf("closed admission: %v", err)
	}
}

type recordFunctionInvoker struct{ calls int }

func (invoker *recordFunctionInvoker) Invoke(_ context.Context, _ adapters.Invocation) adapters.Evidence {
	invoker.calls++
	return adapters.Succeeded{Result: json.RawMessage(`{"n":9007199254740993}`)}
}

func functionFixture(t *testing.T) (*sql.DB, *adaptersqlite.AdapterRepository, adapters.Catalog) {
	t.Helper()
	database, repository, _ := runtimeFixture(t)
	if err := repository.ClaimAdapterRuntime(context.Background(), claimWrite(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	catalog := functionCatalog()
	if err := repository.RegisterCatalog(context.Background(), catalog); err != nil {
		t.Fatal(err)
	}
	return database, repository, catalog
}

func functionCatalog() adapters.Catalog {
	schema := json.RawMessage(
		`{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"],"additionalProperties":false}`,
	)
	return adapters.Catalog{
		ID:        functionRevision,
		AdapterID: "bridge",
		RuntimeID: testRuntime,
		Functions: []adapters.Descriptor{
			{Key: "inspect", Description: "Inspect", InputSchema: schema, OutputSchema: schema, TimeoutMS: 10000},
			{
				Key:          "status",
				Description:  "Status",
				InputSchema:  json.RawMessage(`{"type":"object"}`),
				OutputSchema: json.RawMessage(`{"type":"object"}`),
				TimeoutMS:    10000,
			},
		},
	}
}

func functionInput() adapters.InvokeInput {
	return adapters.InvokeInput{
		AdapterID:       "bridge",
		FunctionKey:     "inspect",
		CatalogRevision: functionRevision,
		Arguments:       json.RawMessage(`{"n":9007199254740993}`),
	}
}

func functionPreparation(
	t *testing.T,
	repository *adaptersqlite.AdapterRepository,
	start time.Time,
) adapters.PreparedInvocation {
	t.Helper()
	function, err := repository.GetFunction(context.Background(), "bridge", "inspect")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := adapters.PrepareInvocation(function, functionInput(), start)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}
