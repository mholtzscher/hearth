package homekit

import (
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"

	"github.com/mholtzscher/hearth/entitytypes/colorhsv1"
	"github.com/mholtzscher/hearth/entitytypes/colortempv1"
)

func mapLightbulb(b *accessoryBuilder) error {
	on, found := assignment(b.exposure, roleOn)
	if !found {
		return nil
	}
	s := service.NewLightbulb()
	bindSet(b, s.On.C, on.EntityID, readBool, boolWriter(on.EntityID, "on"))
	if item, exists := assignment(b.exposure, "brightness"); exists {
		c := characteristic.NewBrightness()
		s.AddC(c.C)
		bindSet(
			b,
			c.C,
			item.EntityID,
			readNumber,
			integerWriter(item.EntityID, "value", "brightness", "", 0, maxPercent),
		)
	}
	if item, exists := assignment(b.exposure, roleColorTemperature); exists {
		c := characteristic.NewColorTemperature()
		support := supportValue[colortempv1.Support](b.inventory.Entities[item.EntityID])
		c.MinVal, c.MaxVal = int(support.State.Minimum), int(support.State.Maximum)
		s.AddC(c.C)
		bindSet(b, c.C, item.EntityID, func(e Entity) (any, bool) {
			state, ok := stateValue[colortempv1.State](e)
			return int(state.Value), ok
		}, integerWriter(item.EntityID, "value", groupColorTemperature, "", float64(support.State.Minimum), float64(support.State.Maximum)))
	}
	if item, exists := assignment(b.exposure, "color"); exists {
		h, sat := characteristic.NewHue(), characteristic.NewSaturation()
		s.AddC(h.C)
		s.AddC(sat.C)
		bindSet(b, h.C, item.EntityID, func(e Entity) (any, bool) {
			state, ok := stateValue[colorhsv1.State](e)
			return float64(state.Hue), ok
		}, integerWriter(item.EntityID, "hue", groupHSV, "hue", 0, maxHue))
		bindSet(b, sat.C, item.EntityID, func(e Entity) (any, bool) {
			state, ok := stateValue[colorhsv1.State](e)
			return float64(state.Saturation), ok
		}, integerWriter(item.EntityID, "saturation", groupHSV, "saturation", 0, maxPercent))
	}
	return b.addService(slot(on), s.S, true)
}

func bindSet(
	b *accessoryBuilder,
	c *characteristic.C,
	id string,
	read func(Entity) (any, bool),
	write func(any) (writeIntent, int),
) {
	if entity, exists := b.inventory.Entities[id]; exists && !supports(entity, "set") {
		readOnly(c)
		write = nil
	}
	b.bind(c, id, read, write)
}

func readBool(e Entity) (any, bool)   { return stateValue[bool](e) }
func readNumber(e Entity) (any, bool) { return stateValue[float64](e) }

func boolWriter(id, group string) func(any) (writeIntent, int) {
	return func(value any) (writeIntent, int) {
		v, ok := value.(bool)
		if !ok {
			return writeIntent{}, invalidValue
		}
		intent := direct(id, "set", "value", v)
		intent.Group = group
		return intent, 0
	}
}

func integerWriter(id, key, group, field string, minValue, maxValue float64) func(any) (writeIntent, int) {
	return func(value any) (writeIntent, int) {
		v, ok := number(value, minValue, maxValue, true)
		if !ok {
			return writeIntent{}, invalidValue
		}
		if field == "hue" && v == maxHue {
			v = maxHue - 1
		}
		intent := direct(id, "set", key, int64(v))
		intent.Group, intent.Field = group, field
		return intent, 0
	}
}
