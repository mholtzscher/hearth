package august

import (
	"encoding/json"
	"time"

	"github.com/mholtzscher/hearth/entitytypes/lockv1"
)

// The pinned august-yale subscriber passes top-level status/doorState through;
// yalexs pubnub_activity.py also recognizes lockAction. Its activity startTime
// and PubNub timetoken are not established State update times, so neither is used.
func decodePushMessage(data json.RawMessage, id lockID, received time.Time) (report, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil || !pushIDsMatch(fields, id) {
		return report{}, false
	}
	result, err := decodeStatus(data, received)
	if err != nil {
		return report{}, false
	}
	// A PubNub message is not authoritative Bridge or battery evidence.
	result.Online, result.Battery = nil, nil
	result.BatteryPresent = false
	if pushExplicitlyOffline(fields["error"]) {
		online := false
		return report{ReceivedAt: received, Online: &online}, true
	}
	if action, exists := fields["lockAction"]; exists {
		result.Lock = decodePushAction(action, fields["dateTime"])
	}
	return result, result.Lock != nil || result.DoorOpen != nil
}

func pushExplicitlyOffline(data json.RawMessage) bool {
	var wire struct {
		RestCode int    `json:"restCode"`
		Name     string `json:"name"`
	}
	if json.Unmarshal(data, &wire) != nil {
		return false
	}
	const bridgeOfflineCode = 98
	return wire.RestCode == bridgeOfflineCode || wire.Name == "ERRNO_BRIDGE_OFFLINE"
}

func decodePushAction(action, rawTime json.RawMessage) *reading[lockv1.State] {
	value, valid := decodeLockState(action)
	if !valid {
		return nil
	}
	var source *time.Time
	if len(rawTime) > 0 {
		source = decodeSourceTime(rawTime)
		if source == nil {
			return nil
		}
	}
	return &reading[lockv1.State]{Value: value, SourceUpdatedAt: source}
}

func pushIDsMatch(fields map[string]json.RawMessage, id lockID) bool {
	for _, key := range []string{"lockId", "lockID", "LockID", "deviceID", "deviceId"} {
		if raw, exists := fields[key]; exists {
			var value string
			if json.Unmarshal(raw, &value) != nil || value != string(id) {
				return false
			}
		}
	}
	if raw, exists := fields["info"]; exists {
		var info map[string]json.RawMessage
		if json.Unmarshal(raw, &info) != nil {
			return false
		}
		for _, key := range []string{"lockId", "lockID", "LockID", "deviceID", "deviceId"} {
			if rawID, present := info[key]; present {
				var value string
				if json.Unmarshal(rawID, &value) != nil || value != string(id) {
					return false
				}
			}
		}
	}
	return true
}
