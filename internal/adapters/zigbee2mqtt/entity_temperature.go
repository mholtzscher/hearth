package zigbee2mqtt

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	contracttemperaturev1 "github.com/mholtzscher/hearth/entitytypes/temperaturev1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdktemperaturev1 "github.com/mholtzscher/hearth/sdk/adapter/temperaturev1"
)

const milliCelsiusPerCelsius = 1_000

// newTemperaturePlan builds the complete read-only temperature translation for
// one State property. Get access alone controls startup refresh: a
// publish-only sensor has no get properties and a nil command translator, so
// it never creates a command route.
//
//nolint:dupl // Temperature and linkquality are parallel read-only sensors over distinct generated contracts.
func newTemperaturePlan(
	metadata adapter.EntityMetadata,
	property string,
	gettable bool,
) (entityPlan, error) {
	descriptor, descriptorErr := sdktemperaturev1.NewEntityDescriptor(metadata, temperatureSupport())
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
			value, err := normalizeTemperature(raw)
			if err != nil {
				return stateReport{}, false, err
			}
			observation, err := sdktemperaturev1.NewObservation(sdktemperaturev1.ObservationInput{
				EntityID: entityID, Support: temperatureSupport(), State: contracttemperaturev1.State(value),
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

func temperatureSupport() contracttemperaturev1.Support {
	return contracttemperaturev1.Support{
		State:      contracttemperaturev1.StateSupport{},
		Operations: contracttemperaturev1.OperationSupport{},
	}
}

// normalizeTemperature converts an upstream Celsius JSON number to integer
// milli-Celsius. It parses the value exactly and requires an integral int64
// result, rejecting rather than clamping, truncating, or rounding. The
// Hearth range lives in the contract State schema and is enforced by State
// Encode inside NewObservation, so out-of-range integers surface as
// per-property plan errors rather than wire conversion failures.
func normalizeTemperature(payload json.RawMessage) (int64, error) {
	var decoded any
	if err := decodeJSON(payload, &decoded); err != nil {
		return 0, fmt.Errorf("decode temperature number: %w", err)
	}
	number, ok := decoded.(json.Number)
	if !ok {
		return 0, errors.New("temperature value must be a JSON number")
	}
	celsius, ok := new(big.Rat).SetString(number.String())
	if !ok {
		return 0, errors.New("temperature value must be a finite number")
	}
	scaled := new(big.Rat).Mul(celsius, big.NewRat(milliCelsiusPerCelsius, 1))
	if !scaled.IsInt() || !scaled.Num().IsInt64() {
		return 0, errors.New("temperature value requires sub-milli-Celsius precision")
	}
	return scaled.Num().Int64(), nil
}
