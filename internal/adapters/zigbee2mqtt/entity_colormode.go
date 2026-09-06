package zigbee2mqtt

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	contractcolormodev1 "github.com/mholtzscher/hearth/entitytypes/colormodev1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkcolormodev1 "github.com/mholtzscher/hearth/sdk/adapter/colormodev1"
)

// Reported color-control modes. Unknown values are invalid observations, not
// a new State and not an inferred mode.
const (
	colorModeXY        = "xy"
	colorModeHS        = "hs"
	colorModeColorTemp = "color_temp"
)

// decodeReportedColorMode parses one reported color-control mode. Hearth
// publishes reported known enum values even when the matching representation
// is not an advertised capability.
func decodeReportedColorMode(payload json.RawMessage) (contractcolormodev1.State, error) {
	var decoded any
	if err := decodeJSON(payload, &decoded); err != nil {
		return "", fmt.Errorf("decode color mode value: %w", err)
	}
	text, ok := decoded.(string)
	if !ok {
		return "", errors.New("color mode value must be a JSON string")
	}
	switch text {
	case colorModeXY, colorModeHS, colorModeColorTemp:
		return contractcolormodev1.State(text), nil
	default:
		return "", fmt.Errorf("unknown color mode value %q", text)
	}
}

// newColorModePlan builds the read-only color-mode translation for one mode
// property. The plan has no translator and no independent get properties
// because sibling color and temperature refreshes already request mode
// upstream.
func newColorModePlan(metadata adapter.EntityMetadata, modeProperty string) (entityPlan, error) {
	support := sdkcolormodev1.Support{}
	descriptor, descriptorErr := sdkcolormodev1.NewEntityDescriptor(metadata, support)
	if descriptorErr != nil {
		return entityPlan{}, descriptorErr
	}
	return entityPlan{
		Descriptor:      descriptor,
		StateProperties: []string{modeProperty},
		DecodeState: func(
			entityID string,
			properties map[string]json.RawMessage,
			receivedAt time.Time,
		) (stateReport, bool, error) {
			raw, present := properties[modeProperty]
			if !present {
				return stateReport{}, false, nil
			}
			mode, err := decodeReportedColorMode(raw)
			if err != nil {
				return stateReport{}, false, fmt.Errorf("color mode value: %w", err)
			}
			observation, err := sdkcolormodev1.NewObservation(sdkcolormodev1.ObservationInput{
				EntityID: entityID, Support: support, State: mode,
				AdapterReceivedAt: receivedAt,
			})
			if err != nil {
				return stateReport{}, false, err
			}
			return stateReport{Observation: observation, semantic: mode}, true, nil
		},
	}, nil
}
