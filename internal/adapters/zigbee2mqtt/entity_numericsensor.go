package zigbee2mqtt

import (
	"encoding/json"
	"sync"
	"time"

	contractnumericsensorv1 "github.com/mholtzscher/hearth/entitytypes/numericsensorv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdknumericsensorv1 "github.com/mholtzscher/hearth/sdk/adapter/numericsensorv1"
)

//nolint:gochecknoglobals // Lazy, concurrency-safe cache of authoritative codecs.
var numericSensorCodecs = sync.OnceValues(contractnumericsensorv1.Compile)

// newNumericSensorPlan translates a read-only JSON number without changing
// its scale. The contract enforces finite bounds and preserves fractions;
// get access alone controls refresh. Specialized conversions, such as
// temperature and exact-integer link quality, keep separate constructors.
func newNumericSensorPlan(
	metadata adapter.EntityMetadata,
	property string,
	minimum, maximum float64,
	unit string,
	gettable bool,
) (entityPlan, error) {
	support := contractnumericsensorv1.Support{
		State: contractnumericsensorv1.StateSupport{
			Minimum: minimum,
			Maximum: maximum,
			Unit:    unit,
		},
		Operations: contractnumericsensorv1.OperationSupport{},
	}
	descriptor, err := sdknumericsensorv1.NewEntityDescriptor(metadata, support)
	if err != nil {
		return entityPlan{}, err
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
			codecs, codecErr := numericSensorCodecs()
			if codecErr != nil {
				return stateReport{}, false, codecErr
			}
			value, _, decodeErr := codecs.State.Decode(raw)
			if decodeErr != nil {
				return stateReport{}, false, decodeErr
			}
			observation, observationErr := sdknumericsensorv1.NewObservation(sdknumericsensorv1.ObservationInput{
				EntityID: entityID, Support: support, State: value,
				AdapterReceivedAt: receivedAt,
			})
			if observationErr != nil {
				return stateReport{}, false, observationErr
			}
			return stateReport{Observation: observation, semantic: float64(value)}, true, nil
		},
		TranslateCommand: nil,
	}, nil
}

// planNumericSensorRoot applies a capability mapping to a resolved root.
// Family planners own root cardinality and bound selection; shared checks
// enforce read-only access, exact units, unique properties and scoped identity.
func planNumericSensorRoot(input devicePlanningInput, root indexedExpose, mapping numericSensorMapping) *entityPlan {
	if !root.resolved || root.expose.Type != upstreamExposeNumeric || root.expose.Name != mapping.exposeName {
		return nil
	}
	expose := root.expose
	if expose.Unit != mapping.upstreamUnit || !readOnlySensorEligible(input, expose) {
		return nil
	}
	key, name := scopedIdentity(mapping.key, mapping.displayName, expose.Endpoint, root.endpoint, root.scoped)
	if !validDescriptorName(name) {
		return nil
	}
	plan, err := newNumericSensorPlan(adapter.EntityMetadata{
		Key:        key,
		ExternalID: input.IEEE + "/" + entityLocation(root) + "/" + mapping.key,
		Name:       name,
	}, expose.Property, mapping.minimum, mapping.maximum, mapping.unit, exposeCanGet(expose))
	if err != nil {
		return nil
	}
	return &plan
}
