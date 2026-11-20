package homekit

import (
	"encoding/json"
	"math"
	"slices"
)

const (
	invalidValue          = -70410
	maxPercent            = 100
	maxHue                = 360
	milliPerCelsius       = 1000
	minSensorTemperature  = -270.0
	filterChangeThreshold = 10
	pmExcellent           = 12
	pmGood                = 35
	pmFair                = 55
	pmInferior            = 150
	modeAuto              = "auto"
	qualityExcellent      = 1
	qualityGood           = 2
	qualityFair           = 3
	qualityInferior       = 4
	qualityPoor           = 5
	tenthsPerCelsius      = 10
)

func stateValue[T any](entity Entity) (T, bool) {
	var value T
	if entity.State == nil || len(entity.State.Value) == 0 || string(entity.State.Value) == "null" {
		return value, false
	}
	err := json.Unmarshal(entity.State.Value, &value)
	return value, err == nil
}

func supportValue[T any](entity Entity) T {
	var value T
	_ = json.Unmarshal(entity.Support, &value)
	return value
}

func supports(entity Entity, operation string) bool {
	var support struct {
		Operations map[string]json.RawMessage `json:"operations"`
	}
	_ = json.Unmarshal(entity.Support, &support)
	raw, ok := support.Operations[operation]
	return ok && string(raw) != "null"
}

func number(value any, minValue, maxValue float64, integral bool) (float64, bool) {
	var n float64
	switch v := value.(type) {
	case float64:
		n = v
	case int:
		// The upstream HAP library normalizes integer characteristics to int.
		n = float64(v)
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0) && n >= minValue && n <= maxValue && (!integral || math.Trunc(n) == n)
}

func milliCelsius(value float64) int64 { return int64(math.Round(value * milliPerCelsius)) }
func celsius(value int64) float64      { return float64(value) / milliPerCelsius }

func speedPercent(index, count int) float64 {
	return math.Round(maxPercent * float64(index+1) / float64(count))
}

func speedIndex(speed float64, count int) int {
	return max(0, min(count-1, int(math.Ceil(speed*float64(count)/maxPercent))-1))
}

func airQuality(pm float64) int {
	switch {
	case pm <= pmExcellent:
		return qualityExcellent
	case pm <= pmGood:
		return qualityGood
	case pm <= pmFair:
		return qualityFair
	case pm <= pmInferior:
		return qualityInferior
	default:
		return qualityPoor
	}
}

func assignment(exposure Exposure, role Role) (RoleAssignment, bool) {
	for _, item := range exposure.Entities {
		if item.Role == role {
			return item, true
		}
	}
	return RoleAssignment{}, false
}

func slot(item RoleAssignment) string { return string(item.Role) + "/" + item.EntityID }

func containsChoice(choices []string, choice string) bool { return slices.Contains(choices, choice) }

func direct(entityID, operation, key string, value any) writeIntent {
	return writeIntent{EntityID: entityID, Operation: operation, Parameters: map[string]any{key: value}}
}
