package scripted_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
)

func TestRegistryKnowsBuiltinCatalog(t *testing.T) {
	t.Parallel()
	types := scripted.KnownTypes()
	if len(types) != 16 {
		t.Fatalf("KnownTypes() has %d entries, want 16", len(types))
	}
	for _, id := range []string{"hearth.power/v1", "hearth.temperature/v1", "hearth.enumevent/v1"} {
		if _, err := scripted.Lookup(id); err != nil {
			t.Fatalf("Lookup(%q) failed: %v", id, err)
		}
	}
	if _, err := scripted.Lookup("hearth.toaster/v9"); err == nil {
		t.Fatal("Lookup(unknown) succeeded, want an error naming the supported set")
	}
}

func TestRegistryValidatesPowerState(t *testing.T) {
	t.Parallel()
	codecs, err := scripted.Lookup("hearth.power/v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, stateErr := codecs.NormalizeState(json.RawMessage(`true`)); stateErr != nil {
		t.Fatalf("valid power State rejected: %v", stateErr)
	}
	if _, stateErr := codecs.NormalizeState(json.RawMessage(`"on"`)); stateErr == nil {
		t.Fatal("invalid power State accepted, want rejection")
	}
	if _, supportErr := codecs.NormalizeSupport(
		json.RawMessage(`{"state":{},"operations":{"set":{}}}`),
	); supportErr != nil {
		t.Fatalf("valid power support rejected: %v", supportErr)
	}
}

// powerDeviceSpec is one valid scripted power Device: the smallest Device that
// Validate accepts, so per-field rejections are attributable to the field under
// test rather than to unrelated structure.
func powerDeviceSpec() scripted.DeviceSpec {
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
		}},
	}
}

// TestDeviceSpecOutputsInterval protects scripted output series without a
// ticker: a series of zero or one value publishes once and runs no interval
// loop, so it must validate without an interval, while a multi-value series is
// stepped by a ticker and still requires a positive interval. It fails if the
// interval is demanded for every outputs block, or if it is not demanded for a
// multi-value script.
func TestDeviceSpecOutputsInterval(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		outputs    *scripted.OutputsSpec
		wantReject bool
	}{
		"no outputs":        {outputs: nil},
		"no values":         {outputs: &scripted.OutputsSpec{}},
		"one value":         {outputs: &scripted.OutputsSpec{Values: []any{true}}},
		"multi no interval": {outputs: &scripted.OutputsSpec{Values: []any{true, false}}, wantReject: true},
		"one value with interval": {
			outputs: &scripted.OutputsSpec{Interval: scripted.Duration(5 * time.Second), Values: []any{true}},
		},
		"multi with interval": {
			outputs: &scripted.OutputsSpec{Interval: scripted.Duration(5 * time.Second), Values: []any{true, false}},
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			device := powerDeviceSpec()
			device.Entities[0].Outputs = testCase.outputs
			err := device.Validate()
			if testCase.wantReject {
				if err == nil {
					t.Fatal("outputs without an interval accepted for a multi-value script")
				}
				if !strings.Contains(err.Error(), "interval") {
					t.Fatalf("error %q does not name the missing interval", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("scripted outputs without a ticker rejected: %v", err)
			}
		})
	}
}

// TestDeviceSpecHealthReasonCode protects the reason codes reported to the
// Session: the sdk/adapter accepts only a lowercase dotted identifier of at
// most 128 characters inside the hearth. or adapter. namespace, so an invalid
// scripted code must be rejected at config load. It fails if DeviceSpec.Validate
// or HealthStatus only checks that the reason is non-empty.
func TestDeviceSpecHealthReasonCode(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		health     string
		wantReject bool
	}{
		"healthy":                        {health: "healthy"},
		"unhealthy hearth code":          {health: "unhealthy:hearth.external_system_unavailable"},
		"unhealthy adapter dotted code":  {health: "unhealthy:adapter.node.measurement_stale"},
		"unhealthy 128 runes":            {health: "unhealthy:hearth." + strings.Repeat("a", 121)},
		"unhealthy bare word":            {health: "unhealthy:offline", wantReject: true},
		"unhealthy overlong":             {health: "unhealthy:hearth." + strings.Repeat("a", 122), wantReject: true},
		"unhealthy uppercase":            {health: "unhealthy:hearth.Offline", wantReject: true},
		"unhealthy foreign namespace":    {health: "unhealthy:vendor.offline", wantReject: true},
		"unhealthy namespace only":       {health: "unhealthy:hearth", wantReject: true},
		"unhealthy empty dotted segment": {health: "unhealthy:hearth..offline", wantReject: true},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			device := powerDeviceSpec()
			device.Health = testCase.health
			err := device.Validate()
			if testCase.wantReject {
				if err == nil {
					t.Fatalf("health %q accepted, want rejection", testCase.health)
				}
				return
			}
			if err != nil {
				t.Fatalf("health %q rejected: %v", testCase.health, err)
			}
		})
	}
}

func TestDeviceSpecHealthStatusParsesReasonCode(t *testing.T) {
	t.Parallel()
	device := powerDeviceSpec()
	device.Health = "unhealthy:adapter.binding_unavailable"
	healthy, reason, err := device.HealthStatus()
	if err != nil {
		t.Fatalf("valid unhealthy health rejected: %v", err)
	}
	if healthy || reason != "adapter.binding_unavailable" {
		t.Fatalf("HealthStatus() = (%t, %q), want (false, %q)", healthy, reason, "adapter.binding_unavailable")
	}
}

// TestDeviceSpecAvailabilityReasonCode protects the reason code reported with
// an unavailable Entity. It fails if a non-empty availability_reason is only
// checked for emptiness, letting a code the adapter rejects reach the Session.
func TestDeviceSpecAvailabilityReasonCode(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		reason     string
		wantReject bool
	}{
		"hearth code":         {reason: "hearth.sensor_stale"},
		"adapter dotted code": {reason: "adapter.demo-button.measurement_stale"},
		"bare word":           {reason: "stale", wantReject: true},
		"uppercase":           {reason: "hearth.SensorStale", wantReject: true},
		"foreign namespace":   {reason: "vendor.sensor.stale", wantReject: true},
		"overlong":            {reason: "hearth." + strings.Repeat("a", 122), wantReject: true},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			device := powerDeviceSpec()
			device.Entities[0].Available = new(bool) // false: unavailable
			device.Entities[0].AvailabilityReason = testCase.reason
			err := device.Validate()
			if testCase.wantReject {
				if err == nil {
					t.Fatalf("availability_reason %q accepted, want rejection", testCase.reason)
				}
				return
			}
			if err != nil {
				t.Fatalf("availability_reason %q rejected: %v", testCase.reason, err)
			}
		})
	}
}

// TestDeviceSpecRejectsEmptyAvailabilityReason keeps the required-with-
// unavailable rule intact alongside reason-code validation.
func TestDeviceSpecRejectsEmptyAvailabilityReason(t *testing.T) {
	t.Parallel()
	device := powerDeviceSpec()
	device.Entities[0].Available = new(bool)
	if err := device.Validate(); err == nil {
		t.Fatal("unavailable Entity without availability_reason accepted, want an error")
	}
}
