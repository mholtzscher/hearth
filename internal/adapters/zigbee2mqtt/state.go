package zigbee2mqtt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// decodedState is one complete Entity State joined to the canonical Entity ID
// that claimed it.
type decodedState struct {
	entityID string
	report   stateReport
}

// decodeDeviceState projects recognized properties in registration order and
// isolates invalid values. One Entity plan may claim several State properties;
// complete same-message properties produce one State while split messages
// produce none. The Adapter does not assemble Entity State across MQTT
// messages and retains no cross-message State cache.
func decodeDeviceState(
	payload []byte,
	entities []runtimeEntity,
	receivedAt time.Time,
) ([]decodedState, []stateDecodeIssue, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, nil, errors.New("device State must be a JSON object")
	}
	var properties map[string]json.RawMessage
	if err := decodeJSON(payload, &properties); err != nil {
		return nil, nil, fmt.Errorf("decode Zigbee2MQTT device State: %w", err)
	}
	states := make([]decodedState, 0, len(entities))
	issues := make([]stateDecodeIssue, 0)
	for _, entity := range entities {
		present := 0
		for _, property := range entity.plan.StateProperties {
			if _, ok := properties[property]; ok {
				present++
			}
		}
		if present == 0 {
			continue
		}
		if present != len(entity.plan.StateProperties) {
			continue
		}
		report, decoded, err := entity.plan.DecodeState(entity.entityID, properties, receivedAt)
		if err != nil {
			issues = append(issues, stateDecodeIssue{
				Properties: append([]string(nil), entity.plan.StateProperties...),
				Err:        err,
			})
			continue
		}
		if !decoded {
			continue
		}
		states = append(states, decodedState{entityID: entity.entityID, report: report})
	}
	return states, issues, nil
}
