package esphome

import (
	"encoding/json"
	"errors"
	"math"
	"time"

	contractbinarysensorv1 "github.com/mholtzscher/hearth/entitytypes/binarysensorv1"
	contractbrightnessv1 "github.com/mholtzscher/hearth/entitytypes/brightnessv1"
	contractcolorrgbv1 "github.com/mholtzscher/hearth/entitytypes/colorrgbv1"
	contractcoverv1 "github.com/mholtzscher/hearth/entitytypes/coverv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	contractpressurev1 "github.com/mholtzscher/hearth/entitytypes/pressurev1"
	contractrelativehumidityv1 "github.com/mholtzscher/hearth/entitytypes/relativehumidityv1"
	contracttemperaturev1 "github.com/mholtzscher/hearth/entitytypes/temperaturev1"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkbinarysensorv1 "github.com/mholtzscher/hearth/sdk/adapter/binarysensorv1"
	sdkbrightnessv1 "github.com/mholtzscher/hearth/sdk/adapter/brightnessv1"
	sdkcolorrgbv1 "github.com/mholtzscher/hearth/sdk/adapter/colorrgbv1"
	sdkcoverv1 "github.com/mholtzscher/hearth/sdk/adapter/coverv1"
	sdkenumsettingv1 "github.com/mholtzscher/hearth/sdk/adapter/enumsettingv1"
	sdklockv1 "github.com/mholtzscher/hearth/sdk/adapter/lockv1"
	sdknumericsensorv1 "github.com/mholtzscher/hearth/sdk/adapter/numericsensorv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
	sdkpressurev1 "github.com/mholtzscher/hearth/sdk/adapter/pressurev1"
	sdkrelativehumidityv1 "github.com/mholtzscher/hearth/sdk/adapter/relativehumidityv1"
	sdktemperaturev1 "github.com/mholtzscher/hearth/sdk/adapter/temperaturev1"
	sdktextsensorv1 "github.com/mholtzscher/hearth/sdk/adapter/textsensorv1"
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
	// Effect constrains State and Commands to effects advertised by this light.
	Effect sdkenumsettingv1.Support
	Cover  sdkcoverv1.Support
	Lock   sdklockv1.Support
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
	case routeCover:
		state, err := coverState(update)
		if err != nil {
			return adapter.Observation{}, err
		}
		return sdkcoverv1.NewObservation(sdkcoverv1.ObservationInput{
			EntityID: entityID, Support: route.Cover, State: state, AdapterReceivedAt: receivedAt,
		})
	case routeLock:
		return sdklockv1.NewObservation(sdklockv1.ObservationInput{
			EntityID:          entityID,
			Support:           route.Lock,
			State:             sdklockv1.State(update.LockState),
			AdapterReceivedAt: receivedAt,
		})
	case routeText:
		return sdktextsensorv1.NewObservation(sdktextsensorv1.ObservationInput{
			EntityID: entityID, Support: sdktextsensorv1.Support{}, State: sdktextsensorv1.State(update.Text),
			AdapterReceivedAt: receivedAt,
		})
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
	case routeLightColor:
		state, err := rgbState(update.RGB, update.ColorBrightness, update.ColorMode&nativeapi.ColorModeRGB != 0)
		if err != nil {
			return adapter.Observation{}, err
		}
		return sdkcolorrgbv1.NewObservation(sdkcolorrgbv1.ObservationInput{
			EntityID: entityID, Support: colorSupport(), State: state, AdapterReceivedAt: receivedAt,
		})
	case routeLightEffect:
		effect := update.Effect
		if effect == "" {
			effect = "None"
		}
		return sdkenumsettingv1.NewObservation(sdkenumsettingv1.ObservationInput{
			EntityID:          entityID,
			Support:           route.Effect,
			State:             sdkenumsettingv1.State(effect),
			AdapterReceivedAt: receivedAt,
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

// stateMatches uses Core's generated RGB and cover matchers and one-point brightness tolerance.
// Remaining routes require exact value equality.
func stateMatches(kind routeKind, value json.RawMessage, target json.RawMessage) bool {
	if kind == routeCover {
		var observed contractcoverv1.State
		var parameters contractcoverv1.SetParameters
		if json.Unmarshal(value, &observed) != nil || json.Unmarshal(target, &parameters) != nil {
			return false
		}
		return contractcoverv1.SetSatisfied(parameters, observed)
	}
	if kind == routeLightColor {
		var observed, wanted contractcolorrgbv1.State
		if json.Unmarshal(value, &observed) != nil || json.Unmarshal(target, &wanted) != nil {
			return false
		}
		return contractcolorrgbv1.SetSatisfied(contractcolorrgbv1.SetParameters{
			Red: wanted.Red, Green: wanted.Green, Blue: wanted.Blue,
		}, observed)
	}
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
