package esphome

import (
	"encoding/json"
	"errors"

	contractlockv1 "github.com/mholtzscher/hearth/entitytypes/lockv1"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	sdklockv1 "github.com/mholtzscher/hearth/sdk/adapter/lockv1"
)

func lockSupport(control bool) sdklockv1.Support {
	support := sdklockv1.Support{}
	if control {
		support.Operations.Set = &sdklockv1.SetSupport{}
	}
	return support
}

func prepareLockSet(route boundRoute, parameters json.RawMessage) (json.RawMessage, nativeapi.Command, error) {
	if route.Lock.Operations.Set == nil {
		return nil, nil, errors.New("esphome: lock is read-only")
	}
	codecs, err := contractlockv1.Compile()
	if err != nil {
		return nil, nil, err
	}
	decoded, _, err := codecs.SetParameters.Decode(parameters)
	if err != nil {
		return nil, nil, err
	}
	if err = contractlockv1.ValidateSetParameters(route.Lock, *route.Lock.Operations.Set, decoded); err != nil {
		return nil, nil, err
	}
	target, _ := json.Marshal(decoded.Value)
	return target, nativeapi.LockCommand{Key: route.Upstream.Key, DeviceID: route.Upstream.DeviceID,
		Locked: decoded.Value == "locked"}, nil
}
