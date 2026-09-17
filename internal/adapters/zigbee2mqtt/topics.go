package zigbee2mqtt

import "strings"

type bridgeTopic uint8

const (
	bridgeTopicUnknown bridgeTopic = iota
	bridgeTopicInfo
	bridgeTopicState
	bridgeTopicDevices
	bridgeTopicEvent
)

func classifyBridgeTopic(base, topic string) bridgeTopic {
	switch topic {
	case base + "/bridge/info":
		return bridgeTopicInfo
	case base + "/bridge/state":
		return bridgeTopicState
	case base + "/bridge/devices":
		return bridgeTopicDevices
	case base + "/bridge/event":
		return bridgeTopicEvent
	default:
		return bridgeTopicUnknown
	}
}

type deviceTopic uint8

const (
	deviceTopicUnknown deviceTopic = iota
	deviceTopicState
	deviceTopicAvailability
)

func parseDeviceTopic(base, topic string) (string, deviceTopic) {
	remainder, ok := strings.CutPrefix(topic, base+"/")
	if !ok {
		return "", deviceTopicUnknown
	}
	// Friendly names are single topic levels, so only the exact availability
	// suffix can follow one. The suffix is tested first because a Device name
	// can never contain the separator that would make the two cases overlap.
	if friendly, found := strings.CutSuffix(remainder, "/availability"); found {
		if validFriendlyName(friendly) {
			return friendly, deviceTopicAvailability
		}
		return "", deviceTopicUnknown
	}
	if validFriendlyName(remainder) {
		return remainder, deviceTopicState
	}
	return "", deviceTopicUnknown
}

func queueableDeviceTopic(base, topic string, inventory *inventoryDiscovery) bool {
	friendly, kind := parseDeviceTopic(base, topic)
	if kind == deviceTopicUnknown {
		return false
	}
	if inventory == nil {
		return true
	}
	for _, device := range inventory.Devices {
		if device.FriendlyName == friendly {
			return true
		}
	}
	return false
}

func classifyDeviceTopic(base, topic string, devices map[string]runtimeDevice) (runtimeDevice, deviceTopic) {
	friendly, kind := parseDeviceTopic(base, topic)
	device, exists := devices[friendly]
	if !exists {
		return runtimeDevice{}, deviceTopicUnknown
	}
	return device, kind
}
