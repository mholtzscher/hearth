package zigbee2mqtt

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	contractnumericsensorv1 "github.com/mholtzscher/hearth/entitytypes/numericsensorv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdknumericsensorv1 "github.com/mholtzscher/hearth/sdk/adapter/numericsensorv1"
)

// Generic numeric sensors are read-only hearth.numericsensor/v1 Entities
// planned by the numeric-sensor profile strategy. The profile supplies the
// accepted upstream units, the Hearth unit, the integer or float number
// format, and the fixed or upstream-or-fallback bound envelope; Go owns
// decoding, support, and observation behavior.
//
//nolint:gochecknoglobals // Lazy, concurrency-safe cache of authoritative codecs.
var electricalSensorCodecs = sync.OnceValues(contractnumericsensorv1.Compile)

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

// newNumericSensorProfilePlan builds the generic read-only numeric-sensor
// translation for one State property. It shares the electrical support and
// float decoding; the number format selects exact-integer decoding
// (integer) or finite JSON numbers with preserved fractions (float). Get access alone controls startup refresh: a publish-only
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
