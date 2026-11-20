package homekit

import (
	"errors"
	"net/http"
	"slices"
	"sync"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
)

type accessoryBuilder struct {
	exposure    Exposure
	inventory   Inventory
	displayUnit string
	lookup      func(string) Entity
	submit      func(uint64, writeIntent) int
	accessory   *accessory.A
	bindings    []characteristicBinding
}

type characteristicBinding struct {
	entity string
	update func(Entity)
}

func buildAccessory(
	exposure Exposure, inventory Inventory, displayUnit string, lookup func(string) Entity,
	submit func(uint64, writeIntent) int,
) (*accessoryBuilder, error) {
	name := exposure.Name
	if name == "" {
		name = inventory.Devices[exposure.DeviceID].Name
	}
	if name == "" {
		name = exposure.DeviceID
	}
	a := accessory.New(accessory.Info{
		Name: name, SerialNumber: exposure.DeviceID, Manufacturer: "Hearth", Model: string(exposure.Kind),
	}, accessory.TypeOther)
	a.Id = exposure.AID
	if err := assignServiceIDs(a.Info.S, 0); err != nil {
		return nil, err
	}
	b := &accessoryBuilder{
		exposure:    exposure,
		inventory:   inventory,
		displayUnit: displayUnit,
		lookup:      lookup,
		submit:      submit,
		accessory:   a,
	}
	if err := b.addMappedServices(); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *accessoryBuilder) addMappedServices() error {
	switch b.exposure.Kind {
	case KindLightbulb:
		return mapLightbulb(b)
	case KindSwitch, KindOutlet:
		return mapSwitch(b)
	case KindLock:
		return mapLock(b)
	case KindWindowCovering:
		return mapCover(b)
	case KindThermostat:
		return mapThermostat(b)
	case KindAirPurifier:
		return mapAirPurifier(b)
	case KindSensor:
		return mapSensor(b)
	default:
		return errors.New("unsupported HomeKit accessory kind")
	}
}

func (b *accessoryBuilder) addService(slotKey string, s *service.S, primary bool) error {
	slot := b.exposure.Slots[slotKey]
	if slot == 0 {
		return errors.New("HomeKit service has no persisted slot")
	}
	if err := assignServiceIDs(s, slot); err != nil {
		return err
	}
	s.Primary = primary
	b.accessory.AddS(s)
	return nil
}

func (b *accessoryBuilder) bind(
	c *characteristic.C, entityID string, read func(Entity) (any, bool), write func(any) (writeIntent, int),
) {
	binding := &boundValue{owner: b, c: c, entityID: entityID, read: read}
	if read != nil {
		c.ValueRequestFunc = binding.value
	}
	if write != nil {
		c.ManagedSetValueRequestFunc = binding.setter(write)
	} else {
		c.Permissions = slices.DeleteFunc(c.Permissions, func(value string) bool {
			return value == characteristic.PermissionWrite || value == characteristic.PermissionTimedWrite
		})
	}
	b.bindings = append(b.bindings, characteristicBinding{entity: entityID, update: binding.update})
	binding.update(b.inventory.Entities[entityID])
}

type boundValue struct {
	owner    *accessoryBuilder
	c        *characteristic.C
	entityID string
	read     func(Entity) (any, bool)

	publishMu sync.Mutex
	mu        sync.Mutex
	serial    uint64
	pending   any
}

func (v *boundValue) value(_ *http.Request) (any, int) {
	entity := v.owner.lookup(v.entityID)
	if !entity.reachable() {
		return nil, hapCommunicationFailure
	}
	v.mu.Lock()
	pending := v.pending
	v.mu.Unlock()
	if pending != nil {
		return pending, 0
	}
	if value, ok := v.read(entity); ok {
		return value, 0
	}
	return nil, hapCommunicationFailure
}

func (v *boundValue) update(entity Entity) {
	v.publishMu.Lock()
	defer v.publishMu.Unlock()
	v.publish(entity, false)
}

func (v *boundValue) publish(entity Entity, force bool) {
	if v.read == nil || !entity.reachable() {
		return
	}
	value, ok := v.read(entity)
	if !ok {
		return
	}
	v.mu.Lock()
	pending := v.pending != nil
	v.mu.Unlock()
	if pending {
		return
	}
	changed, status := v.c.SetValueRequest(value, nil)
	if force && changed == nil && status == 0 {
		v.c.NotifyValue()
	}
}

func (v *boundValue) setter(convert func(any) (writeIntent, int)) func(any, *http.Request) (any, int) {
	return func(value any, _ *http.Request) (any, int) {
		if !v.owner.lookup(v.entityID).reachable() {
			return nil, hapCommunicationFailure
		}
		intent, status := convert(value)
		if status != 0 || intent.Operation == "" {
			return nil, status
		}
		v.mu.Lock()
		defer v.mu.Unlock()
		serial := v.serial + 1
		intent.finish = func(failed bool) { v.complete(serial, failed) }
		status = v.owner.submit(v.owner.exposure.AID, intent)
		if status == 0 {
			v.serial = serial
			if v.c.Type == characteristic.TypeLockTargetState || v.c.Type == characteristic.TypeTargetPosition {
				v.pending = value
			}
		}
		return nil, status
	}
}

func (v *boundValue) complete(serial uint64, failed bool) {
	v.mu.Lock()
	if serial != v.serial {
		v.mu.Unlock()
		return
	}
	hadPending := v.pending != nil
	v.pending = nil
	v.mu.Unlock()
	if hadPending || failed {
		v.publishMu.Lock()
		defer v.publishMu.Unlock()
		v.publish(v.owner.lookup(v.entityID), failed)
	}
}

func assignServiceIDs(s *service.S, slot uint16) error {
	const stride, characteristicStart = 100, 2
	base := uint64(slot) * stride
	s.Id = base
	if slot == 0 {
		s.Id = 1
	}
	for _, c := range s.Cs {
		offset := slices.Index(characteristicIDs(), c.Type)
		if offset < 0 || offset+characteristicStart >= stride {
			return errors.New("HomeKit characteristic has no identifier allocation")
		}
		c.Id = base + uint64(offset+characteristicStart)
	}
	return nil
}

// Keep these positions stable across releases; only append newly used types.
func characteristicIDs() []string {
	return []string{
		characteristic.TypeIdentify,
		characteristic.TypeManufacturer,
		characteristic.TypeModel,
		characteristic.TypeName,
		characteristic.TypeSerialNumber,
		characteristic.TypeFirmwareRevision,
		characteristic.TypeOn,
		characteristic.TypeBrightness,
		characteristic.TypeHue,
		characteristic.TypeSaturation,
		characteristic.TypeColorTemperature,
		characteristic.TypeOutletInUse,
		characteristic.TypeLockCurrentState,
		characteristic.TypeLockTargetState,
		characteristic.TypeCurrentPosition,
		characteristic.TypeTargetPosition,
		characteristic.TypePositionState,
		characteristic.TypeHoldPosition,
		characteristic.TypeCurrentHeatingCoolingState,
		characteristic.TypeTargetHeatingCoolingState,
		characteristic.TypeCurrentTemperature,
		characteristic.TypeTargetTemperature,
		characteristic.TypeTemperatureDisplayUnits,
		characteristic.TypeHeatingThresholdTemperature,
		characteristic.TypeCoolingThresholdTemperature,
		characteristic.TypeCurrentRelativeHumidity,
		characteristic.TypeTargetRelativeHumidity,
		characteristic.TypeActive,
		characteristic.TypeCurrentAirPurifierState,
		characteristic.TypeTargetAirPurifierState,
		characteristic.TypeRotationSpeed,
		characteristic.TypeLockPhysicalControls,
		characteristic.TypeAirQuality,
		characteristic.TypePM2_5Density,
		characteristic.TypeFilterLifeLevel,
		characteristic.TypeFilterChangeIndication,
		characteristic.TypeContactSensorState,
		characteristic.TypeMotionDetected,
		characteristic.TypeOccupancyDetected,
		characteristic.TypeLeakDetected,
	}
}
