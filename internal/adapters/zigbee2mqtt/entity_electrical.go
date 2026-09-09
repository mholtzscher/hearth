package zigbee2mqtt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	contractnumericsensorv1 "github.com/mholtzscher/hearth/entitytypes/numericsensorv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdknumericsensorv1 "github.com/mholtzscher/hearth/sdk/adapter/numericsensorv1"
)

// Electrical sensors are read-only hearth.numericsensor/v1 Entities, but
// numericsensor/v1 requires finite min/max while Zigbee2MQTT omits bounds
// for these exposes. The fallback envelopes below are conservative
// validation envelopes, not claimed operating ranges: they bound obviously
// invalid readings while accepting any plausible report. When the upstream
// expose carries both valid finite bounds they are preferred instead; a
// malformed, one-sided, or inverted upstream bound omits only that Entity.
//
//nolint:gochecknoglobals // Lazy, concurrency-safe cache of authoritative codecs.
var electricalSensorCodecs = sync.OnceValues(contractnumericsensorv1.Compile)

const (
	// electricalACFrequencyFallbackMax bounds plausible mains frequency
	// reports when upstream omits bounds.
	electricalACFrequencyFallbackMax = 1000
	// electricalPowerFallbackMax bounds plausible instantaneous power
	// reports when upstream omits bounds.
	electricalPowerFallbackMax = 1e9
	// electricalPowerFactorFallbackMax bounds the unitless power factor
	// ratio when upstream omits bounds.
	electricalPowerFactorFallbackMax = 1
	// electricalEnergyFallbackMax bounds plausible lifetime energy reports
	// when upstream omits bounds.
	electricalEnergyFallbackMax = 1e15
	// electricalCurrentFallbackMax bounds plausible current reports when
	// upstream omits bounds.
	electricalCurrentFallbackMax = 1e6
	// electricalVoltageFallbackMax bounds plausible voltage reports when
	// upstream omits bounds.
	electricalVoltageFallbackMax = 1e6
)

// electricalSensorSpec is one allowlisted read-only electrical sensor: the
// exact upstream expose name, Entity key/name, the exact expected upstream
// unit (empty means the expose must carry no unit), the Hearth unit (empty
// upstream maps explicitly to "ratio"), and the fallback validation
// envelope used when upstream omits bounds.
type electricalSensorSpec struct {
	exposeName   string
	key          string
	displayName  string
	upstreamUnit string
	hearthUnit   string
	fallbackMin  float64
	fallbackMax  float64
}

// smartPlugElectricalSensors lists the allowlisted smart-plug electrical
// sensors in deterministic planner order. Numeric "power" uses the
// "electricalpower" key and "Electrical Power" name to distinguish it from
// the boolean relay "power" Entity.
func smartPlugElectricalSensors() []electricalSensorSpec {
	return []electricalSensorSpec{
		{
			exposeName: "ac_frequency", key: "acfrequency", displayName: "AC Frequency",
			upstreamUnit: "Hz", hearthUnit: "Hz",
			fallbackMin: 0, fallbackMax: electricalACFrequencyFallbackMax,
		},
		{
			exposeName: "power", key: "electricalpower", displayName: "Electrical Power",
			upstreamUnit: "W", hearthUnit: "W",
			fallbackMin: 0, fallbackMax: electricalPowerFallbackMax,
		},
		{
			exposeName: "power_factor", key: "powerfactor", displayName: "Power Factor",
			upstreamUnit: "", hearthUnit: "ratio",
			fallbackMin: 0, fallbackMax: electricalPowerFactorFallbackMax,
		},
		{
			exposeName: "energy", key: "energy", displayName: "Energy",
			upstreamUnit: "kWh", hearthUnit: "kWh",
			fallbackMin: 0, fallbackMax: electricalEnergyFallbackMax,
		},
		{
			exposeName: "current", key: "current", displayName: "Current",
			upstreamUnit: "A", hearthUnit: "A",
			fallbackMin: 0, fallbackMax: electricalCurrentFallbackMax,
		},
		{
			exposeName: "voltage", key: "voltage", displayName: "Voltage",
			upstreamUnit: "V", hearthUnit: "V",
			fallbackMin: 0, fallbackMax: electricalVoltageFallbackMax,
		},
	}
}

// electricalSensorSupport builds the read-only sensor support for one
// resolved bound and unit.
func electricalSensorSupport(minimum, maximum float64, unit string) contractnumericsensorv1.Support {
	return contractnumericsensorv1.Support{
		State: contractnumericsensorv1.StateSupport{
			Minimum: minimum,
			Maximum: maximum,
			Unit:    unit,
		},
		Operations: contractnumericsensorv1.OperationSupport{},
	}
}

// newElectricalSensorPlan builds the shared read-only translation for one
// electrical State property. Get access alone controls startup refresh: a
// publish-only sensor has no get properties and a nil command translator,
// so it never creates a command route.
func newElectricalSensorPlan(
	metadata adapter.EntityMetadata,
	property string,
	minimum, maximum float64,
	unit string,
	gettable bool,
) (entityPlan, error) {
	support := electricalSensorSupport(minimum, maximum, unit)
	descriptor, descriptorErr := sdknumericsensorv1.NewEntityDescriptor(metadata, support)
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
			// The contract State codec owns JSON number decoding with
			// binary64 semantics, and NewObservation owns the support
			// bounds. Only JSON numbers are accepted, fractions are
			// preserved, and out-of-range payloads are rejected as
			// per-property issues so valid siblings still decode.
			codecs, err := electricalSensorCodecs()
			if err != nil {
				return stateReport{}, false, err
			}
			value, _, err := codecs.State.Decode(raw)
			if err != nil {
				return stateReport{}, false, err
			}
			observation, err := sdknumericsensorv1.NewObservation(sdknumericsensorv1.ObservationInput{
				EntityID: entityID, Support: support, State: value,
				AdapterReceivedAt: receivedAt,
			})
			if err != nil {
				return stateReport{}, false, err
			}
			return stateReport{Observation: observation, semantic: float64(value)}, true, nil
		},
		TranslateCommand: nil,
	}, nil
}

// newNumericSensorProfilePlan builds the generic read-only numeric-sensor
// translation for one State property. It shares the electrical support and
// float decoding so profile-planned sensors behave identically to the
// handwritten electrical sensors; the number format selects exact-integer
// decoding (integer) or finite JSON numbers with preserved fractions
// (float). Get access alone controls startup refresh: a publish-only
// sensor has no get properties and a nil command translator, so it never
// creates a command route.
func newNumericSensorProfilePlan(
	metadata adapter.EntityMetadata,
	property string,
	minimum, maximum float64,
	unit string,
	numberFormat string,
	gettable bool,
) (entityPlan, error) {
	support := electricalSensorSupport(minimum, maximum, unit)
	descriptor, descriptorErr := sdknumericsensorv1.NewEntityDescriptor(metadata, support)
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
			value, err := decodeNumericSensorProfileValue(raw, numberFormat)
			if err != nil {
				return stateReport{}, false, err
			}
			observation, err := sdknumericsensorv1.NewObservation(sdknumericsensorv1.ObservationInput{
				EntityID: entityID, Support: support, State: contractnumericsensorv1.State(value),
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

// decodeNumericSensorProfileValue decodes one numeric-sensor reading in the
// compiled number format. Integer uses the existing exact-integer decoder;
// float accepts finite JSON numbers and preserves fractions. Out-of-range
// payloads are rejected as per-property issues by NewObservation so valid
// siblings still decode.
func decodeNumericSensorProfileValue(payload json.RawMessage, numberFormat string) (float64, error) {
	if numberFormat == profileNumericSensorFormatInteger {
		return decodeIntegerNumericSensorProfileValue(payload)
	}
	codecs, err := electricalSensorCodecs()
	if err != nil {
		return 0, err
	}
	value, _, err := codecs.State.Decode(payload)
	if err != nil {
		return 0, err
	}
	return float64(value), nil
}

// decodeIntegerNumericSensorProfileValue decodes one exact-integer
// numeric-sensor reading. Fractions are rejected as per-property issues so
// valid siblings still decode.
func decodeIntegerNumericSensorProfileValue(payload json.RawMessage) (float64, error) {
	value, err := parseExactIntegerJSON(payload)
	if err != nil {
		if errors.Is(err, errExactIntegerNotNumber) {
			return 0, errors.New("numeric sensor value must be a JSON number")
		}
		if errors.Is(err, errExactIntegerNotInteger) {
			return 0, errors.New("numeric sensor value must be a finite integer")
		}
		return 0, fmt.Errorf("decode numeric sensor number: %w", err)
	}
	return float64(value), nil
}

// electricalSensorBounds resolves the support bounds for one electrical
// expose: both valid finite upstream bounds with minimum < maximum win;
// both absent falls back to the conservative validation envelope; anything
// else (malformed, one-sided, or inverted) makes only that Entity
// ineligible.
func electricalSensorBounds(expose upstreamExpose, fallbackMin, fallbackMax float64) (float64, float64, bool) {
	if expose.ValueMin == nil && expose.ValueMax == nil {
		minimumAbsent := len(bytes.TrimSpace(expose.valueMinRaw)) == 0
		maximumAbsent := len(bytes.TrimSpace(expose.valueMaxRaw)) == 0
		if minimumAbsent && maximumAbsent {
			return fallbackMin, fallbackMax, true
		}
		return 0, 0, false
	}
	if expose.ValueMin == nil || expose.ValueMax == nil {
		return 0, 0, false
	}
	minimum, maximum := *expose.ValueMin, *expose.ValueMax
	if !isFinite(minimum) || !isFinite(maximum) || minimum >= maximum {
		return 0, 0, false
	}
	return minimum, maximum, true
}

// planElectricalSensor discovers one allowlisted read-only electrical
// sensor once per device. The expose must be a resolved unique root with a
// device-unique nonempty property, publish access without set access, and
// the exact expected upstream unit. Get access alone controls startup
// refresh. Ineligible siblings are omitted without affecting valid sensors.
func planElectricalSensor(input devicePlanningInput, spec electricalSensorSpec) *entityPlan {
	root, ok := input.Exposes.UniqueRoot(upstreamExposeNumeric, spec.exposeName)
	if !ok || !root.resolved {
		return nil
	}
	expose := root.expose
	if expose.Property == "" || !exposeCanPublish(expose) || exposeCanSet(expose) ||
		!input.Exposes.PropertyUnique(expose.Property) {
		return nil
	}
	if expose.Unit != spec.upstreamUnit {
		return nil
	}
	minimum, maximum, valid := electricalSensorBounds(expose, spec.fallbackMin, spec.fallbackMax)
	if !valid {
		return nil
	}
	key, name := scopedIdentity(spec.key, spec.displayName, expose.Endpoint, root.endpoint, root.scoped)
	if !validDescriptorName(name) {
		return nil
	}
	plan, err := newElectricalSensorPlan(adapter.EntityMetadata{
		Key:        key,
		ExternalID: input.IEEE + "/" + entityLocation(root) + "/" + spec.key,
		Name:       name,
	}, expose.Property, minimum, maximum, spec.hearthUnit, exposeCanGet(expose))
	if err != nil {
		return nil
	}
	return &plan
}
