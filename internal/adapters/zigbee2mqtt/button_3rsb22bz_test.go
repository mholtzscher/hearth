package zigbee2mqtt //nolint:testpackage // Tests exercise package-private discovery, decoding, and runtime routes.

// Fixture provenance: testdata/bridge-devices-3rsb22bz.json and
// testdata/state-3rsb22bz.json are handcrafted minimal sanitized shapes
// modeled on the live Third Reality 3RSB22BZ smart button capture from host
// wanda on 2026-09-10 (Zigbee2MQTT 2.14.1, firmware software_build_id
// v1.00.35, friendly_name sanitized). Sanitization changed IEEE addresses,
// friendly names, and descriptions only. Model, vendor, build, expose
// nesting, type, name, property, endpoint, access, values, numeric bounds,
// and the non-retained state payload values are retained from the live
// evidence. No real hardware is accessed.
//
// Zigbee2MQTT 2.14.1 lib/state.ts keeps action (and action_*) out of
// cache-expanded and startup-cache messages. A broker-retained live payload
// can still carry action, so only a non-retained message with a present
// supported action is one Event occurrence.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const buttonFixtureName = "bridge-devices-3rsb22bz.json"

func mustButtonDevice(t *testing.T) discoveredDevice {
	t.Helper()
	return mustDiscoveredFixtureDevice(t, buttonFixtureName)
}

func buttonPlanByKey(t *testing.T, device discoveredDevice, key string) entityPlan {
	t.Helper()
	for _, entity := range device.Entities {
		if entity.Descriptor.Key == key {
			return entity
		}
	}
	t.Fatalf("Entity %q missing from %v", key, entityKeys(device.Entities))
	return entityPlan{}
}

func recordedEntityEvents(session *fakeSession) []adapter.EntityEvent {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return append([]adapter.EntityEvent(nil), session.entityEvents...)
}

func recordedObservations(session *fakeSession) []adapter.Observation {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return append([]adapter.Observation(nil), session.observations...)
}

func newButtonRuntime(t *testing.T) (*Adapter, *fakeSession, runtimeDevice) {
	t.Helper()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, _, device := commandReadyAdapterFor(t, recorder, session, buttonFixtureName)
	return z2m, session, device
}

func publishButtonState(z2m *Adapter, device runtimeDevice, payload string, retained bool) error {
	return z2m.publishDeviceState(context.Background(), 1, 1, device, mqttMessage{
		Topic:      "zigbee2mqtt/" + device.friendly,
		Payload:    []byte(payload),
		Retained:   retained,
		ReceivedAt: time.Now().UTC(),
	})
}

func publishButtonMessage(t *testing.T, z2m *Adapter, device runtimeDevice, payload string, retained bool) {
	t.Helper()
	if err := publishButtonState(z2m, device, payload, retained); err != nil {
		t.Fatal(err)
	}
}

// This test protects the captured 3RSB22BZ discovery: the button must keep
// the sensor kind with exactly the battery, linkquality, and action Entities
// in planner order, and the action Entity must carry exactly the observed
// enumevent support. It fails if the event names change, reorder, or gain
// State or Operations.
func TestDiscoverCapturedButton3RSB22BZ(t *testing.T) {
	t.Parallel()
	device := mustButtonDevice(t)
	if device.IEEEAddress != "0x00124b0024abcd04" || device.FriendlyName != "fixture-button-3rsb" ||
		device.Registration.BindingKey != "z2m-00124b0024abcd04" || device.Model != "3RSB22BZ" {
		t.Fatalf("Device identity = %#v", device)
	}
	if device.Registration.Device.Kind != "sensor" {
		t.Fatalf("Device kind = %q, want sensor", device.Registration.Device.Kind)
	}
	ieee := "0x00124b0024abcd04"
	want := []adapter.EntityDescriptor{
		{
			Key: "battery", ExternalID: ieee + "/root/battery", Name: "Battery",
			Type: "hearth.measurement/v1",
			Support: json.RawMessage(
				`{"state":{"maximum":100,"measurement_kind":"battery_level","minimum":0,"unit":"%"},"operations":{}}`,
			),
		},
		{
			Key: "linkquality", ExternalID: ieee + "/root/linkquality", Name: "Link Quality",
			Type:    "hearth.numericsensor/v1",
			Support: json.RawMessage(`{"state":{"maximum":255,"minimum":0,"unit":"lqi"},"operations":{}}`),
		},
		{
			Key: "action", ExternalID: ieee + "/root/action", Name: "Action",
			Type: "hearth.enumevent/v1",
			Support: json.RawMessage(
				`{"state":{},"operations":{},"events":{"names":["single","double","hold","release"]}}`,
			),
		},
	}
	if !reflect.DeepEqual(device.Registration.Entities, want) {
		t.Fatalf("Entity descriptors = %#v, want %#v", device.Registration.Entities, want)
	}
	assertButtonEventPlan(t, device)
	if err := validateEntityPlans(device.Entities); err != nil {
		t.Fatalf("button plans rejected: %v", err)
	}
}

// This test protects the Event freshness boundary and fails if an action
// expose whose property is outside Zigbee2MQTT's action cache-exclusion
// namespace registers and can later fabricate or coalesce occurrences.
func TestPlanActionEventRequiresCacheExcludedProperty(t *testing.T) {
	t.Parallel()
	device := upstreamDevice{
		Definition: &upstreamDefinition{Exposes: []upstreamExpose{{
			Type: upstreamExposeEnum, Name: actionExposeName, Property: "gesture", Access: exposePublishAccessBit,
			Values: []string{"single", "double"},
		}}},
	}
	contribution := planActionEvent(devicePlanningInput{
		IEEE: "0x00124b0024abcdef", Exposes: newExposeIndex(device),
	})
	if len(contribution.Entities) != 0 {
		t.Fatalf("unverified action property planned as an Event: %#v", contribution.Entities)
	}
}

// assertButtonEventPlan pins the seam between the event source and its
// stateful siblings: action decodes Events only, while battery and
// linkquality decode State only. It fails if the action plan regresses to a
// mute descriptor or the sensors start decoding Events.
func assertButtonEventPlan(t *testing.T, device discoveredDevice) {
	t.Helper()
	action := buttonPlanByKey(t, device, "action")
	if action.StatePolicy != entityStateless || len(action.StateProperties) != 0 || action.DecodeState != nil ||
		len(action.GetProperties) != 0 || action.TranslateCommand != nil || action.DecodeEvent == nil {
		t.Fatalf("action plan = %#v, want stateless event source with no route or refresh", action)
	}
	if len(action.EventProperties) != 1 || action.EventProperties[0] != "action" {
		t.Fatalf("action event properties = %v, want the discovered source property", action.EventProperties)
	}
	for _, key := range []string{"battery", "linkquality"} {
		plan := buttonPlanByKey(t, device, key)
		if plan.StatePolicy != entityStateful || plan.DecodeEvent != nil || plan.DecodeState == nil {
			t.Fatalf("%s plan = %#v, want stateful sensor without events", key, plan)
		}
	}
}

// This test protects the read-only action surface: the button registers no
// command route and no get refresh for action. It fails if the event Entity
// gains a translator or a refresh property, or if a startup get requests an
// action value the device never caches.
func TestButtonActionKeepsReadOnlyRoutesAndRefresh(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m := newRuntimeAdapter(t, session, &fakeDialer{})
	inventory, err := discoverInventory(readFixture(t, buttonFixtureName))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := z2m.buildRouteSnapshot(context.Background(), 1, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.routes) != 0 {
		t.Fatalf("routes = %#v, want none for a read-only button", snapshot.routes)
	}
	if _, exists := snapshot.devices["fixture-button-3rsb"]; !exists {
		t.Fatalf("devices = %#v", snapshot.devices)
	}
	connection := newFakeConnection(recorder)
	if err = z2m.requestCurrentState(context.Background(), connection, inventory, snapshot); err != nil {
		t.Fatal(err)
	}
	connection.mutex.Lock()
	published := append([]mqttPublication(nil), connection.published...)
	connection.mutex.Unlock()
	if len(published) != 1 || published[0].payload != `{"battery":""}` || published[0].retained {
		t.Fatalf("startup refresh = %#v, want only a non-retained battery get", published)
	}
	// The captured action message is projected as sensor State plus one Event,
	// never as an action Observation.
	entities := bindPlans(inventory.Devices[0].Entities)
	states, issues, err := decodeDeviceState(readFixture(t, "state-3rsb22bz.json"), entities, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 || len(states) != 2 ||
		states[0].entityID != "entity-battery" || states[1].entityID != "entity-linkquality" {
		t.Fatalf("captured states = %#v, issues = %#v", states, issues)
	}
	for _, state := range states {
		if state.entityID == "entity-action" {
			t.Fatal("action produced a State Observation")
		}
	}
	events, eventIssues, err := decodeDeviceEvents(readFixture(t, "state-3rsb22bz.json"), entities)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventIssues) != 0 || len(events) != 1 ||
		events[0] != (adapter.EntityEvent{EntityID: "entity-action", Name: "single"}) {
		t.Fatalf("captured events = %#v, issues = %#v", events, eventIssues)
	}
}

// This test protects one-for-one fresh action publication and fails if a
// fresh action message is deduplicated, dropped, or published without its
// sibling sensor State.
func TestButtonFreshActionPublishesOneEvent(t *testing.T) {
	t.Parallel()
	z2m, session, device := newButtonRuntime(t)
	publishButtonMessage(t, z2m, device, `{"action":"single","battery":100,"linkquality":42}`, false)
	waitFor(t, func() bool { return len(recordedEntityEvents(session)) == 1 })
	events := recordedEntityEvents(session)
	if events[0] != (adapter.EntityEvent{EntityID: "entity-action", Name: "single"}) {
		t.Fatalf("events = %#v", events)
	}
	observations := recordedObservations(session)
	if len(observations) != 2 || observations[0].EntityID != "entity-battery" ||
		observations[1].EntityID != "entity-linkquality" {
		t.Fatalf("observations = %#v", observations)
	}
}

// This test protects occurrence identity and fails if two separate upstream
// reports of the same action collapse into one event through value
// deduplication.
func TestButtonRepeatedEqualActionsAreSeparateEvents(t *testing.T) {
	t.Parallel()
	z2m, session, device := newButtonRuntime(t)
	publishButtonMessage(t, z2m, device, `{"action":"single"}`, false)
	publishButtonMessage(t, z2m, device, `{"action":"single"}`, false)
	events := recordedEntityEvents(session)
	if len(events) != 2 || events[0] != events[1] {
		t.Fatalf("events = %#v, want two identical separate occurrences", events)
	}
}

// This test protects the retained-replay freshness rule and fails if a
// broker-retained payload carrying an action replays that action as an Event.
func TestButtonRetainedActionEmitsNoEvent(t *testing.T) {
	t.Parallel()
	z2m, session, device := newButtonRuntime(t)
	publishButtonMessage(t, z2m, device, `{"action":"double","battery":90}`, true)
	if events := recordedEntityEvents(session); len(events) != 0 {
		t.Fatalf("retained events = %#v, want none", events)
	}
	if observations := recordedObservations(session); len(observations) != 1 ||
		observations[0].EntityID != "entity-battery" {
		t.Fatalf("retained observations = %#v, want the battery sibling only", observations)
	}
}

// This test protects invalid action isolation and fails if empty, null,
// malformed, or unsupported values publish an event or suppress a valid
// sibling State observation from the same message.
func TestButtonInvalidActionsEmitNoEventAndKeepSiblings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		payload string
	}{
		{name: "unsupported name", payload: `{"action":"triple","battery":50}`},
		{name: "non-string value", payload: `{"action":42,"battery":50}`},
		{name: "null value", payload: `{"action":null,"battery":50}`},
		{name: "empty value", payload: `{"action":"","battery":50}`},
		{name: "object value", payload: `{"action":{"name":"single"},"battery":50}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			z2m, session, device := newButtonRuntime(t)
			publishButtonMessage(t, z2m, device, test.payload, false)
			if events := recordedEntityEvents(session); len(events) != 0 {
				t.Fatalf("invalid action events = %#v, want none", events)
			}
			observations := recordedObservations(session)
			if len(observations) != 1 || observations[0].EntityID != "entity-battery" {
				t.Fatalf("observations = %#v, want the battery sibling only", observations)
			}
		})
	}
}

// This test protects failure parity with ordinary Observation publication and
// fails if a non-cancellation Entity Event SDK failure is swallowed while the
// runtime keeps accepting messages.
func TestButtonEventPublishFailureStopsRuntime(t *testing.T) {
	t.Parallel()
	z2m, session, device := newButtonRuntime(t)
	session.eventHook = func(context.Context, adapter.EntityEvent) error {
		return errors.New("entity event storage failure")
	}
	// The failed publish either reports the failure directly or loses the race
	// with the runtime shutdown that the same failure triggers, so this call
	// deliberately asserts nothing about its own return value. Terminal
	// shutdown is the observable contract.
	_ = publishButtonState(z2m, device, `{"action":"single"}`, false)
	waitFor(t, func() bool {
		select {
		case <-z2m.runtimeDone:
			return true
		default:
			return false
		}
	})
	if err := publishButtonState(z2m, device, `{"action":"double"}`, false); err == nil {
		t.Fatal("publish after runtime stop was accepted")
	}
}

// This test protects Event occurrence preservation while routes are still
// inactive. A plausible defect is a latest-per-topic pending map: it would keep
// only the second action report and the later battery-only report would then
// replace that entry, so the bridge would replay zero Events where the button
// sent two. It fails if the pending queue coalesces non-retained action
// occurrences, reorders them, loses the latest ordinary State, or replays an
// occurrence twice.
func TestPendingButtonActionsReplayOnceInOrder(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	connection := newFakeConnection(recorder)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	buttonTopic := "zigbee2mqtt/fixture-button-3rsb"
	// Every message is emitted before the run loop reconciles, so all three
	// State reports queue while route activation is still pending.
	connection.onSubscribe = func(connection *fakeConnection) {
		connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"online"}`), true, now)
		connection.emit("zigbee2mqtt/bridge/info", readFixture(t, "bridge-info-2.13.0.json"), true, now)
		connection.emit("zigbee2mqtt/bridge/devices", readFixture(t, buttonFixtureName), true, now)
		connection.emit(buttonTopic, []byte(`{"action":"single","battery":100}`), false, now.Add(time.Second))
		connection.emit(buttonTopic, []byte(`{"action":"single","battery":90}`), false, now.Add(2*time.Second))
		connection.emit(buttonTopic, []byte(`{"battery":50}`), false, now.Add(3*time.Second))
	}
	z2m := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitFor(t, func() bool {
		return len(recordedEntityEvents(session)) >= 2 && len(recordedObservations(session)) >= 3
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	events := recordedEntityEvents(session)
	if len(events) != 2 || events[0] != events[1] || events[0].Name != "single" || events[0].EntityID == "" {
		t.Fatalf("replayed events = %#v, want two separate single occurrences", events)
	}
	var battery []string
	for _, observation := range recordedObservations(session) {
		if strings.HasSuffix(observation.EntityID, "-battery") {
			battery = append(battery, string(observation.Value))
		}
	}
	if !slices.Equal(battery, []string{"100", "90", "50"}) {
		t.Fatalf("battery observations = %v, want occurrence siblings then the latest ordinary State", battery)
	}
}

// This test protects stateless plan invariants and fails if an action plan
// silently registers with no behavior, with two behaviors, or with State
// claims, or if an existing dispatched enum-action plan is now rejected.
func TestValidateEventActionPlanInvariants(t *testing.T) {
	t.Parallel()
	valid, err := newActionEventPlan(
		adapter.EntityMetadata{Key: "action", ExternalID: "0x1/root/action", Name: "Action"},
		"action",
		[]string{"single", "double"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if planErr := validateEntityPlans([]entityPlan{valid}); planErr != nil {
		t.Fatalf("valid event action plan was rejected: %v", planErr)
	}
	enumAction, err := newEnumActionPlan(
		adapter.EntityMetadata{Key: "effect", ExternalID: "0x1/root/effect", Name: "Effect"},
		"effect",
		[]string{"blink"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if planErr := validateEntityPlans([]entityPlan{enumAction}); planErr != nil {
		t.Fatalf("existing stateless enum-action plan was rejected: %v", planErr)
	}
	for _, test := range []struct {
		name string
		edit func(*entityPlan)
	}{
		{name: "stateless without behavior", edit: func(plan *entityPlan) { plan.DecodeEvent = nil }},
		{
			name: "stateless with two behaviors",
			edit: func(plan *entityPlan) { plan.TranslateCommand = validTestPlan().TranslateCommand },
		},
		{
			name: "stateless claiming state properties",
			edit: func(plan *entityPlan) { plan.StateProperties = []string{"action"} },
		},
		{
			name: "stateless claiming a state decoder",
			edit: func(plan *entityPlan) { plan.DecodeState = validTestPlan().DecodeState },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan := valid
			test.edit(&plan)
			if planErr := validateEntityPlans([]entityPlan{plan}); planErr == nil {
				t.Fatalf("invalid stateless plan was accepted: %#v", plan)
			}
		})
	}
	// Event source ownership is explicit, so the pending replay queue can
	// trust it: an Event plan must name its properties, and a plan without an
	// Event decoder must name none.
	withEventProperties := func(plan entityPlan, properties ...string) entityPlan {
		plan.EventProperties = properties
		return plan
	}
	for _, test := range []struct {
		name string
		plan entityPlan
	}{
		{name: "event plan without event properties", plan: withEventProperties(valid)},
		{name: "event plan with an empty event property", plan: withEventProperties(valid, "")},
		{
			name: "event plan with duplicated event properties",
			plan: withEventProperties(valid, "action", "action"),
		},
		{name: "command plan claiming event properties", plan: withEventProperties(enumAction, "effect")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if planErr := validateEntityPlans([]entityPlan{test.plan}); planErr == nil {
				t.Fatalf("invalid event property ownership was accepted: %#v", test.plan)
			}
		})
	}
	stateful := validTestPlan()
	stateful.DecodeEvent = func(string, map[string]json.RawMessage) (adapter.EntityEvent, bool, error) {
		return adapter.EntityEvent{}, false, nil
	}
	if planErr := validateEntityPlans([]entityPlan{stateful}); planErr == nil {
		t.Fatal("stateful plan with an event decoder was accepted")
	}
}
