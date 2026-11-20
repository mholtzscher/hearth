package homekit

import (
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
)

func mapSensor(b *accessoryBuilder) error {
	primary := true
	for _, item := range b.exposure.Entities {
		var s *service.S
		var c *characteristic.C
		var read func(Entity) (any, bool)
		switch string(item.Role) {
		case "temperature":
			sensor := service.NewTemperatureSensor()
			sensor.CurrentTemperature.MinVal = minSensorTemperature
			s = sensor.S
			c = sensor.CurrentTemperature.C
			read = func(e Entity) (any, bool) {
				v, ok := stateValue[int64](e)
				return celsius(v), ok
			}
		case "humidity":
			sensor := service.NewHumiditySensor()
			s, c, read = sensor.S, sensor.CurrentRelativeHumidity.C, readNumber
		case "contact":
			sensor := service.NewContactSensor()
			s, c = sensor.S, sensor.ContactSensorState.C
			read = binaryReader(item.Inverted, false)
		case "motion":
			sensor := service.NewMotionSensor()
			s, c = sensor.S, sensor.MotionDetected.C
			read = binaryReader(item.Inverted, true)
		case "occupancy":
			sensor := service.NewOccupancySensor()
			s, c = sensor.S, sensor.OccupancyDetected.C
			read = binaryReader(item.Inverted, false)
		case "leak":
			sensor := service.NewLeakSensor()
			s, c = sensor.S, sensor.LeakDetected.C
			read = binaryReader(item.Inverted, false)
		default:
			continue
		}
		b.bind(c, item.EntityID, read, nil)
		if err := b.addService(slot(item), s, primary); err != nil {
			return err
		}
		primary = false
	}
	return nil
}

func binaryReader(inverted, boolean bool) func(Entity) (any, bool) {
	return func(e Entity) (any, bool) {
		value, ok := stateValue[bool](e)
		if !ok {
			return nil, false
		}
		if inverted {
			value = !value
		}
		if boolean {
			return value, true
		}
		if value {
			return 1, true
		}
		return 0, true
	}
}
