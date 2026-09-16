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

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// TestAutomationConditionsGateAdmissionThroughCore protects A17 at the app
// boundary: one real Core assembly with a numeric illuminance Entity, two
// binary occupancy Entities, and two controllable Entities admits or blocks
// Device-Fact-driven Runs purely on current State evidence.
//
// It fails if the composition of the public definition API, the devices State
// snapshot seam, three-valued evaluation, the durable Skip/history explanation,
// the manual bypass body, or the Command boundary is disconnected. Cases, in
// order, all driven through the same registered Entities:
//
//  1. dark illuminance plus a sibling Entity with no accepted State: the `all`
//     Automation is unknown and records a readable Skip with no Command, while
//     the `any` Automation admits because `any(true, unknown)` is true.
//  2. bright illuminance: the `all` Automation is false and the `any`
//     Automation is unknown, and neither dispatches a Command.
//  3. dark illuminance with fresh occupied evidence: both Automations admit and
//     dispatch their expected Set Commands.
//  4. retained State for an Entity reported unavailable: admission still uses
//     the retained evidence and dispatches.
//  5. normal manual invocation while bright: HTTP 409 with a history reference
//     resolving to a committed `conditions_false` Skip and no Command.
//  6. explicit manual bypass while bright: the Run records `bypassed`, reads no
//     Conditions, and its Command still satisfies normally.
//
//nolint:cyclop,gocognit,gocyclo,paralleltest,tparallel // Ordered cases share one assembly, its State, and its history.
func TestAutomationConditionsGateAdmissionThroughCore(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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

	registered := startConditionsAdapter(ctx, t, server, httpAddress)
	defer registered.stop()

	// Save-time reference validation requires every referenced Entity to exist,
	// so both definitions are created only after registration.
	gatedAutomationID := createConditionsAutomation(ctx, t, httpAddress,
		conditionsAllDefinition(registered), "dark-and-free")
	anyAutomationID := createConditionsAutomation(ctx, t, httpAddress,
		conditionsAnyDefinition(registered), "any-permission")

	// The six ordered cases below share one assembly and one pair of
	// Automations. They stay sequential subtests, never parallel, because each
	// later case depends on the State and history the earlier cases committed.
	t.Run("unknown sibling blocks all and admits any", func(t *testing.T) {
		if err := registered.publishIlluminance(ctx, 10); err != nil {
			t.Fatal(err)
		}
		lightCommandsBefore := conditionsCommandCount(ctx, t, httpAddress, registered.lightEntityID)
		gatedBaseline := conditionsHistoryCount(ctx, t, httpAddress, gatedAutomationID)
		anyBaseline := conditionsHistoryCount(ctx, t, httpAddress, anyAutomationID)
		if err := registered.publishMotion(ctx); err != nil {
			t.Fatal(err)
		}

		gatedUnknown := waitForConditionsHistoryEntry(ctx, t, httpAddress, gatedAutomationID, gatedBaseline)
		if gatedUnknown.Kind != "skip" || gatedUnknown.Reason != "conditions_unknown" ||
			gatedUnknown.Source != "device_fact" || gatedUnknown.ConditionMode != "evaluated" {
			t.Fatalf("dark-and-unknown skip summary = %#v", gatedUnknown)
		}
		if gatedUnknown.ConditionResult == nil || *gatedUnknown.ConditionResult != "unknown" {
			t.Fatalf("dark-and-unknown skip condition result = %v", gatedUnknown.ConditionResult)
		}
		assertConditionsUnknownSkipDetail(ctx, t, httpAddress, gatedAutomationID, gatedUnknown.ID,
			"dark-and-free", "room-dark", "other-room-occupied")

		anyUnknownAdmission := waitForConditionsRunSucceeded(ctx, t, httpAddress, anyAutomationID,
			waitForConditionsHistoryEntry(ctx, t, httpAddress, anyAutomationID, anyBaseline).ID)
		anyEvaluation := assertConditionsEvaluationResult(t, anyUnknownAdmission,
			"any-permission", "room-dark-any")
		assertConditionsOccupiedNode(t, anyEvaluation, "other-room-occupied-any", "unknown")
		assertConditionsRunCommand(ctx, t, httpAddress, registered.fanEntityID, "motion", anyUnknownAdmission)

		if got := conditionsCommandCount(ctx, t, httpAddress, registered.lightEntityID); got != lightCommandsBefore {
			t.Fatalf("blocked all-Automation dispatched a Command: count %d, want %d",
				got, lightCommandsBefore)
		}
	})

	t.Run("bright blocks all and leaves any unknown", func(t *testing.T) {
		if err := registered.publishIlluminance(ctx, 100); err != nil {
			t.Fatal(err)
		}
		lightCommandsBefore := conditionsCommandCount(ctx, t, httpAddress, registered.lightEntityID)
		fanCommandsBefore := conditionsCommandCount(ctx, t, httpAddress, registered.fanEntityID)
		gatedBaseline := conditionsHistoryCount(ctx, t, httpAddress, gatedAutomationID)
		anyBaseline := conditionsHistoryCount(ctx, t, httpAddress, anyAutomationID)
		if err := registered.publishMotion(ctx); err != nil {
			t.Fatal(err)
		}

		gatedBright := waitForConditionsHistoryEntry(ctx, t, httpAddress, gatedAutomationID, gatedBaseline)
		if gatedBright.Kind != "skip" || gatedBright.Reason != "conditions_false" {
			t.Fatalf("bright skip = %#v, want a conditions_false skip", gatedBright)
		}
		assertConditionsFalseSkipDetail(ctx, t, httpAddress, gatedAutomationID, gatedBright.ID, 100)

		anyBright := waitForConditionsHistoryEntry(ctx, t, httpAddress, anyAutomationID, anyBaseline)
		if anyBright.Kind != "skip" || anyBright.Reason != "conditions_unknown" {
			t.Fatalf("bright any skip = %#v", anyBright)
		}
		if got := conditionsCommandCount(ctx, t, httpAddress, registered.lightEntityID); got != lightCommandsBefore {
			t.Fatalf("bright all-Automation dispatched a Command: count %d, want %d",
				got, lightCommandsBefore)
		}
		if got := conditionsCommandCount(ctx, t, httpAddress, registered.fanEntityID); got != fanCommandsBefore {
			t.Fatalf("bright any-Automation dispatched a Command: count %d, want %d",
				got, fanCommandsBefore)
		}
	})

	t.Run("dark and unoccupied admits both", func(t *testing.T) {
		if err := registered.publishIlluminance(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if err := registered.publishOtherRoomOccupied(ctx, false); err != nil {
			t.Fatal(err)
		}
		gatedBaseline := conditionsHistoryCount(ctx, t, httpAddress, gatedAutomationID)
		anyBaseline := conditionsHistoryCount(ctx, t, httpAddress, anyAutomationID)
		if err := registered.publishMotion(ctx); err != nil {
			t.Fatal(err)
		}

		gatedRun := waitForConditionsRunSucceeded(ctx, t, httpAddress, gatedAutomationID,
			waitForConditionsHistoryEntry(ctx, t, httpAddress, gatedAutomationID, gatedBaseline).ID)
		gatedEvaluation := assertConditionsEvaluationResult(t, gatedRun, "dark-and-free", "room-dark")
		assertConditionsOccupiedNode(t, gatedEvaluation, "other-room-occupied", "false")
		assertConditionsRunCommand(ctx, t, httpAddress, registered.lightEntityID, "motion", gatedRun)
		if gatedRun.Snapshot.Conditions == nil || gatedRun.Snapshot.Conditions.ID != "dark-and-free" {
			t.Fatalf("run snapshot Conditions = %#v", gatedRun.Snapshot.Conditions)
		}

		anyRun := waitForConditionsRunSucceeded(ctx, t, httpAddress, anyAutomationID,
			waitForConditionsHistoryEntry(ctx, t, httpAddress, anyAutomationID, anyBaseline).ID)
		anyEvaluation := assertConditionsEvaluationResult(t, anyRun, "any-permission", "room-dark-any")
		assertConditionsOccupiedNode(t, anyEvaluation, "other-room-occupied-any", "false")
		assertConditionsRunCommand(ctx, t, httpAddress, registered.fanEntityID, "motion", anyRun)
	})

	t.Run("retained unavailable State still admits", func(t *testing.T) {
		conditionsSetEntity(ctx, t, httpAddress, registered.lightEntityID, false)
		conditionsSetEntity(ctx, t, httpAddress, registered.fanEntityID, false)
		if err := registered.reportIlluminanceUnavailable(ctx); err != nil {
			t.Fatal(err)
		}
		gatedBaseline := conditionsHistoryCount(ctx, t, httpAddress, gatedAutomationID)
		if err := registered.publishMotion(ctx); err != nil {
			t.Fatal(err)
		}
		retainedRun := waitForConditionsRunSucceeded(ctx, t, httpAddress, gatedAutomationID,
			waitForConditionsHistoryEntry(ctx, t, httpAddress, gatedAutomationID, gatedBaseline).ID)
		retainedEvaluation := assertConditionsEvaluationResult(t, retainedRun, "dark-and-free", "room-dark")
		assertConditionsOccupiedNode(t, retainedEvaluation, "other-room-occupied", "false")
		assertConditionsRunCommand(ctx, t, httpAddress, registered.lightEntityID, "motion", retainedRun)
	})

	t.Run("normal manual invocation records a conditions_false Skip", func(t *testing.T) {
		if err := registered.publishIlluminance(ctx, 100); err != nil {
			t.Fatal(err)
		}
		lightCommandsBefore := conditionsCommandCount(ctx, t, httpAddress, registered.lightEntityID)
		gatedBaseline := conditionsHistoryCount(ctx, t, httpAddress, gatedAutomationID)
		blocked := conditionsManualRun(ctx, t, httpAddress, gatedAutomationID, "")
		if blocked.StatusCode != http.StatusConflict {
			t.Fatalf("condition-blocked manual status = %d, body = %s",
				blocked.StatusCode, readSliceBody(t, blocked))
		}
		problem := decodeConditionsProblem(t, blocked)
		if problem.Code != "conditions_false" || problem.HistoryID == "" ||
			problem.HistoryURL != "/v1/automations/"+gatedAutomationID+"/history/"+problem.HistoryID {
			t.Fatalf("condition-blocked problem = %#v", problem)
		}
		assertConditionsManualSkipDetail(ctx, t, httpAddress, problem.HistoryURL, problem.HistoryID)
		waitForConditionsHistoryEntry(ctx, t, httpAddress, gatedAutomationID, gatedBaseline)
		if got := conditionsCommandCount(ctx, t, httpAddress, registered.lightEntityID); got != lightCommandsBefore {
			t.Fatalf("blocked manual admission dispatched a Command: count %d, want %d",
				got, lightCommandsBefore)
		}
	})

	t.Run("explicit bypass records intent and still dispatches", func(t *testing.T) {
		conditionsSetEntity(ctx, t, httpAddress, registered.lightEntityID, false)
		bypassed := conditionsManualRun(ctx, t, httpAddress, gatedAutomationID,
			`{"bypass_conditions":true}`)
		defer bypassed.Body.Close()
		if bypassed.StatusCode != http.StatusAccepted {
			t.Fatalf("bypass manual status = %d, body = %s",
				bypassed.StatusCode, readSliceBody(t, bypassed))
		}
		bypassedRunID := conditionsLocationRunID(t, bypassed)
		bypassedRun := waitForConditionsRunSucceeded(ctx, t, httpAddress, gatedAutomationID, bypassedRunID)
		if bypassedRun.Source != "manual" || bypassedRun.Fact != nil {
			t.Fatalf("bypassed run provenance = %q / %#v", bypassedRun.Source, bypassedRun.Fact)
		}
		if bypassedRun.ConditionDecision.Mode != "bypassed" || !bypassedRun.ConditionDecision.BypassRequested ||
			bypassedRun.ConditionDecision.Evaluation != nil ||
			bypassedRun.ConditionDecision.Snapshot == nil ||
			bypassedRun.ConditionDecision.Snapshot.ID != "dark-and-free" {
			t.Fatalf("bypassed decision = %#v", bypassedRun.ConditionDecision)
		}
		assertConditionsRunCommand(ctx, t, httpAddress, registered.lightEntityID, "", bypassedRun)
	})
}

// conditionsAdapter is one running adapter bound to Core for the vertical slice.
// It registers two controllable power Entities, one numeric illuminance Entity,
// and two binary occupancy Entities through one Session, and serves both Set
// Command routes so a Run's Command reaches typed evidence.
type conditionsAdapter struct {
	lightEntityID       string
	fanEntityID         string
	motionEntityID      string
	illuminanceEntityID string
	otherRoomEntityID   string
	runtime             *scripted.Runtime
	session             *adapter.Session
	stop                func()
}

func (conditions conditionsAdapter) publishMotion(ctx context.Context) error {
	_, err := conditions.runtime.PublishEnvelope(
		ctx, conditions.motionEntityID, json.RawMessage(`{"value":true}`),
	)
	return err
}

func (conditions conditionsAdapter) publishIlluminance(ctx context.Context, lux float64) error {
	_, err := conditions.runtime.PublishEnvelope(
		ctx, conditions.illuminanceEntityID,
		json.RawMessage(fmt.Sprintf(`{"value":%v}`, lux)),
	)
	return err
}

func (conditions conditionsAdapter) publishOtherRoomOccupied(ctx context.Context, occupied bool) error {
	_, err := conditions.runtime.PublishEnvelope(
		ctx, conditions.otherRoomEntityID,
		json.RawMessage(fmt.Sprintf(`{"value":%v}`, occupied)),
	)
	return err
}

func (conditions conditionsAdapter) reportIlluminanceUnavailable(ctx context.Context) error {
	return conditions.session.ReportEntityAvailability(ctx, []adapter.EntityAvailabilityReport{{
		EntityID: conditions.illuminanceEntityID, Status: adapter.AvailabilityUnavailable,
		ReasonCode: "adapter.hearth-test.operator_reported", SourceObservedAt: time.Now().UTC(),
	}})
}

// startConditionsAdapter wires one scripted adapter to the running Core.
// The scripted runtime answers both power Entities' Set Commands with linked
// State evidence; the sensor Entities publish Observations because they have
// no Operations.
func startConditionsAdapter(
	ctx context.Context,
	t *testing.T,
	server *natsserver.Server,
	httpAddress string,
) conditionsAdapter {
	t.Helper()
	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: sliceAdapterID, SoftwareName: "hearth-conditions-slice",
		SoftwareVersion: "0.1.0", NATSURL: server.ClientURL(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	unavailable := false
	scriptedRuntime, err := scripted.New(session, []scripted.DeviceSpec{
		{
			BindingKey: "office-light",
			Name:       "Office light",
			Kind:       "light",
			Entities: []scripted.EntitySpec{
				{
					Key:  "power",
					Name: "Office light power",
					Type: "hearth.power/v1",
					Support: map[string]any{
						"state":      map[string]any{},
						"operations": map[string]any{"set": map[string]any{}},
					},
					Initial: false,
				},
			},
		},
		{
			BindingKey: "ceiling-fan",
			Name:       "Ceiling fan",
			Kind:       "relay",
			Entities: []scripted.EntitySpec{
				{
					Key:  "power",
					Name: "Ceiling fan relay",
					Type: "hearth.power/v1",
					Support: map[string]any{
						"state":      map[string]any{},
						"operations": map[string]any{"set": map[string]any{}},
					},
					Initial: false,
				},
			},
		},
		{
			BindingKey: "office-motion",
			Name:       "Office and bedroom motion",
			Kind:       "sensor",
			Entities: []scripted.EntitySpec{
				{
					Key:  "illuminance",
					Name: "Office illuminance",
					Type: "hearth.numericsensor/v1",
					Support: map[string]any{
						"state": map[string]any{
							"minimum": 0, "maximum": 1000000000, "unit": "lx",
						},
						"operations": map[string]any{},
					},
					Initial: 10,
				},
				{
					Key:  "occupancy",
					Name: "Office occupancy",
					Type: "hearth.binarysensor/v1",
					Support: map[string]any{
						"state":      map[string]any{},
						"operations": map[string]any{},
					},
					Initial: false,
				},
				{
					Key:  "bedroom-occupancy",
					Name: "Bedroom occupancy",
					Type: "hearth.binarysensor/v1",
					Support: map[string]any{
						"state":      map[string]any{},
						"operations": map[string]any{},
					},
					Initial:            false,
					Available:          &unavailable,
					AvailabilityReason: "adapter.hearth-test.operator_reported",
				},
			},
		},
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
	lightEntityID := string(bindingEntityID(t, bindings[0], "power"))
	fanEntityID := string(bindingEntityID(t, bindings[1], "power"))
	illuminanceEntityID := string(bindingEntityID(t, bindings[2], "illuminance"))
	motionEntityID := string(bindingEntityID(t, bindings[2], "occupancy"))
	otherRoomEntityID := string(bindingEntityID(t, bindings[2], "bedroom-occupancy"))

	// The bedroom occupancy Entity starts unavailable with no State: its absent
	// State is the unknown-evidence case. Initialize publishes the first value of
	// every available Entity and reports health and availability.
	if initializeErr := scriptedRuntime.Initialize(ctx); initializeErr != nil {
		t.Fatal(initializeErr)
	}
	serveContext, stopServe := context.WithCancel(ctx)
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = session.ServeCommands(serveContext, scriptedRuntime.CommandHandler())
	}()

	runtimeID := waitForAdapterRuntime(ctx, t, httpAddress, sliceAdapterID)
	waitForCommandSubscription(t, server, sliceAdapterID, runtimeID, lightEntityID)
	waitForCommandSubscription(t, server, sliceAdapterID, runtimeID, fanEntityID)
	return conditionsAdapter{
		lightEntityID:       lightEntityID,
		fanEntityID:         fanEntityID,
		motionEntityID:      motionEntityID,
		illuminanceEntityID: illuminanceEntityID,
		otherRoomEntityID:   otherRoomEntityID,
		runtime:             scriptedRuntime,
		session:             session,
		stop: func() {
			stopServe()
			<-served
		},
	}
}

// conditionsAllDefinition is an enabled Observation Automation whose root `all`
// requires dark illuminance and an unoccupied sibling, in the exact composition
// the operator guide documents.
func conditionsAllDefinition(conditions conditionsAdapter) string {
	return fmt.Sprintf(`{
		"name": "Office light on motion when dark and unoccupied",
		"enabled": true,
		"triggers": [{"id":"motion","kind":"observation","entity_id":%q,
			"dispositions":["applied","unchanged"],
			"comparisons":[{"pointer":"","operator":"eq","operand":true}]}],
		"conditions": {
			"id": "dark-and-free",
			"kind": "all",
			"children": [
				{"id":"room-dark","kind":"entity_state","entity_id":%q,
					"pointer":"","operator":"lt","operand":30,"max_age_seconds":300},
				{"id":"other-room-unoccupied","kind":"not","child":{
					"id":"other-room-occupied","kind":"entity_state","entity_id":%q,
					"pointer":"","operator":"eq","operand":true,"max_age_seconds":120}}
			]
		},
		"steps": [{"id":"turn_on","entity_id":%q,"operation":"set","parameters":{"value":true}}]
	}`, conditions.motionEntityID, conditions.illuminanceEntityID,
		conditions.otherRoomEntityID, conditions.lightEntityID)
}

// conditionsAnyDefinition is the sibling Automation whose root `any` proves a
// true child admits despite an unknown sibling.
func conditionsAnyDefinition(conditions conditionsAdapter) string {
	return fmt.Sprintf(`{
		"name": "Ceiling fan on motion when any permission",
		"enabled": true,
		"triggers": [{"id":"motion","kind":"observation","entity_id":%q,
			"dispositions":["applied","unchanged"],
			"comparisons":[{"pointer":"","operator":"eq","operand":true}]}],
		"conditions": {
			"id": "any-permission",
			"kind": "any",
			"children": [
				{"id":"room-dark-any","kind":"entity_state","entity_id":%q,
					"pointer":"","operator":"lt","operand":30,"max_age_seconds":300},
				{"id":"other-room-unoccupied-any","kind":"not","child":{
					"id":"other-room-occupied-any","kind":"entity_state","entity_id":%q,
					"pointer":"","operator":"eq","operand":true,"max_age_seconds":120}}
			]
		},
		"steps": [{"id":"turn_on_fan","entity_id":%q,"operation":"set","parameters":{"value":true}}]
	}`, conditions.motionEntityID, conditions.illuminanceEntityID,
		conditions.otherRoomEntityID, conditions.fanEntityID)
}

// createConditionsAutomation POSTs one definition and returns its canonical ID,
// asserting the created definition echoed the expected Condition root.
func createConditionsAutomation(
	ctx context.Context,
	t *testing.T,
	httpAddress, definition, rootID string,
) string {
	t.Helper()
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
			Conditions *conditionsNode `json:"conditions"`
		} `json:"definition"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("created automation has no identity")
	}
	if created.Definition.Conditions == nil || created.Definition.Conditions.ID != rootID {
		t.Fatalf("created definition Conditions = %#v", created.Definition.Conditions)
	}
	return created.ID
}

// conditionsNode is the recursive Condition tree shape the transport returns.
type conditionsNode struct {
	ID       string           `json:"id"`
	Kind     string           `json:"kind"`
	Children []conditionsNode `json:"children"`
	Child    *conditionsNode  `json:"child"`
}

// conditionsDecision mirrors the transport Condition decision. Evaluation is
// present only for the evaluated mode.
type conditionsDecision struct {
	Mode            string              `json:"mode"`
	BypassRequested bool                `json:"bypass_requested"`
	Snapshot        *conditionsNode     `json:"snapshot"`
	Evaluation      *conditionsEvalBody `json:"evaluation"`
}

type conditionsEvalBody struct {
	EvaluatedAt time.Time              `json:"evaluated_at"`
	Result      string                 `json:"result"`
	Nodes       []conditionsNodeResult `json:"nodes"`
}

type conditionsNodeResult struct {
	ID            string          `json:"id"`
	Result        string          `json:"result"`
	UnknownReason *string         `json:"unknown_reason"`
	SelectedValue json.RawMessage `json:"selected_value"`
	ObservationID *string         `json:"observation_id"`
	ObservedAt    *string         `json:"observed_at"`
}

// conditionsHistorySummary mirrors the lightweight history listing projection.
type conditionsHistorySummary struct {
	ID              string  `json:"id"`
	Kind            string  `json:"kind"`
	Status          string  `json:"status"`
	Reason          string  `json:"reason"`
	Source          string  `json:"source"`
	ConditionMode   string  `json:"condition_mode"`
	ConditionResult *string `json:"condition_result"`
	BypassRequested bool    `json:"bypass_requested"`
}

// conditionsRun mirrors the history detail Run body this test inspects.
type conditionsRun struct {
	ID                string             `json:"id"`
	Source            string             `json:"source"`
	Status            string             `json:"status"`
	MatchedTriggerIDs []string           `json:"matched_trigger_ids"`
	Fact              *json.RawMessage   `json:"fact"`
	ConditionDecision conditionsDecision `json:"condition_decision"`
	Snapshot          struct {
		Conditions *conditionsNode `json:"conditions"`
	} `json:"snapshot"`
	Steps []struct {
		StepID            string  `json:"step_id"`
		Status            string  `json:"status"`
		VerifiedCommandID *string `json:"verified_command_id"`
	} `json:"steps"`
}

// conditionsSkip mirrors the history detail Skip body this test inspects.
type conditionsSkip struct {
	ID                string             `json:"id"`
	Source            string             `json:"source"`
	Reason            string             `json:"reason"`
	Fact              *json.RawMessage   `json:"fact"`
	MatchedTriggers   []json.RawMessage  `json:"matched_triggers"`
	ConditionDecision conditionsDecision `json:"condition_decision"`
}

type conditionsHistoryEntry struct {
	Kind string          `json:"kind"`
	Run  *conditionsRun  `json:"run"`
	Skip *conditionsSkip `json:"skip"`
}

type conditionsProblem struct {
	Code       string `json:"code"`
	HistoryID  string `json:"history_id"`
	HistoryURL string `json:"history_url"`
}

func conditionsHistorySummaries(
	ctx context.Context,
	httpAddress, automationID string,
) ([]conditionsHistorySummary, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+httpAddress+"/v1/automations/"+automationID+"/history", nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("history status = %d", response.StatusCode)
	}
	var page struct {
		Items []conditionsHistorySummary `json:"items"`
	}
	if decodeErr := json.NewDecoder(response.Body).Decode(&page); decodeErr != nil {
		return nil, decodeErr
	}
	return page.Items, nil
}

func conditionsHistoryCount(ctx context.Context, t *testing.T, httpAddress, automationID string) int {
	t.Helper()
	items, err := conditionsHistorySummaries(ctx, httpAddress, automationID)
	if err != nil {
		t.Fatal(err)
	}
	return len(items)
}

// waitForConditionsHistoryEntry polls until the Automation's newest-first
// history gains an entry beyond the baseline and returns that newest entry.
func waitForConditionsHistoryEntry(
	ctx context.Context,
	t *testing.T,
	httpAddress, automationID string,
	baseline int,
) conditionsHistorySummary {
	t.Helper()
	var summary conditionsHistorySummary
	waitForMatrixCondition(t, 30*time.Second, func() (bool, error) {
		items, err := conditionsHistorySummaries(ctx, httpAddress, automationID)
		if err != nil {
			return false, nil //nolint:nilerr // A transient read failure is retried until the deadline.
		}
		if len(items) <= baseline {
			return false, nil
		}
		summary = items[0]
		return true, nil
	})
	if summary.ID == "" {
		t.Fatal("automation history never gained an entry")
	}
	return summary
}

func conditionsHistoryDetail(
	ctx context.Context,
	t *testing.T,
	httpAddress, automationID, entryID string,
) conditionsHistoryEntry {
	t.Helper()
	response := sliceRequest(ctx, t, http.MethodGet, httpAddress,
		"/v1/automations/"+automationID+"/history/"+entryID, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("history detail status = %d, body = %s",
			response.StatusCode, readSliceBody(t, response))
	}
	var entry conditionsHistoryEntry
	if err := json.NewDecoder(response.Body).Decode(&entry); err != nil {
		t.Fatal(err)
	}
	return entry
}

// waitForConditionsRunSucceeded polls the Run detail until the worker reaches a
// terminal successful status, so Command assertions never race execution.
func waitForConditionsRunSucceeded(
	ctx context.Context,
	t *testing.T,
	httpAddress, automationID, runID string,
) conditionsRun {
	t.Helper()
	var run conditionsRun
	waitForMatrixCondition(t, 30*time.Second, func() (bool, error) {
		entry := conditionsHistoryDetail(ctx, t, httpAddress, automationID, runID)
		if entry.Run == nil || entry.Run.Status == "running" {
			return false, nil
		}
		run = *entry.Run
		return true, nil
	})
	if run.Status != "succeeded" {
		t.Fatalf("run %s status = %q, want succeeded", runID, run.Status)
	}
	return run
}

// conditionsNodeResultByID returns one evaluated node, failing when the
// evaluation omitted it, so a missing predicate is a detected defect.
func conditionsNodeResultByID(
	t *testing.T,
	evaluation *conditionsEvalBody,
	id string,
) conditionsNodeResult {
	t.Helper()
	if evaluation == nil {
		t.Fatalf("condition evaluation for node %q is absent", id)
	}
	for _, node := range evaluation.Nodes {
		if node.ID == id {
			return node
		}
	}
	t.Fatalf("condition evaluation omitted node %q: %#v", id, evaluation.Nodes)
	return conditionsNodeResult{}
}

// assertConditionsEvaluationResult asserts the evaluated mode, snapshot root,
// true root result, and the dark leaf's retained evidence. The occupied sibling
// is asserted separately because a true root can hold either a false or an
// unknown sibling.
func assertConditionsEvaluationResult(
	t *testing.T,
	run conditionsRun,
	rootID, darkID string,
) *conditionsEvalBody {
	t.Helper()
	decision := run.ConditionDecision
	if decision.Mode != "evaluated" {
		t.Fatalf("run condition mode = %q, want evaluated", decision.Mode)
	}
	if decision.BypassRequested {
		t.Fatal("fact-driven run requested bypass")
	}
	if decision.Snapshot == nil || decision.Snapshot.ID != rootID {
		t.Fatalf("run condition snapshot = %#v", decision.Snapshot)
	}
	if decision.Evaluation == nil || decision.Evaluation.Result != "true" {
		t.Fatalf("run condition evaluation = %#v, want a true result", decision.Evaluation)
	}
	if decision.Evaluation.Nodes[0].Result != "true" {
		t.Fatalf("root node result = %q, want true", decision.Evaluation.Nodes[0].Result)
	}
	dark := conditionsNodeResultByID(t, decision.Evaluation, darkID)
	if dark.Result != "true" || dark.UnknownReason != nil {
		t.Fatalf("dark node = %#v, want a true leaf", dark)
	}
	if dark.SelectedValue == nil || dark.ObservationID == nil || dark.ObservedAt == nil {
		t.Fatalf("dark node evidence = %#v, want selected value and Observation metadata", dark)
	}
	return decision.Evaluation
}

// assertConditionsOccupiedNode asserts the occupied sibling leaf is either a
// false comparison or unknown absent State, never a fabricated value. A reached
// `false` leaf must retain Observation metadata; an unknown `state_missing` leaf
// must retain none.
func assertConditionsOccupiedNode(t *testing.T, evaluation *conditionsEvalBody, id, want string) {
	t.Helper()
	occupied := conditionsNodeResultByID(t, evaluation, id)
	switch want {
	case "false":
		if occupied.Result != "false" || occupied.UnknownReason != nil {
			t.Fatalf("occupied node result = %q reason = %v, want false with no reason",
				occupied.Result, occupied.UnknownReason)
		}
		if occupied.SelectedValue == nil || occupied.ObservationID == nil || occupied.ObservedAt == nil {
			t.Fatalf("occupied false node lost evidence: %#v", occupied)
		}
	case "unknown":
		if occupied.Result != "unknown" || occupied.UnknownReason == nil ||
			*occupied.UnknownReason != "state_missing" {
			t.Fatalf("occupied node result/reason = %q/%v, want unknown/state_missing",
				occupied.Result, occupied.UnknownReason)
		}
		if occupied.SelectedValue != nil || occupied.ObservationID != nil || occupied.ObservedAt != nil {
			t.Fatalf("absent State node retained fabricated evidence: %#v", occupied)
		}
	default:
		t.Fatalf("unknown occupied expectation %q", want)
	}
}

// assertConditionsUnknownSkipDetail proves the unknown Skip is readable,
// explains the missing State, and is not a Condition evaluation of the wrong
// shape.
func assertConditionsUnknownSkipDetail(
	ctx context.Context,
	t *testing.T,
	httpAddress, automationID, entryID, rootID, darkID, occupiedID string,
) {
	t.Helper()
	entry := conditionsHistoryDetail(ctx, t, httpAddress, automationID, entryID)
	if entry.Kind != "skip" || entry.Skip == nil {
		t.Fatalf("history entry = %#v", entry)
	}
	skip := entry.Skip
	if skip.Source != "device_fact" || skip.Fact == nil || len(skip.MatchedTriggers) != 1 {
		t.Fatalf("automatic skip provenance = %#v", skip)
	}
	if skip.ConditionDecision.Mode != "evaluated" || skip.ConditionDecision.Evaluation == nil {
		t.Fatalf("skip decision = %#v", skip.ConditionDecision)
	}
	if skip.ConditionDecision.Evaluation.Result != "unknown" ||
		skip.ConditionDecision.Snapshot == nil || skip.ConditionDecision.Snapshot.ID != rootID {
		t.Fatalf("skip evaluation = %#v", skip.ConditionDecision.Evaluation)
	}
	dark := conditionsNodeResultByID(t, skip.ConditionDecision.Evaluation, darkID)
	if dark.Result != "true" || dark.UnknownReason != nil {
		t.Fatalf("dark skip node = %#v", dark)
	}
	occupied := conditionsNodeResultByID(t, skip.ConditionDecision.Evaluation, occupiedID)
	if occupied.Result != "unknown" || occupied.UnknownReason == nil ||
		*occupied.UnknownReason != "state_missing" {
		t.Fatalf("occupied skip node = %#v", occupied)
	}
	if occupied.SelectedValue != nil || occupied.ObservationID != nil {
		t.Fatalf("state_missing node retained fabricated evidence: %#v", occupied)
	}
}

// assertConditionsFalseSkipDetail proves a bright reading records
// conditions_false and retains the compared value.
func assertConditionsFalseSkipDetail(
	ctx context.Context,
	t *testing.T,
	httpAddress, automationID, entryID string,
	lux float64,
) {
	t.Helper()
	entry := conditionsHistoryDetail(ctx, t, httpAddress, automationID, entryID)
	if entry.Kind != "skip" || entry.Skip == nil || entry.Skip.Reason != "conditions_false" {
		t.Fatalf("bright skip entry = %#v", entry)
	}
	evaluation := entry.Skip.ConditionDecision.Evaluation
	if evaluation == nil || evaluation.Result != "false" {
		t.Fatalf("bright skip evaluation = %#v", evaluation)
	}
	dark := conditionsNodeResultByID(t, evaluation, "room-dark")
	if dark.Result != "false" || dark.UnknownReason != nil {
		t.Fatalf("bright dark node = %#v", dark)
	}
	var selected float64
	if err := json.Unmarshal(dark.SelectedValue, &selected); err != nil {
		t.Fatalf("bright selected value %s: %v", dark.SelectedValue, err)
	}
	if selected != lux {
		t.Fatalf("bright selected value = %v, want %v", selected, lux)
	}
}

// assertConditionsManualSkipDetail fetches the committed manual Skip through the
// history reference a blocked manual request returned.
func assertConditionsManualSkipDetail(
	ctx context.Context,
	t *testing.T,
	httpAddress, historyURL, historyID string,
) {
	t.Helper()
	response := sliceRequest(ctx, t, http.MethodGet, httpAddress, historyURL, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("manual skip history status = %d, body = %s",
			response.StatusCode, readSliceBody(t, response))
	}
	var entry conditionsHistoryEntry
	if err := json.NewDecoder(response.Body).Decode(&entry); err != nil {
		t.Fatal(err)
	}
	if entry.Kind != "skip" || entry.Skip == nil || entry.Skip.ID != historyID {
		t.Fatalf("manual skip entry = %#v", entry)
	}
	skip := entry.Skip
	if skip.Source != "manual" || skip.Fact != nil || len(skip.MatchedTriggers) != 0 {
		t.Fatalf("manual skip provenance = %#v", skip)
	}
	if skip.Reason != "conditions_false" || skip.ConditionDecision.Mode != "evaluated" ||
		skip.ConditionDecision.BypassRequested {
		t.Fatalf("manual skip decision = %#v reason %q", skip.ConditionDecision, skip.Reason)
	}
}

// assertConditionsRunCommand proves one Run's ordered Step reached a verified
// terminal Command for the expected Entity and value, and that a fact-driven Run
// still names its immutable matched Trigger.
func assertConditionsRunCommand(
	ctx context.Context,
	t *testing.T,
	httpAddress, entityID, wantMatchedTrigger string,
	run conditionsRun,
) {
	t.Helper()
	if wantMatchedTrigger == "" {
		if len(run.MatchedTriggerIDs) != 0 {
			t.Fatalf("manual run matched trigger IDs = %v, want none", run.MatchedTriggerIDs)
		}
	} else if len(run.MatchedTriggerIDs) != 1 || run.MatchedTriggerIDs[0] != wantMatchedTrigger {
		t.Fatalf("run matched trigger IDs = %v, want %q", run.MatchedTriggerIDs, wantMatchedTrigger)
	}
	if len(run.Steps) != 1 || run.Steps[0].Status != "satisfied" ||
		run.Steps[0].VerifiedCommandID == nil {
		t.Fatalf("run steps = %#v", run.Steps)
	}
	assertTerminalLinkedCommand(ctx, t, httpAddress, *run.Steps[0].VerifiedCommandID, entityID, true)
}

// conditionsManualRun POSTs one manual admission with the given optional body.
func conditionsManualRun(
	ctx context.Context,
	t *testing.T,
	httpAddress, automationID, body string,
) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+httpAddress+"/v1/automations/"+automationID+"/runs", reader)
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

func decodeConditionsProblem(t *testing.T, response *http.Response) conditionsProblem {
	t.Helper()
	defer response.Body.Close()
	var problem conditionsProblem
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	return problem
}

// conditionsLocationRunID reads the Run identity out of the 202 Location header,
// which ends in the admitted Run's history path.
func conditionsLocationRunID(t *testing.T, response *http.Response) string {
	t.Helper()
	location := response.Header.Get("Location")
	for position := len(location) - 1; position >= 0; position-- {
		if location[position] == '/' {
			runID := location[position+1:]
			if runID == "" {
				t.Fatalf("manual bypass Location = %q", location)
			}
			return runID
		}
	}
	t.Fatalf("manual bypass Location = %q", location)
	return ""
}

// conditionsCommandCount counts durable Commands for one Entity.
func conditionsCommandCount(ctx context.Context, t *testing.T, httpAddress, entityID string) int {
	t.Helper()
	response := sliceRequest(ctx, t, http.MethodGet, httpAddress,
		"/v1/entities/"+entityID+"/commands", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("entity commands status = %d, body = %s",
			response.StatusCode, readSliceBody(t, response))
	}
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	return len(page.Items)
}

// conditionsSetEntity issues one direct Set so a later Run's Set is an applied
// change, and requires the Command to satisfy before returning.
func conditionsSetEntity(
	ctx context.Context,
	t *testing.T,
	httpAddress, entityID string,
	value bool,
) {
	t.Helper()
	status, body, err := postFactsCommand(ctx, httpAddress, entityID, value)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("direct set status = %d, body = %s", status, body)
	}
	var result struct {
		Status string `json:"status"`
	}
	if unmarshalErr := json.Unmarshal(body, &result); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if result.Status != "satisfied" {
		t.Fatalf("direct set result = %s", body)
	}
}
