package zigbee2mqtt //nolint:testpackage // Runtime tests use the package-private MQTT seam.

import "testing"

// This test protects exact topic classification and fails if /set, /get, bridge request/response, nested, group, or
// unknown topics are accidentally interpreted as Device evidence.
func TestTopicClassificationIsExact(t *testing.T) {
	t.Parallel()
	device := runtimeDevice{friendly: "test-light"}
	devices := map[string]runtimeDevice{"test-light": device}
	if classifyBridgeTopic("zigbee2mqtt", "zigbee2mqtt/bridge/devices") != bridgeTopicDevices ||
		classifyBridgeTopic("zigbee2mqtt", "zigbee2mqtt/bridge/request/device/remove") != bridgeTopicUnknown {
		t.Fatal("bridge topic classification was not exact")
	}
	for topic, want := range map[string]deviceTopic{
		"zigbee2mqtt/test-light":              deviceTopicState,
		"zigbee2mqtt/test-light/availability": deviceTopicAvailability,
		"zigbee2mqtt/test-light/set":          deviceTopicUnknown,
		"zigbee2mqtt/test-light/get":          deviceTopicUnknown,
		"zigbee2mqtt/test-light/extra/path":   deviceTopicUnknown,
		"zigbee2mqtt/group":                   deviceTopicUnknown,
		"other/test-light":                    deviceTopicUnknown,
	} {
		if _, got := classifyDeviceTopic("zigbee2mqtt", topic, devices); got != want {
			t.Errorf("classifyDeviceTopic(%q) = %v, want %v", topic, got, want)
		}
	}
	inventory := &inventoryDiscovery{Devices: []discoveredDevice{{FriendlyName: "test-light"}}}
	for topic, want := range map[string]bool{
		"zigbee2mqtt/test-light":                     true,
		"zigbee2mqtt/test-light/availability":        true,
		"zigbee2mqtt/test-light/set":                 false,
		"zigbee2mqtt/bridge/request/device/remove":   false,
		"zigbee2mqtt/unknown-light":                  false,
		"zigbee2mqtt/test-light/additional/segments": false,
	} {
		if got := queueableDeviceTopic("zigbee2mqtt", topic, inventory); got != want {
			t.Errorf("queueableDeviceTopic(%q) = %t, want %t", topic, got, want)
		}
	}
}
