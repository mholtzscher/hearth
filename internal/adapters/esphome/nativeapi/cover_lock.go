package nativeapi

import (
	"errors"
	"math"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
)

func decodeCoverState(data []byte) (StateUpdate, bool, error) {
	var message apipb.CoverStateResponse
	if err := decodeInto(data, &message); err != nil {
		return StateUpdate{}, false, err
	}
	var operation string
	switch message.GetCurrentOperation() {
	case apipb.CoverOperation_COVER_OPERATION_IDLE:
		operation = "idle"
	case apipb.CoverOperation_COVER_OPERATION_IS_OPENING:
		operation = "opening"
	case apipb.CoverOperation_COVER_OPERATION_IS_CLOSING:
		operation = "closing"
	}
	return StateUpdate{
		Type: EntityCover, Key: message.GetKey(), DeviceID: message.GetDeviceId(),
		Position: shortestFloat(message.GetPosition()), CoverOperation: operation, Missing: operation == "",
	}, true, nil
}

func decodeLockState(data []byte) (StateUpdate, bool, error) {
	var message apipb.LockStateResponse
	if err := decodeInto(data, &message); err != nil {
		return StateUpdate{}, false, err
	}
	var state string
	switch message.GetState() {
	case apipb.LockState_LOCK_STATE_NONE:
		state = "unknown"
	case apipb.LockState_LOCK_STATE_LOCKED:
		state = "locked"
	case apipb.LockState_LOCK_STATE_UNLOCKED:
		state = "unlocked"
	case apipb.LockState_LOCK_STATE_JAMMED:
		state = "jammed"
	case apipb.LockState_LOCK_STATE_LOCKING:
		state = "locking"
	case apipb.LockState_LOCK_STATE_UNLOCKING:
		state = "unlocking"
	case apipb.LockState_LOCK_STATE_OPENING, apipb.LockState_LOCK_STATE_OPEN:
		// These have no equivalent in Hearth's lock State contract.
	}
	return StateUpdate{
		Type: EntityLock, Key: message.GetKey(), DeviceID: message.GetDeviceId(),
		LockState: state, Missing: state == "",
	}, true, nil
}

func coverCommandMessage(command CoverCommand) (*apipb.CoverCommandRequest, error) {
	if command.Stop == (command.Position != nil) {
		return nil, errors.New("esphome: cover command requires exactly one of stop or position")
	}
	request := &apipb.CoverCommandRequest{Key: command.Key, DeviceId: command.DeviceID, Stop: command.Stop}
	if command.Position != nil {
		position := *command.Position
		if math.IsNaN(position) || math.IsInf(position, 0) || position < 0 || position > 1 {
			return nil, errors.New("esphome: cover position must be finite and within 0..1")
		}
		request.HasPosition = true
		request.Position = float32(position)
	}
	return request, nil
}
