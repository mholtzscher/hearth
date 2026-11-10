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
// quantity takes its canonical unit from its State contract; only the generic
// numeric sensor takes a plan-owned unit and envelope.
func newQuantityDescriptor(
	kind quantityKind,
	metadata adapter.EntityMetadata,
	unit string,
	minimum, maximum float64,
	semanticKind string,
) (adapter.EntityDescriptor, error) {
	switch kind {
	case quantityTemperature:
		return sdktemperaturev1.NewEntityDescriptor(metadata, sdktemperaturev1.Support{})
	case quantityRelativeHumidity:
		return sdkrelativehumidityv1.NewEntityDescriptor(metadata, sdkrelativehumidityv1.Support{})
	case quantityPressure:
		return sdkpressurev1.NewEntityDescriptor(metadata, sdkpressurev1.Support{})
	case quantitySpeed:
		return sdkspeedv1.NewEntityDescriptor(metadata, sdkspeedv1.Support{})
	case quantityNumericSensor:
		support := numericSensorSupport(unit, minimum, maximum)
		if semanticKind != "" {
			support.State.QuantityKind = &semanticKind
		}
		return sdknumericsensorv1.NewEntityDescriptor(metadata, support)
	}
	return adapter.EntityDescriptor{}, fmt.Errorf("%w: %d", errUnsupportedKind, kind)
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
// measurement. Generic sensor support uses the same plan-owned unit and
// envelope as registration. The generated facade validates support and State
// before publication.
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
			EntityID: entityID, Support: sdktemperaturev1.Support{},
			State:             sdktemperaturev1.State(state.MilliCelsius),
			AdapterReceivedAt: receivedAt, SourceUpdatedAt: sourceUpdatedAt,
		})
	case quantityRelativeHumidity:
		return sdkrelativehumidityv1.NewObservation(sdkrelativehumidityv1.ObservationInput{
			EntityID: entityID, Support: sdkrelativehumidityv1.Support{},
			State:             sdkrelativehumidityv1.State(state.Value),
			AdapterReceivedAt: receivedAt, SourceUpdatedAt: sourceUpdatedAt,
		})
	case quantityPressure:
		return sdkpressurev1.NewObservation(sdkpressurev1.ObservationInput{
			EntityID: entityID, Support: sdkpressurev1.Support{},
			State:             sdkpressurev1.State(state.Value),
			AdapterReceivedAt: receivedAt, SourceUpdatedAt: sourceUpdatedAt,
		})
	case quantitySpeed:
		return sdkspeedv1.NewObservation(sdkspeedv1.ObservationInput{
			EntityID: entityID, Support: sdkspeedv1.Support{},
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
