package homekit

import (
	"slices"
	"sync"

	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
)

const (
	lockLocked   = "locked"
	lockUnlocked = "unlocked"
	lockUnknown  = "unknown"
)

func mapLock(b *accessoryBuilder) error {
	item, found := assignment(b.exposure, "lock")
	if !found {
		return nil
	}
	s := service.NewLockMechanism()
	s.LockTargetState.Permissions = append(s.LockTargetState.Permissions, characteristic.PermissionTimedWrite)
	var mu sync.Mutex
	current := 3
	target, targetKnown := 0, false
	b.bind(s.LockCurrentState.C, item.EntityID, func(e Entity) (any, bool) {
		state, ok := stateValue[string](e)
		if !ok {
			return nil, false
		}
		mu.Lock()
		defer mu.Unlock()
		switch state {
		case lockUnlocked:
			current = 0
		case lockLocked:
			current = 1
		case "jammed":
			current = 2
		case lockUnknown:
			current = 3
		case "locking", "unlocking":
		default:
			return nil, false
		}
		return current, true
	}, nil)
	var write func(any) (writeIntent, int)
	if supports(b.inventory.Entities[item.EntityID], "set") {
		write = func(value any) (writeIntent, int) {
			n, ok := number(value, 0, 1, true)
			if !ok {
				return writeIntent{}, invalidValue
			}
			state := lockUnlocked
			if n == 1 {
				state = lockLocked
			}
			return direct(item.EntityID, "set", "value", state), 0
		}
	} else {
		s.LockTargetState.Permissions = slices.DeleteFunc(
			s.LockTargetState.Permissions,
			func(p string) bool { return p == characteristic.PermissionWrite },
		)
	}
	b.bind(s.LockTargetState.C, item.EntityID, func(e Entity) (any, bool) {
		state, ok := stateValue[string](e)
		if !ok {
			return nil, false
		}
		mu.Lock()
		defer mu.Unlock()
		switch state {
		case lockUnlocked, "unlocking":
			target, targetKnown = 0, true
		case lockLocked, "locking":
			target, targetKnown = 1, true
		case "jammed", lockUnknown:
		default:
			return nil, false
		}
		return target, targetKnown
	}, write)
	return b.addService(slot(item), s.S, true)
}
