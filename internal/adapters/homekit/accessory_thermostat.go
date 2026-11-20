package homekit

import (
	"math"
	"slices"

	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"

	"github.com/mholtzscher/hearth/entitytypes/thermostatv1"
)

func mapThermostat(b *accessoryBuilder) error {
	item, found := assignment(b.exposure, "thermostat")
	if !found {
		return nil
	}
	entity := b.inventory.Entities[item.EntityID]
	support := supportValue[thermostatv1.Support](entity)
	s := service.NewThermostat()
	s.CurrentTemperature.MinVal = minSensorTemperature
	b.bind(s.CurrentHeatingCoolingState.C, item.EntityID, thermostatActivity, nil)
	mapThermostatMode(b, s, item, entity, support)
	b.bind(s.CurrentTemperature.C, item.EntityID, thermostatTemperature("current"), nil)
	minTemp, maxTemp := celsius(support.State.TemperatureMinimum), celsius(support.State.TemperatureMaximum)
	var targetWrite func(any) (writeIntent, int)
	s.TargetTemperature.MinVal, s.TargetTemperature.MaxVal = minTemp, maxTemp
	if supports(entity, "set_target") {
		targetWrite = temperatureWriter(item.EntityID, "set_target", "temperature", "", minTemp, maxTemp)
	} else {
		readOnly(s.TargetTemperature.C)
	}
	b.bind(s.TargetTemperature.C, item.EntityID, thermostatTemperature("target"), targetWrite)
	readOnly(s.TemperatureDisplayUnits.C)
	b.bind(s.TemperatureDisplayUnits.C, item.EntityID, func(Entity) (any, bool) {
		if b.displayUnit == "fahrenheit" {
			return characteristic.TemperatureDisplayUnitsFahrenheit, true
		}
		return characteristic.TemperatureDisplayUnitsCelsius, true
	}, nil)
	mapThermostatOptionals(b, s, item, entity, support, minTemp, maxTemp)
	return b.addService(slot(item), s.S, true)
}

func thermostatActivity(e Entity) (any, bool) {
	state, ok := stateValue[thermostatv1.State](e)
	if !ok {
		return nil, false
	}
	if state.Activity != nil {
		switch *state.Activity {
		case "heating", "preheating":
			return characteristic.CurrentHeatingCoolingStateHeat, true
		case "cooling":
			return characteristic.CurrentHeatingCoolingStateCool, true
		}
	}
	return characteristic.CurrentHeatingCoolingStateOff, true
}

func mapThermostatMode(
	b *accessoryBuilder,
	s *service.Thermostat,
	item RoleAssignment,
	entity Entity,
	support thermostatv1.Support,
) {
	var valid []int
	for _, mode := range support.State.Modes {
		value, selectable := targetMode(mode)
		if selectable && !slices.Contains(valid, value) {
			valid = append(valid, value)
		}
	}
	s.TargetHeatingCoolingState.ValidVals = valid
	var modeWrite func(any) (writeIntent, int)
	if supports(entity, "set_mode") {
		modeWrite = func(value any) (writeIntent, int) {
			v, ok := number(value, 0, float64(characteristic.TargetHeatingCoolingStateAuto), true)
			if !ok || !slices.Contains(valid, int(v)) {
				return writeIntent{}, invalidValue
			}
			modes := []string{"off", "heat", "cool", modeAuto}
			mode := modes[int(v)]
			if mode == modeAuto && containsChoice(support.State.Modes, "heat_cool") {
				mode = "heat_cool"
			}
			return direct(item.EntityID, "set_mode", "mode", mode), 0
		}
	} else {
		readOnly(s.TargetHeatingCoolingState.C)
	}
	b.bind(s.TargetHeatingCoolingState.C, item.EntityID, func(e Entity) (any, bool) {
		state, ok := stateValue[thermostatv1.State](e)
		if !ok {
			return nil, false
		}
		if state.Mode == "dry" || state.Mode == "fan_only" {
			return characteristic.TargetHeatingCoolingStateCool, true
		}
		v, ok := targetMode(state.Mode)
		return v, ok
	}, modeWrite)
}

func mapThermostatOptionals(
	b *accessoryBuilder,
	s *service.Thermostat,
	item RoleAssignment,
	entity Entity,
	support thermostatv1.Support,
	minTemp, maxTemp float64,
) {
	if supports(entity, "set_range") {
		low, high := characteristic.NewHeatingThresholdTemperature(), characteristic.NewCoolingThresholdTemperature()
		low.MinVal, low.MaxVal, high.MinVal, high.MaxVal = minTemp, maxTemp, minTemp, maxTemp
		s.AddC(low.C)
		s.AddC(high.C)
		b.bind(
			low.C,
			item.EntityID,
			thermostatTemperature("low"),
			temperatureWriter(item.EntityID, "set_range", "low", "low", minTemp, maxTemp),
		)
		b.bind(
			high.C,
			item.EntityID,
			thermostatTemperature("high"),
			temperatureWriter(item.EntityID, "set_range", "high", "high", minTemp, maxTemp),
		)
	}
	if hasThermostatHumidity(entity) {
		currentHumidity := characteristic.NewCurrentRelativeHumidity()
		s.AddC(currentHumidity.C)
		b.bind(currentHumidity.C, item.EntityID, thermostatHumidity(false), nil)
	}
	if supports(entity, "set_humidity") {
		c := characteristic.NewTargetRelativeHumidity()
		c.MinVal, c.MaxVal = support.State.HumidityMinimum, support.State.HumidityMaximum
		s.AddC(c.C)
		b.bind(c.C, item.EntityID, thermostatHumidity(true), func(value any) (writeIntent, int) {
			v, ok := number(value, support.State.HumidityMinimum, support.State.HumidityMaximum, false)
			if !ok {
				return writeIntent{}, invalidValue
			}
			return direct(item.EntityID, "set_humidity", "humidity", v), 0
		})
	}
}

func hasThermostatHumidity(entity Entity) bool {
	state, ok := stateValue[thermostatv1.State](entity)
	return ok && state.CurrentHumidity != nil
}

func readOnly(c *characteristic.C) {
	c.Permissions = slices.DeleteFunc(c.Permissions, func(p string) bool { return p == characteristic.PermissionWrite })
}

func targetMode(mode string) (int, bool) {
	switch mode {
	case "off":
		return characteristic.TargetHeatingCoolingStateOff, true
	case "heat":
		return characteristic.TargetHeatingCoolingStateHeat, true
	case "cool":
		return characteristic.TargetHeatingCoolingStateCool, true
	case modeAuto, "heat_cool":
		return characteristic.TargetHeatingCoolingStateAuto, true
	default:
		return 0, false
	}
}

func thermostatTemperature(field string) func(Entity) (any, bool) {
	return func(e Entity) (any, bool) {
		state, ok := stateValue[thermostatv1.State](e)
		if !ok {
			return nil, false
		}
		var v *int64
		switch field {
		case "current":
			v = state.CurrentTemperature
		case "target":
			v = state.TargetTemperature
		case "low":
			v = state.TargetTemperatureLow
		case "high":
			v = state.TargetTemperatureHigh
		}
		if v == nil {
			return nil, false
		}
		return celsius(*v), true
	}
}

func thermostatHumidity(target bool) func(Entity) (any, bool) {
	return func(e Entity) (any, bool) {
		state, ok := stateValue[thermostatv1.State](e)
		if !ok {
			return nil, false
		}
		v := state.CurrentHumidity
		if target {
			v = state.TargetHumidity
		}
		if v == nil {
			return nil, false
		}
		return *v, true
	}
}

func temperatureWriter(id, operation, key, field string, minValue, maxValue float64) func(any) (writeIntent, int) {
	return func(value any) (writeIntent, int) {
		v, ok := number(value, minValue, maxValue, false)
		if !ok || math.Abs(v*tenthsPerCelsius-math.Round(v*tenthsPerCelsius)) > 1e-8 {
			return writeIntent{}, invalidValue
		}
		intent := direct(id, operation, key, milliCelsius(v))
		if field != "" {
			intent.Group, intent.Field = groupTemperatureRange, field
		}
		return intent, 0
	}
}
