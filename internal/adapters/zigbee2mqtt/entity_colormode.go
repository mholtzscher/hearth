package zigbee2mqtt

import (
	"encoding/json"
	"fmt"
	"sync"
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

// reportedColorModeCodecs compiles the authoritative colormode State codec
// once and shares it across every color decoder.
//
//nolint:gochecknoglobals // Lazy, concurrency-safe cache of authoritative codecs.
var reportedColorModeCodecs = sync.OnceValues(contractcolormodev1.Compile)

// decodeReportedColorMode parses one reported color-control mode through the
// authoritative colormode State codec, so unknown or malformed modes are
// invalid observations exactly as the shared contract defines. Hearth
// publishes reported known enum values even when the matching representation
// is not an advertised capability.
func decodeReportedColorMode(payload json.RawMessage) (contractcolormodev1.State, error) {
	codecs, err := reportedColorModeCodecs()
	if err != nil {
		return "", fmt.Errorf("compile color mode codec: %w", err)
	}
	mode, _, err := codecs.State.Decode(payload)
	if err != nil {
		return "", fmt.Errorf("decode color mode value: %w", err)
	}
	return mode, nil
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
