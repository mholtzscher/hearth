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

// appendBinarySensorPlans adds one read-only Entity for each eligible root
// matching a binary capability in inventory order. Because it visits every
// retained root, endpoint-scoped roots of the same capability keep distinct
// keys while same-key duplicates are omitted later by per-contribution
// deduplication.
func appendBinarySensorPlans(
	contribution *plannerContribution,
	input devicePlanningInput,
	mapping binarySensorMapping,
) {
	for _, root := range input.Exposes.roots {
		if plan := planBinarySensorRoot(input, root, mapping); plan != nil {
			contribution.Entities = append(contribution.Entities, *plan)
		}
	}
}

// planBinarySensorRoot applies a binary capability mapping to one resolved
// root. Root selection, the Device-unique State property, endpoint identity,
// and the declared distinct value_on/value_off scalars all come from
// inventory, never from the record. An ineligible root returns nil and never
// suppresses a valid sibling.
func planBinarySensorRoot(
	input devicePlanningInput,
	root indexedExpose,
	mapping binarySensorMapping,
) *entityPlan {
	if !root.resolved || root.expose.Type != upstreamExposeBinary || root.expose.Name != mapping.exposeName {
		return nil
	}
	expose := root.expose
	if !readOnlySensorEligible(input, expose) {
		return nil
	}
	on, off, declared := declaredBinarySensorValues(expose)
	if !declared {
		return nil
	}
	key, name := scopedIdentity(mapping.key, mapping.displayName, expose.Endpoint, root.endpoint, root.scoped)
	if !validDescriptorName(name) {
		return nil
	}
	plan, err := newBinarySensorPlan(adapter.EntityMetadata{
		Key:        key,
		ExternalID: input.IEEE + "/" + entityLocation(root) + "/" + mapping.key,
		Name:       name,
	}, expose.Property, on, off, exposeCanGet(expose))
	if err != nil {
		return nil
	}
	return &plan
}
