package august

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"time"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/entitytypes/lockv1"
)

// Inventory must be complete and unambiguous before an omission can remove a route.
func decodeInventory(data []byte) ([]inventoryEntry, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, &providerError{code: codeInvalid}
	}
	entries := make([]inventoryEntry, 0)
	seen := make(map[lockID]bool)
	for decoder.More() {
		key, keyErr := decoder.Token()
		if keyErr != nil {
			return nil, &providerError{code: codeInvalid}
		}
		text, ok := key.(string)
		if !ok {
			return nil, &providerError{code: codeInvalid}
		}
		id, idErr := parseLockID(text)
		if idErr != nil || seen[id] {
			return nil, &providerError{code: codeInvalid}
		}
		var entry struct {
			Name     string `json:"LockName"`
			UserType string `json:"UserType"`
		}
		if decoder.Decode(&entry) != nil || !validName(entry.Name) || entry.UserType == "" {
			return nil, &providerError{code: codeInvalid}
		}
		seen[id] = true
		entries = append(entries, inventoryEntry{ID: id, Name: entry.Name, UserType: entry.UserType})
	}
	if _, err = decoder.Token(); err != nil {
		return nil, &providerError{code: codeInvalid}
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, &providerError{code: codeInvalid}
	}
	return entries, nil
}

func validName(name string) bool {
	return name != "" && utf8.ValidString(name) && utf8.RuneCountInString(name) <= 128
}

func decodeDetail(data []byte, entry inventoryEntry, received time.Time) (deviceDetail, error) {
	var wire struct {
		ID       string          `json:"LockID"`
		Name     string          `json:"LockName"`
		Model    string          `json:"skuNumber"`
		Firmware string          `json:"currentFirmwareVersion"`
		Channel  string          `json:"pubsubChannel"`
		Status   json.RawMessage `json:"LockStatus"`
		Battery  json.RawMessage `json:"battery"`
		Bridge   json.RawMessage `json:"Bridge"`
	}
	if json.Unmarshal(data, &wire) != nil || !validName(wire.Name) ||
		!validName(wire.Model) || !validName(wire.Firmware) {
		return deviceDetail{}, &providerError{code: codeInvalid}
	}
	id, err := parseLockID(wire.ID)
	if err != nil || id != entry.ID {
		return deviceDetail{}, &providerError{code: codeInvalid}
	}
	result := deviceDetail{Device: device{ID: id, Name: wire.Name, Model: wire.Model,
		Firmware: wire.Firmware, UserType: entry.UserType}, Channel: wire.Channel}
	result.Report, _ = decodeStatus(wire.Status, received)
	result.Report.ReceivedAt = received
	result.Report.Battery = decodeBattery(wire.Battery)
	result.Report.Online = decodeBridge(wire.Bridge)
	return result, nil
}

func decodeStatus(data []byte, received time.Time) (report, error) {
	var wire struct {
		Lock    json.RawMessage `json:"status"`
		Door    json.RawMessage `json:"doorState"`
		Time    json.RawMessage `json:"dateTime"`
		Battery json.RawMessage `json:"battery"`
		Bridge  json.RawMessage `json:"Bridge"`
	}
	result := report{ReceivedAt: received}
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) || json.Unmarshal(data, &wire) != nil {
		return result, &providerError{code: codeInvalid}
	}
	result.Battery, result.Online = decodeBattery(wire.Battery), decodeBridge(wire.Bridge)
	var source *time.Time
	if len(wire.Time) > 0 {
		source = decodeSourceTime(wire.Time)
		if source == nil {
			return result, nil
		}
	}
	if value, ok := decodeLockState(wire.Lock); ok {
		result.Lock = &reading[lockv1.State]{Value: value, SourceUpdatedAt: source}
	}
	if value, ok := decodeDoor(wire.Door); ok {
		result.DoorOpen = &reading[bool]{Value: value, SourceUpdatedAt: source}
	}
	return result, nil
}

func decodeSourceTime(data []byte) *time.Time {
	var text string
	if json.Unmarshal(data, &text) != nil {
		return nil
	}
	stamp, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || stamp.IsZero() {
		return nil
	}
	return &stamp
}

func decodeLockState(data []byte) (lockv1.State, bool) {
	var text string
	if json.Unmarshal(data, &text) != nil {
		return "", false
	}
	switch text {
	case "locked", "lock", "kAugLockState_Locked":
		return "locked", true
	case "unlocked", "unlock", "kAugLockState_Unlocked":
		return "unlocked", true
	case "locking", "kAugLockState_Locking":
		return "locking", true
	case "unlocking", "kAugLockState_Unlocking":
		return "unlocking", true
	case "jammed", "kAugLockState_UnknownStaticPosition", "FAILED_BRIDGE_ERROR_LOCK_JAMMED":
		return "jammed", true
	case "unknown", "kAugLockState_Unknown":
		return "unknown", true
	default:
		return "", false
	}
}

func decodeDoor(data []byte) (bool, bool) {
	var text string
	if json.Unmarshal(data, &text) != nil {
		return false, false
	}
	switch text {
	case "open", "kAugLockDoorState_Open", "kAugDoorState_Open":
		return true, true
	case "closed", "kAugLockDoorState_Closed", "kAugDoorState_Closed":
		return false, true
	default:
		return false, false
	}
}

func decodeBattery(data []byte) *reading[float64] {
	var value *float64
	if json.Unmarshal(data, &value) != nil || value == nil || math.IsInf(*value, 0) ||
		math.IsNaN(*value) || *value < 0 || *value > 1 {
		return nil
	}
	const percentScale = 100
	return &reading[float64]{Value: *value * percentScale}
}

func decodeBridge(data []byte) *bool {
	if len(data) == 0 {
		return nil
	}
	var wire struct {
		Operative *bool `json:"operative"`
		Status    struct {
			Current string `json:"current"`
		} `json:"status"`
	}
	online := json.Unmarshal(data, &wire) == nil && wire.Operative != nil &&
		*wire.Operative && wire.Status.Current == "online"
	return &online
}
