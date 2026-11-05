//go:build e2e

package hearthd //nolint:testpackage // Exercises real Core, real SDK, and the simulator app end to end.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	simulatorapp "github.com/mholtzscher/hearth/internal/app/simulator"
	adaptersapi "github.com/mholtzscher/hearth/internal/modules/adapters/httpapi"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

const simulatorFunctionAdapterID = "sim-functions"

// This test proves the simulator can validate the Adapter Function boundary
// without a vendor adapter: real Core discovers the scripted catalog, routes
// invocations, retains exact arguments and results, and records every terminal
// status. It also proves a held invocation reports running and finishes after a
// control release.
func TestSimulatorAdapterFunctionsEndToEnd(t *testing.T) {
	t.Parallel()
	broker := startLifecycleNATSServer(t)
	core := startFunctionCore(t, Config{
		HouseholdTimezone: "UTC",
		NATSURL:           broker.ClientURL(),
		SQLitePath:        dbtest.MigratedPath(t),
		Agent:             requiredAgentConfig(t),
	})
	client := newNonPoolingHTTPClient(t)

	controlAddr := freeSimulatorControlAddr(t)
	specs := []scripted.FunctionSpec{
		{
			Key: "inspect", Description: "Echo the invocation arguments as the successful result.",
			InputSchema:   map[string]any{"type": "object", "additionalProperties": true},
			OutputSchema:  map[string]any{"type": "object", "additionalProperties": true},
			EchoArguments: true,
		},
		{
			Key: "fail", Description: "Return a failed terminal outcome.",
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
			Outcome:      scripted.FunctionOutcomeFailed, ReasonCode: "simulated_failure",
		},
		{
			Key: "not-executed", Description: "Return a not-executed terminal outcome.",
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
			Outcome:      scripted.FunctionOutcomeNotExecuted, ReasonCode: "simulated_unavailable",
		},
		{
			Key: "unknown", Description: "Return an unknown terminal outcome.",
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
			Outcome:      scripted.FunctionOutcomeUnknown, ReasonCode: "simulated_unknown",
		},
		{
			Key: "hold", Description: "Stay running until the control channel releases it.",
			TimeoutMS:    30000,
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
			Hold:         true,
		},
	}
	simulator := startSimulatorApp(t, simulatorapp.Config{
		NATSURL:     broker.ClientURL(),
		ControlAddr: controlAddr,
		Adapters: []simulatorapp.ScriptedAdapterConfig{{
			AdapterID: simulatorFunctionAdapterID,
			Functions: specs,
		}},
	})

	revision := waitForSimulatorCatalog(t, client, core, len(specs))
	// Catalog registration precedes the control listener, so discovery alone
	// does not establish that control requests can connect yet.
	waitForSimulatorControlReady(t, client, simulator)
	inspect := invokeSimulatorFunction(t, client, core, revision, "inspect", functionExactObject)
	assertSimulatorInvocation(t, inspect, simulatorInvocationWant{
		id: inspect.ID, key: "inspect", revision: revision, status: "succeeded",
		arguments: functionExactObject, result: functionExactObject,
	})
	for key, want := range map[string]struct{ status, reason string }{
		"fail":         {"failed", "simulated_failure"},
		"not-executed": {"not_executed", "simulated_unavailable"},
		"unknown":      {"unknown", "simulated_unknown"},
	} {
		invocation := invokeSimulatorFunction(t, client, core, revision, key, `{}`)
		expected := simulatorInvocationWant{
			id: invocation.ID, key: key, revision: revision, status: want.status,
			reason: want.reason, arguments: `{}`,
		}
		assertSimulatorInvocation(t, invocation, expected)
		// The response and retained history must agree on the exact outcome.
		assertSimulatorInvocation(t, retainedSimulatorInvocation(t, client, core, invocation.ID), expected)
	}

	invokeCtx, cancelInvoke := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancelInvoke()
	held := make(chan simulatorInvocationResult, 1)
	go func() {
		status, raw, err := functionNetworkRequest(
			invokeCtx,
			client,
			core.address,
			http.MethodPost,
			"/v1/adapters/"+simulatorFunctionAdapterID+"/functions/hold/invocations",
			`{"catalog_revision":`+strconv.Quote(revision)+`,"arguments":{}}`,
		)
		held <- simulatorInvocationResult{status: status, raw: raw, err: err}
	}()
	var invocationID string
	waitForMatrixCondition(t, 15*time.Second, func() (bool, error) {
		status, raw, err := functionNetworkRequest(
			t.Context(),
			client,
			core.address,
			http.MethodGet,
			"/v1/adapters/"+simulatorFunctionAdapterID+"/function-invocations?function_key=hold&limit=50",
			"",
		)
		if err != nil || status != http.StatusOK {
			return false, err
		}
		page := decodeNotification[adaptersapi.InvocationCollectionBody](t, raw)
		for _, item := range page.Items {
			if item.Status == "running" {
				invocationID = item.ID
				return true, nil
			}
		}
		return false, nil
	})
	if invocationID == "" {
		t.Fatal("Core never exposed a running held invocation")
	}
	// Core commits running before dispatch reaches the adapter. Wait for this
	// exact invocation to become releasable, then issue one release instead of
	// mutating the control request with 404 retries.
	waitForSimulatorPendingInvocation(t, client, simulator, invocationID)
	releaseStatus, releaseRaw, releaseErr := functionNetworkRequest(
		t.Context(),
		client,
		simulator.controlAddr,
		http.MethodPost,
		"/v1/sim/functions/invocations/"+invocationID+"/release",
		"",
	)
	if releaseErr != nil || releaseStatus != http.StatusNoContent {
		t.Fatalf("release held invocation: %d %s %v", releaseStatus, releaseRaw, releaseErr)
	}
	expected := simulatorInvocationWant{
		id: invocationID, key: "hold", revision: revision, status: "succeeded",
		arguments: `{}`, result: `{}`,
	}
	select {
	case result := <-held:
		if result.err != nil || result.status != http.StatusOK {
			t.Fatalf("held invocation finished as %d %s %v", result.status, result.raw, result.err)
		}
		assertSimulatorInvocation(t, decodeNotification[adaptersapi.InvocationBody](t, result.raw), expected)
	case <-invokeCtx.Done():
		t.Fatal("held invocation never finished after release")
	}
	assertSimulatorInvocation(t, retainedSimulatorInvocation(t, client, core, invocationID), expected)
	// Join the simulator while Core keeps running: the process would otherwise
	// release its SDK Session concurrently with Core's test-context teardown.
	if err := simulator.stopAndJoin(20 * time.Second); err != nil {
		t.Fatalf("simulator shutdown: %v", err)
	}
}

// A held handler must not block simulator shutdown. The app cancels the worker
// context before rejoining the function server, so shutdown finishes inside a
// two-second budget while the handler's ten-second deadline is still ahead.
func TestSimulatorFunctionHoldJoinsShutdown(t *testing.T) {
	t.Parallel()
	broker := startLifecycleNATSServer(t)
	core := startFunctionCore(t, Config{
		HouseholdTimezone: "UTC",
		NATSURL:           broker.ClientURL(),
		SQLitePath:        dbtest.MigratedPath(t),
		Agent:             requiredAgentConfig(t),
	})
	client := newNonPoolingHTTPClient(t)
	controlAddr := freeSimulatorControlAddr(t)
	simConfig := simulatorapp.Config{
		NATSURL:     broker.ClientURL(),
		ControlAddr: controlAddr,
		Adapters: []simulatorapp.ScriptedAdapterConfig{{
			AdapterID: simulatorFunctionAdapterID,
			Functions: []scripted.FunctionSpec{{
				Key: "hold", Description: "Hold until shutdown",
				TimeoutMS:    10000,
				InputSchema:  map[string]any{"type": "object"},
				OutputSchema: map[string]any{"type": "object"},
				Hold:         true,
			}},
		}},
	}
	simulator := launchSimulator(t, simConfig)
	var stopOnce sync.Once
	var stopErr error
	stopAndWait := func() error {
		stopOnce.Do(func() {
			simulator.stop()
			select {
			case <-simulator.done:
				stopErr = simulator.err
			case <-time.After(2 * time.Second):
				stopErr = errors.New("simulator shutdown did not join a held function handler")
			}
		})
		return stopErr
	}
	t.Cleanup(func() {
		if err := stopAndWait(); err != nil {
			t.Errorf("simulator shutdown: %v", err)
		}
	})
	revision := waitForSimulatorCatalog(t, client, core, 1)
	// Catalog registration precedes the control listener, so discovery alone
	// does not establish that control requests can connect yet.
	waitForSimulatorControlReady(t, client, simulator)
	invokeCtx, cancelInvoke := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancelInvoke()
	held := make(chan simulatorInvocationResult, 1)
	go func() {
		status, raw, err := functionNetworkRequest(
			invokeCtx,
			client,
			core.address,
			http.MethodPost,
			"/v1/adapters/"+simulatorFunctionAdapterID+"/functions/hold/invocations",
			`{"catalog_revision":`+strconv.Quote(revision)+`,"arguments":{}}`,
		)
		held <- simulatorInvocationResult{status: status, raw: raw, err: err}
	}()
	var invocationID string
	waitForMatrixCondition(t, 15*time.Second, func() (bool, error) {
		status, raw, err := functionNetworkRequest(
			t.Context(),
			client,
			core.address,
			http.MethodGet,
			"/v1/adapters/"+simulatorFunctionAdapterID+"/function-invocations?function_key=hold&limit=50",
			"",
		)
		if err != nil || status != http.StatusOK {
			return false, err
		}
		page := decodeNotification[adaptersapi.InvocationCollectionBody](t, raw)
		for _, item := range page.Items {
			if item.Status == "running" {
				invocationID = item.ID
				return true, nil
			}
		}
		return false, nil
	})
	if invocationID == "" {
		t.Fatal("Core never exposed a running held invocation")
	}
	// Core commits running before dispatch reaches the adapter. Wait for the
	// simulator to hold this exact invocation so shutdown exercises a real
	// admitted handler rather than an empty server.
	waitForSimulatorPendingInvocation(t, client, simulator, invocationID)
	if err := stopAndWait(); err != nil {
		t.Fatal(err)
	}
	// The handler's ten-second deadline is still ahead: shutdown must join by
	// stopping holds with the adapter's documented stopping outcome.
	var result simulatorInvocationResult
	select {
	case result = <-held:
	case <-time.After(10 * time.Second):
		cancelInvoke()
		t.Fatal("held invocation request did not return after simulator shutdown")
	}
	if result.err != nil || result.status != http.StatusOK {
		t.Fatalf("held invocation finished as %d %s %v", result.status, result.raw, result.err)
	}
	expected := simulatorInvocationWant{
		id: invocationID, key: "hold", revision: revision, status: "unknown",
		reason: "adapter_stopping", arguments: `{}`,
	}
	assertSimulatorInvocation(t, decodeNotification[adaptersapi.InvocationBody](t, result.raw), expected)
	// Core, not the departing simulator, retains the authoritative outcome.
	assertSimulatorInvocation(t, retainedSimulatorInvocation(t, client, core, invocationID), expected)
}

type simulatorInvocationResult struct {
	status int
	raw    []byte
	err    error
}

type simulatorRun struct {
	controlAddr string
	stop        context.CancelFunc
	done        chan struct{}
	err         error
}

// stopAndJoin is safe from both the successful body and cleanup because
// [context.CancelFunc] is idempotent.
func (run *simulatorRun) stopAndJoin(timeout time.Duration) error {
	run.stop()
	select {
	case <-run.done:
		return run.err
	case <-time.After(timeout):
		return errors.New("simulator did not join")
	}
}

// launchSimulator starts the process without owning its shutdown, so a test
// can join it under a scenario-specific deadline.
func launchSimulator(t *testing.T, config simulatorapp.Config) *simulatorRun {
	t.Helper()
	if err := config.Validate(); err != nil {
		t.Fatalf("simulator config: %v", err)
	}
	ctx, stop := context.WithCancel(t.Context())
	run := &simulatorRun{controlAddr: config.ControlAddr, stop: stop, done: make(chan struct{})}
	go func() {
		run.err = simulatorapp.Run(ctx, config, slog.New(slog.DiscardHandler))
		close(run.done)
	}()
	return run
}

func startSimulatorApp(t *testing.T, config simulatorapp.Config) *simulatorRun {
	t.Helper()
	run := launchSimulator(t, config)
	t.Cleanup(func() {
		if err := run.stopAndJoin(20 * time.Second); err != nil {
			t.Errorf("simulator shutdown: %v", err)
		}
	})
	return run
}

// waitForSimulatorControl polls the loopback control listener until observe
// accepts a response. Core can expose the registered catalog before the control
// listener starts, so connection failures are expected during startup. A
// simulator exit fails the wait immediately, and the last response is reported
// if the bound expires.
func waitForSimulatorControl(
	t *testing.T,
	client *http.Client,
	run *simulatorRun,
	description string,
	observe func(status int, raw []byte, err error) bool,
) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	var (
		lastStatus int
		lastRaw    []byte
		lastErr    error
	)
	fail := func(reason error) {
		t.Fatalf(
			"waiting for %s: %v (last control response %d %s %v)",
			description, reason, lastStatus, lastRaw, lastErr,
		)
	}
	for {
		select {
		case <-run.done:
			t.Fatalf(
				"simulator exited before %s: %v (last control response %d %s %v)",
				description, run.err, lastStatus, lastRaw, lastErr,
			)
		case <-waitCtx.Done():
			fail(waitCtx.Err())
		default:
		}
		status, raw, err := functionNetworkRequest(
			waitCtx,
			client,
			run.controlAddr,
			http.MethodGet,
			"/v1/sim/functions/invocations",
			"",
		)
		if observe(status, raw, err) {
			return
		}
		lastStatus, lastRaw, lastErr = status, raw, err
		select {
		case <-waitCtx.Done():
			fail(waitCtx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForSimulatorControlReady(t *testing.T, client *http.Client, run *simulatorRun) {
	t.Helper()
	waitForSimulatorControl(
		t,
		client,
		run,
		"simulator control channel readiness",
		func(status int, _ []byte, err error) bool {
			return err == nil && status == http.StatusOK
		},
	)
}

type simulatorPendingFunction struct {
	AdapterID    string `json:"adapter_id"`
	InvocationID string `json:"invocation_id"`
}

func waitForSimulatorPendingInvocation(
	t *testing.T,
	client *http.Client,
	run *simulatorRun,
	invocationID string,
) {
	t.Helper()
	waitForSimulatorControl(
		t,
		client,
		run,
		"held invocation "+invocationID+" to be pending",
		func(status int, raw []byte, err error) bool {
			if err != nil || status != http.StatusOK {
				return false
			}
			var pending []simulatorPendingFunction
			if decodeErr := json.Unmarshal(raw, &pending); decodeErr != nil {
				return false
			}
			// The scenario holds one invocation; asserting exactly that entry
			// proves the announced ID is the one shutdown will exercise.
			return len(pending) == 1 &&
				pending[0].AdapterID == simulatorFunctionAdapterID &&
				pending[0].InvocationID == invocationID
		},
	)
}

type simulatorInvocationWant struct {
	id, key, revision, status, reason, arguments, result string
}

func assertSimulatorInvocation(
	t *testing.T,
	invocation adaptersapi.InvocationBody,
	want simulatorInvocationWant,
) {
	t.Helper()
	if invocation.ID != want.id || invocation.AdapterID != simulatorFunctionAdapterID ||
		invocation.FunctionKey != want.key || invocation.CatalogRevision != want.revision ||
		string(invocation.Arguments) != want.arguments || invocation.Status != want.status ||
		invocation.ReasonCode != want.reason || string(invocation.Result) != want.result ||
		invocation.CompletedAt == nil {
		t.Fatalf("%s invocation = %+v, want %+v", want.key, invocation, want)
	}
}

func retainedSimulatorInvocation(
	t *testing.T,
	client *http.Client,
	core *functionCoreRun,
	invocationID string,
) adaptersapi.InvocationBody {
	t.Helper()
	return decodeNotification[adaptersapi.InvocationBody](
		t,
		successfulFunctionCoreRequest(
			t,
			client,
			core,
			http.MethodGet,
			"/v1/adapter-function-invocations/"+invocationID,
			"",
		),
	)
}

func waitForSimulatorCatalog(
	t *testing.T,
	client *http.Client,
	core *functionCoreRun,
	want int,
) string {
	t.Helper()
	revision := ""
	waitForMatrixCondition(t, 15*time.Second, func() (bool, error) {
		status, raw, err := functionNetworkRequest(
			t.Context(),
			client,
			core.address,
			http.MethodGet,
			"/v1/adapters/"+simulatorFunctionAdapterID+"/functions?limit="+strconv.Itoa(want),
			"",
		)
		if err != nil || status != http.StatusOK {
			return false, err
		}
		page := decodeNotification[adaptersapi.FunctionCollectionBody](t, raw)
		if len(page.Items) != want {
			return false, nil
		}
		revision = page.Items[0].CatalogRevision
		for _, item := range page.Items {
			if !item.Callable || item.CatalogRevision != revision {
				return false, nil
			}
		}
		return true, nil
	})
	if revision == "" {
		t.Fatal("simulator catalog has no revision")
	}
	return revision
}

func invokeSimulatorFunction(
	t *testing.T,
	client *http.Client,
	core *functionCoreRun,
	revision, key, arguments string,
) adaptersapi.InvocationBody {
	t.Helper()
	status, raw, err := functionNetworkRequest(
		t.Context(),
		client,
		core.address,
		http.MethodPost,
		"/v1/adapters/"+simulatorFunctionAdapterID+"/functions/"+key+"/invocations",
		`{"catalog_revision":`+strconv.Quote(revision)+`,"arguments":`+arguments+`}`,
	)
	if err != nil || status != http.StatusOK {
		t.Fatalf("invoke %s: %d %s %v", key, status, raw, err)
	}
	return decodeNotification[adaptersapi.InvocationBody](t, raw)
}

func freeSimulatorControlAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if closeErr := listener.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	return address
}
