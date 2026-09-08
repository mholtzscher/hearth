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
	humidityExposeName = "humidity"
	batteryExposeName  = "battery"
	// percentageSensorUnit is the only unit a humidity or battery expose
	// may carry. Any other unit omits the entity instead of guessing a scale.
	percentageSensorUnit    = "%"
	percentageSensorMinimum = 0
	percentageSensorMaximum = 100
)

// newPercentageSensorPlan builds the shared read-only percent translation
// for humidity and battery. The sensor planner owns the expose allowlist;
// get access alone controls startup refresh.
//
//nolint:dupl // Percentage sensors parallel temperature and linkquality as read-only single-property sensors.
func newPercentageSensorPlan(
	metadata adapter.EntityMetadata,
	property string,
	gettable bool,
) (entityPlan, error) {
	descriptor, descriptorErr := sdknumericsensorv1.NewEntityDescriptor(metadata, percentageSensorSupport())
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
			value, err := normalizePercentageSensor(raw)
			if err != nil {
				return stateReport{}, false, err
			}
			observation, err := sdknumericsensorv1.NewObservation(sdknumericsensorv1.ObservationInput{
				EntityID: entityID, Support: percentageSensorSupport(), State: contractnumericsensorv1.State(value),
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

func percentageSensorSupport() contractnumericsensorv1.Support {
	return contractnumericsensorv1.Support{
		State: contractnumericsensorv1.StateSupport{
			Minimum: percentageSensorMinimum,
			Maximum: percentageSensorMaximum,
			Unit:    percentageSensorUnit,
		},
		Operations: contractnumericsensorv1.OperationSupport{},
	}
}

// normalizePercentageSensor checks the exact JSON number against 0–100
// before converting it to float64. Fractions are preserved for both humidity and battery; only JSON
// numbers are accepted, and out-of-range payloads are rejected as
// per-property issues so valid siblings still decode.
func normalizePercentageSensor(payload json.RawMessage) (float64, error) {
	var decoded any
	if err := decodeJSON(payload, &decoded); err != nil {
		return 0, fmt.Errorf("decode percentage sensor number: %w", err)
	}
	number, ok := decoded.(json.Number)
	if !ok {
		return 0, errors.New("percentage sensor value must be a JSON number")
	}
	exact, ok := new(big.Rat).SetString(number.String())
	if !ok {
		return 0, errors.New("percentage sensor value must be a finite number")
	}
	if exact.Cmp(big.NewRat(percentageSensorMinimum, 1)) < 0 ||
		exact.Cmp(big.NewRat(percentageSensorMaximum, 1)) > 0 {
		return 0, errors.New("percentage sensor value is outside its supported range")
	}
	value, _ := exact.Float64()
	return value, nil
}
