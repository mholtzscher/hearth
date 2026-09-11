package zigbee2mqtt

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	contractbinarysensorv1 "github.com/mholtzscher/hearth/entitytypes/binarysensorv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkbinarysensorv1 "github.com/mholtzscher/hearth/sdk/adapter/binarysensorv1"
)

const (
	occupancyExposeName  = "occupancy"
	occupancyKey         = "occupancy"
	occupancyDisplayName = "Occupancy"
)

// newBinarySensorPlan builds the complete read-only boolean translation for
// one State property. The declared upstream on/off scalars, not an assumed
// boolean, define decoding; translateBinarySensorState canonicalizes the raw
// payload and matches it against those scalars, so true/false, "ON"/"OFF",
// and 1/0 each decode exactly when the expose declares them. Get access
// alone controls startup refresh: a publish-only sensor has no get
// properties and a nil command translator, so it never creates a command
// route.
func newBinarySensorPlan(
	metadata adapter.EntityMetadata,
	property string,
	on, off scalarValue,
	gettable bool,
) (entityPlan, error) {
	support := binarySensorSupport()
	descriptor, descriptorErr := sdkbinarysensorv1.NewEntityDescriptor(metadata, support)
	if descriptorErr != nil {
		return entityPlan{}, descriptorErr
	}
	var getProperties []string
	if gettable {
		getProperties = []string{property}
	}
	return entityPlan{
		Descriptor:      descriptor,
		StateProperties: []string{property},
		GetProperties:   getProperties,
		DecodeState: func(
			entityID string,
			properties map[string]json.RawMessage,
			receivedAt time.Time,
		) (stateReport, bool, error) {
			raw, present := properties[property]
			if !present {
				return stateReport{}, false, nil
			}
			value, err := translateBinarySensorState(raw, on, off)
			if err != nil {
				return stateReport{}, false, err
			}
			observation, err := sdkbinarysensorv1.NewObservation(sdkbinarysensorv1.ObservationInput{
				EntityID: entityID, Support: support, State: contractbinarysensorv1.State(value),
				AdapterReceivedAt: receivedAt,
			})
			if err != nil {
				return stateReport{}, false, err
			}
			return stateReport{Observation: observation, semantic: value}, true, nil
		},
		TranslateCommand: nil,
	}, nil
}

func binarySensorSupport() contractbinarysensorv1.Support {
	return contractbinarysensorv1.Support{
		State:      contractbinarysensorv1.StateSupport{},
		Operations: contractbinarysensorv1.OperationSupport{},
	}
}

// translateBinarySensorState maps one upstream JSON value to Hearth true or
// false by comparing its canonical scalar with the expose's declared value_on
// and value_off. Any other value, including a scalar of the wrong JSON type,
// is an error so valid sibling State still publishes. Unmapped values are
// never coerced: the declaration is the only source of the on/off mapping.
func translateBinarySensorState(payload json.RawMessage, on, off scalarValue) (bool, error) {
	value, err := canonicalScalar(payload)
	if err != nil {
		return false, fmt.Errorf("decode binary sensor scalar: %w", err)
	}
	switch value.canonical {
	case on.canonical:
		return true, nil
	case off.canonical:
		return false, nil
	default:
		return false, errors.New("binary sensor scalar does not match value_on or value_off")
	}
}

// declaredBinarySensorValues resolves one binary root expose's on/off
// declaration into comparable scalar forms. Both values must be present and
// distinct; an absent, non-scalar, or identical pair leaves the expose
// ineligible instead of guessing a mapping.
func declaredBinarySensorValues(expose upstreamExpose) (scalarValue, scalarValue, bool) {
	on, onErr := canonicalScalar(expose.ValueOn)
	off, offErr := canonicalScalar(expose.ValueOff)
	if onErr != nil || offErr != nil || on.canonical == off.canonical {
		return scalarValue{}, scalarValue{}, false
	}
	return on, off, true
}

// planOccupancy supports one device-root binary occupancy expose on any
// Device kind. Eligibility requires the single resolved root expose to be
// named and property-aliased occupancy, to publish without accepting set
// commands, to own its property device-wide, and to declare explicit distinct
// value_on/value_off scalars. A duplicate occupancy root, a foreign claim on
// the property, or an absent/ambiguous declaration omits occupancy without
// affecting valid siblings.
//
// The contribution is supplemental: occupancy does not displace a primary
// light or relay kind, while an occupancy-only Device is a sensor. Get access
// alone controls startup refresh and never adds a command route.
func planOccupancy(input devicePlanningInput) plannerContribution {
	contribution := plannerContribution{Kind: upstreamDeviceKindSensor, Role: plannerRoleSupplemental}
	root, ok := input.Exposes.UniqueRoot(upstreamExposeBinary, occupancyExposeName)
	if !ok || !root.resolved {
		return contribution
	}
	expose := root.expose
	if expose.Property != occupancyExposeName ||
		!exposeCanPublish(expose) || exposeCanSet(expose) ||
		!input.Exposes.PropertyUnique(expose.Property) {
		return contribution
	}
	on, off, declared := declaredBinarySensorValues(expose)
	if !declared {
		return contribution
	}
	key, name := scopedIdentity(
		occupancyKey,
		occupancyDisplayName,
		expose.Endpoint,
		root.endpoint,
		root.scoped,
	)
	if !validDescriptorName(name) {
		return contribution
	}
	plan, err := newBinarySensorPlan(adapter.EntityMetadata{
		Key:        key,
		ExternalID: input.IEEE + "/" + entityLocation(root) + "/" + occupancyKey,
		Name:       name,
	}, expose.Property, on, off, exposeCanGet(expose))
	if err != nil {
		return contribution
	}
	contribution.Entities = append(contribution.Entities, plan)
	return contribution
}
