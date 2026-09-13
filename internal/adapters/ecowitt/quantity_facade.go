package ecowitt

import (
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
	sdknumericsensorv1 "github.com/mholtzscher/hearth/sdk/adapter/numericsensorv1"
	sdkpressurev1 "github.com/mholtzscher/hearth/sdk/adapter/pressurev1"
	sdkrelativehumidityv1 "github.com/mholtzscher/hearth/sdk/adapter/relativehumidityv1"
	sdkspeedv1 "github.com/mholtzscher/hearth/sdk/adapter/speedv1"
	sdktemperaturev1 "github.com/mholtzscher/hearth/sdk/adapter/temperaturev1"
)

// This file is the Adapter's only dependency on the generated Entity-type
// facades. Every Observation and descriptor is built by a generated facade
// from typed input; the Adapter never hand-builds type-specific JSON. The
// catalog selects the facade through quantityKind, so adding a reusable
// physical quantity is one catalog entry plus one case here.

// newQuantityDescriptor builds one registered Entity descriptor for the
// generated facade that owns the plan's quantity. Every reusable physical
// quantity takes its canonical unit from the plan and is validated against the
// unit its generated contract schema fixes; the generic numeric sensor
// additionally takes its envelope.
func newQuantityDescriptor(
	kind quantityKind,
	metadata adapter.EntityMetadata,
	unit string,
	minimum, maximum float64,
) (adapter.EntityDescriptor, error) {
	switch kind {
	case quantityTemperature:
		return sdktemperaturev1.NewEntityDescriptor(metadata, temperatureSupport(unit))
	case quantityRelativeHumidity:
		return sdkrelativehumidityv1.NewEntityDescriptor(metadata, relativeHumiditySupport(unit))
	case quantityPressure:
		return sdkpressurev1.NewEntityDescriptor(metadata, pressureSupport(unit))
	case quantitySpeed:
		return sdkspeedv1.NewEntityDescriptor(metadata, speedSupport(unit))
	case quantityNumericSensor:
		return sdknumericsensorv1.NewEntityDescriptor(metadata, numericSensorSupport(unit, minimum, maximum))
	}
	return adapter.EntityDescriptor{}, fmt.Errorf("%w: %d", errUnsupportedKind, kind)
}

// temperatureSupport, relativeHumiditySupport, pressureSupport, and speedSupport
// pin the plan's canonical unit into each reusable physical quantity's support.
// The generated contract schema requires exactly one state unit and constrains
// it, so a plan whose unit disagrees with its Entity type is rejected at
// construction instead of publishing a mislabeled reading.
func temperatureSupport(unit string) sdktemperaturev1.Support {
	return sdktemperaturev1.Support{State: sdktemperaturev1.StateSupport{Unit: unit}}
}

func relativeHumiditySupport(unit string) sdkrelativehumidityv1.Support {
	return sdkrelativehumidityv1.Support{State: sdkrelativehumidityv1.StateSupport{Unit: unit}}
}

func pressureSupport(unit string) sdkpressurev1.Support {
	return sdkpressurev1.Support{State: sdkpressurev1.StateSupport{Unit: unit}}
}

func speedSupport(unit string) sdkspeedv1.Support {
	return sdkspeedv1.Support{State: sdkspeedv1.StateSupport{Unit: unit}}
}

// numericSensorSupport carries the configurable unit and envelope the generic
// numeric-sensor contract lets each Entity narrow.
func numericSensorSupport(unit string, minimum, maximum float64) sdknumericsensorv1.Support {
	return sdknumericsensorv1.Support{
		State: sdknumericsensorv1.StateSupport{
			Minimum: minimum,
			Maximum: maximum,
			Unit:    unit,
		},
	}
}

// newQuantityObservation builds one typed Observation from a normalized
// measurement. Support is reconstructed from the plan's unit and envelope so
// every Observation exactly matches its registration, and the generated facade
// validates that support and State before publication.
func newQuantityObservation(
	kind quantityKind,
	entityID string,
	state normalizedState,
	unit string,
	minimum, maximum float64,
	receivedAt time.Time,
	sourceUpdatedAt *time.Time,
) (adapter.Observation, error) {
	switch kind {
	case quantityTemperature:
		return sdktemperaturev1.NewObservation(sdktemperaturev1.ObservationInput{
			EntityID: entityID, Support: temperatureSupport(unit),
			State:             sdktemperaturev1.State(state.MilliCelsius),
			AdapterReceivedAt: receivedAt, SourceUpdatedAt: sourceUpdatedAt,
		})
	case quantityRelativeHumidity:
		return sdkrelativehumidityv1.NewObservation(sdkrelativehumidityv1.ObservationInput{
			EntityID: entityID, Support: relativeHumiditySupport(unit),
			State:             sdkrelativehumidityv1.State(state.Value),
			AdapterReceivedAt: receivedAt, SourceUpdatedAt: sourceUpdatedAt,
		})
	case quantityPressure:
		return sdkpressurev1.NewObservation(sdkpressurev1.ObservationInput{
			EntityID: entityID, Support: pressureSupport(unit),
			State:             sdkpressurev1.State(state.Value),
			AdapterReceivedAt: receivedAt, SourceUpdatedAt: sourceUpdatedAt,
		})
	case quantitySpeed:
		return sdkspeedv1.NewObservation(sdkspeedv1.ObservationInput{
			EntityID: entityID, Support: speedSupport(unit),
			State:             sdkspeedv1.State(state.Value),
			AdapterReceivedAt: receivedAt, SourceUpdatedAt: sourceUpdatedAt,
		})
	case quantityNumericSensor:
		return sdknumericsensorv1.NewObservation(sdknumericsensorv1.ObservationInput{
			EntityID:          entityID,
			Support:           numericSensorSupport(unit, minimum, maximum),
			State:             sdknumericsensorv1.State(state.Value),
			AdapterReceivedAt: receivedAt, SourceUpdatedAt: sourceUpdatedAt,
		})
	}
	return adapter.Observation{}, fmt.Errorf("%w: %d", errUnsupportedKind, kind)
}
