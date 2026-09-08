package zigbee2mqtt //nolint:testpackage // Tests exercise package-private Entity plans and generic execution.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

var errTestInvalid = errors.New("invalid test State")

func validTestPlan() entityPlan {
	plan, err := newPowerPlan(
		adapter.EntityMetadata{Key: "power", ExternalID: "0x1/root/power", Name: "Power"},
		"state",
		scalarValue{Raw: json.RawMessage(`"ON"`), canonical: "x"},
		scalarValue{Raw: json.RawMessage(`"OFF"`), canonical: "y"},
	)
	if err != nil {
		panic(err)
	}
	return plan
}

// This test protects complete-plan validation and fails if an incomplete
// descriptor, ambiguous property claim, missing decoder, uncontrolled refresh,
// or duplicate key can reach registration.
func TestValidateEntityPlans(t *testing.T) {
	t.Parallel()
	duplicate := validTestPlan()
	for _, test := range []struct {
		name string
		edit func(*entityPlan)
	}{
		{name: "empty key", edit: func(plan *entityPlan) { plan.Descriptor.Key = "" }},
		{name: "empty support", edit: func(plan *entityPlan) { plan.Descriptor.Support = nil }},
		{name: "no state properties", edit: func(plan *entityPlan) { plan.StateProperties = nil }},
		{name: "empty state property", edit: func(plan *entityPlan) { plan.StateProperties = []string{""} }},
		{
			name: "duplicated state property",
			edit: func(plan *entityPlan) { plan.StateProperties = []string{"state", "state"} },
		},
		{name: "empty get property", edit: func(plan *entityPlan) { plan.GetProperties = []string{""} }},
		{
			name: "duplicated get property",
			edit: func(plan *entityPlan) { plan.GetProperties = []string{"state", "state"} },
		},
		{
			name: "get outside state",
			edit: func(plan *entityPlan) { plan.GetProperties = []string{"other"} },
		},
		{name: "missing decoder", edit: func(plan *entityPlan) { plan.DecodeState = nil }},
		{name: "controllable without refresh", edit: func(plan *entityPlan) { plan.GetProperties = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan := validTestPlan()
			test.edit(&plan)
			if err := validateEntityPlans([]entityPlan{plan}); err == nil {
				t.Fatalf("invalid plan was accepted: %#v", plan)
			}
		})
	}
	readOnly := validTestPlan()
	readOnly.GetProperties = nil
	readOnly.TranslateCommand = nil
	if err := validateEntityPlans([]entityPlan{readOnly}); err != nil {
		t.Fatalf("read-only plan without refresh was rejected: %v", err)
	}
	if err := validateEntityPlans([]entityPlan{validTestPlan(), duplicate}); err == nil {
		t.Fatal("duplicate Entity keys were accepted")
	}
	if err := validateEntityPlans([]entityPlan{validTestPlan()}); err != nil {
		t.Fatalf("valid plan was rejected: %v", err)
	}
}

// This test protects ordered refresh subsets and fails if GetProperties can
// reorder StateProperties. A reordered refresh would publish get requests in
// an order the Entity State does not define.
func TestValidateEntityPlansRejectsOutOfOrderGet(t *testing.T) {
	t.Parallel()
	ordered := twoPropertyPlan()
	if err := validateEntityPlans([]entityPlan{ordered}); err != nil {
		t.Fatalf("ordered refresh was rejected: %v", err)
	}
	reordered := twoPropertyPlan()
	reordered.GetProperties = []string{"y", "x"}
	if err := validateEntityPlans([]entityPlan{reordered}); err == nil {
		t.Fatal("out-of-order refresh was accepted")
	}
	subset := twoPropertyPlan()
	subset.GetProperties = []string{"y"}
	if err := validateEntityPlans([]entityPlan{subset}); err != nil {
		t.Fatalf("ordered single-property subset was rejected: %v", err)
	}
}

// This test protects planned-command validation and fails if an empty set, a
// missing or malformed refresh list, a missing deadline, or a missing matcher
// can reach MQTT publication. Active refresh is mandatory, so an empty
// GetProperties list is rejected even when the set payload is valid.
func TestValidatePlannedCommand(t *testing.T) {
	t.Parallel()
	valid := plannedCommand{
		SetValues:     map[string]json.RawMessage{"state": json.RawMessage(`"ON"`)},
		GetProperties: []string{"state"},
		Deadline:      time.Now().Add(time.Minute),
		Matches:       exactMatcher(true),
	}
	if err := validatePlannedCommand(valid); err != nil {
		t.Fatalf("valid planned Command was rejected: %v", err)
	}
	for _, test := range []struct {
		name string
		edit func(*plannedCommand)
	}{
		{name: "empty set", edit: func(planned *plannedCommand) { planned.SetValues = nil }},
		{
			name: "empty set property",
			edit: func(planned *plannedCommand) {
				planned.SetValues = map[string]json.RawMessage{"": json.RawMessage(`"ON"`)}
			},
		},
		{
			name: "empty set value",
			edit: func(planned *plannedCommand) {
				planned.SetValues = map[string]json.RawMessage{"state": nil}
			},
		},
		{
			name: "empty refresh",
			edit: func(planned *plannedCommand) { planned.GetProperties = nil },
		},
		{
			name: "empty refresh property",
			edit: func(planned *plannedCommand) { planned.GetProperties = []string{""} },
		},
		{
			name: "duplicated refresh property",
			edit: func(planned *plannedCommand) { planned.GetProperties = []string{"state", "state"} },
		},
		{name: "zero deadline", edit: func(planned *plannedCommand) { planned.Deadline = time.Time{} }},
		{name: "nil matcher", edit: func(planned *plannedCommand) { planned.Matches = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			planned := valid
			planned.GetProperties = append([]string(nil), valid.GetProperties...)
			planned.SetValues = map[string]json.RawMessage{"state": json.RawMessage(`"ON"`)}
			test.edit(&planned)
			if err := validatePlannedCommand(planned); err == nil {
				t.Fatalf("invalid planned Command was accepted: %#v", planned)
			}
		})
	}
}

// This test protects dispatched command validation and fails if a
// dispatched plan without refresh or matcher is rejected, or if a
// dispatched plan carrying refresh properties, a matcher, or no deadline
// can reach MQTT publication.
func TestValidateDispatchedPlannedCommand(t *testing.T) {
	t.Parallel()
	valid := plannedCommand{
		SetValues: map[string]json.RawMessage{"effect": json.RawMessage(`"breathe"`)},
		Deadline:  time.Now().Add(time.Minute),
		Outcome:   plannedDispatched,
	}
	if err := validatePlannedCommand(valid); err != nil {
		t.Fatalf("valid dispatched Command was rejected: %v", err)
	}
	for _, test := range []struct {
		name string
		edit func(*plannedCommand)
	}{
		{name: "empty set", edit: func(planned *plannedCommand) { planned.SetValues = nil }},
		{
			name: "refresh requested",
			edit: func(planned *plannedCommand) { planned.GetProperties = []string{"effect"} },
		},
		{
			name: "matcher installed",
			edit: func(planned *plannedCommand) { planned.Matches = exactMatcher(true) },
		},
		{name: "zero deadline", edit: func(planned *plannedCommand) { planned.Deadline = time.Time{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			planned := valid
			planned.SetValues = map[string]json.RawMessage{"effect": json.RawMessage(`"breathe"`)}
			test.edit(&planned)
			if err := validatePlannedCommand(planned); err == nil {
				t.Fatalf("invalid dispatched Command was accepted: %#v", planned)
			}
		})
	}
}

// This test protects stateless plan validation and fails if a stateless
// effect plan without State properties or a decoder is rejected, or if an
// ordinary stateful plan without them is accepted.
func TestValidateStatelessEntityPlan(t *testing.T) {
	t.Parallel()
	plan, err := newEffectPlan(
		adapter.EntityMetadata{Key: "effect", ExternalID: "0x1/root/effect", Name: "Effect"},
		"effect",
		[]string{"blink", "breathe"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.StatePolicy != entityStateless || len(plan.StateProperties) != 0 || plan.DecodeState != nil ||
		len(plan.GetProperties) != 0 || plan.TranslateCommand == nil {
		t.Fatalf("effect plan = %#v", plan)
	}
	if planErr := validateEntityPlans([]entityPlan{plan}); planErr != nil {
		t.Fatalf("stateless plan was rejected: %v", planErr)
	}
	stateful := validTestPlan()
	stateful.StateProperties = nil
	if statefulErr := validateEntityPlans([]entityPlan{stateful}); statefulErr == nil {
		t.Fatal("stateful plan without State properties was accepted")
	}
}

// This test protects typed outcome matching and fails if a semantic-type
// mismatch panics instead of returning false.
func TestExactMatcherRejectsTypeMismatch(t *testing.T) {
	t.Parallel()
	matches := exactMatcher(true)
	if !matches(stateReport{semantic: true}) {
		t.Fatal("exact bool match was rejected")
	}
	if matches(stateReport{semantic: false}) {
		t.Fatal("different bool value matched")
	}
	if matches(stateReport{semantic: int64(1)}) {
		t.Fatal("mismatched semantic type matched")
	}
	if matches(stateReport{}) {
		t.Fatal("nil semantic matched")
	}
}

func twoPropertyPlan() entityPlan {
	return entityPlan{
		Descriptor: adapter.EntityDescriptor{
			Key: "combo", ExternalID: "0x1/root/combo", Name: "Combo", Type: "test/combo",
			Support: json.RawMessage(`{}`),
		},
		StateProperties: []string{"x", "y"},
		GetProperties:   []string{"x", "y"},
		DecodeState: func(
			entityID string,
			properties map[string]json.RawMessage,
			_ time.Time,
		) (stateReport, bool, error) {
			x, xErr := decodeTestNumber(properties["x"])
			y, yErr := decodeTestNumber(properties["y"])
			if xErr != nil || yErr != nil {
				return stateReport{}, false, errTestInvalid
			}
			return stateReport{
				Observation: adapter.Observation{EntityID: entityID, Value: json.RawMessage(jsonNumber(x + y))},
				semantic:    x + y,
			}, true, nil
		},
		TranslateCommand: func(
			_ context.Context,
			_ string,
			_ adapter.Command,
			_ adapter.Responder,
		) (plannedCommand, error) {
			return plannedCommand{
				SetValues: map[string]json.RawMessage{
					"y": json.RawMessage(`2`),
					"x": json.RawMessage(`1`),
				},
				GetProperties: []string{"x", "y"},
				Deadline:      time.Now().Add(time.Minute),
				Matches:       exactMatcher(3),
			}, nil
		},
	}
}

func decodeTestNumber(payload json.RawMessage) (int, error) {
	var value int
	if err := json.Unmarshal(payload, &value); err != nil {
		return 0, err
	}
	return value, nil
}

// This test protects the same-message multi-property seam and fails if split
// messages assemble cached State or a partial message produces synthetic
// State.
func TestMultiPropertyStateRequiresOneMessage(t *testing.T) {
	t.Parallel()
	entities := []runtimeEntity{{plan: twoPropertyPlan(), entityID: "entity-combo"}}
	receivedAt := time.Unix(1, 0).UTC()
	states, issues, err := decodeDeviceState([]byte(`{"x":1,"y":2}`), entities, receivedAt)
	if err != nil || len(issues) != 0 || len(states) != 1 ||
		string(states[0].report.Observation.Value) != "3" {
		t.Fatalf("complete states = %#v, issues = %#v, err = %v", states, issues, err)
	}
	for _, payload := range []string{`{"x":1}`, `{"y":2}`, `{}`} {
		states, issues, err = decodeDeviceState([]byte(payload), entities, receivedAt)
		if err != nil || len(states) != 0 || len(issues) != 0 {
			t.Fatalf("split %s: states=%#v issues=%#v err=%v", payload, states, issues, err)
		}
	}
	states, issues, err = decodeDeviceState([]byte(`{"x":1,"y":"bad"}`), entities, receivedAt)
	if err != nil || len(states) != 0 || len(issues) != 1 ||
		!reflect.DeepEqual(issues[0].Properties, []string{"x", "y"}) {
		t.Fatalf("invalid states = %#v, issues = %#v, err = %v", states, issues, err)
	}
}

// This test protects multi-property command planning and fails if set values
// are lost, refresh properties are dropped, or the payload order is
// nondeterministic.
func TestMultiPropertyCommandPlansEveryProperty(t *testing.T) {
	t.Parallel()
	route := commandRoute{entityID: "entity-combo", entity: runtimeEntity{plan: twoPropertyPlan()}}
	payload, planned, err := translateCommand(
		context.Background(),
		route,
		testCommand("entity-combo", `{}`),
		newFakeResponder(&runtimeRecorder{}, newFakeSession(&runtimeRecorder{})),
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"x":1,"y":2}` {
		t.Fatalf("payload = %s", payload)
	}
	if !reflect.DeepEqual(planned.GetProperties, []string{"x", "y"}) {
		t.Fatalf("refresh = %v", planned.GetProperties)
	}
	if !planned.Matches(stateReport{semantic: 3}) || planned.Matches(stateReport{semantic: 4}) {
		t.Fatal("multi-property matcher did not enforce the combined State")
	}
}

// This test protects relay State and Command use of discovered scalars and
// fails if a plug hard-codes ON/OFF instead of translating its expose values.
func TestRelayPowerUsesDiscoveredScalars(t *testing.T) {
	t.Parallel()
	on, err := canonicalScalar(json.RawMessage(`1`))
	if err != nil {
		t.Fatal(err)
	}
	off, err := canonicalScalar(json.RawMessage(`0`))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := newPowerPlan(
		adapter.EntityMetadata{Key: "power", ExternalID: "0x1/root/power", Name: "Power"},
		"state",
		on,
		off,
	)
	if err != nil {
		t.Fatal(err)
	}
	receivedAt := time.Unix(1, 0).UTC()
	report, present, err := plan.DecodeState(
		"entity-power",
		map[string]json.RawMessage{"state": json.RawMessage(`1`)},
		receivedAt,
	)
	if err != nil || !present || string(report.Observation.Value) != "true" {
		t.Fatalf("relay ON = %#v, present=%t, err=%v", report, present, err)
	}
	if _, present, err = plan.DecodeState(
		"entity-power",
		map[string]json.RawMessage{"state": json.RawMessage(`"ON"`)},
		receivedAt,
	); err == nil || present {
		t.Fatalf("relay accepted undiscovered scalar: present=%t, err=%v", present, err)
	}
	recorder := &runtimeRecorder{}
	route := commandRoute{entityID: "entity-power", entity: runtimeEntity{plan: plan}}
	payload, planned, err := translateCommand(
		context.Background(),
		route,
		testCommand("entity-power", `{"value":true}`),
		newFakeResponder(recorder, newFakeSession(recorder)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"state":1}` {
		t.Fatalf("relay payload = %s", payload)
	}
	if !planned.Matches(stateReport{semantic: true}) || planned.Matches(stateReport{semantic: false}) {
		t.Fatal("relay matcher did not enforce the commanded power")
	}
}

// This test protects read-only reconciliation and fails if a temperature
// Entity creates a command route or a publish-only sensor triggers startup
// refresh.
func TestReconcileSeparatesReadOnlyRoutesFromRefresh(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		access      int
		wantRoutes  int
		wantRefresh int
	}{
		{name: "publish-only", access: 1, wantRoutes: 0, wantRefresh: 0},
		{name: "gettable", access: 1 | 4, wantRoutes: 0, wantRefresh: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			published := reconcileSensor(t, test.access)
			if len(published.routes) != test.wantRoutes || len(published.devices) != 1 {
				t.Fatalf("routes=%d devices=%d", len(published.routes), len(published.devices))
			}
			if len(published.refresh) != test.wantRefresh {
				t.Fatalf("refresh publications = %#v", published.refresh)
			}
			if test.wantRefresh == 1 && (published.refresh[0].payload != `{"temperature":""}` ||
				published.refresh[0].qos != mqttQoS || published.refresh[0].retained) {
				t.Fatalf("refresh publication = %#v", published.refresh[0])
			}
		})
	}
}

type reconciledSensor struct {
	routes  map[string]commandRoute
	devices map[string]runtimeDevice
	refresh []mqttPublication
}

func reconcileSensor(t *testing.T, access int) reconciledSensor {
	t.Helper()
	return reconcileSensorDevice(t, eligibleSensorDevice("temperature", access))
}

func reconcileSensorDevice(t *testing.T, device upstreamDevice) reconciledSensor {
	t.Helper()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m := newRuntimeAdapter(t, session, &fakeDialer{})
	payload, err := json.Marshal([]upstreamDevice{device})
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := discoverInventory(payload)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := z2m.buildRouteSnapshot(context.Background(), 1, inventory)
	if err != nil {
		t.Fatal(err)
	}
	connection := newFakeConnection(recorder)
	if err = z2m.requestCurrentState(context.Background(), connection, inventory, snapshot); err != nil {
		t.Fatal(err)
	}
	return reconciledSensor{routes: snapshot.routes, devices: snapshot.devices, refresh: connection.published}
}
