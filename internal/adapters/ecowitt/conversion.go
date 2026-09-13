package ecowitt

import (
	"errors"
	"math/big"
)

// Source-value and envelope failures. Both are plain sentinels: a rejected
// measurement never repeats the value it rejected.
var (
	errSourceValue     = errors.New("ecowitt measurement is not a decimal value")
	errOutOfEnvelope   = errors.New("ecowitt measurement is outside the Entity support envelope")
	errUnsupportedKind = errors.New("ecowitt measurement has no Entity type")
)

// Canonical conversion constants, expressed as exact rationals so a source
// decimal is converted without a floating-point intermediate. The decimal
// expansion of each factor is fixed by the Adapter contract:
//
//	1 inHg   = 33.8638866667 hPa
//	1 mph    = 0.44704 m/s
//	1 inch   = 25.4 mm
//	1 inch/h = 25.4 mm/h
const (
	fahrenheitFreezingPoint    = 32
	fahrenheitScaleNumerator   = 5
	fahrenheitScaleDenominator = 9
	milliCelsiusPerDegree      = 1000

	inchOfMercuryNumerator   = 338638866667
	inchOfMercuryDenominator = 10000000000

	milesPerHourNumerator   = 44704
	milesPerHourDenominator = 100000

	millimetersPerInchNumerator   = 254
	millimetersPerInchDenominator = 10
)

// Canonical State envelopes. They mirror each generated Entity-type State
// schema and are written here independently so a schema regression cannot
// restate its own expectation. They are validation envelopes, not expected
// operating ranges: an out-of-envelope value is rejected, never clamped.
const (
	temperatureMinimumMilliCelsius = -273150
	temperatureMaximumMilliCelsius = 1000000
	relativeHumidityMinimumPercent = 0
	relativeHumidityMaximumPercent = 100
	pressureMinimumHectopascals    = 0
	pressureMaximumHectopascals    = 2000
	speedMinimumMetersPerSecond    = 0
	speedMaximumMetersPerSecond    = 200
)

// parseSourceDecimal parses one Ecowitt decimal string exactly into a rational
// number. It rejects empty values, whitespace, exponent notation, fractions,
// hexadecimal digits, trailing data, a sign without digits, and a decimal
// point without digits on either side, so a value that is not a plain decimal
// can never be silently reinterpreted.
func parseSourceDecimal(value string) (*big.Rat, error) {
	digits := value
	negative := false
	if digits != "" && (digits[0] == '+' || digits[0] == '-') {
		negative = digits[0] == '-'
		digits = digits[1:]
	}
	if digits == "" {
		return nil, errSourceValue
	}
	integerDigits := 0
	fractionDigits := 0
	seenDot := false
	for index := range len(digits) {
		switch character := digits[index]; {
		case character >= '0' && character <= '9':
			if seenDot {
				fractionDigits++
			} else {
				integerDigits++
			}
		case character == '.':
			if seenDot {
				return nil, errSourceValue
			}
			seenDot = true
		default:
			return nil, errSourceValue
		}
	}
	if integerDigits == 0 || seenDot && fractionDigits == 0 {
		return nil, errSourceValue
	}
	canonical := digits
	if negative {
		canonical = "-" + digits
	}
	parsed, ok := new(big.Rat).SetString(canonical)
	if !ok {
		return nil, errSourceValue
	}
	return parsed, nil
}

// fahrenheitToMilliCelsius applies ((F - 32) * 5 / 9) * 1000 exactly.
func fahrenheitToMilliCelsius(fahrenheit *big.Rat) *big.Rat {
	shifted := new(big.Rat).Sub(fahrenheit, big.NewRat(fahrenheitFreezingPoint, 1))
	scale := big.NewRat(fahrenheitScaleNumerator*milliCelsiusPerDegree, fahrenheitScaleDenominator)
	return shifted.Mul(shifted, scale)
}

// inchOfMercuryToHectopascals applies 1 inHg = 33.8638866667 hPa.
func inchOfMercuryToHectopascals(inches *big.Rat) *big.Rat {
	return new(big.Rat).Mul(inches, big.NewRat(inchOfMercuryNumerator, inchOfMercuryDenominator))
}

// milesPerHourToMetersPerSecond applies 1 mph = 0.44704 m/s.
func milesPerHourToMetersPerSecond(milesPerHour *big.Rat) *big.Rat {
	return new(big.Rat).Mul(milesPerHour, big.NewRat(milesPerHourNumerator, milesPerHourDenominator))
}

// roundHalfAwayFromZero rounds an exact rational to the nearest integer with
// halves moving away from zero, matching the temperature contract. It returns
// an exact [big.Int] so a value that does not fit int64 is rejected by its
// caller instead of silently truncating into a plausible milli-Celsius reading.
func roundHalfAwayFromZero(value *big.Rat) *big.Int {
	magnitude := new(big.Int).Abs(value.Num())
	quotient, remainder := new(big.Int).QuoRem(magnitude, value.Denom(), new(big.Int))
	if new(big.Int).Lsh(remainder, 1).Cmp(value.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if value.Sign() < 0 {
		quotient.Neg(quotient)
	}
	return quotient
}

// canonicalFloat converts an exact rational into the finite canonical unit
// value a generated facade accepts. Every canonical envelope starts at zero, so
// only the upper bound is checked, and that bound is inclusive. The envelope is
// compared against the exact rational before any conversion, so a decimal that
// lies just outside an integer bound cannot round onto the boundary and slip
// through; a rejected value is never clamped, rounded up, or returned as zero
// masquerading as a reading. The returned float64 is the nearest representable
// value of an in-envelope measurement.
func canonicalFloat(value *big.Rat, maximum float64) (float64, error) {
	if value.Sign() < 0 {
		return 0, errOutOfEnvelope
	}
	limit := new(big.Rat).SetFloat64(maximum)
	// A non-finite bound means an envelope constant is broken; fail closed
	// rather than treat the envelope as unbounded.
	if limit == nil || value.Cmp(limit) > 0 {
		return 0, errOutOfEnvelope
	}
	converted, _ := value.Float64()
	return converted, nil
}

// decodeFahrenheitTemperature converts tempinf and tempf into integer
// milli-Celsius.
func decodeFahrenheitTemperature(value string) (normalizedState, error) {
	parsed, err := parseSourceDecimal(value)
	if err != nil {
		return normalizedState{}, err
	}
	rounded := roundHalfAwayFromZero(fahrenheitToMilliCelsius(parsed))
	// A huge exact rational must be rejected by the envelope check, not
	// truncated by an int64 conversion into a plausible value.
	if !rounded.IsInt64() {
		return normalizedState{}, errOutOfEnvelope
	}
	milliCelsius := rounded.Int64()
	if milliCelsius < temperatureMinimumMilliCelsius || milliCelsius > temperatureMaximumMilliCelsius {
		return normalizedState{}, errOutOfEnvelope
	}
	return normalizedState{Kind: quantityTemperature, MilliCelsius: milliCelsius}, nil
}

// decodeRelativeHumidityPercent converts humidityin and humidity into decimal
// percent.
func decodeRelativeHumidityPercent(value string) (normalizedState, error) {
	parsed, err := parseSourceDecimal(value)
	if err != nil {
		return normalizedState{}, err
	}
	converted, err := canonicalFloat(parsed, relativeHumidityMaximumPercent)
	if err != nil {
		return normalizedState{}, err
	}
	return normalizedState{Kind: quantityRelativeHumidity, Value: converted}, nil
}

// decodeInchOfMercuryPressure converts baromrelin and baromabsin into decimal
// hectopascals.
func decodeInchOfMercuryPressure(value string) (normalizedState, error) {
	parsed, err := parseSourceDecimal(value)
	if err != nil {
		return normalizedState{}, err
	}
	converted, err := canonicalFloat(
		inchOfMercuryToHectopascals(parsed),
		pressureMaximumHectopascals,
	)
	if err != nil {
		return normalizedState{}, err
	}
	return normalizedState{Kind: quantityPressure, Value: converted}, nil
}

// decodeMilesPerHourSpeed converts windspeedmph, windgustmph, and maxdailygust
// into decimal metres per second.
func decodeMilesPerHourSpeed(value string) (normalizedState, error) {
	parsed, err := parseSourceDecimal(value)
	if err != nil {
		return normalizedState{}, err
	}
	converted, err := canonicalFloat(
		milesPerHourToMetersPerSecond(parsed),
		speedMaximumMetersPerSecond,
	)
	if err != nil {
		return normalizedState{}, err
	}
	return normalizedState{Kind: quantitySpeed, Value: converted}, nil
}

// decodeCanonicalNumeric parses one non-negative decimal value that is already
// expressed in its Entity's canonical unit and checks its envelope.
func decodeCanonicalNumeric(maximum float64) func(string) (normalizedState, error) {
	return func(value string) (normalizedState, error) {
		parsed, err := parseSourceDecimal(value)
		if err != nil {
			return normalizedState{}, err
		}
		converted, err := canonicalFloat(parsed, maximum)
		if err != nil {
			return normalizedState{}, err
		}
		return normalizedState{Kind: quantityNumericSensor, Value: converted}, nil
	}
}

// decodeInchScaledNumeric parses one inch-scaled decimal and converts it with
// the 1 inch = 25.4 mm factor. It serves both rain totals (inches to
// millimetres) and rain rate (inches per hour to millimetres per hour), which
// share the same factor.
func decodeInchScaledNumeric(maximum float64) func(string) (normalizedState, error) {
	factor := big.NewRat(millimetersPerInchNumerator, millimetersPerInchDenominator)
	return func(value string) (normalizedState, error) {
		parsed, err := parseSourceDecimal(value)
		if err != nil {
			return normalizedState{}, err
		}
		converted, err := canonicalFloat(new(big.Rat).Mul(parsed, factor), maximum)
		if err != nil {
			return normalizedState{}, err
		}
		return normalizedState{Kind: quantityNumericSensor, Value: converted}, nil
	}
}
