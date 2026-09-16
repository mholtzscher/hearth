package scripted_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

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

// TestDeviceSpecOmitAvailabilityRequiresUnhealthyHealth protects the
// omit_availability_when_unhealthy contract: the flag suppresses Entity
// availability reports only for a Device whose health parses unhealthy, so
// setting it on a healthy (or default-healthy) Device is an inert
// misconfiguration the loader must reject rather than silently keep. It fails
// if Validate ignores the flag, accepting it on any health.
func TestDeviceSpecOmitAvailabilityRequiresUnhealthyHealth(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		health     string
		omit       bool
		wantReject bool
	}{
		"omit with unhealthy":           {health: "unhealthy:hearth.external_system_unavailable", omit: true},
		"omit with adapter unhealthy":   {health: "unhealthy:adapter.node.measurement_stale", omit: true},
		"omit with bare default health": {omit: true, wantReject: true},
		"omit with explicit healthy":    {health: "healthy", omit: true, wantReject: true},
		"no omit with healthy":          {health: "healthy"},
		"no omit with unhealthy":        {health: "unhealthy:hearth.external_system_unavailable"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			device := powerDeviceSpec()
			device.Health = testCase.health
			device.OmitAvailabilityWhenUnhealthy = testCase.omit
			err := device.Validate()
			if testCase.wantReject {
				if err == nil {
					t.Fatalf("omit_availability_when_unhealthy accepted with health %q", testCase.health)
				}
				if !strings.Contains(err.Error(), "omit_availability_when_unhealthy") {
					t.Fatalf("error %q does not name omit_availability_when_unhealthy", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("health %q with omit=%t rejected: %v", testCase.health, testCase.omit, err)
			}
		})
	}
}

// TestEntitySpecClockOffsetsParse protects the offset YAML surface: the
// source_time_offset and received_time_offset fields parse any
// [time.ParseDuration] value, negative included, and leave both zero when unset.
// It fails if the shared Duration type still rejects non-positive values, or if
// a bare number is silently taken as nanoseconds.
func TestEntitySpecClockOffsetsParse(t *testing.T) {
	t.Parallel()
	const base = "key: power\nname: Power\ntype: hearth.power/v1\n" +
		"support: {state: {}, operations: {set: {}}}\ninitial: true\n"
	cases := map[string]struct {
		extra        string
		wantSource   time.Duration
		wantReceived time.Duration
		wantReject   bool
	}{
		"unset": {},
		"negative source and positive received": {
			extra:        "source_time_offset: -24h\nreceived_time_offset: +2m\n",
			wantSource:   -24 * time.Hour,
			wantReceived: 2 * time.Minute,
		},
		"positive source": {extra: "source_time_offset: 24h\n", wantSource: 24 * time.Hour},
		"zero source":     {extra: "source_time_offset: 0s\n", wantSource: 0},
		"invalid duration": {
			extra:      "source_time_offset: soon\n",
			wantReject: true,
		},
		"bare number is not seconds": {
			extra:      "received_time_offset: 300\n",
			wantReject: true,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var spec scripted.EntitySpec
			err := yaml.Unmarshal([]byte(base+testCase.extra), &spec)
			if testCase.wantReject {
				if err == nil {
					t.Fatalf("offset %q accepted, want rejection", testCase.extra)
				}
				return
			}
			if err != nil {
				t.Fatalf("offsets %q rejected: %v", testCase.extra, err)
			}
			if got := time.Duration(spec.SourceTimeOffset); got != testCase.wantSource {
				t.Fatalf("source_time_offset = %s, want %s", got, testCase.wantSource)
			}
			if got := time.Duration(spec.ReceivedTimeOffset); got != testCase.wantReceived {
				t.Fatalf("received_time_offset = %s, want %s", got, testCase.wantReceived)
			}
		})
	}
}

// TestCommandBehaviorMarkAvailableRequiresAcceptBehavior protects the
// mark_available contract: it repairs availability only when the Command is
// accepted, so pairing it with the reject behavior is silently inert and must
// fail config validation. It fails if Validate accepts mark_available on a
// rejecting Command, or rejects it on an accepting one.
func TestCommandBehaviorMarkAvailableRequiresAcceptBehavior(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		behavior   string
		mark       bool
		wantReject bool
	}{
		"accept and publish":         {behavior: scripted.CommandBehaviorAcceptPublish, mark: true},
		"default accept and publish": {mark: true},
		"accept without publish":     {behavior: scripted.CommandBehaviorAcceptSilent, mark: true},
		"reject with mark":           {behavior: scripted.CommandBehaviorReject, mark: true, wantReject: true},
		"reject without mark":        {behavior: scripted.CommandBehaviorReject},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			device := powerDeviceSpec()
			device.Entities[0].Commands = map[string]scripted.CommandBehavior{
				"set": {Behavior: testCase.behavior, MarkAvailable: testCase.mark},
			}
			err := device.Validate()
			if testCase.wantReject {
				if err == nil {
					t.Fatalf("mark_available with behavior %q accepted, want rejection", testCase.behavior)
				}
				if !strings.Contains(err.Error(), "mark_available") {
					t.Fatalf("error %q does not name mark_available", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("mark_available with behavior %q rejected: %v", testCase.behavior, err)
			}
		})
	}
}
