//go:build e2e

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/httpapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This test protects the Core-offline vertical slice from the Devices spec and
// fails if broker-acknowledged reports do not survive a Core restart exactly
// once, if heartbeat retries end the Session, or if the event source Entity
// reads synthetic State.
//
//nolint:gocognit,gocyclo,cyclop // The offline recovery sequence is clearer as one causal test.
func TestCoreOfflineEntityEventRecoveryVerticalSlice(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sessionLogger, sessionRecords := withRecording(slog.LevelDebug)
	client := newNonPoolingHTTPClient(t)
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	server := startCoreNATSServer(t)

	firstAddress, firstOptions := reserveLoopbackListener(t)
	firstContext, stopFirstCore := context.WithCancel(ctx)
	defer stopFirstCore()
	firstErrors := make(chan error, 1)
	go func() {
		firstErrors <- runWithOptions(firstContext, Config{
			HouseholdTimezone: "UTC", HTTPAddr: firstAddress,
			NATSURL: server.ClientURL(), SQLitePath: databasePath,
			Agent: requiredAgentConfig(t),
		}, slog.New(slog.DiscardHandler), firstOptions)
	}()
	waitForCoreHTTPStatus(ctx, t, client, firstAddress, "/healthz", firstErrors)

	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: "simulator", SoftwareName: "hearth-simulator",
		SoftwareVersion: "0.1.0", NATSURL: server.ClientURL(), Logger: sessionLogger,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := session.Close(); closeErr != nil && !errors.Is(closeErr, adapter.ErrRuntimeFenced) {
			t.Errorf("close simulator Session: %v", closeErr)
		}
	})
	scriptedRuntime, err := scripted.New(session, []scripted.DeviceSpec{
		sliceEntityEventsDevice("simulated-light", "Simulated light"),
	})
	if err != nil {
		t.Fatal(err)
	}
	registrations := scriptedRuntime.Registrations()
	bindings := make([]adapter.Binding, 0, len(registrations))
	for _, registration := range registrations {
		binding, registerErr := session.Register(ctx, registration)
		if registerErr != nil {
			t.Fatal(registerErr)
		}
		bindings = append(bindings, binding)
	}
	if attachErr := scriptedRuntime.Attach(bindings); attachErr != nil {
		t.Fatal(attachErr)
	}
	powerEntityID := bindingEntityID(t, bindings[0], "power")
	eventsEntityID := bindingEntityID(t, bindings[0], "events")
	if powerEntityID == eventsEntityID {
		t.Fatalf("registration reused one Entity ID for power and events: %s", powerEntityID)
	}
	if err = scriptedRuntime.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	waitForCoreHTTPStatus(ctx, t, client, firstAddress, "/readyz", firstErrors)

	stopFirstCore()
	select {
	case runErr := <-firstErrors:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("hearthd did not stop")
	}

	// Core is offline: a connected, registered Session stores reports in the
	// durable stream and returns JetStream acknowledgements only.
	expected := map[string]string{}
	for _, name := range []string{
		sliceEntityEventSinglePress,
		sliceEntityEventDoublePress,
		sliceEntityEventSinglePress,
	} {
		result, publishErr := scriptedRuntime.PublishNow(
			ctx, string(eventsEntityID), json.RawMessage(fmt.Sprintf(`{"name":%q}`, name)),
		)
		if publishErr != nil {
			t.Fatalf("offline PublishNow(%q): %v", name, publishErr)
		}
		eventID := result.EventID
		if eventID == "" {
			t.Fatalf("offline PublishNow(%q) returned no identity", name)
		}
		if _, duplicate := expected[string(eventID)]; duplicate {
			t.Fatalf("offline reports reused event ID %s", eventID)
		}
		expected[string(eventID)] = name
	}
	// Wake the heartbeat loop now instead of waiting for its periodic timer.
	// SetHealth waits for Core acknowledgement, so it completes after restart.
	healthErrors := make(chan error, 1)
	go func() {
		healthErrors <- session.SetHealth(ctx, adapter.HealthReport{
			Status: adapter.HealthHealthy, SourceObservedAt: time.Now().UTC(),
		})
	}()
	// Heartbeat retries must not end the Session: wait for more than one
	// missed heartbeat, then prove the Session still publishes.
	waitForMatrixCondition(t, 25*time.Second, func() (bool, error) {
		retries := 0
		for _, record := range recordsWithEvent(sessionRecords.snapshot(), "dependency.retrying") {
			if operation, ok := recordAttr(record, "operation"); ok && operation.String() == "heartbeat" {
				retries++
			}
		}
		return retries >= 2, nil
	})
	lateResult, err := scriptedRuntime.PublishNow(
		ctx, string(eventsEntityID),
		json.RawMessage(fmt.Sprintf(`{"name":%q}`, sliceEntityEventSinglePress)),
	)
	if err != nil {
		t.Fatalf("PublishNow after missed heartbeats: %v", err)
	}
	expected[string(lateResult.EventID)] = sliceEntityEventSinglePress
	if stopped := recordsWithEvent(sessionRecords.snapshot(), "adapter.heartbeat_stopped"); len(stopped) != 0 {
		t.Fatalf("missed heartbeats stopped the Session: %#v", stopped)
	}

	secondAddress, secondOptions := reserveLoopbackListener(t)
	secondContext, stopSecondCore := context.WithCancel(ctx)
	defer stopSecondCore()
	secondErrors := make(chan error, 1)
	go func() {
		secondErrors <- runWithOptions(secondContext, Config{
			HouseholdTimezone: "UTC", HTTPAddr: secondAddress,
			NATSURL: server.ClientURL(), SQLitePath: databasePath,
			Agent: requiredAgentConfig(t),
		}, slog.New(slog.DiscardHandler), secondOptions)
	}()
	waitForCoreHTTPStatus(ctx, t, client, secondAddress, "/healthz", secondErrors)
	waitForCoreHTTPStatus(ctx, t, client, secondAddress, "/readyz", secondErrors)
	select {
	case healthErr := <-healthErrors:
		if healthErr != nil {
			t.Fatalf("SetHealth after Core restart: %v", healthErr)
		}
	case <-ctx.Done():
		t.Fatal("heartbeat was not acknowledged after Core restart")
	}

	// Every broker-acknowledged report is recorded exactly once after restart.
	var collection devicesapi.EntityEventCollectionBody
	waitForMatrixCondition(t, 20*time.Second, func() (bool, error) {
		collection = getEntityEvents(ctx, t, client, secondAddress, string(eventsEntityID))
		return len(collection.Items) == len(expected), nil
	})
	recorded := map[string]devicesapi.EntityEventBody{}
	for _, item := range collection.Items {
		if _, duplicate := recorded[item.EventID]; duplicate {
			t.Fatalf("event %s was recorded more than once", item.EventID)
		}
		recorded[item.EventID] = item
	}
	for eventID, wantName := range expected {
		item, ok := recorded[eventID]
		if !ok {
			t.Fatalf("event %s was not recorded", eventID)
		}
		if item.Name != wantName || item.EntityID != string(eventsEntityID) ||
			item.Disposition != string(devices.EntityEventDispositionAccepted) || item.RejectionCode != nil {
			t.Fatalf("recorded event = %#v", item)
		}
	}
	// Events are not State: the event source Entity still reads state null.
	assertEventSourceStateIsNull(ctx, t, client, secondAddress, string(eventsEntityID))

	stopSecondCore()
	select {
	case runErr := <-secondErrors:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("hearthd did not stop after restart")
	}
}

func bindingEntityID(t *testing.T, binding adapter.Binding, key string) devices.EntityID {
	t.Helper()
	for _, entity := range binding.Entities {
		if entity.Key == key {
			entityID, err := devices.ParseEntityID(entity.EntityID)
			if err != nil {
				t.Fatal(err)
			}
			return entityID
		}
	}
	t.Fatalf("registration response omitted Entity key %q", key)
	return ""
}

func getEntityEvents(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	baseURL, entityID string,
) devicesapi.EntityEventCollectionBody {
	t.Helper()
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, "http://"+baseURL+"/v1/entities/"+entityID+"/events?limit=50", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		return devicesapi.EntityEventCollectionBody{}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("event history status = %d, body = %s", response.StatusCode, body)
	}
	var collection devicesapi.EntityEventCollectionBody
	if decodeErr := json.Unmarshal(body, &collection); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	return collection
}

func assertEventSourceStateIsNull(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	baseURL, entityID string,
) {
	t.Helper()
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, "http://"+baseURL+"/v1/entities/"+entityID, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Type  string          `json:"type"`
		State json.RawMessage `json:"state"`
	}
	if decodeErr := json.Unmarshal(body, &view); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if view.Type != "hearth.enumevent/v1" || string(view.State) != "null" {
		t.Fatalf("event source Entity read = %s", body)
	}
}
