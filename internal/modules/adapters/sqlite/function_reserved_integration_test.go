//go:build integration

package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
)

type reservedInvoker struct{ invocations []adapters.Invocation }

func (invoker *reservedInvoker) Invoke(_ context.Context, invocation adapters.Invocation) adapters.Evidence {
	invoker.invocations = append(invoker.invocations, invocation)
	return adapters.Succeeded{Result: invocation.Arguments}
}

func reserveFunction(t *testing.T, service *adapters.FunctionService) adapters.ReservedInvokeInput {
	t.Helper()
	function, err := service.GetFunction(t.Context(), "bridge", "inspect")
	if err != nil {
		t.Fatal(err)
	}
	id, err := adapters.NewInvocationID()
	if err != nil {
		t.Fatal(err)
	}
	contract := adapters.ContractInput{
		AdapterID: "bridge", FunctionKey: "inspect", ContractFingerprint: function.ContractFingerprint,
		Arguments: json.RawMessage(` {"n":9007199254740993} `),
	}
	return adapters.ReservedInvokeInput{InvocationID: id, ContractInput: contract}
}

func TestReservedInvocationIdentityAndTerminalConflict(t *testing.T) {
	t.Parallel()
	database, repository, _ := functionFixture(t)
	invoker := &reservedInvoker{}
	service, err := adapters.NewFunctionService(t.Context(), repository, invoker)
	if err != nil {
		t.Fatal(err)
	}
	input := reserveFunction(t, service)
	terminal, err := service.InvokeReserved(t.Context(), input)
	if err != nil || terminal.ID != input.InvocationID || terminal.Completion == nil {
		t.Fatalf("terminal=%+v err=%v", terminal, err)
	}
	if outcome, ok := terminal.Completion.Evidence.(adapters.Succeeded); !ok ||
		string(outcome.Result) != `{"n":9007199254740993}` {
		t.Fatalf("outcome=%+v", terminal.Completion.Evidence)
	}
	for _, arguments := range []string{`{"n":9007199254740993}`, `{"n":7}`} {
		duplicate := input
		duplicate.Arguments = json.RawMessage(arguments)
		if _, err = service.InvokeReserved(t.Context(), duplicate); !errors.Is(err, adapters.ErrInvocationIDConflict) {
			t.Fatalf("duplicate err=%v", err)
		}
	}
	retained, err := service.GetInvocation(t.Context(), input.InvocationID)
	if err != nil || retained.ID != terminal.ID || !retained.Completion.At.Equal(terminal.Completion.At) ||
		string(retained.Arguments) != `{"n":9007199254740993}` {
		t.Fatalf("retained=%+v err=%v", retained, err)
	}
	if len(invoker.invocations) != 1 || invoker.invocations[0].ID != input.InvocationID ||
		invoker.invocations[0].Route.RuntimeID != testRuntime || string(invoker.invocations[0].Arguments) != `{"n":9007199254740993}` {
		t.Fatalf("dispatches=%+v", invoker.invocations)
	}
	if countRows(t, database, "adapter_function_invocations") != 1 {
		t.Fatal("duplicate created another invocation")
	}
}

func TestReservedInvocationSurvivesSameContractRuntimeRestart(t *testing.T) {
	t.Parallel()
	database, repository, catalog := functionFixture(t)
	invoker := &reservedInvoker{}
	service, err := adapters.NewFunctionService(t.Context(), repository, invoker)
	if err != nil {
		t.Fatal(err)
	}
	input := reserveFunction(t, service)
	if _, err = service.InvokeReserved(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	// Retained discovery can validate while no runtime is callable.
	if err = repository.ReleaseAdapterRuntime(
		t.Context(),
		adapters.ReleaseRuntimeWrite{AdapterID: "bridge", RuntimeID: testRuntime, ReleasedAt: time.Now().UTC()},
	); err != nil {
		t.Fatal(err)
	}
	if normalized, validateErr := service.ValidateContractInput(
		t.Context(),
		input.ContractInput,
	); validateErr != nil ||
		string(normalized) != `{"n":9007199254740993}` {
		t.Fatalf("offline arguments=%s err=%v", normalized, validateErr)
	}
	claim := claimWrite(time.Now().UTC())
	claim.RuntimeID = replacementRuntime
	if err = repository.ClaimAdapterRuntime(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	catalog.RuntimeID, catalog.ID = replacementRuntime, nextFunctionRevision
	catalog.Functions[0].Description = "New outer description"
	catalog.Functions[0].InputSchema = json.RawMessage(
		` {"additionalProperties":false,"required":["n"],"properties":{"n":{"type":"integer"}},"type":"object"} `,
	)
	if err = repository.RegisterCatalog(t.Context(), catalog); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Invoke(t.Context(), functionInput()); !errors.Is(err, adapters.ErrCatalogChanged) {
		t.Fatalf("public stale revision=%v", err)
	}
	second := reserveFunction(t, service)
	if second.ContractFingerprint != input.ContractFingerprint {
		t.Fatalf("restart changed contract: %s != %s", second.ContractFingerprint, input.ContractFingerprint)
	}
	terminal, err := service.InvokeReserved(t.Context(), second)
	if err != nil || terminal.ID != second.InvocationID || terminal.CatalogRevision != nextFunctionRevision ||
		terminal.Route.RuntimeID != replacementRuntime {
		t.Fatalf("restarted invocation=%+v err=%v", terminal, err)
	}
	if len(invoker.invocations) != 2 || invoker.invocations[1].ID != second.InvocationID ||
		invoker.invocations[1].Route.RuntimeID != replacementRuntime ||
		string(invoker.invocations[1].Arguments) != `{"n":9007199254740993}` ||
		countRows(t, database, "adapter_function_invocations") != 2 {
		t.Fatalf("dispatches=%+v", invoker.invocations)
	}
}

type beforeFunctionAdmission struct {
	adapters.FunctionRepository

	before func()
}

func (r *beforeFunctionAdmission) Admit(
	ctx context.Context,
	prepared adapters.PreparedInvocation,
) (adapters.Invocation, error) {
	r.before()
	return r.FunctionRepository.Admit(ctx, prepared)
}

func TestReservedInvocationRejectsCatalogReplacementBetweenResolutionAndAdmission(t *testing.T) {
	t.Parallel()
	database, repository, catalog := functionFixture(t)
	invoker := &reservedInvoker{}
	wrapped := &beforeFunctionAdmission{FunctionRepository: repository, before: func() {
		replaceFunctionRuntime(t, repository, catalog)
	}}
	service, err := adapters.NewFunctionService(t.Context(), wrapped, invoker)
	if err != nil {
		t.Fatal(err)
	}
	input := reserveFunction(t, service)
	if _, err = service.InvokeReserved(t.Context(), input); !errors.Is(err, adapters.ErrCatalogChanged) {
		t.Fatalf("race=%v", err)
	}
	if len(invoker.invocations) != 0 || countRows(t, database, "adapter_function_invocations") != 0 {
		t.Fatalf("stale attempt dispatched=%+v", invoker.invocations)
	}
	// A separate explicit attempt can use the equivalent replacement; no retry
	// or rediscovery occurred in the rejected attempt above.
	wrapped.before = func() {}
	next := reserveFunction(t, service)
	terminal, err := service.InvokeReserved(t.Context(), next)
	if err != nil || terminal.ID != next.InvocationID || len(invoker.invocations) != 1 ||
		invoker.invocations[0].Route.RuntimeID != replacementRuntime {
		t.Fatalf("next=%+v err=%v calls=%+v", terminal, err, invoker.invocations)
	}
}

func replaceFunctionRuntime(t *testing.T, repository *adaptersqlite.AdapterRepository, catalog adapters.Catalog) {
	t.Helper()
	if err := repository.ReleaseAdapterRuntime(
		t.Context(),
		adapters.ReleaseRuntimeWrite{AdapterID: "bridge", RuntimeID: testRuntime, ReleasedAt: time.Now().UTC()},
	); err != nil {
		t.Fatal(err)
	}
	claim := claimWrite(time.Now().UTC())
	claim.RuntimeID = replacementRuntime
	if err := repository.ClaimAdapterRuntime(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	catalog.RuntimeID, catalog.ID = replacementRuntime, nextFunctionRevision
	if err := repository.RegisterCatalog(t.Context(), catalog); err != nil {
		t.Fatal(err)
	}
}

func TestFunctionPublicClosurePreservesReservedAdmission(t *testing.T) {
	t.Parallel()
	_, repository, _ := functionFixture(t)
	invoker := &reservedInvoker{}
	service, err := adapters.NewFunctionService(t.Context(), repository, invoker)
	if err != nil {
		t.Fatal(err)
	}
	input := reserveFunction(t, service)
	service.ClosePublicAdmission()
	service.ClosePublicAdmission()
	if service.AdmissionOpen() {
		t.Fatal("public admission open")
	}
	if _, err = service.Invoke(t.Context(), functionInput()); !errors.Is(err, adapters.ErrFunctionAdmissionClosed) {
		t.Fatalf("public err=%v", err)
	}
	terminal, err := service.InvokeReserved(t.Context(), input)
	if err != nil || terminal.ID != input.InvocationID || terminal.Completion == nil || len(invoker.invocations) != 1 ||
		string(invoker.invocations[0].Arguments) != `{"n":9007199254740993}` {
		t.Fatalf("reserved=%+v err=%v dispatches=%+v", terminal, err, invoker.invocations)
	}
	service.StopAdmission()
	if _, err = service.InvokeReserved(
		t.Context(),
		reserveFunction(t, service),
	); !errors.Is(
		err,
		adapters.ErrFunctionAdmissionClosed,
	) {
		t.Fatalf("internal err=%v", err)
	}
	if err = service.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestReservedInvocationChangedContractNeverAdmits(t *testing.T) {
	t.Parallel()
	database, repository, catalog := functionFixture(t)
	invoker := &reservedInvoker{}
	service, err := adapters.NewFunctionService(t.Context(), repository, invoker)
	if err != nil {
		t.Fatal(err)
	}
	input := reserveFunction(t, service)
	catalog.Functions[0].TimeoutMS++
	replaceFunctionRuntime(t, repository, catalog)
	if _, err = service.InvokeReserved(t.Context(), input); !errors.Is(err, adapters.ErrFunctionContractChanged) {
		t.Fatalf("changed=%v", err)
	}
	if len(invoker.invocations) != 0 || countRows(t, database, "adapter_function_invocations") != 0 {
		t.Fatalf("changed contract dispatched: %+v", invoker.invocations)
	}
	next := reserveFunction(t, service)
	terminal, err := service.InvokeReserved(t.Context(), next)
	if err != nil || terminal.ID != next.InvocationID ||
		terminal.Function.TimeoutMS != catalog.Functions[0].TimeoutMS ||
		len(invoker.invocations) != 1 {
		t.Fatalf("accepted replacement=%+v err=%v", terminal, err)
	}
}

type pendingReservedInvoker struct {
	entered chan adapters.Invocation
	release chan struct{}
	calls   atomic.Int64
}

func (invoker *pendingReservedInvoker) Invoke(_ context.Context, invocation adapters.Invocation) adapters.Evidence {
	if invoker.calls.Add(1) == 1 {
		invoker.entered <- invocation
		<-invoker.release
	}
	return adapters.Succeeded{Result: invocation.Arguments}
}

func TestDuplicateReservedIDCannotJoinOrRedispatchRunningInvocation(t *testing.T) {
	t.Parallel()
	database, repository, _ := functionFixture(t)
	invoker := &pendingReservedInvoker{entered: make(chan adapters.Invocation, 1), release: make(chan struct{})}
	service, err := adapters.NewFunctionService(t.Context(), repository, invoker)
	if err != nil {
		t.Fatal(err)
	}
	input := reserveFunction(t, service)
	type result struct {
		invocation adapters.Invocation
		err        error
	}
	done := make(chan result, 1)
	go func() {
		invocation, invokeErr := service.InvokeReserved(t.Context(), input)
		done <- result{invocation: invocation, err: invokeErr}
	}()
	dispatched := <-invoker.entered
	duplicate := make(chan error, 1)
	go func() {
		_, duplicateErr := service.InvokeReserved(t.Context(), input)
		duplicate <- duplicateErr
	}()
	const livenessLimit = 5 * time.Second
	select {
	case err = <-duplicate:
	case <-time.After(livenessLimit):
		close(invoker.release)
		<-done
		t.Fatal("duplicate joined or stalled behind the running invocation")
	}
	if !errors.Is(err, adapters.ErrInvocationIDConflict) {
		close(invoker.release)
		<-done
		t.Fatalf("running duplicate=%v", err)
	}
	retained, readErr := repository.GetInvocation(t.Context(), input.InvocationID)
	close(invoker.release)
	completed := <-done
	if readErr != nil || retained.ID != input.InvocationID || retained.Completion != nil ||
		string(retained.Arguments) != `{"n":9007199254740993}` {
		t.Fatalf("running evidence=%+v err=%v", retained, readErr)
	}
	if completed.err != nil || completed.invocation.ID != input.InvocationID || completed.invocation.Completion == nil {
		t.Fatalf("completed=%+v err=%v", completed.invocation, completed.err)
	}
	if dispatched.ID != input.InvocationID || invoker.calls.Load() != 1 ||
		countRows(t, database, "adapter_function_invocations") != 1 {
		t.Fatalf(
			"completed=%+v err=%v dispatched=%+v calls=%d",
			completed.invocation,
			completed.err,
			dispatched,
			invoker.calls.Load(),
		)
	}
}
