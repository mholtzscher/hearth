// observation.go owns typed State translation. It maps one upstream current
// Value to exactly one typed Hearth Observation per planned Entity, rejects
// every value that is not a representable State, and orders the Observations of
// one frame so power precedes brightness. It keeps no cache: an unrepresentable
// value is diagnosed and skipped for that Entity alone.

package zwavejs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	contractbrightnessv1 "github.com/mholtzscher/hearth/entitytypes/brightnessv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkbrightnessv1 "github.com/mholtzscher/hearth/sdk/adapter/brightnessv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

const (
	// encodedTrue and encodedFalse are the exact JSON encodings hearth.power/v1
	// and this Adapter's power decoder accept.
	encodedTrue  = "true"
	encodedFalse = "false"
)

// entityObservation is one translated typed Observation and the planned Entity
// that produced it.
type entityObservation struct {
	Key         string
	EntityID    string
	Observation adapter.Observation
}

// entityStateIssue records one planned Entity whose upstream current Value is
// not a representable State. It is a per-Entity diagnostic: every valid sibling
// still publishes.
type entityStateIssue struct {
	Key      string
	EntityID string
	Err      error
}

// decodeBinaryPowerState decodes one Binary Switch currentValue: exactly the
// JSON boolean true or false. A numeric string, a number, null, and a missing
// value are not a representable power State.
func decodeBinaryPowerState(current json.RawMessage) (bool, error) {
	return decodeBooleanStateJSON(current)
}

// decodeBooleanStateJSON decodes one strict JSON boolean. It is shared by the
// Binary Switch power decoder and by power outcome matching.
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

// decodeMultilevelPowerState decodes one Multilevel Switch currentValue as the
// power State derived from it: exactly zero is off, and 1 through 99 is on.
// Restore-previous-level and out-of-range values are not a representable State.
func decodeMultilevelPowerState(current json.RawMessage) (bool, error) {
	level, err := decodeZwaveLevel(current)
	if err != nil {
		return false, err
	}
	return level > 0, nil
}

// decodeZwaveLevel decodes one strict integral Multilevel Switch level in 0..99.
// Fractions, numeric strings, null, non-finite numbers, restore-previous level,
// and out-of-range values are not a representable level.
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

// isFiniteFloat reports whether one decoded number is usable as a level or a
// metadata bound. NaN and both infinities are never representable.
func isFiniteFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// encodePowerState renders one validated power State as its typed State JSON.
func encodePowerState(value bool) json.RawMessage {
	if value {
		return json.RawMessage(encodedTrue)
	}
	return json.RawMessage(encodedFalse)
}

// encodeBrightnessState renders one validated brightness level as its typed
// State JSON.
func encodeBrightnessState(level int64) json.RawMessage {
	return json.RawMessage(strconv.FormatInt(level, 10))
}

// encodeMultilevelPowerValue renders the Command Class Multilevel Switch value
// of one power Command: numeric 0 for off, and the restore-previous-level value
// 255 for on.
func encodeMultilevelPowerValue(value bool) json.RawMessage {
	if value {
		return json.RawMessage(strconv.Itoa(zWaveRestorePreviousLevel))
	}
	return json.RawMessage("0")
}

// observe translates one upstream current Value for one planned Entity into a
// typed Observation through the generated Entity-type facade. The Adapter owns
// the receive time and schema 29 supplies no source timestamp, so
// SourceUpdatedAt stays nil.
func (plan entityPlan) observe(
	entityID string,
	receivedAt time.Time,
	current json.RawMessage,
) (adapter.Observation, error) {
	state, err := plan.DecodeState(current)
	if err != nil {
		return adapter.Observation{}, err
	}
	switch plan.Kind {
	case entityKindPower:
		var value contractpowerv1.State
		if err = json.Unmarshal(state, &value); err != nil {
			return adapter.Observation{}, fmt.Errorf("zwavejs: power State is unusable: %w", err)
		}
		return sdkpowerv1.NewObservation(sdkpowerv1.ObservationInput{
			EntityID:          entityID,
			Support:           powerSupport(),
			State:             value,
			AdapterReceivedAt: receivedAt,
		})
	case entityKindBrightness:
		var value contractbrightnessv1.State
		if err = json.Unmarshal(state, &value); err != nil {
			return adapter.Observation{}, fmt.Errorf("zwavejs: brightness State is unusable: %w", err)
		}
		return sdkbrightnessv1.NewObservation(sdkbrightnessv1.ObservationInput{
			EntityID:          entityID,
			Support:           brightnessSupport(),
			State:             value,
			AdapterReceivedAt: receivedAt,
		})
	default:
		return adapter.Observation{}, fmt.Errorf(
			"zwavejs: Entity %q has no Entity kind",
			plan.Key,
		)
	}
}

// resolveCurrentValue finds the snapshot Value of one planned current Value ID.
// A planned Value ID always names an unkeyed string property, so resolution
// requires an exact unkeyed identity: a numeric property or a propertyKey never
// resolves, because either names a different Value than the planned one. An
// explicit JSON null key counts as absent, exactly as it does during planning.
// An absent Value is not a State report at all.
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

// translateNodeValues translates the current Values of one node snapshot into
// typed Observations in route order. Because plans order power before brightness
// per endpoint, one frame that maps to both publishes power first. One Entity's
// unrepresentable value becomes an issue instead of suppressing its siblings.
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
				Key:      route.Plan.Key,
				EntityID: route.EntityID,
				Err:      err,
			})
			continue
		}
		observations = append(observations, entityObservation{
			Key:         route.Plan.Key,
			EntityID:    route.EntityID,
			Observation: observation,
		})
	}
	return observations, issues
}
