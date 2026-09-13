package zigbee2mqtt

import (
	"encoding/json"
	"sync"
	"time"

	contractmeasurementv1 "github.com/mholtzscher/hearth/entitytypes/measurementv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkmeasurementv1 "github.com/mholtzscher/hearth/sdk/adapter/measurementv1"
)

// measurementMapping maps one exact Zigbee2MQTT numeric expose to a read-only
// hearth.measurement/v1 Entity. The record repeats the measurement kind,
// canonical unit, and bounds so the adapter can build a self-describing
// descriptor, but it is not authoritative: the generated facade rejects any
// kind/unit pair that support.schema.json does not admit.
//
// Exact upstream-unit matching remains adapter-owned. Native-to-canonical
// conversion is adapter-owned; the initial mappings need only the Celsius
// magnitude relabeled to the canonical UCUM `Cel` unit.
//
// This is capability data, not a device-model catalog or a planning language.
// Familiar exposes on another model need no entry. New conversion or family
// behavior belongs in Go, not in flags or expressions on these records.
type measurementMapping struct {
	exposeName      string
	key             string
	displayName     string
	upstreamUnit    string
	measurementKind string
	canonicalUnit   string
	minimum         float64
	maximum         float64
}

// support builds the hearth.measurement/v1 support for one mapping. The kind,
// canonical unit, and bounds are literal capability data; the generated
// support schema remains authoritative for their coherence.
func (mapping measurementMapping) support() sdkmeasurementv1.Support {
	return sdkmeasurementv1.Support{
		State: sdkmeasurementv1.StateSupport{
			MeasurementKind: mapping.measurementKind,
			Unit:            mapping.canonicalUnit,
			Minimum:         mapping.minimum,
			Maximum:         mapping.maximum,
		},
		Operations: sdkmeasurementv1.OperationSupport{},
	}
}

//nolint:gochecknoglobals // Lazy, concurrency-safe cache of authoritative codecs.
var measurementCodecs = sync.OnceValues(contractmeasurementv1.Compile)

// newMeasurementPlan builds the complete read-only semantic measurement
// translation for one State property and validates its descriptor and
// observations through the generated measurement/v1 facade. Get access alone
// controls startup refresh: a publish-only sensor has no get properties and a
// nil command translator, so it never creates a command route.
//
// The contract State codec owns binary64 decoding, so finite fractional JSON
// numbers survive unchanged and malformed, non-number, or overflowing input
// becomes a per-property decode issue. NewObservation then enforces the
// support bounds, so an out-of-range reading never suppresses a valid sibling.
func newMeasurementPlan(
	metadata adapter.EntityMetadata,
	property string,
	support sdkmeasurementv1.Support,
	gettable bool,
) (entityPlan, error) {
	descriptor, descriptorErr := sdkmeasurementv1.NewEntityDescriptor(metadata, support)
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
			codecs, codecErr := measurementCodecs()
			if codecErr != nil {
				return stateReport{}, false, codecErr
			}
			value, _, decodeErr := codecs.State.Decode(raw)
			if decodeErr != nil {
				return stateReport{}, false, decodeErr
			}
			observation, observationErr := sdkmeasurementv1.NewObservation(sdkmeasurementv1.ObservationInput{
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

// planMeasurementRoot applies one measurement capability mapping to a
// resolved root. Family planners own root cardinality; shared checks enforce
// read-only access, exact upstream units, unique properties and scoped
// identity. Bounds, kind, and canonical unit come from the mapping.
func planMeasurementRoot(
	input devicePlanningInput,
	root indexedExpose,
	mapping measurementMapping,
) *entityPlan {
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
	plan, err := newMeasurementPlan(adapter.EntityMetadata{
		Key:        key,
		ExternalID: input.IEEE + "/" + entityLocation(root) + "/" + mapping.key,
		Name:       name,
	}, expose.Property, mapping.support(), exposeCanGet(expose))
	if err != nil {
		return nil
	}
	return &plan
}
