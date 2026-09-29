// observation.go owns typed State translation. Each planned Entity produces at most one Observation per current Value.
// Power precedes brightness within a frame. An unrepresentable value is diagnosed and skipped for that Entity only.
// Nothing is cached.

package zwavejs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	// encodedTrue and encodedFalse are the JSON encodings hearth.power/v1 and this Adapter's power decoder accept.
	encodedTrue  = "true"
	encodedFalse = "false"
)

// entityObservation pairs a typed Observation with its planned Entity.
type entityObservation struct {
	Key         string
	EntityID    string
	Observation adapter.Observation
}

// entityStateIssue records an unrepresentable State for one planned Entity. Valid sibling Entities still publish.
type entityStateIssue struct {
	Key      string
	EntityID string
	Err      error
}

// decodeBinaryPowerState accepts only JSON boolean true or false as a Binary Switch power State.
func decodeBinaryPowerState(current json.RawMessage) (bool, error) {
	return decodeBooleanStateJSON(current)
}

// decodeBooleanStateJSON accepts only a JSON boolean for power decoding and outcome matching.
func decodeBooleanStateJSON(payload json.RawMessage) (bool, error) {
	trimmed := bytes.TrimSpace(payload)
	switch string(trimmed) {
	case encodedTrue:
		return true, nil
	case encodedFalse:
		return false, nil
	default:
		return false, errors.New("zwavejs: power State is not the JSON boolean true or false")
	}
}

// decodeMultilevelPowerState maps zero to off and levels 1 through 99 to on. Restore-previous-level and out-of-range
// values are not representable State.
func decodeMultilevelPowerState(current json.RawMessage) (bool, error) {
	level, err := decodeZwaveLevel(current)
	if err != nil {
		return false, err
	}
	return level > 0, nil
}

// decodeZwaveLevel accepts integral JSON numbers from 0 through 99. It rejects fractions, strings, null, non-finite
// numbers, and restore-previous levels.
func decodeZwaveLevel(payload json.RawMessage) (int64, error) {
	trimmed := bytes.TrimSpace(payload)
	switch {
	case len(trimmed) == 0:
		return 0, errors.New("zwavejs: Multilevel Switch level is missing")
	case trimmed[0] == '"':
		return 0, errors.New("zwavejs: Multilevel Switch level is a string, not a JSON number")
	case bytes.Equal(trimmed, []byte("null")):
		return 0, errors.New("zwavejs: Multilevel Switch level is null")
	}
	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err != nil {
		return 0, fmt.Errorf("zwavejs: Multilevel Switch level is not a JSON number: %w", err)
	}
	parsed, err := number.Float64()
	if err != nil {
		return 0, fmt.Errorf("zwavejs: Multilevel Switch level is not a number: %w", err)
	}
	if !isFiniteFloat(parsed) {
		return 0, errors.New("zwavejs: Multilevel Switch level is not finite")
	}
	level := math.Trunc(parsed)
	if level != parsed {
		return 0, fmt.Errorf("zwavejs: Multilevel Switch level %s is not integral", number)
	}
	if level < 0 || level > zWaveLevelMaximum {
		return 0, fmt.Errorf(
			"zwavejs: Multilevel Switch level %s is outside 0..%d",
			number,
			zWaveLevelMaximum,
		)
	}
	return int64(level), nil
}

// isFiniteFloat rejects NaN and infinities as levels or metadata bounds.
func isFiniteFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// encodePowerState renders a validated power State as typed JSON.
func encodePowerState(value bool) json.RawMessage {
	if value {
		return json.RawMessage(encodedTrue)
	}
	return json.RawMessage(encodedFalse)
}

// encodeBrightnessState renders a validated brightness level as typed State JSON.
func encodeBrightnessState(level int64) json.RawMessage {
	return json.RawMessage(strconv.FormatInt(level, 10))
}

// encodeMultilevelPowerValue uses 0 for off and restore-previous-level 255 for on in a Multilevel Switch power Command.
func encodeMultilevelPowerValue(value bool) json.RawMessage {
	if value {
		return json.RawMessage(strconv.Itoa(zWaveRestorePreviousLevel))
	}
	return json.RawMessage("0")
}

// observe uses the generated Entity-type facade to translate a current Value. The Adapter supplies receive time. Schema
// 29 supplies no source timestamp, so SourceUpdatedAt remains nil.
func (plan entityPlan) observe(
	entityID string,
	receivedAt time.Time,
	current json.RawMessage,
) (adapter.Observation, error) {
	return plan.Observe(entityID, receivedAt, current)
}

// resolveCurrentValue requires an exact unkeyed string property for a planned Value ID. Numeric properties and
// propertyKeys identify different Values. A JSON null key counts as absent, as during planning. An absent Value is not
// a State report.
func resolveCurrentValue(values []valueState, id valueID) (json.RawMessage, bool) {
	key := id.valueKey()
	for index := range values {
		value := &values[index]
		if value.Property.Numeric || valueIDHasPropertyKey(value.valueID) {
			continue
		}
		if value.valueKey() == key && len(value.Value) != 0 {
			return value.Value, true
		}
	}
	return nil, false
}

// translateNodeValues follows route order, which puts power before brightness per endpoint. An unrepresentable Value
// produces an issue without suppressing valid siblings.
func translateNodeValues(
	routes []entityRoute,
	receivedAt time.Time,
	values []valueState,
) ([]entityObservation, []entityStateIssue) {
	observations := make([]entityObservation, 0, len(routes))
	issues := make([]entityStateIssue, 0)
	for _, route := range routes {
		current, present := resolveCurrentValue(values, route.Plan.CurrentValueID)
		if !present {
			continue
		}
		observation, err := route.Plan.observe(route.EntityID, receivedAt, current)
		if err != nil {
			issues = append(issues, entityStateIssue{
				Key:      route.Plan.Descriptor.Key,
				EntityID: route.EntityID,
				Err:      err,
			})
			continue
		}
		observations = append(observations, entityObservation{
			Key:         route.Plan.Descriptor.Key,
			EntityID:    route.EntityID,
			Observation: observation,
		})
	}
	return observations, issues
}
