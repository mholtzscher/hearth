package esphome

import (
	"encoding/json"
	"errors"
	"math"
	"time"

	contractbinarysensorv1 "github.com/mholtzscher/hearth/entitytypes/binarysensorv1"
	contractbrightnessv1 "github.com/mholtzscher/hearth/entitytypes/brightnessv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	contractpressurev1 "github.com/mholtzscher/hearth/entitytypes/pressurev1"
	contractrelativehumidityv1 "github.com/mholtzscher/hearth/entitytypes/relativehumidityv1"
	contracttemperaturev1 "github.com/mholtzscher/hearth/entitytypes/temperaturev1"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkbinarysensorv1 "github.com/mholtzscher/hearth/sdk/adapter/binarysensorv1"
	sdkbrightnessv1 "github.com/mholtzscher/hearth/sdk/adapter/brightnessv1"
	sdknumericsensorv1 "github.com/mholtzscher/hearth/sdk/adapter/numericsensorv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
	sdkpressurev1 "github.com/mholtzscher/hearth/sdk/adapter/pressurev1"
	sdkrelativehumidityv1 "github.com/mholtzscher/hearth/sdk/adapter/relativehumidityv1"
	sdktemperaturev1 "github.com/mholtzscher/hearth/sdk/adapter/temperaturev1"
)

// errUnprojectable reports a state the route's Entity type cannot carry, such as an out-of-range value.
var errUnprojectable = errors.New("esphome: state outside the Entity type's range")

// entityRoute is one planned Hearth Entity and the upstream entity that feeds it.
type entityRoute struct {
	EntityKey  string
	Upstream   upstreamRef
	Kind       routeKind
	Conversion unitConversion
	// Numeric is the support of a routeNumeric Entity.
	Numeric sdknumericsensorv1.Support
}

// project converts one push into the Entity's canonical Observation. ok is false when the push carries no value;
// err wraps errUnprojectable when the value falls outside the Entity type.
func project(
	route entityRoute,
	entityID string,
	update nativeapi.StateUpdate,
	receivedAt time.Time,
) (adapter.Observation, bool, error) {
	if update.Missing {
		return adapter.Observation{}, false, nil
	}
	if update.Type == nativeapi.EntitySensor && (math.IsNaN(update.Number) || math.IsInf(update.Number, 0)) {
		return adapter.Observation{}, false, nil
	}
	observation, err := buildObservation(route, entityID, update, receivedAt)
	if err != nil {
		if _, ok := errors.AsType[*adapter.ValidationError](err); ok {
			return adapter.Observation{}, false, errors.Join(errUnprojectable, err)
		}
		return adapter.Observation{}, false, err
	}
	return observation, true, nil
}

func buildObservation(
	route entityRoute,
	entityID string,
	update nativeapi.StateUpdate,
	receivedAt time.Time,
) (adapter.Observation, error) {
	switch route.Kind {
	case routeTemperature:
		milli := route.Conversion.convert(update.Number)
		if milli < math.MinInt64 || milli > math.MaxInt64 {
			return adapter.Observation{}, errUnprojectable
		}
		return sdktemperaturev1.NewObservation(sdktemperaturev1.ObservationInput{
			EntityID: entityID, Support: temperatureSupport(),
			State: contracttemperaturev1.State(milli), AdapterReceivedAt: receivedAt,
		})
	case routeHumidity:
		return sdkrelativehumidityv1.NewObservation(sdkrelativehumidityv1.ObservationInput{
			EntityID: entityID, Support: humiditySupport(),
			State: contractrelativehumidityv1.State(update.Number), AdapterReceivedAt: receivedAt,
		})
	case routePressure:
		return sdkpressurev1.NewObservation(sdkpressurev1.ObservationInput{
			EntityID: entityID, Support: pressureSupport(),
			State: contractpressurev1.State(route.Conversion.convert(update.Number)), AdapterReceivedAt: receivedAt,
		})
	case routeNumeric:
		return sdknumericsensorv1.NewObservation(sdknumericsensorv1.ObservationInput{
			EntityID: entityID, Support: route.Numeric,
			State: sdknumericsensorv1.State(update.Number), AdapterReceivedAt: receivedAt,
		})
	case routeBinary:
		return sdkbinarysensorv1.NewObservation(sdkbinarysensorv1.ObservationInput{
			EntityID: entityID, Support: binarySensorSupport(),
			State: contractbinarysensorv1.State(update.Bool), AdapterReceivedAt: receivedAt,
		})
	case routeSwitchPower, routeLightPower:
		return sdkpowerv1.NewObservation(sdkpowerv1.ObservationInput{
			EntityID: entityID, Support: powerSupport(),
			State: contractpowerv1.State(update.Bool), AdapterReceivedAt: receivedAt,
		})
	case routeLightBrightness:
		if update.Bool &&
			(math.IsNaN(update.Brightness) || math.IsInf(update.Brightness, 0) || update.Brightness < 0 || update.Brightness > 1) {
			return adapter.Observation{}, errUnprojectable
		}
		return sdkbrightnessv1.NewObservation(sdkbrightnessv1.ObservationInput{
			EntityID: entityID, Support: brightnessSupport(),
			State: contractbrightnessv1.State(lightBrightness(update)), AdapterReceivedAt: receivedAt,
		})
	default:
		return adapter.Observation{}, errors.New("esphome: route has no Entity kind")
	}
}

// lightBrightness is 0 while the light is off and at least 1 while it is on, matching the Z-Wave JS Adapter.
func lightBrightness(update nativeapi.StateUpdate) int64 {
	if !update.Bool {
		return 0
	}
	percent := int64(math.Round(update.Brightness * 100)) //nolint:mnd // Percent; buildObservation checks the range.
	return max(percent, 1)
}

// stateMatches reports whether an Observation's value equals a Command's target. Brightness uses Core's
// one-percentage-point tolerance.
func stateMatches(kind routeKind, value json.RawMessage, target json.RawMessage) bool {
	if kind != routeLightBrightness {
		return string(value) == string(target)
	}
	var observed, wanted int64
	if json.Unmarshal(value, &observed) != nil || json.Unmarshal(target, &wanted) != nil {
		return false
	}
	return observed-wanted <= 1 && wanted-observed <= 1
}

func temperatureSupport() sdktemperaturev1.Support { return sdktemperaturev1.Support{} }

func humiditySupport() sdkrelativehumidityv1.Support { return sdkrelativehumidityv1.Support{} }

func pressureSupport() sdkpressurev1.Support { return sdkpressurev1.Support{} }

func binarySensorSupport() sdkbinarysensorv1.Support { return sdkbinarysensorv1.Support{} }

func powerSupport() sdkpowerv1.Support {
	return sdkpowerv1.Support{Operations: sdkpowerv1.OperationSupport{Set: sdkpowerv1.SetSupport{}}}
}

func brightnessSupport() sdkbrightnessv1.Support {
	return sdkbrightnessv1.Support{Operations: sdkbrightnessv1.OperationSupport{Set: sdkbrightnessv1.SetSupport{}}}
}
