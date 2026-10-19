package hearthd //nolint:testpackage // Exercises real Core, real SDK, and the simulator app end to end.

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	simulatorapp "github.com/mholtzscher/hearth/internal/app/simulator"
	adaptersapi "github.com/mholtzscher/hearth/internal/modules/adapters/api"
)

const simulatorFunctionAdapterID = "sim-functions"

// This test proves the simulator can validate the Adapter Function boundary
// without a vendor adapter: real Core discovers the scripted catalog, routes
// invocations, retains exact arguments and results, and records every terminal
// status. It also proves a held invocation reports running and finishes after a
// control release.
//
//nolint:gocognit // One end-to-end scenario keeps discovery, every status, and held release causally connected.
func TestSimulatorAdapterFunctionsEndToEnd(t *testing.T) {
	t.Parallel()
	broker := startLifecycleNATSServer(t)
	core := startFunctionCore(t, Config{
		HouseholdTimezone: "UTC",
		NATSURL:           broker.ClientURL(),
		SQLitePath:        filepath.Join(t.TempDir(), "core.db"),
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
	startSimulatorApp(t, simulatorapp.Config{
		NATSURL:     broker.ClientURL(),
		ControlAddr: controlAddr,
		Adapters: []simulatorapp.ScriptedAdapterConfig{{
			AdapterID: simulatorFunctionAdapterID,
			Functions: specs,
		}},
	})

	revision := waitForSimulatorCatalog(t, client, core, len(specs))
	if invocation := invokeSimulatorFunction(
		t,
		client,
		core,
		revision,
		"inspect",
		functionExactObject,
	); invocation.Status != "succeeded" ||
		string(invocation.Arguments) != functionExactObject ||
		string(invocation.Result) != functionExactObject {
		t.Fatalf("echo invocation = %+v", invocation)
	}
	for key, want := range map[string]struct{ status, reason string }{
		"fail":         {"failed", "simulated_failure"},
		"not-executed": {"not_executed", "simulated_unavailable"},
		"unknown":      {"unknown", "simulated_unknown"},
	} {
		invocation := invokeSimulatorFunction(t, client, core, revision, key, `{}`)
		if invocation.Status != want.status || invocation.ReasonCode != want.reason {
			t.Fatalf("%s invocation = %+v", key, invocation)
		}
	}

	invokeCtx, cancelInvoke := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancelInvoke()
	type heldResult struct {
		status int
		raw    []byte
		err    error
	}
	held := make(chan heldResult, 1)
	go func() {
		status, raw, err := functionNetworkRequest(
			invokeCtx,
			client,
			core.address,
			http.MethodPost,
			"/v1/adapters/"+simulatorFunctionAdapterID+"/functions/hold/invocations",
			`{"catalog_revision":`+strconv.Quote(revision)+`,"arguments":{}}`,
		)
		held <- heldResult{status, raw, err}
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
	var (
		releaseStatus int
		releaseRaw    []byte
		releaseErr    error
	)
	released := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		releaseStatus, releaseRaw, releaseErr = functionNetworkRequest(
			t.Context(),
			client,
			controlAddr,
			http.MethodPost,
			"/v1/sim/functions/invocations/"+invocationID+"/release",
			"",
		)
		if releaseErr != nil {
			t.Fatalf("release held invocation: %v", releaseErr)
		}
		switch releaseStatus {
		case http.StatusNoContent:
			released = true
		case http.StatusNotFound:
			// Core commits admission before dispatch, so running can precede
			// the adapter's releasable hold by a short scheduling window.
			time.Sleep(10 * time.Millisecond)
		default:
			t.Fatalf("release held invocation: %d %s", releaseStatus, releaseRaw)
		}
		if released {
			break
		}
	}
	if !released {
		t.Fatalf("held invocation %s was never admitted for release: %d %s", invocationID, releaseStatus, releaseRaw)
	}
	select {
	case result := <-held:
		if result.err != nil || result.status != http.StatusOK {
			t.Fatalf("held invocation finished as %d %s %v", result.status, result.raw, result.err)
		}
		invocation := decodeNotification[adaptersapi.InvocationBody](t, result.raw)
		if invocation.ID != invocationID || invocation.Status != "succeeded" || invocation.CompletedAt == nil {
			t.Fatalf("released invocation = %+v", invocation)
		}
	case <-invokeCtx.Done():
		t.Fatal("held invocation never finished after release")
	}
	retained := decodeNotification[adaptersapi.InvocationBody](
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
	if retained.ID != invocationID || retained.Status != "succeeded" || retained.ReasonCode != "" {
		t.Fatalf("retained invocation = %+v", retained)
	}
}

// A held handler must not block simulator shutdown. The app cancels the worker
// context before rejoining the function server, so shutdown finishes well before
// the configured 30-second deadline.
func TestSimulatorFunctionHoldJoinsShutdown(t *testing.T) {
	t.Parallel()
	broker := startLifecycleNATSServer(t)
	core := startFunctionCore(t, Config{
		HouseholdTimezone: "UTC",
		NATSURL:           broker.ClientURL(),
		SQLitePath:        filepath.Join(t.TempDir(), "core.db"),
		Agent:             requiredAgentConfig(t),
	})
	client := newNonPoolingHTTPClient(t)
	simConfig := simulatorapp.Config{
		NATSURL: broker.ClientURL(),
		Adapters: []simulatorapp.ScriptedAdapterConfig{{
			AdapterID: simulatorFunctionAdapterID,
			Functions: []scripted.FunctionSpec{{
				Key: "hold", Description: "Hold until shutdown",
				TimeoutMS:    30000,
				InputSchema:  map[string]any{"type": "object"},
				OutputSchema: map[string]any{"type": "object"},
				Hold:         true,
			}},
		}},
	}
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- simulatorapp.Run(ctx, simConfig, slog.New(slog.DiscardHandler))
	}()
	var stopOnce sync.Once
	var stopErr error
	stopAndWait := func() error {
		stopOnce.Do(func() {
			stop()
			select {
			case stopErr = <-done:
			case <-time.After(10 * time.Second):
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
	invokeCtx, cancelInvoke := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancelInvoke()
	heldDone := make(chan struct{})
	go func() {
		_, _, _ = functionNetworkRequest(
			invokeCtx,
			client,
			core.address,
			http.MethodPost,
			"/v1/adapters/"+simulatorFunctionAdapterID+"/functions/hold/invocations",
			`{"catalog_revision":`+strconv.Quote(revision)+`,"arguments":{}}`,
		)
		close(heldDone)
	}()
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
				return true, nil
			}
		}
		return false, nil
	})
	if err := stopAndWait(); err != nil {
		t.Fatal(err)
	}
	cancelInvoke()
	select {
	case <-heldDone:
	case <-time.After(10 * time.Second):
		t.Fatal("held invocation request did not return after simulator shutdown")
	}
}

func startSimulatorApp(t *testing.T, config simulatorapp.Config) {
	t.Helper()
	if err := config.Validate(); err != nil {
		t.Fatalf("simulator config: %v", err)
	}
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- simulatorapp.Run(ctx, config, slog.New(slog.DiscardHandler))
	}()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("simulator shutdown: %v", err)
			}
		case <-time.After(20 * time.Second):
			t.Error("simulator did not join")
		}
	})
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
