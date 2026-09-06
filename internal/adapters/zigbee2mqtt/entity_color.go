package zigbee2mqtt

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

// Shared exact numeric decoding for Zigbee2MQTT color composites. Every
// coordinate arrives as a JSON number; strings, null, Booleans, and raw
// out-of-range values fail before rounding so no observation is clamped or
// coerced.

const (
	upstreamColorXYName = "color_xy"
	upstreamColorHSName = "color_hs"
	upstreamColorMode   = "color_mode"

	colorXYScale = 10000

	// Raw domain maxima: hue spans 0..360 degrees, saturation 0..100 percent.
	hueDomainMaximum        = 360
	saturationDomainMaximum = 100
)

//nolint:gochecknoglobals // Exact upstream domains shared read-only by every color decoder.
var (
	// Exact raw domains: XY is unit 0..1, hue is 0..360 degrees, saturation is 0..100 percent.
	maxRawXY         = big.NewRat(1, 1)
	maxRawHue        = big.NewRat(hueDomainMaximum, 1)
	maxRawSaturation = big.NewRat(saturationDomainMaximum, 1)
)

func isColorCompositeName(name string) bool {
	return name == upstreamColorXYName || name == upstreamColorHSName
}

// decodeScaledCoordinate parses one exact JSON number in 0..maximum, rejects
// raw out-of-range values before rounding, and rounds half-up to scale steps.
func decodeScaledCoordinate(payload json.RawMessage, maximum *big.Rat, scale int64) (int64, error) {
	var decoded any
	if err := decodeJSON(payload, &decoded); err != nil {
		return 0, fmt.Errorf("decode color coordinate number: %w", err)
	}
	number, ok := decoded.(json.Number)
	if !ok {
		return 0, errors.New("color coordinate value must be a JSON number")
	}
	raw, ok := new(big.Rat).SetString(number.String())
	if !ok {
		return 0, errors.New("color coordinate value must be a finite number")
	}
	if raw.Sign() < 0 || raw.Cmp(maximum) > 0 {
		return 0, errors.New("color coordinate value is outside its upstream range")
	}
	scaled := new(big.Rat).Mul(raw, big.NewRat(scale, 1))
	return roundHalfUpNonnegative(scaled), nil
}

// roundHalfUpNonnegative rounds one nonnegative rational half-up to an int64.
// Operands stay nonnegative by construction: callers reject negative inputs
// before scaling, so the addition cannot overflow the supported extremes.
func roundHalfUpNonnegative(value *big.Rat) int64 {
	numerator := value.Num()
	denominator := value.Denom()
	doubled := new(big.Int).Lsh(new(big.Int).Set(numerator), 1)
	doubled.Add(doubled, denominator)
	divisor := new(big.Int).Lsh(new(big.Int).Set(denominator), 1)
	return new(big.Int).Quo(doubled, divisor).Int64()
}

// colorObjectFields extracts the upstream color object behind one color
// property. A missing axis is a partial pair, not an absent value; callers
// report it as a per-representation decode issue.
func colorObjectFields(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := decodeJSON(raw, &fields); err != nil {
		return nil, fmt.Errorf("decode color object: %w", err)
	}
	return fields, nil
}

// formatScaledUnit renders one 0..10000 XY step as exact base-10 decimal JSON
// without float arithmetic, so no float-artifact digits reach the wire.
func formatScaledUnit(value int64) string {
	if value <= 0 {
		return "0"
	}
	if value >= colorXYScale {
		return "1"
	}
	digits := fmt.Sprintf("%04d", value)
	trimmed := digits
	for len(trimmed) > 1 && trimmed[len(trimmed)-1] == '0' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return "0." + trimmed
}

// colorAxisBound checks one optional explicit child bound against its upstream
// domain. Absent bounds are allowed; a present bound must match numerically.
// Raw spellings compare by value, so "1.0" and "1e0" both match an XY maximum
// of one.
func colorAxisBound(raw json.RawMessage, fallback *float64, bound *big.Rat) bool {
	if len(raw) != 0 {
		var decoded any
		if decodeJSON(raw, &decoded) != nil {
			return false
		}
		number, ok := decoded.(json.Number)
		if !ok {
			return false
		}
		exact, ok := new(big.Rat).SetString(number.String())
		if !ok {
			return false
		}
		return exact.Cmp(bound) == 0
	}
	if fallback == nil {
		return true
	}
	approximate, ok := new(big.Rat).SetString(fmt.Sprintf("%v", *fallback))
	if !ok {
		return false
	}
	return approximate.Cmp(bound) == 0
}
