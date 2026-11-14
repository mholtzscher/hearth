package nativeapi

import "github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"

func decodeSettingState(messageID uint16, data []byte) (StateUpdate, bool, error) {
	if messageID == idNumberState {
		var message apipb.NumberStateResponse
		if err := decodeInto(data, &message); err != nil {
			return StateUpdate{}, false, err
		}
		return StateUpdate{
			Type: EntityNumber, Key: message.GetKey(), DeviceID: message.GetDeviceId(),
			Missing: message.GetMissingState(), Number: shortestFloat(message.GetState()),
		}, true, nil
	}
	var message apipb.SelectStateResponse
	if err := decodeInto(data, &message); err != nil {
		return StateUpdate{}, false, err
	}
	return StateUpdate{
		Type: EntitySelect, Key: message.GetKey(), DeviceID: message.GetDeviceId(),
		Missing: message.GetMissingState(), Text: message.GetState(),
	}, true, nil
}
