package homekit

import (
	"math"
	"slices"

	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"

	"github.com/mholtzscher/hearth/entitytypes/enumsettingv1"
)

func mapAirPurifier(b *accessoryBuilder) error {
	active, found := assignment(b.exposure, roleActive)
	if !found {
		return nil
	}
	s := service.NewAirPurifier()
	bindSet(b, s.Active.C, active.EntityID, binaryReader(false, false), binaryIntWriter(active.EntityID))
	b.bind(s.CurrentAirPurifierState.C, active.EntityID, purifierCurrentState, nil)
	mapPurifierMode(b, s, active)
	mapPurifierSpeed(b, s)
	if child, exists := assignment(b.exposure, roleChildLock); exists {
		c := characteristic.NewLockPhysicalControls()
		s.AddC(c.C)
		bindSet(b, c.C, child.EntityID, binaryReader(false, false), binaryIntWriter(child.EntityID))
	}
	if err := b.addService(slot(active), s.S, true); err != nil {
		return err
	}
	if pm, exists := assignment(b.exposure, rolePM25); exists {
		quality := service.NewAirQualitySensor()
		density := characteristic.NewPM2_5Density()
		density.Unit = characteristic.UnitMicrogramsPerCubicMeter
		quality.AddC(density.C)
		b.bind(density.C, pm.EntityID, readNumber, nil)
		b.bind(quality.AirQuality.C, pm.EntityID, func(e Entity) (any, bool) {
			v, ok := stateValue[float64](e)
			if !ok || math.IsNaN(v) {
				return nil, false
			}
			return airQuality(v), true
		}, nil)
		if err := b.addService(slot(pm), quality.S, false); err != nil {
			return err
		}
	}
	if filter, exists := assignment(b.exposure, roleFilter); exists {
		maintenance := service.NewFilterMaintenance()
		level := characteristic.NewFilterLifeLevel()
		maintenance.AddC(level.C)
		b.bind(level.C, filter.EntityID, readNumber, nil)
		b.bind(maintenance.FilterChangeIndication.C, filter.EntityID, func(e Entity) (any, bool) {
			v, ok := stateValue[float64](e)
			if !ok {
				return nil, false
			}
			if v <= filterChangeThreshold {
				return 1, true
			}
			return 0, true
		}, nil)
		if err := b.addService(slot(filter), maintenance.S, false); err != nil {
			return err
		}
	}
	return nil
}

func purifierCurrentState(e Entity) (any, bool) {
	value, ok := stateValue[bool](e)
	if !ok {
		return nil, false
	}
	if value {
		return characteristic.CurrentAirPurifierStatePurifyingAir, true
	}
	return characteristic.CurrentAirPurifierStateInactive, true
}

func mapPurifierMode(b *accessoryBuilder, s *service.AirPurifier, active RoleAssignment) {
	mode, exists := assignment(b.exposure, roleMode)
	if !exists {
		// HomeKit has no mode control without a mapped Entity.
		s.TargetAirPurifierState.ValidVals = []int{characteristic.TargetAirPurifierStateManual}
		b.bind(s.TargetAirPurifierState.C, active.EntityID, func(Entity) (any, bool) {
			return characteristic.TargetAirPurifierStateManual, true
		}, nil)
		return
	}
	choices := supportValue[enumsettingv1.Support](b.inventory.Entities[mode.EntityID]).State.Choices
	var valid []int
	if slices.ContainsFunc(choices, func(choice string) bool { return choice != modeAuto }) {
		valid = append(valid, 0)
	}
	if containsChoice(choices, modeAuto) {
		valid = append(valid, 1)
	}
	s.TargetAirPurifierState.ValidVals = valid
	bindSet(b, s.TargetAirPurifierState.C, mode.EntityID, func(e Entity) (any, bool) {
		value, ok := stateValue[string](e)
		if !ok {
			return nil, false
		}
		if value == modeAuto {
			return 1, true
		}
		return 0, true
	}, purifierModeWriter(mode.EntityID, choices, valid))
}

func purifierModeWriter(id string, choices []string, valid []int) func(any) (writeIntent, int) {
	return func(value any) (writeIntent, int) {
		v, ok := number(value, 0, 1, true)
		if !ok || !slices.Contains(valid, int(v)) {
			return writeIntent{}, invalidValue
		}
		choice := modeAuto
		if v == 0 {
			choice = "manual"
			if !containsChoice(choices, choice) {
				for _, candidate := range choices {
					if candidate != modeAuto {
						choice = candidate
						break
					}
				}
			}
		}
		return direct(id, "set", "value", choice), 0
	}
}

func mapPurifierSpeed(b *accessoryBuilder, s *service.AirPurifier) {
	speed, exists := assignment(b.exposure, roleSpeed)
	if !exists {
		return
	}
	choices := supportValue[enumsettingv1.Support](b.inventory.Entities[speed.EntityID]).State.Choices
	if len(choices) == 0 {
		return
	}
	c := characteristic.NewRotationSpeed()
	c.StepVal = maxPercent / float64(len(choices))
	s.AddC(c.C)
	bindSet(b, c.C, speed.EntityID, func(e Entity) (any, bool) {
		value, ok := stateValue[string](e)
		if !ok {
			return nil, false
		}
		index := slices.Index(choices, value)
		if index < 0 {
			return nil, false
		}
		return speedPercent(index, len(choices)), true
	}, func(value any) (writeIntent, int) {
		v, ok := number(value, 0, maxPercent, false)
		if !ok {
			return writeIntent{}, invalidValue
		}
		if v == 0 {
			return writeIntent{}, 0
		}
		return direct(speed.EntityID, "set", "value", choices[speedIndex(v, len(choices))]), 0
	})
}

func binaryIntWriter(id string) func(any) (writeIntent, int) {
	return func(value any) (writeIntent, int) {
		v, ok := number(value, 0, 1, true)
		if !ok {
			return writeIntent{}, invalidValue
		}
		return direct(id, "set", "value", v == 1), 0
	}
}
