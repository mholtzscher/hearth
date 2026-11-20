package homekit

import (
	"slices"

	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"

	"github.com/mholtzscher/hearth/entitytypes/coverv1"
)

func mapCover(b *accessoryBuilder) error {
	item, found := assignment(b.exposure, rolePosition)
	if !found {
		return nil
	}
	s := service.NewWindowCovering()
	readPosition := func(e Entity) (any, bool) {
		state, ok := stateValue[coverv1.State](e)
		return int(state.Position), ok
	}
	b.bind(s.CurrentPosition.C, item.EntityID, readPosition, nil)
	var write func(any) (writeIntent, int)
	if supports(b.inventory.Entities[item.EntityID], "set") {
		position := integerWriter(item.EntityID, "position", "", "", 0, maxPercent)
		write = func(value any) (writeIntent, int) {
			intent, status := position(value)
			// Do not leave an older move queued to run after a later Stop.
			intent.immediate = true
			return intent, status
		}
	} else {
		s.TargetPosition.Permissions = slices.DeleteFunc(
			s.TargetPosition.Permissions,
			func(p string) bool { return p == characteristic.PermissionWrite },
		)
	}
	b.bind(s.TargetPosition.C, item.EntityID, readPosition, write)
	b.bind(s.PositionState.C, item.EntityID, func(e Entity) (any, bool) {
		state, ok := stateValue[coverv1.State](e)
		if !ok {
			return nil, false
		}
		switch state.Motion {
		case "closing":
			return characteristic.PositionStateDecreasing, true
		case "opening":
			return characteristic.PositionStateIncreasing, true
		case "idle":
			return characteristic.PositionStateStopped, true
		default:
			return nil, false
		}
	}, nil)
	if supports(b.inventory.Entities[item.EntityID], "stop") {
		hold := characteristic.NewHoldPosition()
		s.AddC(hold.C)
		b.bind(hold.C, item.EntityID, nil, func(value any) (writeIntent, int) {
			hold, ok := value.(bool)
			if !ok {
				return writeIntent{}, invalidValue
			}
			if !hold {
				return writeIntent{}, 0
			}
			return writeIntent{EntityID: item.EntityID, Operation: "stop", immediate: true}, 0
		})
	}
	return b.addService(slot(item), s.S, true)
}
