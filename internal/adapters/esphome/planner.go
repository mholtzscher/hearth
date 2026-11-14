package esphome

import (
	"fmt"
	"slices"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkbinarysensorv1 "github.com/mholtzscher/hearth/sdk/adapter/binarysensorv1"
	sdkbrightnessv1 "github.com/mholtzscher/hearth/sdk/adapter/brightnessv1"
	sdkcolorrgbv1 "github.com/mholtzscher/hearth/sdk/adapter/colorrgbv1"
	sdkcoverv1 "github.com/mholtzscher/hearth/sdk/adapter/coverv1"
	sdkenumsettingv1 "github.com/mholtzscher/hearth/sdk/adapter/enumsettingv1"
	sdklockv1 "github.com/mholtzscher/hearth/sdk/adapter/lockv1"
	sdknumericsensorv1 "github.com/mholtzscher/hearth/sdk/adapter/numericsensorv1"
	sdknumericsettingv1 "github.com/mholtzscher/hearth/sdk/adapter/numericsettingv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
	sdkpressurev1 "github.com/mholtzscher/hearth/sdk/adapter/pressurev1"
	sdkrelativehumidityv1 "github.com/mholtzscher/hearth/sdk/adapter/relativehumidityv1"
	sdktemperaturev1 "github.com/mholtzscher/hearth/sdk/adapter/temperaturev1"
	sdktextsensorv1 "github.com/mholtzscher/hearth/sdk/adapter/textsensorv1"
	sdkthermostatv1 "github.com/mholtzscher/hearth/sdk/adapter/thermostatv1"
)

const (
	// maximumPlannedEntities is Core's per-registration limit; a physical device is never split.
	maximumPlannedEntities = 64

	deviceKindSensor = "sensor"
	deviceKindLight  = "light"
	deviceKindRelay  = "relay"
)

// Skip reasons logged for entities the plan leaves out.
const (
	skipExposureDefault       = "exposure_default"
	skipExcluded              = "excluded"
	skipTypeUnsupported       = "type_unsupported"
	skipSubDevice             = "sub_device"
	skipAssumedState          = "assumed_state"
	skipUnitUnsupported       = "unit_unsupported"
	skipCapabilityUnsupported = "capability_unsupported"
)

// unsupportedDeviceError reports a device the Adapter registers nothing for. Reason is an availability reason code.
type unsupportedDeviceError struct {
	Reason string
	Detail string
}

func (err *unsupportedDeviceError) Error() string {
	return "esphome: device unsupported: " + err.Detail
}

// devicePlan is one device's registration and the routes that feed its Entities.
type devicePlan struct {
	Registration adapter.Registration
	Routes       []entityRoute
	Skipped      []skippedEntity
	// UnmatchedOverrides lists include or exclude entries that name no entity on the device.
	UnmatchedOverrides []EntityRef
}

type skippedEntity struct {
	Ref          EntityRef
	UpstreamType string
	Reason       string
}

func (planner *entityPlanner) skipCapability(entity nativeapi.EntityInfo, reason string) {
	planner.skipped = append(planner.skipped, skippedEntity{
		Ref:          EntityRef{Type: entity.Type, Name: entity.Name},
		UpstreamType: entity.UpstreamType, Reason: reason,
	})
}

// planDevice maps one device's entities into a Registration. It is pure: it reads only its arguments.
func planDevice(
	mac MAC,
	info nativeapi.DeviceInfo,
	entities []nativeapi.EntityInfo,
	exposure Exposure,
) (devicePlan, error) {
	if info.HasDeepSleep {
		return devicePlan{}, &unsupportedDeviceError{Reason: reasonDeepSleepUnsupported, Detail: "deep sleep"}
	}
	deviceName := info.FriendlyName
	if deviceName == "" {
		deviceName = info.Name
	}
	if deviceName == "" {
		deviceName = mac.String()
	}
	planner := entityPlanner{
		mac:        mac,
		exposure:   exposure,
		deviceName: deviceName,
		seen:       map[EntityRef]struct{}{},
		keys:       map[string]string{},
	}
	for _, entity := range entities {
		if err := planner.add(entity); err != nil {
			return devicePlan{}, err
		}
	}
	if len(planner.descriptors) > maximumPlannedEntities {
		return devicePlan{}, &unsupportedDeviceError{
			Reason: reasonPlanningFailed,
			Detail: fmt.Sprintf("%d planned Entities exceed %d", len(planner.descriptors), maximumPlannedEntities),
		}
	}
	externalID := mac.String()
	plan := devicePlan{
		Registration: adapter.Registration{
			BindingKey: mac.BindingKey(),
			Device: adapter.DeviceDescriptor{
				ExternalID: &externalID,
				Name:       truncateName(deviceName),
				Kind:       planner.deviceKind(),
			},
			Entities: planner.descriptors,
		},
		Routes:  planner.routes,
		Skipped: planner.skipped,
	}
	for _, overrides := range []map[EntityRef]struct{}{exposure.Include, exposure.Exclude} {
		for ref := range overrides {
			if _, seen := planner.seen[ref]; !seen {
				plan.UnmatchedOverrides = append(plan.UnmatchedOverrides, ref)
			}
		}
	}
	return plan, nil
}

// entityPlanner accumulates descriptors and routes in upstream list order.
type entityPlanner struct {
	mac         MAC
	exposure    Exposure
	deviceName  string
	descriptors []adapter.EntityDescriptor
	routes      []entityRoute
	skipped     []skippedEntity
	seen        map[EntityRef]struct{}
	// keys maps each planned Entity key to its upstream identity to detect collisions.
	keys map[string]string
}

func (planner *entityPlanner) add(entity nativeapi.EntityInfo) error {
	ref := EntityRef{Type: entity.Type, Name: entity.Name}
	if entity.Type != nativeapi.EntityOther {
		planner.seen[ref] = struct{}{}
	}
	if reason := planner.skipReason(entity, ref); reason != "" {
		planner.skipped = append(
			planner.skipped,
			skippedEntity{Ref: ref, UpstreamType: entity.UpstreamType, Reason: reason},
		)
		return nil
	}
	upstream := upstreamRef{Type: entity.Type, DeviceID: entity.DeviceID, Key: entity.Key}
	identity := upstreamIdentity(entity)
	name := entity.Name
	if name == "" {
		name = planner.deviceName
	}
	switch entity.Type {
	case nativeapi.EntityClimate:
		return planner.planThermostat(name, identity, upstream, entity)
	case nativeapi.EntityNumber, nativeapi.EntitySelect:
		return planner.planSetting(name, identity, upstream, entity)
	case nativeapi.EntitySensor:
		mapping, ok := catalogSensor(entity.DeviceClass, entity.Unit)
		if !ok {
			planner.skipped = append(
				planner.skipped,
				skippedEntity{Ref: ref, UpstreamType: entity.UpstreamType, Reason: skipUnitUnsupported},
			)
			return nil
		}
		return planner.plan(
			name,
			identity,
			entityRoute{
				Upstream:   upstream,
				Kind:       mapping.kind,
				Conversion: mapping.conversion,
				Numeric:    numericSupport(mapping),
			},
		)
	case nativeapi.EntityBinarySensor:
		return planner.plan(name, identity, entityRoute{Upstream: upstream, Kind: routeBinary})
	case nativeapi.EntityTextSensor:
		return planner.plan(name, identity, entityRoute{Upstream: upstream, Kind: routeText})
	case nativeapi.EntitySwitch:
		return planner.plan(name, identity+"#power", entityRoute{Upstream: upstream, Kind: routeSwitchPower})
	case nativeapi.EntityLight:
		return planner.planLight(name, identity, upstream, entity)
	case nativeapi.EntityCover:
		return planner.plan(name, identity, entityRoute{Upstream: upstream, Kind: routeCover,
			Cover: coverSupport(entity.SupportsStop)})
	case nativeapi.EntityLock:
		return planner.plan(name, identity, entityRoute{Upstream: upstream, Kind: routeLock,
			Lock: lockSupport(!entity.RequiresCode)})
	case nativeapi.EntityOther:
		return nil
	default:
		return nil
	}
}

func (planner *entityPlanner) planLight(
	name, identity string,
	upstream upstreamRef,
	entity nativeapi.EntityInfo,
) error {
	if err := planner.plan(
		name,
		identity+"#power",
		entityRoute{Upstream: upstream, Kind: routeLightPower},
	); err != nil {
		return err
	}
	if supportsBrightness(entity.ColorModes) {
		if err := planner.plan(name+" Brightness", identity+"#brightness",
			entityRoute{Upstream: upstream, Kind: routeLightBrightness}); err != nil {
			return err
		}
	}
	if supportsRGB(entity.ColorModes) {
		if err := planner.plan(name+" Color", identity+"#color",
			entityRoute{Upstream: upstream, Kind: routeLightColor}); err != nil {
			return err
		}
	}
	if len(entity.Effects) > 0 {
		choices := slices.Clone(entity.Effects)
		if !slices.Contains(choices, "None") {
			choices = append([]string{"None"}, choices...)
		}
		return planner.plan(name+" Effect", identity+"#effect", entityRoute{
			Upstream: upstream, Kind: routeLightEffect, Effect: effectSupport(choices),
		})
	}
	return nil
}

func supportsRGB(modes []uint32) bool {
	return slices.ContainsFunc(modes, func(mode uint32) bool { return mode&nativeapi.ColorModeRGB != 0 })
}

// skipReason applies the eligibility and exposure rules in order. An empty reason plans the entity.
func (planner *entityPlanner) skipReason(entity nativeapi.EntityInfo, ref EntityRef) string {
	switch {
	case entity.DeviceID != eligibleDeviceID:
		return skipSubDevice
	case entity.Type == nativeapi.EntityOther:
		return skipTypeUnsupported
	case (entity.Type == nativeapi.EntitySwitch || entity.Type == nativeapi.EntityCover ||
		entity.Type == nativeapi.EntityLock) && entity.AssumedState:
		return skipAssumedState
	case entity.Type == nativeapi.EntityCover && !entity.SupportsPosition:
		return skipCapabilityUnsupported
	}
	if _, excluded := planner.exposure.Exclude[ref]; excluded {
		return skipExcluded
	}
	if _, included := planner.exposure.Include[ref]; included {
		return ""
	}
	if entity.Category != nativeapi.CategoryNone || entity.DisabledByDefault {
		return skipExposureDefault
	}
	return ""
}

// plan builds one descriptor through its typed facade and records its route.
func (planner *entityPlanner) plan(name, identity string, route entityRoute) error {
	key := entityKey(name, identity)
	if previous, collides := planner.keys[key]; collides && previous != identity {
		return &unsupportedDeviceError{Reason: reasonPlanningFailed, Detail: "Entity key collision"}
	}
	planner.keys[key] = identity
	externalID := planner.mac.String() + "/" + identity
	metadata := adapter.EntityMetadata{Key: key, ExternalID: externalID, Name: truncateName(name)}
	descriptor, err := newDescriptor(metadata, route)
	if err != nil {
		return &unsupportedDeviceError{Reason: reasonPlanningFailed, Detail: err.Error()}
	}
	route.EntityKey = key
	planner.descriptors = append(planner.descriptors, descriptor)
	planner.routes = append(planner.routes, route)
	return nil
}

func (planner *entityPlanner) deviceKind() string {
	for _, route := range planner.routes {
		if route.Kind == routeCover {
			return "cover"
		}
	}
	for _, route := range planner.routes {
		if route.Kind == routeThermostat {
			return "thermostat"
		}
	}
	hasBrightness := false
	for _, route := range planner.routes {
		switch route.Kind {
		case routeTemperature, routeHumidity, routePressure, routeNumeric, routeBinary, routeText:
			return deviceKindSensor
		case routeLightBrightness:
			hasBrightness = true
		case routeSwitchPower, routeLightPower, routeLightColor, routeLightEffect, routeCover, routeLock,
			routeNumber, routeSelect, routeThermostat:
			// These controls do not distinguish the device kind.
		}
	}
	if hasBrightness {
		return deviceKindLight
	}
	return deviceKindRelay
}

func newDescriptor(metadata adapter.EntityMetadata, route entityRoute) (adapter.EntityDescriptor, error) {
	switch route.Kind {
	case routeThermostat:
		return sdkthermostatv1.NewEntityDescriptor(metadata, route.Thermostat)
	case routeNumber:
		return sdknumericsettingv1.NewEntityDescriptor(metadata, route.Number)
	case routeSelect:
		return sdkenumsettingv1.NewEntityDescriptor(metadata, route.Select)
	case routeTemperature:
		return sdktemperaturev1.NewEntityDescriptor(metadata, temperatureSupport())
	case routeHumidity:
		return sdkrelativehumidityv1.NewEntityDescriptor(metadata, humiditySupport())
	case routePressure:
		return sdkpressurev1.NewEntityDescriptor(metadata, pressureSupport())
	case routeNumeric:
		return sdknumericsensorv1.NewEntityDescriptor(metadata, route.Numeric)
	case routeBinary:
		return sdkbinarysensorv1.NewEntityDescriptor(metadata, binarySensorSupport())
	case routeSwitchPower, routeLightPower:
		return sdkpowerv1.NewEntityDescriptor(metadata, powerSupport())
	case routeLightBrightness:
		return sdkbrightnessv1.NewEntityDescriptor(metadata, brightnessSupport())
	case routeLightColor:
		return sdkcolorrgbv1.NewEntityDescriptor(metadata, colorSupport())
	case routeLightEffect:
		return sdkenumsettingv1.NewEntityDescriptor(metadata, route.Effect)
	case routeText:
		return sdktextsensorv1.NewEntityDescriptor(metadata, sdktextsensorv1.Support{})
	case routeCover:
		return sdkcoverv1.NewEntityDescriptor(metadata, route.Cover)
	case routeLock:
		return sdklockv1.NewEntityDescriptor(metadata, route.Lock)
	default:
		return adapter.EntityDescriptor{}, fmt.Errorf("route kind %d has no Entity type", route.Kind)
	}
}

func numericSupport(mapping sensorMapping) sdknumericsensorv1.Support {
	if mapping.kind != routeNumeric {
		return sdknumericsensorv1.Support{}
	}
	support := sdknumericsensorv1.Support{State: sdknumericsensorv1.StateSupport{
		Minimum: mapping.minimum, Maximum: mapping.maximum, Unit: mapping.unit,
	}}
	if mapping.quantityKind != "" {
		kind := mapping.quantityKind
		support.State.QuantityKind = &kind
	}
	return support
}

func supportsBrightness(colorModes []uint32) bool {
	for _, mode := range colorModes {
		if mode&nativeapi.ColorModeBrightness != 0 {
			return true
		}
	}
	return false
}
