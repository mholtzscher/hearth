package homekit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	"github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

type fakeFunctionRuntime struct {
	inventory   Inventory
	store       storeFile
	controllers int
	pairErr     error
	changeErr   error
	resetErr    error
	removed     bool
	desired     Exposure
	remove      bool
	changes     int
	resets      int
}

func (f *fakeFunctionRuntime) inventorySnapshot() Inventory { return f.inventory }
func (f *fakeFunctionRuntime) exposureSnapshot() storeFile  { return f.store }
func (f *fakeFunctionRuntime) pairingStatus() (int, SetupIdentity, error) {
	return f.controllers, f.store.Setup, f.pairErr
}
func (f *fakeFunctionRuntime) changeExposure(_ context.Context, desired Exposure, remove bool) (Exposure, bool, error) {
	f.desired, f.remove = desired, remove
	f.changes++
	desired.AID = 2
	return desired, f.removed, f.changeErr
}
func (f *fakeFunctionRuntime) resetPairing(context.Context) error {
	f.resets++
	if f.resetErr == nil {
		f.controllers = 0
	}
	return f.resetErr
}

func functionFixture() *fakeFunctionRuntime {
	return &fakeFunctionRuntime{
		inventory: Inventory{
			ReadAt:  time.Now(),
			Devices: map[string]Device{"dev_1": {ID: "dev_1", Name: "Light", Kind: "light"}},
			Entities: map[string]Entity{
				"ent_1": {
					ID:           "ent_1",
					DeviceID:     "dev_1",
					Name:         "Power",
					Type:         powerv1.TypeID,
					Enabled:      true,
					Availability: "available",
					State:        &EntityState{},
				},
			},
		},
		store: storeFile{Setup: SetupIdentity{Code: "03145154", ID: "LD96"}, Exposures: map[string]Exposure{}},
	}
}

func invokeFunction(t *testing.T, runtime *fakeFunctionRuntime, key, args string) adapter.FunctionResult {
	t.Helper()
	for _, fn := range bridgeFunctions(runtime) {
		if fn.Descriptor.Key != key {
			continue
		}
		result := fn.Handler(context.Background(), adapter.FunctionRequest{Arguments: json.RawMessage(args)})
		if success, ok := result.(adapter.FunctionSucceeded); ok {
			schema, err := functionschema.Compile(fn.Descriptor.OutputSchema)
			if err != nil {
				t.Fatalf("compile %s output: %v", key, err)
			}
			if err = schema.Validate(success.Result); err != nil {
				t.Fatalf("%s output violates schema: %s", key, success.Result)
			}
		}
		return result
	}
	t.Fatalf("missing function %s", key)
	return nil
}

func decodeSuccess[T any](t *testing.T, result adapter.FunctionResult) T {
	t.Helper()
	success, ok := result.(adapter.FunctionSucceeded)
	if !ok {
		t.Fatalf("expected success, got %#v", result)
	}
	var value T
	if err := json.Unmarshal(success.Result, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestBridgeFunctionSchemasAndValidation(t *testing.T) {
	t.Parallel()
	functions := bridgeFunctions(functionFixture())
	if len(functions) != 5 {
		t.Fatalf("got %d functions", len(functions))
	}
	for _, fn := range functions {
		for _, document := range []json.RawMessage{fn.Descriptor.InputSchema, fn.Descriptor.OutputSchema} {
			if _, err := functionschema.Compile(document); err != nil {
				t.Fatalf("%s: invalid schema", fn.Descriptor.Key)
			}
		}
		timeout := functionReadTimeout
		if fn.Descriptor.Key == "expose_device" || fn.Descriptor.Key == "unexpose_device" ||
			fn.Descriptor.Key == "reset_pairing" {
			timeout = functionWriteTimeout
		}
		if fn.Descriptor.TimeoutMS != timeout {
			t.Fatalf("%s timeout %d", fn.Descriptor.Key, fn.Descriptor.TimeoutMS)
		}
	}
	runtime := functionFixture()
	for _, input := range []string{`{}`, `{"confirm":"wrong"}`, `{"confirm":"reset_all_homekit_pairings","extra":true}`} {
		got := invokeFunction(t, runtime, "reset_pairing", input)
		if got != (adapter.FunctionNotExecuted{ReasonCode: "invalid_arguments"}) || runtime.resets != 0 {
			t.Fatalf("invalid reset %s: %#v", input, got)
		}
	}
}

func TestListAccessoriesAndCandidates(t *testing.T) {
	t.Parallel()
	r := functionFixture()
	r.store.Exposures["dev_1"] = Exposure{
		DeviceID: "dev_1",
		Kind:     KindLightbulb,
		AID:      2,
		Entities: []RoleAssignment{{EntityID: "ent_1", Role: roleOn}},
	}
	r.inventory.Devices["dev_2"] = Device{ID: "dev_2", Name: "Purifier", Kind: "air-purifier"}
	r.inventory.Entities["ent_2"] = Entity{ID: "ent_2", DeviceID: "dev_2", Name: "Power", Type: powerv1.TypeID}
	r.controllers = 2
	got := decodeSuccess[listResult](t, invokeFunction(t, r, "list_accessories", `{}`))
	if !got.Paired || got.AccessoryCount != 1 || got.Truncated || len(got.Accessories) != 1 ||
		got.Accessories[0].Status != "reachable" {
		t.Fatalf("list: %#v", got)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].DefaultKind != KindAirPurifier ||
		len(got.Candidates[0].Unresolved) != 2 ||
		got.Candidates[0].Unresolved[0].Candidates[0].Name != "Power" {
		t.Fatalf("candidate: %#v", got.Candidates)
	}
	r.inventory.ReadAt = time.Time{}
	if got := invokeFunction(
		t,
		r,
		"list_accessories",
		`{}`,
	); got != (adapter.FunctionNotExecuted{ReasonCode: "core_unreachable"}) {
		t.Fatalf("stale: %#v", got)
	}
	r.inventory.ReadAt = time.Now()
	r.pairErr = errors.New("transient")
	if got := invokeFunction(
		t,
		r,
		"list_accessories",
		`{}`,
	); got != (adapter.FunctionNotExecuted{ReasonCode: "bridge_busy"}) {
		t.Fatalf("pairing unavailable: %#v", got)
	}
}

func TestListIsDeterministicallyByteBounded(t *testing.T) {
	t.Parallel()
	r := functionFixture()
	for i := range 800 {
		id := fmt.Sprintf("dev_%04d", i)
		r.inventory.Devices[id] = Device{ID: id, Name: strings.Repeat("x", 400), Kind: "light"}
	}
	first := invokeFunction(t, r, "list_accessories", `{}`)
	second := invokeFunction(t, r, "list_accessories", `{}`)
	a := first.(adapter.FunctionSucceeded)
	b := second.(adapter.FunctionSucceeded)
	result := decodeSuccess[listResult](t, first)
	if !result.Truncated || len(result.Candidates) == 0 || len(result.Candidates) >= 512 ||
		len(a.Result) > functionschema.MaxValueBytes ||
		string(a.Result) != string(b.Result) {
		t.Fatalf(
			"truncation: entries %d bytes %d deterministic %v",
			len(result.Candidates),
			len(a.Result),
			string(a.Result) == string(b.Result),
		)
	}
}

func TestExposeAndUnexpose(t *testing.T) {
	t.Parallel()
	r := functionFixture()
	got := decodeSuccess[accessoryResult](t, invokeFunction(t, r, "expose_device", `{"device_id":"dev_1"}`))
	if got.Kind != KindLightbulb || got.AID != 2 ||
		!reflect.DeepEqual(r.desired.Entities, []RoleAssignment{{EntityID: "ent_1", Role: roleOn}}) {
		t.Fatalf("expose: %#v desired %#v", got, r.desired)
	}
	r.inventory.Entities["ent_2"] = Entity{ID: "ent_2", DeviceID: "dev_1", Type: powerv1.TypeID}
	if got := invokeFunction(
		t,
		r,
		"expose_device",
		`{"device_id":"dev_1"}`,
	); got != (adapter.FunctionNotExecuted{ReasonCode: "roles_unresolved"}) ||
		r.changes != 1 {
		t.Fatalf("ambiguous: %#v", got)
	}
	_ = decodeSuccess[accessoryResult](
		t,
		invokeFunction(t, r, "expose_device", `{"device_id":"dev_1","entities":[{"entity_id":"ent_2","role":"on"}]}`),
	)
	if r.desired.Entities[0].EntityID != "ent_2" {
		t.Fatalf("explicit assignment: %#v", r.desired)
	}
	for _, removed := range []bool{true, false} {
		r.removed = removed
		output := decodeSuccess[map[string]string](t, invokeFunction(t, r, "unexpose_device", `{"device_id":"dev_1"}`))
		expected := "not_exposed"
		if removed {
			expected = "unexposed"
		}
		if !r.remove || output["status"] != expected {
			t.Fatalf("remove %v: %#v", removed, output)
		}
	}
}

func TestExposeRejectsOversizedInferredResultBeforeMutation(t *testing.T) {
	t.Parallel()
	r := functionFixture()
	r.inventory.Devices["dev_1"] = Device{ID: "dev_1", Name: "Relay", Kind: "relay"}
	for i := range 513 {
		id := fmt.Sprintf("ent_%04d", i)
		r.inventory.Entities[id] = Entity{ID: id, DeviceID: "dev_1", Type: powerv1.TypeID}
	}
	got := invokeFunction(t, r, "expose_device", `{"device_id":"dev_1"}`)
	if got != (adapter.FunctionNotExecuted{ReasonCode: "result_too_large"}) || r.changes != 0 {
		t.Fatalf("oversized exposure: %#v, changes %d", got, r.changes)
	}
}

func TestAccessoryReachability(t *testing.T) {
	t.Parallel()
	r := functionFixture()
	exposure := Exposure{DeviceID: "dev_1", Entities: []RoleAssignment{{EntityID: "ent_1"}, {EntityID: "ent_2"}}}
	r.inventory.Entities["ent_2"] = Entity{ID: "ent_2", DeviceID: "dev_1"}
	if got := describeAccessory(exposure, r.inventory).Status; got != "partially_unreachable" {
		t.Fatalf("partly reachable: %s", got)
	}
	entity := r.inventory.Entities["ent_1"]
	entity.Enabled = false
	r.inventory.Entities["ent_1"] = entity
	if got := describeAccessory(exposure, r.inventory).Status; got != "unreachable" {
		t.Fatalf("unreachable: %s", got)
	}
	delete(r.inventory.Entities, "ent_2")
	if got := describeAccessory(exposure, r.inventory).Status; got != "missing" {
		t.Fatalf("missing entity: %s", got)
	}
	delete(r.inventory.Devices, "dev_1")
	if got := describeAccessory(exposure, r.inventory).Status; got != "missing" {
		t.Fatalf("missing device: %s", got)
	}
}

func TestMutationErrorClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  error
		want adapter.FunctionResult
	}{
		{errors.New("secret"), adapter.FunctionNotExecuted{ReasonCode: "store_write_failed"}},
		{errBridgeBusy, adapter.FunctionNotExecuted{ReasonCode: "bridge_busy"}},
		{errReloadFailed, adapter.FunctionFailed{ReasonCode: "reload_failed"}},
		{errors.Join(errReloadFailed, errStoreDurabilityUncertain), adapter.FunctionUnknown{ReasonCode: "store_write_failed"}},
		{&PlanError{Reason: "entity_not_on_device"}, adapter.FunctionNotExecuted{ReasonCode: "entity_not_on_device"}},
		{&PlanError{Reason: "private"}, adapter.FunctionNotExecuted{ReasonCode: "store_write_failed"}},
	} {
		r := functionFixture()
		r.changeErr = tc.err
		if got := invokeFunction(t, r, "expose_device", `{"device_id":"dev_1"}`); got != tc.want {
			t.Errorf("%v: got %#v want %#v", tc.err, got, tc.want)
		}
		r.resetErr = tc.err
		if got := invokeFunction(t, r, "reset_pairing", `{"confirm":"reset_all_homekit_pairings"}`); got != tc.want {
			t.Errorf("reset %v: got %#v want %#v", tc.err, got, tc.want)
		}
	}
}

func TestPairingCredentialsOnlyWhenUnpairedOrReset(t *testing.T) {
	t.Parallel()
	r := functionFixture()
	unpaired := decodeSuccess[map[string]any](t, invokeFunction(t, r, "pairing_info", `{}`))
	if unpaired["setup_code"] != "031-45-154" || unpaired["setup_uri"] != "X-HM://0023ISYWYLD96" ||
		unpaired["paired"] != false {
		t.Fatalf("unpaired: %#v", unpaired)
	}
	r.controllers = 2
	paired := decodeSuccess[map[string]any](t, invokeFunction(t, r, "pairing_info", `{}`))
	if len(paired) != 2 || paired["paired"] != true || paired["controller_count"] != float64(2) {
		t.Fatalf("paired: %#v", paired)
	}
	reset := decodeSuccess[map[string]any](
		t,
		invokeFunction(t, r, "reset_pairing", `{"confirm":"reset_all_homekit_pairings"}`),
	)
	if !reflect.DeepEqual(reset, unpaired) || r.resets != 1 {
		t.Fatalf("reset: %#v", reset)
	}
	r.pairErr = errors.New("transient")
	if got := invokeFunction(
		t,
		r,
		"pairing_info",
		`{}`,
	); got != (adapter.FunctionNotExecuted{ReasonCode: "bridge_busy"}) {
		t.Fatalf("transient: %#v", got)
	}
}
