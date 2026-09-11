package zigbee2mqtt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// decodedState is one complete Entity State joined to the canonical Entity ID
// that claimed it.
type decodedState struct {
	entityID string
	report   stateReport
}

// decodeDeviceProperties parses one MQTT payload into its top-level
// properties once so State and Event decoding share a single parse. A
// payload that is not a JSON object is rejected before any per-Entity
// decode.
func decodeDeviceProperties(payload []byte) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errors.New("device State must be a JSON object")
	}
	var properties map[string]json.RawMessage
	if err := decodeJSON(payload, &properties); err != nil {
		return nil, fmt.Errorf("decode Zigbee2MQTT device State: %w", err)
	}
	return properties, nil
}

// decodeDeviceState projects recognized State properties from one whole
// message. It is the State-only entry point used by focused tests; runtime
// message handling parses once and calls decodeStatesFromProperties.
func decodeDeviceState(
	payload []byte,
	entities []runtimeEntity,
	receivedAt time.Time,
) ([]decodedState, []stateDecodeIssue, error) {
	properties, err := decodeDeviceProperties(payload)
	if err != nil {
		return nil, nil, err
	}
	states, issues := decodeStatesFromProperties(properties, entities, receivedAt)
	return states, issues, nil
}

// decodeStatesFromProperties projects recognized properties in registration
// order and isolates invalid values. One Entity plan may claim several State
// properties; complete same-message properties produce one State while split
// messages produce none. The Adapter does not assemble Entity State across
// MQTT messages and retains no cross-message State cache.
func decodeStatesFromProperties(
	properties map[string]json.RawMessage,
	entities []runtimeEntity,
	receivedAt time.Time,
) ([]decodedState, []stateDecodeIssue) {
	states := make([]decodedState, 0, len(entities))
	issues := make([]stateDecodeIssue, 0)
	for _, entity := range entities {
		if entity.plan.DecodeState == nil {
			continue
		}
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
	return states, issues
}

// decodeDeviceEvents projects every recognized Entity Event from one whole
// message. It is the Event-only entry point used by focused tests; runtime
// message handling parses once and calls decodeEventsFromProperties. Each
// returned Event already carries the canonical Entity ID that claimed it.
func decodeDeviceEvents(
	payload []byte,
	entities []runtimeEntity,
) ([]adapter.EntityEvent, []stateDecodeIssue, error) {
	properties, err := decodeDeviceProperties(payload)
	if err != nil {
		return nil, nil, err
	}
	events, issues := decodeEventsFromProperties(properties, entities)
	return events, issues, nil
}

// decodeEventsFromProperties projects every present, valid Entity Event in
// registration order. Each message carries at most one report per Event
// Entity, so consecutive identical upstream reports stay separate
// occurrences and are never value-deduplicated. An invalid or unsupported
// value becomes a per-Entity issue instead of a fabricated Event, so valid
// sibling State still publishes.
func decodeEventsFromProperties(
	properties map[string]json.RawMessage,
	entities []runtimeEntity,
) ([]adapter.EntityEvent, []stateDecodeIssue) {
	events := make([]adapter.EntityEvent, 0, len(entities))
	issues := make([]stateDecodeIssue, 0)
	for _, entity := range entities {
		if entity.plan.DecodeEvent == nil {
			continue
		}
		event, decoded, err := entity.plan.DecodeEvent(entity.entityID, properties)
		if err != nil {
			// The Event source property is fixed per Entity, so the issue
			// carries only the decode failure.
			issues = append(issues, stateDecodeIssue{Err: err})
			continue
		}
		if !decoded {
			continue
		}
		events = append(events, event)
	}
	return events, issues
}
