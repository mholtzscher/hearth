package nativeapi //nolint:testpackage // Checks explicit Native API wire shapes.

import (
	"math"
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
)

func TestCoverLockDiscovery(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		message proto.Message
		want    EntityInfo
	}{
		{&apipb.ListEntitiesCoverResponse{Key: 7, DeviceId: 2, Name: "Door", DeviceClass: "garage",
			SupportsPosition: true, SupportsStop: true, AssumedState: true},
			EntityInfo{Type: EntityCover, UpstreamType: "cover", Key: 7, DeviceID: 2, Name: "Door", DeviceClass: "garage",
				SupportsPosition: true, SupportsStop: true, AssumedState: true}},
		{&apipb.ListEntitiesLockResponse{Key: 8, Name: "Lock remotes", RequiresCode: true, AssumedState: true},
			EntityInfo{Type: EntityLock, UpstreamType: "lock", Key: 8, Name: "Lock remotes",
				RequiresCode: true, AssumedState: true}},
	} {
		id, data, err := encode(test.message)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeEntity(id, data)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Errorf("%T: %+v, %v; want %+v", test.message, got, err, test.want)
		}
	}
}

func TestCoverLockStateDecoding(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		message proto.Message
		want    StateUpdate
	}{
		{&apipb.CoverStateResponse{Key: 7}, StateUpdate{Type: EntityCover, Key: 7, CoverOperation: "idle"}},
		{&apipb.CoverStateResponse{Key: 7, Position: 1, CurrentOperation: apipb.CoverOperation_COVER_OPERATION_IDLE},
			StateUpdate{Type: EntityCover, Key: 7, Position: 1, CoverOperation: "idle"}},
		{&apipb.CoverStateResponse{Key: 7, DeviceId: 2, Position: 0.5,
			CurrentOperation: apipb.CoverOperation_COVER_OPERATION_IS_OPENING},
			StateUpdate{Type: EntityCover, Key: 7, DeviceID: 2, Position: 0.5, CoverOperation: "opening"}},
		{&apipb.CoverStateResponse{Key: 7, Position: 0.3, CurrentOperation: apipb.CoverOperation_COVER_OPERATION_IS_CLOSING},
			StateUpdate{Type: EntityCover, Key: 7, Position: 0.3, CoverOperation: "closing"}},
		{&apipb.CoverStateResponse{Key: 7, CurrentOperation: apipb.CoverOperation(99)},
			StateUpdate{Type: EntityCover, Key: 7, Missing: true}},
		{&apipb.LockStateResponse{Key: 8}, StateUpdate{Type: EntityLock, Key: 8, LockState: "unknown"}},
		{&apipb.LockStateResponse{Key: 8, DeviceId: 2, State: apipb.LockState_LOCK_STATE_LOCKED},
			StateUpdate{Type: EntityLock, Key: 8, DeviceID: 2, LockState: "locked"}},
		{&apipb.LockStateResponse{Key: 8, State: apipb.LockState_LOCK_STATE_UNLOCKED},
			StateUpdate{Type: EntityLock, Key: 8, LockState: "unlocked"}},
		{&apipb.LockStateResponse{Key: 8, State: apipb.LockState_LOCK_STATE_JAMMED},
			StateUpdate{Type: EntityLock, Key: 8, LockState: "jammed"}},
		{&apipb.LockStateResponse{Key: 8, State: apipb.LockState_LOCK_STATE_LOCKING},
			StateUpdate{Type: EntityLock, Key: 8, LockState: "locking"}},
		{&apipb.LockStateResponse{Key: 8, State: apipb.LockState_LOCK_STATE_UNLOCKING},
			StateUpdate{Type: EntityLock, Key: 8, LockState: "unlocking"}},
		{&apipb.LockStateResponse{Key: 8, State: apipb.LockState_LOCK_STATE_OPEN},
			StateUpdate{Type: EntityLock, Key: 8, Missing: true}},
		{&apipb.LockStateResponse{Key: 8, State: apipb.LockState_LOCK_STATE_OPENING},
			StateUpdate{Type: EntityLock, Key: 8, Missing: true}},
		{&apipb.LockStateResponse{Key: 8, State: apipb.LockState(99)},
			StateUpdate{Type: EntityLock, Key: 8, Missing: true}},
	} {
		id, data, err := encode(test.message)
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := decodeState(id, data)
		if err != nil || !ok || got != test.want {
			t.Errorf("%v: %+v, %t, %v; want %+v", test.message, got, ok, err, test.want)
		}
	}
}

func TestCoverLockCommands(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		command Command
		want    proto.Message
	}{
		{CoverCommand{Key: 7, DeviceID: 2, Position: new(1.0)},
			&apipb.CoverCommandRequest{Key: 7, DeviceId: 2, HasPosition: true, Position: 1}},
		{CoverCommand{Key: 7, Position: new(0.0)},
			&apipb.CoverCommandRequest{Key: 7, HasPosition: true}},
		{CoverCommand{Key: 7, Position: new(0.5)},
			&apipb.CoverCommandRequest{Key: 7, HasPosition: true, Position: 0.5}},
		{CoverCommand{Key: 7, DeviceID: 2, Stop: true},
			&apipb.CoverCommandRequest{Key: 7, DeviceId: 2, Stop: true}},
		{LockCommand{Key: 8, DeviceID: 2, Locked: true},
			&apipb.LockCommandRequest{Key: 8, DeviceId: 2, Command: apipb.LockCommand_LOCK_LOCK}},
		{LockCommand{Key: 8},
			&apipb.LockCommandRequest{Key: 8, Command: apipb.LockCommand_LOCK_UNLOCK}},
	} {
		got, err := commandMessage(test.command)
		if err != nil || !proto.Equal(got, test.want) {
			t.Errorf("%+v: %v, %v; want %v", test.command, got, err, test.want)
		}
	}
	for _, command := range []CoverCommand{
		{}, {Position: new(0.5), Stop: true}, {Position: new(-0.1)}, {Position: new(1.1)},
		{Position: new(math.NaN())}, {Position: new(math.Inf(1))},
	} {
		if _, err := commandMessage(command); err == nil {
			t.Errorf("accepted invalid cover command %+v", command)
		}
	}
}
