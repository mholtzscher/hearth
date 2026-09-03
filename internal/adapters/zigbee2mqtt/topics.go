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
	if validRouteSlug(remainder) && remainder != "bridge" {
		return remainder, deviceTopicState
	}
	friendly, suffix, hasSuffix := strings.Cut(remainder, "/")
	if hasSuffix && suffix == "availability" && validRouteSlug(friendly) && friendly != "bridge" {
		return friendly, deviceTopicAvailability
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
