package zigbee2mqtt

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	contractnumericsensorv1 "github.com/mholtzscher/hearth/entitytypes/numericsensorv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdknumericsensorv1 "github.com/mholtzscher/hearth/sdk/adapter/numericsensorv1"
)

const (
	linkqualityExposeName = "linkquality"
	linkqualityMinimum    = 0
	linkqualityMaximum    = 255
	// linkqualityUnit is the only unit a linkquality expose may carry. An
	// empty upstream unit maps to it explicitly; any other unit omits the
	// entity instead of guessing a scale.
	linkqualityUnit = "lqi"
)

// newLinkqualityPlan builds the complete read-only linkquality translation
// for one State property. Get access alone controls startup refresh: a
// publish-only sensor has no get properties and a nil command translator,
// so it never creates a command route.
//
//nolint:dupl // Linkquality and temperature are parallel read-only sensors over distinct generated contracts.
func newLinkqualityPlan(
	metadata adapter.EntityMetadata,
	property string,
	gettable bool,
) (entityPlan, error) {
	descriptor, descriptorErr := sdknumericsensorv1.NewEntityDescriptor(metadata, linkqualitySupport())
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
			value, err := normalizeLinkquality(raw)
			if err != nil {
				return stateReport{}, false, err
			}
			observation, err := sdknumericsensorv1.NewObservation(sdknumericsensorv1.ObservationInput{
				EntityID: entityID, Support: linkqualitySupport(), State: contractnumericsensorv1.State(value),
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

func linkqualitySupport() contractnumericsensorv1.Support {
	return contractnumericsensorv1.Support{
		State: contractnumericsensorv1.StateSupport{
			Minimum: linkqualityMinimum,
			Maximum: linkqualityMaximum,
			Unit:    linkqualityUnit,
		},
		Operations: contractnumericsensorv1.OperationSupport{},
	}
}

// linkqualityUnitFor maps the upstream unit to the Hearth unit. Only the
// empty string (explicit known mapping) and the lqi passthrough are
// accepted; any other unit omits the entity.
func linkqualityUnitFor(unit string) (string, bool) {
	switch unit {
	case "", linkqualityUnit:
		return linkqualityUnit, true
	default:
		return "", false
	}
}

// linkqualityPlanner supports numeric device-root linkquality exposes on
// any Device kind. An eligible expose requires publish access with no set
// access, a Device-unique property, and an empty or lqi unit. Get access
// alone controls startup refresh: a publish-only expose has no get
// properties. It never gates or joins the power family: it appends as a
// device-kind-agnostic supplement alongside whatever primary contribution
// wins.
type linkqualityPlanner struct{}

func (linkqualityPlanner) Plan(input devicePlanningInput) plannerContribution {
	contribution := plannerContribution{Kind: upstreamDeviceKindSensor, Role: plannerRoleSupplemental}
	root, ok := input.Exposes.UniqueRoot(upstreamExposeNumeric, linkqualityExposeName)
	if !ok || !root.resolved {
		return contribution
	}
	expose := root.expose
	if expose.Property == "" || !exposeCanPublish(expose) || exposeCanSet(expose) ||
		!input.Exposes.PropertyUnique(expose.Property) {
		return contribution
	}
	if _, unitOK := linkqualityUnitFor(expose.Unit); !unitOK {
		return contribution
	}
	key, name := scopedIdentity(
		"linkquality",
		"Link Quality",
		expose.Endpoint,
		root.endpoint,
		root.scoped,
	)
	if !validDescriptorName(name) {
		return contribution
	}
	plan, err := newLinkqualityPlan(adapter.EntityMetadata{
		Key:        key,
		ExternalID: input.IEEE + "/" + entityLocation(root) + "/linkquality",
		Name:       name,
	}, expose.Property, exposeCanGet(expose))
	if err != nil {
		return contribution
	}
	contribution.Entities = append(contribution.Entities, plan)
	return contribution
}

// normalizeLinkquality decodes an exact-integer linkquality reading.
// Fractions are rejected as per-property issues so valid siblings still
// decode: integer-only is adapter-enforced while NewObservation owns the
// 0–255 support bounds.
func normalizeLinkquality(payload json.RawMessage) (float64, error) {
	var decoded any
	if err := decodeJSON(payload, &decoded); err != nil {
		return 0, fmt.Errorf("decode linkquality number: %w", err)
	}
	number, ok := decoded.(json.Number)
	if !ok {
		return 0, errors.New("linkquality value must be a JSON number")
	}
	exact, ok := new(big.Rat).SetString(number.String())
	if !ok || !exact.IsInt() || !exact.Num().IsInt64() {
		return 0, errors.New("linkquality value must be a finite integer")
	}
	return float64(exact.Num().Int64()), nil
}
