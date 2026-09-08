package zigbee2mqtt

import (
	"encoding/json"
	"sync"
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

// percentageSensorCodecs shares immutable schemas across percentage plans.
//
//nolint:gochecknoglobals // Lazy, concurrency-safe cache of authoritative codecs.
var percentageSensorCodecs = sync.OnceValues(contractnumericsensorv1.Compile)

// newPercentageSensorPlan builds the shared read-only percent translation
// for humidity and battery. The sensor planner owns the expose allowlist;
// get access alone controls startup refresh.
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
			// The contract State codec owns JSON number decoding with
			// binary64 semantics, and NewObservation owns the 0-100
			// support bounds below. Only JSON numbers are accepted, and
			// out-of-range payloads are rejected as per-property issues
			// so valid siblings still decode.
			codecs, err := percentageSensorCodecs()
			if err != nil {
				return stateReport{}, false, err
			}
			value, _, err := codecs.State.Decode(raw)
			if err != nil {
				return stateReport{}, false, err
			}
			observation, err := sdknumericsensorv1.NewObservation(sdknumericsensorv1.ObservationInput{
				EntityID: entityID, Support: percentageSensorSupport(), State: value,
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
