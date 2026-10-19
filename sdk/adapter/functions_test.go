package adapter //nolint:testpackage // Exercises public SDK methods with the shared real-NATS fixture.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/trace"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

// Core's fixture deliberately withholds acknowledgement, not SDK admission:
// invocation must already work and retries must preserve identity and bytes.
func TestFunctionsServeBeforeRegistrationReplyAndRetainCatalogAcrossRestart(t *testing.T) {
	t.Parallel()
	fixture := newFunctionFixture(t, false)
	functions := []Function{functionForTest(successfulFunction), functionForTest(successfulFunction)}
	functions[0].Descriptor.Key = "zeta"
	functions[1].Descriptor.Key = "alpha"
	type startResult struct {
		server *FunctionServer
		err    error
	}
	started := make(chan startResult, 1)
	ctx := trace.ContextWithSpanContext(testContext(t), sampleSpanContext())
	go func() {
		server, err := fixture.session.StartFunctionServer(ctx, functions)
		started <- startResult{server, err}
	}()
	first := receiveFunctionValue(t, fixture.registrations)
	registration := fixture.registration(t, first)
	if registration.Data.Functions[0].Key != "alpha" || registration.Data.Functions[1].Key != "zeta" {
		t.Fatal("registration is not sorted by key")
	}
	// Caller-owned schemas/descriptors must not mutate admitted serving state.
	functions[1].Descriptor.Description = "Changed by caller"
	copy(functions[1].Descriptor.OutputSchema, []byte(`{"type":"string"}`))
	response := fixture.invoke(t, fixture.payload(t, registration.ID, "alpha", `{}`, time.Now().Add(time.Second)))
	if response.Status != "succeeded" || string(response.Result) != `{"value":9007199254740993}` {
		t.Fatalf("commit-before-reply invocation = %#v", response)
	}
	second := receiveFunctionValue(t, fixture.registrations)
	if !bytes.Equal(first.Data, second.Data) || !reflect.DeepEqual(first.Header, second.Header) ||
		first.Header.Get("traceparent") == "" {
		t.Fatal("retry changed registration bytes or trace metadata")
	}
	fixture.accept(t, second)
	result := receiveFunctionValue(t, started)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if err := result.server.Close(testContext(t)); err != nil {
		t.Fatal(err)
	}
	if err := result.server.Wait(testContext(t)); !errors.Is(err, ErrClosed) {
		t.Fatalf("Wait = %v", err)
	}
	// A new handler is allowed, but the descriptor set is not.
	functions[1] = functionForTest(successfulFunction)
	functions[1].Descriptor.Key = "alpha"
	functions[1].Descriptor.Description = "changed"
	if _, err := fixture.session.StartFunctionServer(testContext(t), functions); err == nil {
		t.Fatal("catalog changed within Session")
	}
	functions[1].Descriptor.Description = "Refresh configured data"
	go func() {
		server, err := fixture.session.StartFunctionServer(testContext(t), functions)
		started <- startResult{server, err}
	}()
	restarted := receiveFunctionValue(t, fixture.registrations)
	if !bytes.Equal(first.Data, restarted.Data) || !reflect.DeepEqual(first.Header, restarted.Header) {
		t.Fatal("restart changed catalog identity")
	}
	fixture.accept(t, restarted)
	result = receiveFunctionValue(t, started)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if err := result.server.Close(testContext(t)); err != nil {
		t.Fatal(err)
	}
}

//nolint:paralleltest,tparallel // Sequential invalid starts share one Session and must not claim its catalog.
func TestFunctionStartValidatesLocalDescriptorsAndRejectsSecondServer(t *testing.T) {
	t.Parallel()
	fixture := newFunctionFixture(t, true)
	cases := []struct {
		name      string
		functions []Function
	}{
		{"empty", nil}, {"too_many", make([]Function, 65)},
		{"nil_handler", []Function{functionForTest(nil)}},
		{"duplicate", []Function{functionForTest(successfulFunction), functionForTest(successfulFunction)}},
	}
	largeCatalog := make([]Function, 64)
	for index := range largeCatalog {
		function := functionForTest(successfulFunction)
		function.Descriptor.Key = "function_" + strconv.Itoa(index)
		function.Descriptor.InputSchema = json.RawMessage(
			`{"type":"object","description":"` + strings.Repeat("x", 2000) + `"}`,
		)
		function.Descriptor.OutputSchema = function.Descriptor.InputSchema
		largeCatalog[index] = function
	}
	cases = append(cases, struct {
		name      string
		functions []Function
	}{"registration_envelope_bound", largeCatalog})
	for _, field := range []string{"key", "description", "timeout", "schema"} {
		function := functionForTest(successfulFunction)
		switch field {
		case "key":
			function.Descriptor.Key = "bad.key"
		case "description":
			function.Descriptor.Description = "  "
		case "timeout":
			function.Descriptor.TimeoutMS = 99
		case "schema":
			function.Descriptor.InputSchema = json.RawMessage(`{"type":"object","$ref":"file:///secret"}`)
		}
		cases = append(cases, struct {
			name      string
			functions []Function
		}{field, []Function{function}})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := fixture.session.StartFunctionServer(testContext(t), test.functions)
			if _, invalid := errors.AsType[*ValidationError](err); !invalid {
				t.Fatalf("start error = %v", err)
			}
		})
	}
	select {
	case <-fixture.registrations:
		t.Fatal("invalid catalog reached Core")
	default:
	}
	server, err := fixture.session.StartFunctionServer(testContext(t), []Function{functionForTest(successfulFunction)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.session.StartFunctionServer(
		testContext(t),
		[]Function{functionForTest(successfulFunction)},
	); err == nil {
		t.Fatal("two function servers active")
	}
	if err = server.Close(testContext(t)); err != nil {
		t.Fatal(err)
	}
}

//nolint:paralleltest,tparallel // One server/call-count assertion owns this sequential protocol table.
func TestFunctionInvocationGuardsAndSafeHandlerEvidence(t *testing.T) {
	t.Parallel()
	fixture := newFunctionFixture(t, true)
	var calls atomic.Int32
	function := functionForTest(func(_ context.Context, request FunctionRequest) FunctionResult {
		calls.Add(1)
		switch string(request.Arguments) {
		case `{"mode":"panic"}`:
			panic("private provider payload")
		case `{"mode":"nil"}`:
			return nil
		case `{"mode":"invalid"}`:
			return FunctionSucceeded{Result: json.RawMessage(`{"value":"private"}`)}
		case `{"mode":"oversized"}`:
			return FunctionSucceeded{
				Result: json.RawMessage(`{"value":1,"padding":"` + strings.Repeat("x", 65*1024) + `"}`),
			}
		case `{"mode":"reason"}`:
			return FunctionFailed{ReasonCode: "Private provider error!"}
		case `{"mode":"failed"}`:
			return FunctionFailed{ReasonCode: "upstream_failed"}
		case `{"mode":"busy"}`:
			return FunctionNotExecuted{ReasonCode: "adapter_busy"}
		case `{"mode":"unknown"}`:
			return FunctionUnknown{ReasonCode: "upstream_unknown"}
		default:
			return successfulFunction(context.Background(), request)
		}
	})
	function.Descriptor.InputSchema = json.RawMessage(
		`{"type":"object","properties":{"mode":{"type":"string"}},"additionalProperties":false}`,
	)
	function.Descriptor.OutputSchema = json.RawMessage(
		`{"type":"object","required":["value"],"properties":{"value":{"type":"integer"},"padding":{"type":"string"}},"additionalProperties":false}`,
	)
	server, err := fixture.session.StartFunctionServer(testContext(t), []Function{function})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close(testContext(t)) }()
	revision := fixture.registration(t, receiveFunctionValue(t, fixture.registrations)).ID
	waitCtx, cancelWait := context.WithTimeout(testContext(t), 10*time.Millisecond)
	if err = server.Wait(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("open server Wait = %v", err)
	}
	cancelWait()
	cases := []struct {
		name, revision, key, args, status, reason string
		expired                                   bool
	}{
		{"wrong_revision", mustID(t, "afc"), "refresh", `{}`, "not_executed", "catalog_changed", false},
		{"unknown_key", revision, "missing", `{}`, "not_executed", "function_unavailable", false},
		{"expired", revision, "refresh", `{}`, "not_executed", "deadline_expired", true},
		{"input", revision, "refresh", `{"mode":42}`, "not_executed", "invalid_input", false},
		{
			"oversized_input",
			revision,
			"refresh",
			`{"mode":"` + strings.Repeat("x", 65*1024) + `"}`,
			"not_executed",
			"invalid_input",
			false,
		},
		{"panic", revision, "refresh", `{"mode":"panic"}`, "unknown", "handler_panic", false},
		{"nil", revision, "refresh", `{"mode":"nil"}`, "unknown", "invalid_response", false},
		{"output", revision, "refresh", `{"mode":"invalid"}`, "unknown", "invalid_response", false},
		{"oversized_output", revision, "refresh", `{"mode":"oversized"}`, "unknown", "invalid_response", false},
		{"reason", revision, "refresh", `{"mode":"reason"}`, "unknown", "invalid_response", false},
		{"failed", revision, "refresh", `{"mode":"failed"}`, "failed", "upstream_failed", false},
		{"busy", revision, "refresh", `{"mode":"busy"}`, "not_executed", "adapter_busy", false},
		{"unknown", revision, "refresh", `{"mode":"unknown"}`, "unknown", "upstream_unknown", false},
		{"success", revision, "refresh", `{}`, "succeeded", "", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			deadline := time.Now().Add(time.Second)
			if test.expired {
				deadline = time.Now().Add(-time.Second)
			}
			response := fixture.invoke(t, fixture.payload(t, test.revision, test.key, test.args, deadline))
			if response.Status != test.status || response.ReasonCode != test.reason {
				t.Fatalf("response = %#v", response)
			}
			if test.status != "succeeded" && response.Result != nil {
				t.Fatal("invalid result leaked")
			}
		})
	}
	if calls.Load() != 9 {
		t.Fatalf("handler calls = %d, want 9", calls.Load())
	}
}

func TestFunctionMalformedIdentityAndEnvelopeNeverReachHandler(t *testing.T) {
	t.Parallel()
	fixture := newFunctionFixture(t, true)
	var calls atomic.Int32
	server, err := fixture.session.StartFunctionServer(
		testContext(t),
		[]Function{functionForTest(func(ctx context.Context, request FunctionRequest) FunctionResult {
			calls.Add(1)
			return successfulFunction(ctx, request)
		})},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close(testContext(t)) }()
	revision := fixture.registration(t, receiveFunctionValue(t, fixture.registrations)).ID
	valid := fixture.payload(t, revision, "refresh", `{}`, time.Now().Add(3*time.Second))
	var request natswire.Envelope[functionInvokeRequest]
	if err = json.Unmarshal(valid, &request); err != nil {
		t.Fatal(err)
	}
	wrongID := bytes.Replace(
		valid,
		[]byte(`"invocation_id":"`+request.ID),
		[]byte(`"invocation_id":"`+mustID(t, "afi")),
		1,
	)
	wrongRuntime, err := natswire.FunctionInvokeSubject(fixture.session.adapterID, mustID(t, "run"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.core.RequestWithContext(
		testContext(t),
		wrongRuntime,
		valid,
	); !errors.Is(
		err,
		natsgo.ErrNoResponders,
	) {
		t.Fatalf("wrong runtime request = %v", err)
	}
	oversized := []byte(string(valid) + strings.Repeat(" ", 130*1024))
	subject, err := natswire.FunctionInvokeSubject(fixture.session.adapterID, fixture.session.runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range [][]byte{wrongID, []byte(`{"malformed":true}`), oversized} {
		ctx, cancel := context.WithTimeout(testContext(t), 100*time.Millisecond)
		_, requestErr := fixture.core.RequestWithContext(ctx, subject, payload)
		cancel()
		if !errors.Is(requestErr, context.DeadlineExceeded) {
			t.Fatalf("malformed request = %v", requestErr)
		}
	}
	response := fixture.invoke(t, valid)
	if response.Status != "succeeded" || calls.Load() != 1 {
		t.Fatalf("positive control = %#v, calls %d", response, calls.Load())
	}
}

func TestFunctionDeadlineRepliesButCloseJoinsActualConcurrentHandlers(t *testing.T) {
	t.Parallel()
	fixture := newFunctionFixture(t, true)
	const concurrency = 24
	entered := make(chan struct{}, concurrency)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	function := functionForTest(func(ctx context.Context, request FunctionRequest) FunctionResult {
		if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(request.Deadline) {
			t.Error("wrong handler deadline")
		}
		entered <- struct{}{}
		<-release // Deliberately ignore cancellation: reply is not actual completion.
		return successfulFunction(ctx, request)
	})
	server, err := fixture.session.StartFunctionServer(testContext(t), []Function{function})
	if err != nil {
		t.Fatal(err)
	}
	revision := fixture.registration(t, receiveFunctionValue(t, fixture.registrations)).ID
	responses := make(chan functionInvokeResponse, concurrency)
	for range concurrency {
		payload := fixture.payload(t, revision, "refresh", `{}`, time.Now().Add(500*time.Millisecond))
		go func() { responses <- fixture.invoke(t, payload) }()
	}
	for range concurrency {
		receiveFunctionValue(t, entered)
	}
	for range concurrency {
		response := receiveFunctionValue(t, responses)
		if response.Status != "unknown" || response.ReasonCode != "outcome_timeout" {
			t.Fatalf("deadline = %#v", response)
		}
	}
	ctx, cancel := context.WithTimeout(testContext(t), 30*time.Millisecond)
	defer cancel()
	if err = server.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("early Close = %v", err)
	}
	if err = server.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("early Wait = %v", err)
	}
	if _, err = fixture.session.StartFunctionServer(testContext(t), []Function{function}); err == nil {
		t.Fatal("restart while handlers still running")
	}
	releaseOnce.Do(func() { close(release) })
	if err = server.Close(testContext(t)); err != nil {
		t.Fatal(err)
	}
	if err = server.Wait(testContext(t)); !errors.Is(err, ErrClosed) {
		t.Fatalf("joined Wait = %v", err)
	}
}

func TestFunctionFailedStartCancellationJoinsAndRetainsAmbiguousCatalog(t *testing.T) {
	t.Parallel()
	fixture := newFunctionFixture(t, false)
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	function := functionForTest(func(ctx context.Context, _ FunctionRequest) FunctionResult {
		entered <- ctx
		<-release
		return FunctionNotExecuted{ReasonCode: "adapter_busy"}
	})
	startCtx, cancelStart := context.WithCancel(testContext(t))
	started := make(chan error, 1)
	go func() { _, err := fixture.session.StartFunctionServer(startCtx, []Function{function}); started <- err }()
	first := receiveFunctionValue(t, fixture.registrations)
	revision := fixture.registration(t, first).ID
	responses := make(chan functionInvokeResponse, 1)
	payload := fixture.payload(t, revision, "refresh", `{}`, time.Now().Add(3*time.Second))
	go func() { responses <- fixture.invoke(t, payload) }()
	handlerCtx := receiveFunctionValue(t, entered)
	cancelStart()
	// The start wait is caller-owned; admitted work has its own absolute deadline.
	select {
	case <-handlerCtx.Done():
		t.Fatal("start cancellation reached admitted handler")
	case err := <-started:
		t.Fatalf("start returned before actual handler: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if err := receiveFunctionValue(t, started); !errors.Is(err, context.Canceled) {
		t.Fatalf("start = %v", err)
	}
	if response := receiveFunctionValue(t, responses); response.ReasonCode != "adapter_busy" {
		t.Fatalf("response = %#v", response)
	}
	restarted := make(chan *FunctionServer, 1)
	go func() {
		server, err := fixture.session.StartFunctionServer(
			testContext(t),
			[]Function{functionForTest(successfulFunction)},
		)
		if err != nil {
			t.Error(err)
		}
		restarted <- server
	}()
	next := receiveFunctionValue(t, fixture.registrations)
	if !bytes.Equal(first.Data, next.Data) {
		t.Fatal("failed start lost catalog identity")
	}
	fixture.accept(t, next)
	server := receiveFunctionValue(t, restarted)
	if server == nil {
		t.Fatal("restart failed")
	}
	if err := server.Close(testContext(t)); err != nil {
		t.Fatal(err)
	}
}

func TestFunctionSessionCloseJoinsBeforeRuntimeRelease(t *testing.T) {
	t.Parallel()
	fixture := newFunctionFixture(t, true)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	released := make(chan struct{}, 1)
	_, err := fixture.core.Subscribe(natswire.AdapterReleaseWildcard(), func(*natsgo.Msg) { released <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	if err = fixture.core.Flush(); err != nil {
		t.Fatal(err)
	}
	server, err := fixture.session.StartFunctionServer(
		testContext(t),
		[]Function{functionForTest(func(ctx context.Context, request FunctionRequest) FunctionResult {
			entered <- struct{}{}
			<-release
			// Reenter a Session method to expose a Session mutex held while joining.
			if _, startErr := fixture.session.StartFunctionServer(
				testContext(t),
				[]Function{functionForTest(successfulFunction)},
			); !errors.Is(
				startErr,
				ErrClosed,
			) {
				t.Errorf("start during Session close = %v", startErr)
			}
			if healthErr := fixture.session.SetHealth(
				testContext(t),
				HealthReport{Status: HealthHealthy, SourceObservedAt: time.Now()},
			); healthErr != nil {
				t.Error(healthErr)
			}
			return successfulFunction(ctx, request)
		})},
	)
	if err != nil {
		t.Fatal(err)
	}
	revision := fixture.registration(t, receiveFunctionValue(t, fixture.registrations)).ID
	response := make(chan functionInvokeResponse, 1)
	payload := fixture.payload(t, revision, "refresh", `{}`, time.Now().Add(3*time.Second))
	go func() { response <- fixture.invoke(t, payload) }()
	receiveFunctionValue(t, entered)
	closed := make(chan error, 1)
	go func() { closed <- fixture.session.Close() }()
	ctx, cancel := context.WithTimeout(testContext(t), 30*time.Millisecond)
	defer cancel()
	if err = server.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("premature server completion = %v", err)
	}
	select {
	case <-released:
		t.Fatal("runtime released before handler returned")
	default:
	}
	once.Do(func() { close(release) })
	if result := receiveFunctionValue(t, response); result.Status != "succeeded" {
		t.Fatalf("response = %#v", result)
	}
	if err = receiveFunctionValue(t, closed); err != nil {
		t.Fatal(err)
	}
	receiveFunctionValue(t, released)
	if _, err = fixture.session.StartFunctionServer(
		testContext(t),
		[]Function{functionForTest(successfulFunction)},
	); !errors.Is(
		err,
		ErrClosed,
	) {
		t.Fatalf("closed Session start = %v", err)
	}
}

func TestFunctionHandlerRetainsIncomingTraceAndDeadlineAfterStartCancellation(t *testing.T) {
	t.Parallel()
	fixture := newFunctionFixture(t, true)
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	startCtx, cancelStart := context.WithCancel(trace.ContextWithSpanContext(testContext(t), sampleSpanContext()))
	defer cancelStart()
	server, err := fixture.session.StartFunctionServer(
		startCtx,
		[]Function{functionForTest(func(ctx context.Context, request FunctionRequest) FunctionResult {
			entered <- ctx
			<-release
			if string(request.Arguments) != `{"value":9007199254740993}` {
				t.Errorf("normalized arguments = %s", request.Arguments)
			}
			return successfulFunction(ctx, request)
		})},
	)
	if err != nil {
		t.Fatal(err)
	}
	revision := fixture.registration(t, receiveFunctionValue(t, fixture.registrations)).ID
	incoming := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1},
		SpanID:  trace.SpanID{8, 7, 6, 5, 4, 3, 2, 1}, TraceFlags: trace.FlagsSampled,
	})
	headers := make(natsgo.Header)
	natswire.InjectTrace(trace.ContextWithSpanContext(testContext(t), incoming), headers)
	deadline := time.Now().UTC().Add(3 * time.Second)
	payload := fixture.payload(t, revision, "refresh", `{"value": 9007199254740993}`, deadline)
	responses := make(chan functionInvokeResponse, 1)
	go func() { responses <- fixture.invokeWithHeaders(t, payload, headers) }()
	handlerCtx := receiveFunctionValue(t, entered)
	if actual := trace.SpanContextFromContext(
		handlerCtx,
	); actual.TraceID() != incoming.TraceID() ||
		actual.SpanID() != incoming.SpanID() {
		t.Fatalf("handler trace = %v, want incoming %v", actual, incoming)
	}
	if actual, ok := handlerCtx.Deadline(); !ok || !actual.Equal(deadline) {
		t.Fatalf("handler deadline = %v", actual)
	}
	cancelStart()
	// Close waits for actual user code; neither it nor start cancellation cancels
	// the already admitted invocation's independent deadline.
	ctx, cancel := context.WithTimeout(testContext(t), 30*time.Millisecond)
	defer cancel()
	if err = server.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close = %v", err)
	}
	if handlerCtx.Err() != nil {
		t.Fatalf("admitted context canceled: %v", handlerCtx.Err())
	}
	once.Do(func() { close(release) })
	if response := receiveFunctionValue(t, responses); response.Status != "succeeded" {
		t.Fatalf("response = %#v", response)
	}
	if err = server.Close(testContext(t)); err != nil {
		t.Fatal(err)
	}
}

func TestFunctionFencingJoinsAdmittedHandlerBeforeConnectionCloses(t *testing.T) {
	t.Parallel()
	fixture := newFunctionFixture(t, false)
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	started := make(chan error, 1)
	go func() {
		_, err := fixture.session.StartFunctionServer(
			testContext(t),
			[]Function{functionForTest(func(ctx context.Context, request FunctionRequest) FunctionResult {
				entered <- ctx
				<-release
				return successfulFunction(ctx, request)
			})},
		)
		started <- err
	}()
	registrationMessage := receiveFunctionValue(t, fixture.registrations)
	registration := fixture.registration(t, registrationMessage)
	responses := make(chan functionInvokeResponse, 1)
	payload := fixture.payload(t, registration.ID, "refresh", `{}`, time.Now().Add(3*time.Second))
	go func() { responses <- fixture.invoke(t, payload) }()
	handlerCtx := receiveFunctionValue(t, entered)
	respondTestLifecycle(
		t,
		fixture.session.validator,
		registrationMessage,
		registration.ID,
		registration.CorrelationID,
		contractsv1.AdapterFunctionRegistrationResponseSchemaID,
		functionRegistrationResponse{Status: "rejected", ReasonCode: "stale_runtime"},
	)
	if err := fixture.session.Wait(testContext(t)); !errors.Is(err, ErrRuntimeFenced) {
		t.Fatalf("Session.Wait = %v", err)
	}
	select {
	case <-handlerCtx.Done():
	case <-testContext(t).Done():
		t.Fatal("fencing did not cancel handler context")
	}
	// A response published through the Session's still-live connection proves
	// fencing did not withdraw transport while user work remained admitted.
	if response := receiveFunctionValue(
		t,
		responses,
	); response.Status != "unknown" ||
		response.ReasonCode != "transport_error" {
		t.Fatalf("fenced response = %#v", response)
	}
	closed := make(chan error, 1)
	go func() { closed <- fixture.session.Close() }()
	select {
	case err := <-started:
		t.Fatalf("failed start abandoned admitted work: %v", err)
	case err := <-closed:
		t.Fatalf("Session.Close abandoned admitted work: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	if err := receiveFunctionValue(t, started); !errors.Is(err, ErrRuntimeFenced) {
		t.Fatalf("start = %v", err)
	}
	if err := receiveFunctionValue(t, closed); !errors.Is(err, ErrRuntimeFenced) {
		t.Fatalf("Close = %v", err)
	}
}

func TestFunctionRegistrationRefusalAndFencing(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"catalog_conflict", "stale_runtime"} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			fixture := newFunctionFixture(t, false)
			started := make(chan error, 1)
			go func() {
				_, err := fixture.session.StartFunctionServer(
					testContext(t),
					[]Function{functionForTest(successfulFunction)},
				)
				started <- err
			}()
			message := receiveFunctionValue(t, fixture.registrations)
			request := fixture.registration(t, message)
			respondTestLifecycle(
				t,
				fixture.session.validator,
				message,
				request.ID,
				request.CorrelationID,
				contractsv1.AdapterFunctionRegistrationResponseSchemaID,
				functionRegistrationResponse{Status: "rejected", ReasonCode: reason},
			)
			err := receiveFunctionValue(t, started)
			if reason == "stale_runtime" {
				if !errors.Is(err, ErrRuntimeFenced) {
					t.Fatalf("start = %v", err)
				}
				if waitErr := fixture.session.Wait(testContext(t)); !errors.Is(waitErr, ErrRuntimeFenced) {
					t.Fatalf("Session.Wait = %v", waitErr)
				}
			} else {
				rejected, ok := errors.AsType[*FunctionRegistrationRejectedError](err)
				if !ok || rejected.ReasonCode != reason {
					t.Fatalf("start = %v", err)
				}
			}
		})
	}
}

func TestFunctionRegistrationRejectsMismatchedResponseIdentity(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"catalog_revision", "correlation", "causation"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			fixture := newFunctionFixture(t, false)
			started := make(chan error, 1)
			go func() {
				_, err := fixture.session.StartFunctionServer(
					testContext(t),
					[]Function{functionForTest(successfulFunction)},
				)
				started <- err
			}()
			message := receiveFunctionValue(t, fixture.registrations)
			request := fixture.registration(t, message)
			response := functionRegistrationResponse{Status: "accepted", CatalogRevision: request.ID}
			switch field {
			case "catalog_revision":
				response.CatalogRevision = mustID(t, "afc")
			case "correlation":
				request.CorrelationID = mustID(t, "cor")
			case "causation":
				request.ID = mustID(t, "afc")
			}
			respondTestLifecycle(t, fixture.session.validator, message, request.ID, request.CorrelationID,
				contractsv1.AdapterFunctionRegistrationResponseSchemaID, response)
			if err := receiveFunctionValue(t, started); err == nil {
				t.Fatal("mismatched registration accepted")
			}
		})
	}
}
