package scripted_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

type fakeEvidence struct {
	session *fakeSession
}

func (evidence *fakeEvidence) PublishObservation(
	_ context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	evidence.session.mu.Lock()
	defer evidence.session.mu.Unlock()
	evidence.session.linked = append(evidence.session.linked, observation)
	return adapter.ObservationID(fmt.Sprintf("obs_%d", len(evidence.session.linked))), nil
}

type fakeResponder struct {
	session  *fakeSession
	accepted bool
	rejected string
	// onAccept runs inside Accept, before the handler sees the outcome, so a
	// test can land a concurrent write while the responder blocks the handler.
	onAccept func()
}

func (responder *fakeResponder) Accept() (adapter.CommandEvidence, error) {
	responder.accepted = true
	if responder.onAccept != nil {
		responder.onAccept()
	}
	return &fakeEvidence{session: responder.session}, nil
}

func (responder *fakeResponder) Reject(message string) error {
	responder.rejected = message
	return nil
}

func (responder *fakeResponder) RejectUnavailable(message string) error {
	responder.rejected = "unavailable:" + message
	return nil
}

type fakeSession struct {
	mu                    sync.Mutex
	observations          []adapter.Observation
	issuedObservationIDs  []adapter.ObservationID
	linked                []adapter.Observation
	events                []adapter.EntityEvent
	issuedEventIDs        []adapter.EntityEventID
	health                []adapter.HealthReport
	availability          [][]adapter.EntityAvailabilityReport
	lastResponder         *fakeResponder
	observationPublishErr error
	eventPublishErr       error
}

func (session *fakeSession) PublishObservation(
	_ context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.observations = append(session.observations, observation)
	issued := adapter.ObservationID(fmt.Sprintf("obs_%d", len(session.observations)))
	session.issuedObservationIDs = append(session.issuedObservationIDs, issued)
	// The SDK returns the ID it minted alongside some failures. Control
	// publishes must not surface that as a success ID.
	if session.observationPublishErr != nil {
		return issued, session.observationPublishErr
	}
	return issued, nil
}

func (session *fakeSession) PublishEntityEvent(
	_ context.Context,
	event adapter.EntityEvent,
) (adapter.EntityEventID, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.events = append(session.events, event)
	issued := adapter.EntityEventID(fmt.Sprintf("evt_%d", len(session.events)))
	session.issuedEventIDs = append(session.issuedEventIDs, issued)
	if session.eventPublishErr != nil {
		return issued, session.eventPublishErr
	}
	return issued, nil
}

// issuedObservations returns the Observation IDs the fake Session minted, in
// publication order: the oracle control publishes are asserted against.
func (session *fakeSession) issuedObservations() []adapter.ObservationID {
	session.mu.Lock()
	defer session.mu.Unlock()
	return append([]adapter.ObservationID(nil), session.issuedObservationIDs...)
}

// issuedEvents returns the Entity Event IDs the fake Session minted, in
// publication order.
func (session *fakeSession) issuedEvents() []adapter.EntityEventID {
	session.mu.Lock()
	defer session.mu.Unlock()
	return append([]adapter.EntityEventID(nil), session.issuedEventIDs...)
}

// failObservationPublishes makes every later PublishObservation fail with a
// minted-but-unusable ID, mirroring the SDK's ambiguous-failure behaviour.
func (session *fakeSession) failObservationPublishes(err error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.observationPublishErr = err
}

func (session *fakeSession) failEventPublishes(err error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.eventPublishErr = err
}

func (session *fakeSession) SetHealth(
	_ context.Context,
	report adapter.HealthReport,
) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.health = append(session.health, report)
	return nil
}

// fakeAvailabilityBatchLimit mirrors the SDK's per-call limit (1-256 reports),
// so a runtime that sends every report in one batch fails here instead of
// silently passing.
const fakeAvailabilityBatchLimit = 256

func (session *fakeSession) ReportEntityAvailability(
	_ context.Context,
	reports []adapter.EntityAvailabilityReport,
) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.availability = append(session.availability, reports)
	if len(reports) < 1 || len(reports) > fakeAvailabilityBatchLimit {
		return fmt.Errorf("entity availability batch must contain 1-256 reports, got %d", len(reports))
	}
	return nil
}

func (session *fakeSession) dispatch(
	ctx context.Context,
	runtime *scripted.Runtime,
	entityID, operation string,
	parameters json.RawMessage,
) *fakeResponder {
	return session.dispatchDuringAccept(ctx, runtime, entityID, operation, parameters, nil)
}

// dispatchDuringAccept dispatches one Command and runs duringAccept while the
// handler is blocked in the responder's Accept, so a test can interleave a
// concurrent write with an in-flight Command.
func (session *fakeSession) dispatchDuringAccept(
	ctx context.Context,
	runtime *scripted.Runtime,
	entityID, operation string,
	parameters json.RawMessage,
	duringAccept func(),
) *fakeResponder {
	responder := &fakeResponder{session: session, onAccept: duringAccept}
	session.mu.Lock()
	session.lastResponder = responder
	session.mu.Unlock()
	handler := runtime.CommandHandler()
	err := handler(ctx, adapter.Command{
		EntityID:      entityID,
		OperationName: operation,
		Parameters:    parameters,
	}, responder)
	if err != nil {
		panic(err)
	}
	return responder
}

// manyEntityDevice builds one Device holding entityCount Power Entities, so a
// test can spread the spec maximum of 64 Entities across several Devices.
func manyEntityDevice(deviceIndex, entityCount int) scripted.DeviceSpec {
	device := scripted.DeviceSpec{
		BindingKey: fmt.Sprintf("simulated-light-%d", deviceIndex),
		Name:       fmt.Sprintf("Simulated light %d", deviceIndex),
		Kind:       "light",
	}
	for entityIndex := range entityCount {
		device.Entities = append(device.Entities, scripted.EntitySpec{
			Key:     fmt.Sprintf("power-%d", entityIndex),
			Name:    fmt.Sprintf("Power %d", entityIndex),
			Type:    "hearth.power/v1",
			Support: map[string]any{"state": map[string]any{}, "operations": map[string]any{"set": map[string]any{}}},
			Initial: true,
		})
	}
	return device
}

func powerDevice() scripted.DeviceSpec {
	return scripted.DeviceSpec{
		BindingKey: "simulated-light",
		Name:       "Simulated light",
		Kind:       "light",
		Entities: []scripted.EntitySpec{{
			Key:     "power",
			Name:    "Power",
			Type:    "hearth.power/v1",
			Support: map[string]any{"state": map[string]any{}, "operations": map[string]any{"set": map[string]any{}}},
			Initial: true,
			Outputs: &scripted.OutputsSpec{
				Interval: scripted.Duration(5_000_000_000),
				Values:   []any{true, false},
			},
		}},
	}
}

func eventDevice() scripted.DeviceSpec {
	return scripted.DeviceSpec{
		BindingKey: "simulated-button",
		Name:       "Simulated button",
		Kind:       "sensor",
		Entities: []scripted.EntitySpec{{
			Key:  "events",
			Name: "Events",
			Type: "hearth.enumevent/v1",
			Support: map[string]any{
				"state":      map[string]any{},
				"operations": map[string]any{},
				"events":     map[string]any{"names": []any{"single_press", "double_press"}},
			},
			Outputs: &scripted.OutputsSpec{
				Interval: scripted.Duration(1_000_000_000),
				Values:   []any{"single_press", "double_press"},
			},
		}},
	}
}

func attachOne(t *testing.T, runtime *scripted.Runtime, bindingKey, key, entityID string) {
	t.Helper()
	if err := runtime.Attach([]adapter.Binding{{
		BindingKey: bindingKey,
		Entities:   []adapter.EntityBinding{{Key: key, EntityID: entityID}},
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeInitializesAndLoopsScript(t *testing.T) {
	t.Parallel()
	session := &fakeSession{}
	runtime, err := scripted.New(session, []scripted.DeviceSpec{powerDevice()})
	if err != nil {
		t.Fatal(err)
	}
	attachOne(t, runtime, "simulated-light", "power", "ent_power")
	if initErr := runtime.Initialize(context.Background()); initErr != nil {
		t.Fatal(initErr)
	}
	if len(session.health) != 1 || session.health[0].Status != adapter.HealthHealthy {
		t.Fatalf("health reports = %+v, want one healthy", session.health)
	}
	if len(session.observations) != 1 || string(session.observations[0].Value) != "true" {
		t.Fatalf("initial observations = %+v, want [true]", session.observations)
	}
	ctx := context.Background()
	if tickErr := runtime.Tick(ctx, "ent_power"); tickErr != nil {
		t.Fatal(tickErr)
	}
	if tickErr := runtime.Tick(ctx, "ent_power"); tickErr != nil {
		t.Fatal(tickErr)
	}
	var values []string
	for _, observation := range session.observations {
		values = append(values, string(observation.Value))
	}
	want := []string{"true", "false", "true"}
	if fmt.Sprint(values) != fmt.Sprint(want) {
		t.Fatalf("published values = %v, want %v", values, want)
	}
}

func TestRuntimePauseHoldsScript(t *testing.T) {
	t.Parallel()
	session := &fakeSession{}
	runtime, err := scripted.New(session, []scripted.DeviceSpec{powerDevice()})
	if err != nil {
		t.Fatal(err)
	}
	attachOne(t, runtime, "simulated-light", "power", "ent_power")
	if initErr := runtime.Initialize(context.Background()); initErr != nil {
		t.Fatal(initErr)
	}
	if pauseErr := runtime.SetPaused("ent_power", true); pauseErr != nil {
		t.Fatal(pauseErr)
	}
	if tickErr := runtime.Tick(context.Background(), "ent_power"); tickErr != nil {
		t.Fatal(tickErr)
	}
	if len(session.observations) != 1 {
		t.Fatalf("paused tick published, observations = %d, want 1", len(session.observations))
	}
}

func TestRuntimeCommandAppliesParametersAndPublishes(t *testing.T) {
	t.Parallel()
	session := &fakeSession{}
	runtime, err := scripted.New(session, []scripted.DeviceSpec{powerDevice()})
	if err != nil {
		t.Fatal(err)
	}
	attachOne(t, runtime, "simulated-light", "power", "ent_power")
	if initErr := runtime.Initialize(context.Background()); initErr != nil {
		t.Fatal(initErr)
	}
	responder := session.dispatch(
		context.Background(), runtime, "ent_power", "set", json.RawMessage(`{"value":false}`),
	)
	if !responder.accepted {
		t.Fatal("set Command was not accepted")
	}
	if len(session.linked) != 1 || string(session.linked[0].Value) != "false" {
		t.Fatalf("linked observations = %+v, want [false]", session.linked)
	}
	infos := runtime.Snapshot()
	if len(infos) != 1 || string(infos[0].Current) != "false" {
		t.Fatalf("snapshot = %+v, want current false", infos)
	}
}

// TestRuntimeCommandPublishesValueCapturedBeforeAccept protects that a Command
// observation carries the State its own parameters produced even when a
// concurrent Tick replaces Entity state while the responder Accept blocks: the
// value is captured with the parameter application, not reread after Accept.
func TestRuntimeCommandPublishesValueCapturedBeforeAccept(t *testing.T) {
	t.Parallel()
	device := powerDevice()
	// Every scripted step is true while the Command applies false, so a value
	// reread after Accept is distinguishable from the Command's own value.
	device.Entities[0].Outputs.Values = []any{true, true, true}
	session := &fakeSession{}
	runtime, err := scripted.New(session, []scripted.DeviceSpec{device})
	if err != nil {
		t.Fatal(err)
	}
	attachOne(t, runtime, "simulated-light", "power", "ent_power")
	ctx := context.Background()
	if initErr := runtime.Initialize(ctx); initErr != nil {
		t.Fatal(initErr)
	}
	responder := session.dispatchDuringAccept(
		ctx, runtime, "ent_power", "set", json.RawMessage(`{"value":false}`),
		func() {
			// The interloper: a Tick that lands while Accept is in flight.
			if tickErr := runtime.Tick(ctx, "ent_power"); tickErr != nil {
				t.Fatalf("interloping tick: %v", tickErr)
			}
		},
	)
	if !responder.accepted {
		t.Fatal("set Command was not accepted")
	}
	if len(session.linked) != 1 {
		t.Fatalf("linked observations = %+v, want 1", session.linked)
	}
	if got := string(session.linked[0].Value); got != "false" {
		t.Fatalf("linked observation = %s, want false (the Command-applied value)", got)
	}
	// The interloper did land: the snapshot keeps the ticker's value while the
	// Command outcome kept its own.
	infos := runtime.Snapshot()
	if len(infos) != 1 || string(infos[0].Current) != "true" {
		t.Fatalf("snapshot = %+v, want the interloper current true", infos)
	}
}

// TestRuntimePublishNowUnsupportedEventLeavesSnapshotUnchanged protects that a
// rejected event publish does not record the unsupported name as the Entity's
// snapshot current: the name is validated before state changes, and a
// supported name still updates the snapshot.
func TestRuntimePublishNowUnsupportedEventLeavesSnapshotUnchanged(t *testing.T) {
	t.Parallel()
	session := &fakeSession{}
	runtime, err := scripted.New(session, []scripted.DeviceSpec{eventDevice()})
	if err != nil {
		t.Fatal(err)
	}
	attachOne(t, runtime, "simulated-button", "events", "ent_events")
	ctx := context.Background()
	if initErr := runtime.Initialize(ctx); initErr != nil {
		t.Fatal(initErr)
	}
	// Initialize published script values[0], which is also the snapshot current.
	if _, publishErr := runtime.PublishNow(
		ctx, "ent_events", json.RawMessage(`{"name":"triple_press"}`),
	); publishErr == nil {
		t.Fatal("unsupported event name published, want an error")
	}
	infos := runtime.Snapshot()
	if len(infos) != 1 || string(infos[0].Current) != `"single_press"` {
		t.Fatalf("snapshot after rejected event = %+v, want current \"single_press\"", infos)
	}
	if len(session.events) != 1 {
		t.Fatalf("events = %+v, want only the Initialize event", session.events)
	}
	if _, publishErr := runtime.PublishNow(
		ctx, "ent_events", json.RawMessage(`{"name":"double_press"}`),
	); publishErr != nil {
		t.Fatal(publishErr)
	}
	infos = runtime.Snapshot()
	if len(infos) != 1 || string(infos[0].Current) != `"double_press"` {
		t.Fatalf("snapshot after supported event = %+v, want current \"double_press\"", infos)
	}
}

// TestRuntimeInitializePagesEntityAvailability protects that Initialize reports
// availability in batches the SDK accepts, at most 256 reports per call. Specs
// allow 64 Entities on each of any number of Devices, so 5 Devices with 64
// Entities each must still deliver every one of their 320 reports.
func TestRuntimeInitializePagesEntityAvailability(t *testing.T) {
	t.Parallel()
	const (
		deviceCount       = 5
		entitiesPerDevice = 64
		wantReports       = deviceCount * entitiesPerDevice
	)
	devices := make([]scripted.DeviceSpec, 0, deviceCount)
	for deviceIndex := range deviceCount {
		devices = append(devices, manyEntityDevice(deviceIndex, entitiesPerDevice))
	}
	session := &fakeSession{}
	runtime, err := scripted.New(session, devices)
	if err != nil {
		t.Fatal(err)
	}
	bindings := make([]adapter.Binding, 0, len(devices))
	for _, device := range devices {
		binding := adapter.Binding{BindingKey: device.BindingKey}
		for _, entity := range device.Entities {
			binding.Entities = append(binding.Entities, adapter.EntityBinding{
				Key:      entity.Key,
				EntityID: device.BindingKey + "." + entity.Key,
			})
		}
		bindings = append(bindings, binding)
	}
	if attachErr := runtime.Attach(bindings); attachErr != nil {
		t.Fatal(attachErr)
	}
	if initErr := runtime.Initialize(context.Background()); initErr != nil {
		t.Fatal(initErr)
	}
	if len(session.availability) == 0 {
		t.Fatal("Session saw no availability report call")
	}
	delivered := make(map[string]adapter.EntityAvailabilityStatus, wantReports)
	for batchIndex, batch := range session.availability {
		if len(batch) > fakeAvailabilityBatchLimit {
			t.Fatalf("availability batch %d carried %d reports; the SDK accepts at most %d",
				batchIndex, len(batch), fakeAvailabilityBatchLimit)
		}
		for _, report := range batch {
			if _, duplicate := delivered[report.EntityID]; duplicate {
				t.Fatalf("availability report for %s delivered twice", report.EntityID)
			}
			delivered[report.EntityID] = report.Status
		}
	}
	if len(delivered) != wantReports {
		t.Fatalf("delivered %d availability reports, want %d", len(delivered), wantReports)
	}
	for entityID, status := range delivered {
		if status != adapter.AvailabilityAvailable {
			t.Fatalf("availability for %s = %q, want available", entityID, status)
		}
	}
}

func TestRuntimeCommandRejectBehavior(t *testing.T) {
	t.Parallel()
	device := powerDevice()
	device.Entities[0].Commands = map[string]scripted.CommandBehavior{
		"set": {Behavior: scripted.CommandBehaviorReject, Reason: "simulated upstream rejection"},
	}
	session := &fakeSession{}
	runtime, err := scripted.New(session, []scripted.DeviceSpec{device})
	if err != nil {
		t.Fatal(err)
	}
	attachOne(t, runtime, "simulated-light", "power", "ent_power")
	if initErr := runtime.Initialize(context.Background()); initErr != nil {
		t.Fatal(initErr)
	}
	responder := session.dispatch(
		context.Background(), runtime, "ent_power", "set", json.RawMessage(`{"value":false}`),
	)
	if responder.rejected != "simulated upstream rejection" {
		t.Fatalf("rejection = %q, want the configured reason", responder.rejected)
	}
	if len(session.linked) != 0 {
		t.Fatalf("rejected Command published %d linked observations", len(session.linked))
	}
}

func TestRuntimeCommandSilentAcceptPublishesNothing(t *testing.T) {
	t.Parallel()
	device := powerDevice()
	device.Entities[0].Commands = map[string]scripted.CommandBehavior{
		"set": {Behavior: scripted.CommandBehaviorAcceptSilent},
	}
	session := &fakeSession{}
	runtime, err := scripted.New(session, []scripted.DeviceSpec{device})
	if err != nil {
		t.Fatal(err)
	}
	attachOne(t, runtime, "simulated-light", "power", "ent_power")
	if initErr := runtime.Initialize(context.Background()); initErr != nil {
		t.Fatal(initErr)
	}
	responder := session.dispatch(
		context.Background(), runtime, "ent_power", "set", json.RawMessage(`{"value":false}`),
	)
	if !responder.accepted {
		t.Fatal("silent Command was not accepted")
	}
	if len(session.linked) != 0 || len(session.observations) != 1 {
		t.Fatal("silent accept must publish no outcome Observation")
	}
}

func TestRuntimeRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*scripted.DeviceSpec){
		"unknown type": func(device *scripted.DeviceSpec) {
			device.Entities[0].Type = "hearth.toaster/v9"
		},
		"invalid initial": func(device *scripted.DeviceSpec) {
			device.Entities[0].Initial = "on"
		},
		"invalid script value": func(device *scripted.DeviceSpec) {
			device.Entities[0].Outputs.Values[1] = "on"
		},
		"unavailable without reason": func(device *scripted.DeviceSpec) {
			device.Entities[0].Available = new(false)
		},
		"bad behavior": func(device *scripted.DeviceSpec) {
			device.Entities[0].Commands = map[string]scripted.CommandBehavior{
				"set": {Behavior: "explode"},
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			device := powerDevice()
			mutate(&device)
			if _, err := scripted.New(&fakeSession{}, []scripted.DeviceSpec{device}); err == nil {
				t.Fatalf("invalid config accepted (%s)", name)
			}
		})
	}
	if _, err := scripted.New(&fakeSession{}, nil); err == nil {
		t.Fatal("empty Devices accepted, want an error")
	}
	duplicates := []scripted.DeviceSpec{powerDevice(), powerDevice()}
	if _, err := scripted.New(&fakeSession{}, duplicates); err == nil {
		t.Fatal("duplicate binding_key accepted, want an error")
	}
}

func TestRuntimeEmitsEntityEventsInOrder(t *testing.T) {
	t.Parallel()
	session := &fakeSession{}
	runtime, err := scripted.New(session, []scripted.DeviceSpec{eventDevice()})
	if err != nil {
		t.Fatal(err)
	}
	attachOne(t, runtime, "simulated-button", "events", "ent_events")
	ctx := context.Background()
	if initErr := runtime.Initialize(ctx); initErr != nil {
		t.Fatal(initErr)
	}
	if tickErr := runtime.Tick(ctx, "ent_events"); tickErr != nil {
		t.Fatal(tickErr)
	}
	if len(session.events) != 2 {
		t.Fatalf("events = %+v, want 2", session.events)
	}
	if session.events[0].Name != "single_press" || session.events[1].Name != "double_press" {
		t.Fatalf("events = %+v, want single_press then double_press", session.events)
	}
	if _, publishErr := runtime.PublishNow(
		ctx,
		"ent_events",
		json.RawMessage(`{"name":"double_press"}`),
	); publishErr != nil {
		t.Fatal(publishErr)
	}
	if len(session.events) != 3 || session.events[2].Name != "double_press" {
		t.Fatalf("events after PublishNow = %+v", session.events)
	}
	if _, publishErr := runtime.PublishNow(
		ctx,
		"ent_events",
		json.RawMessage(`{"name":"triple_press"}`),
	); publishErr == nil {
		t.Fatal("unsupported event name published, want an error")
	}
}

// TestRuntimePublishNowReturnsSessionIssuedIDs protects that a control publish
// reports the canonical ID the Session issued for that publication — not a
// synthetic, constant, or stale one — and that the Observation ID and Event ID
// stay mutually exclusive.
func TestRuntimePublishNowReturnsSessionIssuedIDs(t *testing.T) {
	t.Parallel()
	session := &fakeSession{}
	runtime, err := scripted.New(session, []scripted.DeviceSpec{powerDevice()})
	if err != nil {
		t.Fatal(err)
	}
	attachOne(t, runtime, "simulated-light", "power", "ent_power")
	ctx := context.Background()
	if initErr := runtime.Initialize(ctx); initErr != nil {
		t.Fatal(initErr)
	}
	first, firstErr := runtime.PublishNow(ctx, "ent_power", json.RawMessage(`false`))
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	second, secondErr := runtime.PublishNow(ctx, "ent_power", nil)
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	issued := session.issuedObservations()
	// One Observation at Initialize, then one per control publish.
	if len(issued) != 3 {
		t.Fatalf("Session issued %d Observation IDs, want 3", len(issued))
	}
	if first.ObservationID != issued[1] || second.ObservationID != issued[2] {
		t.Fatalf("published IDs = %q, %q, want the Session-issued %q, %q",
			first.ObservationID, second.ObservationID, issued[1], issued[2])
	}
	if first.ObservationID == second.ObservationID {
		t.Fatal("two publishes returned the same Observation ID; the ID is stale or constant")
	}
	if first.EventID != "" || second.EventID != "" {
		t.Fatalf("State publishes returned Event IDs %q, %q", first.EventID, second.EventID)
	}
}

// TestRuntimePublishNowReturnsSessionIssuedEventIDs protects the event-source
// half of the same contract: each event publish reports that Session-issued
// Entity Event ID and no Observation ID.
func TestRuntimePublishNowReturnsSessionIssuedEventIDs(t *testing.T) {
	t.Parallel()
	session := &fakeSession{}
	runtime, err := scripted.New(session, []scripted.DeviceSpec{eventDevice()})
	if err != nil {
		t.Fatal(err)
	}
	attachOne(t, runtime, "simulated-button", "events", "ent_events")
	ctx := context.Background()
	if initErr := runtime.Initialize(ctx); initErr != nil {
		t.Fatal(initErr)
	}
	first, firstErr := runtime.PublishNow(ctx, "ent_events", json.RawMessage(`{"name":"double_press"}`))
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	second, secondErr := runtime.PublishNow(ctx, "ent_events", json.RawMessage(`{"name":"single_press"}`))
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	issued := session.issuedEvents()
	// One Event at Initialize, then one per control publish.
	if len(issued) != 3 {
		t.Fatalf("Session issued %d Entity Event IDs, want 3", len(issued))
	}
	if first.EventID != issued[1] || second.EventID != issued[2] {
		t.Fatalf("published Event IDs = %q, %q, want the Session-issued %q, %q",
			first.EventID, second.EventID, issued[1], issued[2])
	}
	if first.EventID == second.EventID {
		t.Fatal("two publishes returned the same Entity Event ID; the ID is stale or constant")
	}
	if first.ObservationID != "" || second.ObservationID != "" {
		t.Fatalf("event publishes returned Observation IDs %q, %q",
			first.ObservationID, second.ObservationID)
	}
}

// TestRuntimePublishEnvelopeReturnsSessionIssuedIDs covers the control-channel
// entry point itself for both Entity kinds.
func TestRuntimePublishEnvelopeReturnsSessionIssuedIDs(t *testing.T) {
	t.Parallel()
	session := &fakeSession{}
	runtime, err := scripted.New(session, []scripted.DeviceSpec{powerDevice(), eventDevice()})
	if err != nil {
		t.Fatal(err)
	}
	if attachErr := runtime.Attach([]adapter.Binding{
		{BindingKey: "simulated-light", Entities: []adapter.EntityBinding{{Key: "power", EntityID: "ent_power"}}},
		{BindingKey: "simulated-button", Entities: []adapter.EntityBinding{{Key: "events", EntityID: "ent_events"}}},
	}); attachErr != nil {
		t.Fatal(attachErr)
	}
	ctx := context.Background()
	if initErr := runtime.Initialize(ctx); initErr != nil {
		t.Fatal(initErr)
	}
	state, stateErr := runtime.PublishEnvelope(ctx, "ent_power", json.RawMessage(`{"value":false}`))
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	event, eventErr := runtime.PublishEnvelope(ctx, "ent_events", json.RawMessage(`{"name":"double_press"}`))
	if eventErr != nil {
		t.Fatal(eventErr)
	}
	observations := session.issuedObservations()
	events := session.issuedEvents()
	if len(observations) != 2 || len(events) != 2 {
		t.Fatalf("Session issued %d Observations and %d Events, want 2 of each",
			len(observations), len(events))
	}
	if state.ObservationID != observations[1] || state.EventID != "" {
		t.Fatalf("State envelope result = %+v, want Observation %q only", state, observations[1])
	}
	if event.EventID != events[1] || event.ObservationID != "" {
		t.Fatalf("event envelope result = %+v, want Event %q only", event, events[1])
	}
}

// TestRuntimePublishFailuresReturnNoPublicationID protects that a failed
// publish reports the zero PublicationResult: an ID is evidence of storage, so
// a failure that still carries a minted ID — the SDK returns one from some
// retry and validation failures — must not surface it as a success ID.
func TestRuntimePublishFailuresReturnNoPublicationID(t *testing.T) {
	t.Parallel()
	session := &fakeSession{}
	runtime, err := scripted.New(session, []scripted.DeviceSpec{powerDevice(), eventDevice()})
	if err != nil {
		t.Fatal(err)
	}
	if attachErr := runtime.Attach([]adapter.Binding{
		{BindingKey: "simulated-light", Entities: []adapter.EntityBinding{{Key: "power", EntityID: "ent_power"}}},
		{BindingKey: "simulated-button", Entities: []adapter.EntityBinding{{Key: "events", EntityID: "ent_events"}}},
	}); attachErr != nil {
		t.Fatal(attachErr)
	}
	ctx := context.Background()
	if initErr := runtime.Initialize(ctx); initErr != nil {
		t.Fatal(initErr)
	}
	assertNoSuccessID := func(label string, result scripted.PublicationResult, publishErr error) {
		t.Helper()
		if publishErr == nil {
			t.Fatalf("%s reported success", label)
		}
		if result.ObservationID != "" || result.EventID != "" {
			t.Fatalf("%s returned IDs %+v alongside error %v", label, result, publishErr)
		}
	}
	invalid, invalidErr := runtime.PublishNow(ctx, "ent_power", json.RawMessage(`"on"`))
	assertNoSuccessID("invalid State", invalid, invalidErr)
	unknown, unknownErr := runtime.PublishNow(ctx, "ent_missing", json.RawMessage(`true`))
	assertNoSuccessID("unknown Entity", unknown, unknownErr)
	session.failObservationPublishes(errors.New("jetstream unavailable"))
	failed, failedErr := runtime.PublishNow(ctx, "ent_power", json.RawMessage(`false`))
	assertNoSuccessID("publisher failure", failed, failedErr)
	observations := session.issuedObservations()
	if observations[len(observations)-1] == "" {
		t.Fatal("fake Session minted no Observation ID before failing, so the test proves nothing")
	}
	session.failEventPublishes(errors.New("jetstream unavailable"))
	eventFailed, eventFailedErr := runtime.PublishNow(ctx, "ent_events", json.RawMessage(`{"name":"double_press"}`))
	assertNoSuccessID("event publisher failure", eventFailed, eventFailedErr)
	events := session.issuedEvents()
	if events[len(events)-1] == "" {
		t.Fatal("fake Session minted no Entity Event ID before failing, so the test proves nothing")
	}
}

// TestRuntimeSnapshotCarriesNoPublicationIDs protects that listing Entities
// keeps the flat snapshot shape: publication IDs belong to a publish response,
// never to EntityInfo.
func TestRuntimeSnapshotCarriesNoPublicationIDs(t *testing.T) {
	t.Parallel()
	session := &fakeSession{}
	runtime, err := scripted.New(session, []scripted.DeviceSpec{powerDevice()})
	if err != nil {
		t.Fatal(err)
	}
	attachOne(t, runtime, "simulated-light", "power", "ent_power")
	if initErr := runtime.Initialize(context.Background()); initErr != nil {
		t.Fatal(initErr)
	}
	encoded, marshalErr := json.Marshal(runtime.Snapshot())
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	var rows []map[string]json.RawMessage
	if unmarshalErr := json.Unmarshal(encoded, &rows); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if len(rows) != 1 {
		t.Fatalf("snapshot %s has %d rows, want 1", encoded, len(rows))
	}
	for _, field := range []string{"binding_key", "key", "entity_id", "entity_type", "event_source", "paused", "current"} {
		if _, present := rows[0][field]; !present {
			t.Fatalf("snapshot %s dropped field %s", encoded, field)
		}
	}
	for _, field := range []string{"observation_id", "event_id"} {
		if _, present := rows[0][field]; present {
			t.Fatalf("snapshot %s exposes publication ID field %s", encoded, field)
		}
	}
}
