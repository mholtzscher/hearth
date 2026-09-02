package zigbee2mqtt //nolint:testpackage // Runtime tests use the package-private MQTT seam.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This test protects paginated ownership reconciliation and the fixed healthy -> availability -> State -> /get startup order.
// It fails if missing, disabled, and removed capabilities collapse to one reason or retained evidence is emitted too early.
func TestRunReconcilesOwnedMappingsAndOrdersStartupEvidence(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	currentBinding := "z2m-00124b0024abcdef"
	session.pages = map[string]adapter.OwnedMappingPage{
		"": {
			Items: []adapter.OwnedMapping{
				{BindingKey: currentBinding, EntityKey: "power", EntityID: currentBinding + "-power"},
				{BindingKey: "z2m-0000000000000003", EntityKey: "power", EntityID: "missing-power"},
			},
			NextCursor: "page-2",
		},
		"page-2": {Items: []adapter.OwnedMapping{
			{BindingKey: "z2m-0000000000000004", EntityKey: "power", EntityID: "disabled-power"},
			{BindingKey: "z2m-0000000000000005", EntityKey: "power", EntityID: "removed-power"},
		}},
	}
	current := eligibleDevice()
	session.bindings[currentBinding] = adapter.Binding{
		BindingKey: currentBinding,
		DeviceID:   "dev-current",
		Entities: []adapter.EntityBinding{
			{Key: "power", EntityID: currentBinding + "-power", Enabled: true},
			{Key: "brightness", EntityID: currentBinding + "-brightness", Enabled: true},
			{Key: "removed-diagnostic", EntityID: "returned-removed", Enabled: true},
		},
	}
	disabled := eligibleDevice()
	disabled.IEEEAddress, disabled.FriendlyName, disabled.Disabled = "0x0000000000000004", "disabled-light", true
	removed := eligibleDevice()
	removed.IEEEAddress, removed.FriendlyName = "0x0000000000000005", "removed-light"
	removed.Definition.Exposes = []upstreamExpose{{Type: "switch"}}
	inventory, err := json.Marshal([]upstreamDevice{current, disabled, removed})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	connection := newFakeConnection(recorder)
	connection.onSubscribe = func(connection *fakeConnection) {
		connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"online"}`), true, now)
		connection.emit("zigbee2mqtt/bridge/info", readFixture(t, "bridge-info-2.13.0.json"), true, now)
		connection.emit("zigbee2mqtt/bridge/devices", inventory, true, now)
		connection.emit("zigbee2mqtt/test-light/availability", []byte(`{"state":"online"}`), true, now)
		connection.emit("zigbee2mqtt/test-light", []byte(`{"state":"ON","brightness":63.75}`), true, now)
	}
	z2m := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitFor(t, func() bool {
		session.mutex.Lock()
		observations := len(session.observations)
		session.mutex.Unlock()
		connection.mutex.Lock()
		publications := len(connection.published)
		connection.mutex.Unlock()
		return observations == 2 && publications == 2
	})
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}

	if got := session.pageRequests; !reflect.DeepEqual(
		got,
		[]adapter.OwnedMappingPageRequest{{Limit: 200}, {Limit: 200, Cursor: "page-2"}},
	) {
		t.Fatalf("mapping page requests = %#v", got)
	}
	reasons := make(map[string]string)
	for _, report := range session.availability {
		reasons[report.EntityID] = report.ReasonCode
	}
	if reasons["missing-power"] != deviceMissingReason || reasons["disabled-power"] != deviceDisabledReason ||
		reasons["removed-power"] != capabilityMissingReason || reasons["returned-removed"] != capabilityMissingReason ||
		reasons[currentBinding+"-power"] != "" {
		t.Fatalf("availability reasons = %#v", reasons)
	}
	events := recorder.snapshot()
	assertOrdered(
		t,
		events,
		"list:",
		"list:page-2",
		"subscribe:zigbee2mqtt/#",
		"health:healthy:",
		"availability",
		"observation",
		"mqtt:zigbee2mqtt/test-light/get",
	)
	for _, publication := range connection.published {
		if publication.qos != 1 || publication.retained || publication.topic != "zigbee2mqtt/test-light/get" {
			t.Fatalf("startup publication = %#v", publication)
		}
	}
}

// This test protects recoverable exact health reasons and latest-per-topic pending State. It fails if malformed or
// incompatible info permits registration, stale State history floods Core, or availability precedes healthy acknowledgement.
func TestRunRecoversFromIncompatibleBridgeConfiguration(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	connection := newFakeConnection(recorder)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	inventory, _ := json.Marshal([]upstreamDevice{eligibleDevice()})
	badInfo := []byte(
		`{"version":"2.13.0","config":{"mqtt":{"version":4},"availability":{"enabled":false},"device_options":{"optimistic":false}}}`,
	)
	connection.onSubscribe = func(connection *fakeConnection) {
		connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"online"}`), true, now)
		connection.emit("zigbee2mqtt/bridge/info", []byte(`{}`), true, now)
		connection.emit("zigbee2mqtt/bridge/info", badInfo, true, now)
		connection.emit("zigbee2mqtt/bridge/devices", inventory, true, now)
		connection.emit("zigbee2mqtt/test-light", []byte(`{"state":"ON"}`), false, now.Add(time.Second))
		connection.emit("zigbee2mqtt/test-light", []byte(`{"state":"OFF"}`), false, now.Add(2*time.Second))
		connection.emit("zigbee2mqtt/test-light", []byte(`{"state":"ON"}`), false, now.Add(3*time.Second))
		connection.emit(
			"zigbee2mqtt/bridge/info",
			readFixture(t, "bridge-info-2.13.0.json"),
			false,
			now.Add(time.Second),
		)
		connection.emit(
			"zigbee2mqtt/test-light/availability",
			[]byte(`{"state":"online"}`),
			false,
			now.Add(time.Second),
		)
	}
	z2m := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.health) >= 3 && len(session.availability) >= 2 && len(session.observations) == 1
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if string(session.observations[0].Value) != "true" ||
		session.observations[0].AdapterReceivedAt != now.Add(3*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("replayed pending Observations = %#v, want only latest State", session.observations)
	}
	if session.health[0].Status != adapter.HealthUnhealthy || session.health[0].ReasonCode != invalidInventoryReason ||
		session.health[1].Status != adapter.HealthUnhealthy ||
		session.health[1].ReasonCode != incompatibleConfigurationReason ||
		session.health[len(session.health)-1].Status != adapter.HealthHealthy {
		t.Fatalf("health reports = %#v", session.health)
	}
	assertOrdered(
		t,
		recorder.snapshot(),
		"health:unhealthy:"+invalidInventoryReason,
		"health:unhealthy:"+incompatibleConfigurationReason,
		"health:healthy:",
		"availability",
	)
}
