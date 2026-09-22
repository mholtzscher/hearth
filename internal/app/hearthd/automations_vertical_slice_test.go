package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	sliceAdapterID = "simulator"
	// sliceEntityEventSinglePress and sliceEntityEventDoublePress are the
	// synthetic Entity Event names the slice Devices advertise in support and
	// tests report.
	sliceEntityEventSinglePress = "single_press"
	sliceEntityEventDoublePress = "double_press"
)

// TestAutomationFactDrivesCommandThroughCore protects A14's observation family
// at the app boundary: one real SDK adapter publishes an accepted Observation,
// the relay publishes its Device Fact, the runtime's automation consumer admits
// it against an HTTP-created Automation, the Run executes one typed Command, and
// API history explains the immutable matched Trigger, Fact evidence, ordered
// Step, and verified Command link. It fails if any producer→broker→admission→
// Command seam is disconnected, which no single package test can detect.
func TestAutomationFactDrivesCommandThroughCore(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	httpAddress := unusedLoopbackAddress(t)

	runContext, stopCore := context.WithCancel(ctx)
	defer stopCore()
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{HouseholdTimezone: "UTC",
			HTTPAddr: httpAddress, NATSURL: server.ClientURL(),
			SQLitePath: filepath.Join(t.TempDir(), "hearth.db"),
			Agent:      requiredAgentConfig(t),
		}, slog.New(slog.DiscardHandler))
	}()
	waitForCoreHealthz(ctx, t, httpAddress, runErrors)

	entityAdapter := startSliceAdapter(ctx, t, server, httpAddress)
	defer entityAdapter.stop()

	automationID := createObservationAutomation(ctx, t, httpAddress, entityAdapter.powerEntityID)
	if err := entityAdapter.publish(ctx); err != nil {
		t.Fatal(err)
	}

	runID := waitForAutomationRun(ctx, t, httpAddress, automationID)
	assertAutomationRunCommandLink(ctx, t, httpAddress, automationID, runID, entityAdapter.powerEntityID)
}

// sliceAdapter is one running simulator adapter bound to Core: its registered
// power and event-source Entity identities, the live Session and simulator used
// to publish trigger evidence, and a stop that joins the command subscription.
// Both fact families share one adapter because A14 proves the same
// producer→broker→admission→Command path for an Observation and an Entity Event.
type sliceAdapter struct {
	powerEntityID string
	eventEntityID string
	session       *adapter.Session
	runtime       *scripted.Runtime
	stop          func()
}

// publish sends one accepted Observation for the power Entity with value true,
// which the Observation Trigger matches.
func (slice sliceAdapter) publish(ctx context.Context) error {
	_, err := slice.session.PublishObservation(ctx, adapter.Observation{
		EntityID: slice.powerEntityID, Value: json.RawMessage("true"),
		AdapterReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	return err
}

// emit sends one accepted Entity Event for the event-source Entity, which the
// Entity Event Trigger matches.
func (slice sliceAdapter) emit(ctx context.Context, name string) error {
	_, err := slice.runtime.PublishNow(
		ctx, slice.eventEntityID, json.RawMessage(fmt.Sprintf(`{"name":%q}`, name)),
	)
	return err
}

// sliceEntityEventsDevice builds the scripted Device every adapter in these
// slices registers: a stateful Power Entity that starts false and answers set
// with the scripted default accept-and-publish behavior, plus an event-source
// Entity advertising the synthetic single_press and double_press reports.
func sliceEntityEventsDevice(bindingKey, name string) scripted.DeviceSpec {
	return scripted.DeviceSpec{
		BindingKey: bindingKey,
		Name:       name,
		Kind:       "light",
		Entities: []scripted.EntitySpec{
			{
				Key:  "power",
				Name: "Power",
				Type: "hearth.power/v1",
				Support: map[string]any{
					"state":      map[string]any{},
					"operations": map[string]any{"set": map[string]any{}},
				},
				Initial: false,
			},
			{
				Key:  "events",
				Name: "Events",
				Type: "hearth.enumevent/v1",
				Support: map[string]any{
					"state":      map[string]any{},
					"operations": map[string]any{},
					"events": map[string]any{
						"names": []any{sliceEntityEventSinglePress, sliceEntityEventDoublePress},
					},
				},
			},
		},
	}
}

// startSliceAdapter wires one scripted adapter to the running Core: it registers
// a stateful power Entity and an event-source Entity, initializes their health
// and availability, and serves Commands for the power Entity. The Device script
// supplies both the power support and the synthetic single_press/double_press
// event names, so one adapter proves both fact families. The returned stop
// function joins the command subscription.
func startSliceAdapter(
	ctx context.Context,
	t *testing.T,
	server *natsserver.Server,
	httpAddress string,
) sliceAdapter {
	t.Helper()
	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: sliceAdapterID, SoftwareName: "hearth-automation-slice",
		SoftwareVersion: "0.1.0", NATSURL: server.ClientURL(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	scriptedRuntime, err := scripted.New(session, []scripted.DeviceSpec{
		sliceEntityEventsDevice("office-light", "Office light"),
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
	powerEntityID := string(bindingEntityID(t, bindings[0], "power"))
	eventEntityID := string(bindingEntityID(t, bindings[0], "events"))
	if powerEntityID == eventEntityID {
		t.Fatalf("registration reused one Entity ID for power and events: %s", powerEntityID)
	}
	if initializeErr := scriptedRuntime.Initialize(ctx); initializeErr != nil {
		t.Fatal(initializeErr)
	}
	serveContext, stopServe := context.WithCancel(ctx)
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = session.ServeCommands(serveContext, scriptedRuntime.CommandHandler())
	}()
	// The command subject needs the Adapter runtime identity Core assigned, so
	// the barrier reads it from the running Core instead of guessing.
	runtimeID := waitForAdapterRuntime(ctx, t, httpAddress, sliceAdapterID)
	waitForCommandSubscription(t, server, sliceAdapterID, runtimeID, powerEntityID)
	return sliceAdapter{
		powerEntityID: powerEntityID,
		eventEntityID: eventEntityID,
		session:       session,
		runtime:       scriptedRuntime,
		stop: func() {
			stopServe()
			<-served
		},
	}
}

// createObservationAutomation POSTs one enabled Automation whose Observation
// Trigger matches an applied Observation of the Entity with value true and whose
// single Step sets the same Entity to false. The value comparison is
// deliberately part of the Trigger: the Command's own observation reports
// false, so it can never re-trigger the Automation and loop.
func createObservationAutomation(
	ctx context.Context,
	t *testing.T,
	httpAddress string,
	entityID string,
) string {
	t.Helper()
	definition := fmt.Sprintf(`{
		"name": "Office light off on activity",
		"enabled": true,
		"triggers": [{"id":"activity","kind":"observation","entity_id":%q,
			"dispositions":["applied"],
			"comparisons":[{"value_pointer":"","operator":"eq","operand":true}]}],
		"steps": [{"id":"turn_off","entity_id":%q,"operation":"set","parameters":{"value":false}}]
	}`, entityID, entityID)
	response := sliceRequest(ctx, t, http.MethodPost, httpAddress, "/v1/automations",
		bytes.NewBufferString(definition))
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create automation status = %d, body = %s",
			response.StatusCode, readSliceBody(t, response))
	}
	var created struct {
		ID         string `json:"id"`
		Definition struct {
			Name string `json:"name"`
		} `json:"definition"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Definition.Name == "" {
		t.Fatalf("created automation = %#v", created)
	}
	return created.ID
}

// waitForAutomationRun polls history until the fact-driven Run appears and is
// terminal, proving the Run worker finished its Step before the assertions.
func waitForAutomationRun(ctx context.Context, t *testing.T, httpAddress, automationID string) string {
	t.Helper()
	var entryID string
	waitForMatrixCondition(t, 20*time.Second, func() (bool, error) {
		response := sliceRequest(ctx,
			t, http.MethodGet, httpAddress, "/v1/automations/"+automationID+"/history", nil)
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return false, fmt.Errorf("history status = %d", response.StatusCode)
		}
		var page struct {
			Items []struct {
				ID     string `json:"id"`
				Kind   string `json:"kind"`
				Status string `json:"status"`
			} `json:"items"`
		}
		if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
			return false, err
		}
		for _, item := range page.Items {
			if item.Kind == "run" && item.Status != "running" {
				entryID = item.ID
				return true, nil
			}
		}
		return false, nil
	})
	if entryID == "" {
		t.Fatal("automation run never reached a terminal status")
	}
	return entryID
}

// assertAutomationRunCommandLink proves the retained history explains the
// device-fact provenance, the immutable matched Trigger, the ordered Step, and a
// verified Command identity that names a real terminal Command.
func assertAutomationRunCommandLink(
	ctx context.Context,
	t *testing.T,
	httpAddress, automationID, runID, powerEntityID string,
) {
	t.Helper()
	response := sliceRequest(ctx, t, http.MethodGet, httpAddress,
		"/v1/automations/"+automationID+"/history/"+runID, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("history entry status = %d, body = %s",
			response.StatusCode, readSliceBody(t, response))
	}
	var entry struct {
		Kind string `json:"kind"`
		Run  *struct {
			Source            string   `json:"source"`
			Status            string   `json:"status"`
			MatchedTriggerIDs []string `json:"matched_trigger_ids"`
			Fact              *struct {
				Family      string `json:"family"`
				Variant     string `json:"variant"`
				CausationID string `json:"causation_id"`
			} `json:"fact"`
			Steps []struct {
				StepID            string  `json:"step_id"`
				Status            string  `json:"status"`
				VerifiedCommandID *string `json:"verified_command_id"`
			} `json:"steps"`
		} `json:"run"`
	}
	if err := json.NewDecoder(response.Body).Decode(&entry); err != nil {
		t.Fatal(err)
	}
	if entry.Kind != "run" || entry.Run == nil {
		t.Fatalf("history entry = %#v", entry)
	}
	if entry.Run.Source != "device_fact" || entry.Run.Status != "succeeded" {
		t.Fatalf("run source/status = %q/%q", entry.Run.Source, entry.Run.Status)
	}
	if len(entry.Run.MatchedTriggerIDs) != 1 || entry.Run.MatchedTriggerIDs[0] != "activity" {
		t.Fatalf("matched trigger IDs = %v", entry.Run.MatchedTriggerIDs)
	}
	if entry.Run.Fact == nil || entry.Run.Fact.Family != "observation" ||
		entry.Run.Fact.Variant != "applied" || entry.Run.Fact.CausationID == "" {
		t.Fatalf("fact evidence = %#v", entry.Run.Fact)
	}
	if len(entry.Run.Steps) != 1 || entry.Run.Steps[0].StepID != "turn_off" ||
		entry.Run.Steps[0].Status != "satisfied" || entry.Run.Steps[0].VerifiedCommandID == nil {
		t.Fatalf("run steps = %#v", entry.Run.Steps)
	}
	assertTerminalLinkedCommand(ctx, t, httpAddress, *entry.Run.Steps[0].VerifiedCommandID, powerEntityID, false)
}

// waitForAdapterRuntime polls the Adapter read until Core assigned the claimed
// runtime identity the command subject is scoped to.
func waitForAdapterRuntime(
	ctx context.Context,
	t *testing.T,
	httpAddress, adapterID string,
) string {
	t.Helper()
	var runtimeID string
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		response := sliceRequest(ctx, t, http.MethodGet, httpAddress, "/v1/adapters/"+adapterID, nil)
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return false, nil
		}
		var body struct {
			Health struct {
				Runtime *struct {
					ID string `json:"id"`
				} `json:"runtime"`
			} `json:"health"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			return false, err
		}
		if body.Health.Runtime == nil || body.Health.Runtime.ID == "" {
			return false, nil
		}
		runtimeID = body.Health.Runtime.ID
		return true, nil
	})
	if runtimeID == "" {
		t.Fatal("adapter never reported a runtime identity")
	}
	return runtimeID
}

// waitForCommandSubscription blocks until the Adapter's set Command subject has
// a subscriber, so the Run cannot race an unactivated command handler.
func waitForCommandSubscription(
	t *testing.T,
	server *natsserver.Server,
	adapterID, runtimeID, entityID string,
) {
	t.Helper()
	subject, err := natswire.CommandSubject(adapterID, runtimeID, entityID, "set")
	if err != nil {
		t.Fatal(err)
	}
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		subscriptions, subscriptionsErr := server.Subsz(&natsserver.SubszOptions{
			Subscriptions: true,
			Test:          subject,
		})
		if subscriptionsErr != nil {
			return false, subscriptionsErr
		}
		return subscriptions.Total > 0, nil
	})
}

func sliceRequest(
	ctx context.Context,
	t *testing.T,
	method, httpAddress, path string,
	body io.Reader,
) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(
		ctx, method, "http://"+httpAddress+path, body,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func readSliceBody(t *testing.T, response *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
