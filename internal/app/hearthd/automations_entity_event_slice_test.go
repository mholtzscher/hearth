package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	simulatoradapter "github.com/mholtzscher/hearth/internal/adapters/simulator"
)

// TestAutomationEntityEventFactDrivesCommandThroughCore protects A14's entity
// event family at the app boundary: one real SDK adapter reports an accepted
// Entity Event, the relay publishes its Device Fact on the entity-event route,
// the runtime's automation consumer admits it against an HTTP-created Entity
// Event Automation, the Run executes one typed Command against a different
// Entity, and API history explains the immutable matched Trigger, Fact evidence,
// ordered Step, and verified terminal Command.
//
// It fails if the entity-event fact route, the entity-event Trigger kind, or the
// history link for an entity-event Run is disconnected. The Observation twin is
// TestAutomationFactDrivesCommandThroughCore; together they prove both published
// fact families reach automation admission.
func TestAutomationEntityEventFactDrivesCommandThroughCore(t *testing.T) {
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
		}, slog.New(slog.DiscardHandler))
	}()
	waitForCoreHealthz(ctx, t, httpAddress, runErrors)

	adapter := startSliceAdapter(ctx, t, server, httpAddress)
	defer adapter.stop()

	automationID := createEntityEventAutomation(
		ctx, t, httpAddress, adapter.eventEntityID, adapter.powerEntityID,
	)
	if err := adapter.emit(ctx, simulatoradapter.EntityEventSinglePress); err != nil {
		t.Fatal(err)
	}

	runID := waitForAutomationRun(ctx, t, httpAddress, automationID)
	assertEntityEventRunCommandLink(
		ctx, t, httpAddress, automationID, runID,
		adapter.eventEntityID, adapter.powerEntityID,
	)
	// The Automation was driven by accepted evidence, not a rejected report: the
	// same event is durable and accepted in the device-facing read model.
	assertAcceptedEntityEventRecorded(ctx, t, httpAddress, adapter.eventEntityID)
}

// createEntityEventAutomation POSTs one enabled Automation whose Entity Event
// Trigger matches an accepted single_press for the event source Entity and whose
// single Step sets the power Entity to true. It returns the created identity.
func createEntityEventAutomation(
	ctx context.Context,
	t *testing.T,
	httpAddress, eventEntityID, powerEntityID string,
) string {
	t.Helper()
	definition := fmt.Sprintf(`{
		"name": "Button turns on light",
		"enabled": true,
		"triggers": [{"id":"single_press","kind":"entity_event","entity_id":%q,
			"event_name":"single_press"}],
		"steps": [{"id":"turn_on","entity_id":%q,"operation":"set","parameters":{"value":true}}]
	}`, eventEntityID, powerEntityID)
	response := sliceRequest(ctx, t, http.MethodPost, httpAddress, "/v1/automations",
		bytes.NewBufferString(definition))
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create automation status = %d, body = %s",
			response.StatusCode, readSliceBody(t, response))
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("created automation has no identity")
	}
	return created.ID
}

// assertEntityEventRunCommandLink proves the retained history explains the
// entity-event provenance, the immutable matched Trigger, the ordered Step, and
// the verified Command identity, and that the linked Command is a real terminal
// Command for the expected Entity, Operation, and parameters.
func assertEntityEventRunCommandLink(
	ctx context.Context,
	t *testing.T,
	httpAddress, automationID, runID, eventEntityID, powerEntityID string,
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
				Family           string          `json:"family"`
				EntityID         string          `json:"entity_id"`
				Variant          string          `json:"variant"`
				CausationID      string          `json:"causation_id"`
				ObservationValue json.RawMessage `json:"observation_value"`
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
	run := entry.Run
	if run.Source != "device_fact" || run.Status != "succeeded" {
		t.Fatalf("run source/status = %q/%q, want device_fact/succeeded", run.Source, run.Status)
	}
	if len(run.MatchedTriggerIDs) != 1 || run.MatchedTriggerIDs[0] != "single_press" {
		t.Fatalf("matched trigger IDs = %v, want [single_press]", run.MatchedTriggerIDs)
	}
	fact := run.Fact
	if fact == nil || fact.Family != "entity_event" || fact.Variant != "single_press" {
		t.Fatalf("entity-event fact evidence = %#v", fact)
	}
	if fact.EntityID != eventEntityID || fact.CausationID == "" {
		t.Fatalf("entity-event fact entity/causation = %q/%q", fact.EntityID, fact.CausationID)
	}
	// An Entity Event fact carries no Observation value; only the name is the
	// report, so history must not invent one.
	if len(fact.ObservationValue) != 0 {
		t.Fatalf("entity-event fact carries an Observation value: %s", fact.ObservationValue)
	}
	if len(run.Steps) != 1 || run.Steps[0].StepID != "turn_on" ||
		run.Steps[0].Status != "satisfied" || run.Steps[0].VerifiedCommandID == nil {
		t.Fatalf("run steps = %#v", run.Steps)
	}
	assertTerminalLinkedCommand(ctx, t, httpAddress, *run.Steps[0].VerifiedCommandID, powerEntityID, true)
}

// assertAcceptedEntityEventRecorded proves the Entity Event that drove the Run
// was accepted device evidence, so the fact could only exist because an accepted
// report was recorded first.
func assertAcceptedEntityEventRecorded(
	ctx context.Context,
	t *testing.T,
	httpAddress, eventEntityID string,
) {
	t.Helper()
	collection := getEntityEvents(ctx, t, http.DefaultClient, httpAddress, eventEntityID)
	found := false
	for _, item := range collection.Items {
		if item.Name != simulatoradapter.EntityEventSinglePress {
			continue
		}
		if item.Disposition != "accepted" || item.RejectionCode != nil {
			t.Fatalf("recorded entity event = %#v, want accepted without a rejection code", item)
		}
		found = true
	}
	if !found {
		t.Fatalf("no accepted %q entity event was recorded", simulatoradapter.EntityEventSinglePress)
	}
}

// assertTerminalLinkedCommand proves a history Command link names a real terminal
// Command for the expected Entity, Operation, and static parameter, not a merely
// reserved or unrelated identity.
func assertTerminalLinkedCommand(
	ctx context.Context,
	t *testing.T,
	httpAddress, commandID, entityID string,
	value bool,
) {
	t.Helper()
	response := sliceRequest(ctx, t, http.MethodGet, httpAddress, "/v1/commands/"+commandID, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("command read status = %d, body = %s",
			response.StatusCode, readSliceBody(t, response))
	}
	var command struct {
		EntityID    string         `json:"entity_id"`
		Operation   string         `json:"operation"`
		Parameters  map[string]any `json:"parameters"`
		Status      string         `json:"status"`
		CompletedAt *string        `json:"completed_at"`
	}
	if err := json.NewDecoder(response.Body).Decode(&command); err != nil {
		t.Fatal(err)
	}
	if command.Status != "satisfied" || command.CompletedAt == nil {
		t.Fatalf("linked command status/completion = %q/%v, want a terminal satisfied Command",
			command.Status, command.CompletedAt)
	}
	if command.EntityID != entityID || command.Operation != "set" {
		t.Fatalf("linked command entity/operation = %q/%q", command.EntityID, command.Operation)
	}
	if got, ok := command.Parameters["value"].(bool); !ok || got != value {
		t.Fatalf("linked command parameters = %#v, want value %t", command.Parameters, value)
	}
}
