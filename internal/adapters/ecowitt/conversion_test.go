package ecowitt //nolint:testpackage // Conversion tests exercise the package-private decoders.

import (
	"errors"
	"math"
	"math/big"
	"testing"
)

// TestParseSourceDecimalAcceptsPlainDecimals protects the accepted decimal
// grammar with hand-written cases.
func TestParseSourceDecimalAcceptsPlainDecimals(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"0", "0.000", "3.13", "32.866", "45", "100", "-0.5", "+7", "007.5"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			parsed, err := parseSourceDecimal(value)
			if err != nil {
				t.Fatalf("parse %q: %v", value, err)
			}
			if parsed == nil {
				t.Fatalf("parse %q returned no value", value)
			}
		})
	}
}

// TestParseSourceDecimalRejectsNonDecimals protects the rejected grammar. A
// value that is not a plain decimal must never be silently reinterpreted as
// one.
func TestParseSourceDecimalRejectsNonDecimals(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"", " ", " 5", "5 ", "5.0 ", "1e5", "1E5", ".5", "5.", "1.2.3", "1/2", "0x10", "--5",
		"++5", "+", "-", "abc", "5a", "5,0", "NaN", "Inf", "-Inf", "1_000", "٣",
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if _, err := parseSourceDecimal(value); !errors.Is(err, errSourceValue) {
				t.Fatalf("parse %q error = %v, want errSourceValue", value, err)
			}
		})
	}
}

// TestTemperatureConversionRoundsExactFahrenheit protects the exact
// ((F-32)*5/9)*1000 conversion with hand-computed oracles, including the
// round-half-away-from-zero rule.
func TestTemperatureConversionRoundsExactFahrenheit(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		value string
		want  int64
	}{
		{"32", 0},
		{"212", 100000},
		{"-459.67", -273150},
		{"71.96", 22200},
		{"65.84", 18800},
		{"32.0009", 1},
		{"31.9991", -1},
		{"0", -17778},
	} {
		t.Run(testCase.value, func(t *testing.T) {
			t.Parallel()
			state, err := decodeFahrenheitTemperature(testCase.value)
			if err != nil {
				t.Fatalf("decode %q: %v", testCase.value, err)
			}
			if state.Kind != quantityTemperature {
				t.Fatalf("kind = %d, want temperature", state.Kind)
			}
			if state.MilliCelsius != testCase.want {
				t.Fatalf("milli-Celsius = %d, want %d", state.MilliCelsius, testCase.want)
			}
		})
	}
}

// TestTemperatureRejectsOutOfEnvelopeValues protects rejection instead of
// clamping at the temperature contract's exact bounds. The final case is a huge
// exact rational whose milli-Celsius result truncates to zero through an int64
// conversion, so it must be rejected by the envelope check rather than
// accepted as a plausible reading.
func TestTemperatureRejectsOutOfEnvelopeValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"2000", "-500", "-459.68", "1000000000", "33204139332677224.9088",
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			state, err := decodeFahrenheitTemperature(value)
			if !errors.Is(err, errOutOfEnvelope) {
				t.Fatalf("decode %q error = %v, want errOutOfEnvelope", value, err)
			}
			if state.MilliCelsius != 0 {
				t.Fatalf("rejected value returned %d, want the zero value", state.MilliCelsius)
			}
		})
	}
}

// TestTemperatureEnvelopeBoundsAreInclusive protects the temperature envelope
// at its exact rounded bounds. Fahrenheit 1832 is exactly the 1000000
// milli-Celsius maximum and -459.67 is exactly the -273150 minimum, so both
// are accepted; the nearest source step past the half-away rounding threshold
// is rejected.
func TestTemperatureEnvelopeBoundsAreInclusive(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		value   string
		want    int64
		wantErr error
	}{
		{value: "1832", want: 1000000},
		{value: "-459.67", want: -273150},
		{value: "1832.001", wantErr: errOutOfEnvelope},
		{value: "-459.671", wantErr: errOutOfEnvelope},
	} {
		t.Run(testCase.value, func(t *testing.T) {
			t.Parallel()
			state, err := decodeFahrenheitTemperature(testCase.value)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("decode %q error = %v, want %v", testCase.value, err, testCase.wantErr)
			}
			if err != nil {
				if state.MilliCelsius != 0 {
					t.Fatalf("rejected value returned %d, want the zero value", state.MilliCelsius)
				}
				return
			}
			if state.MilliCelsius != testCase.want {
				t.Fatalf("decode %q = %d, want %d", testCase.value, state.MilliCelsius, testCase.want)
			}
		})
	}
}

// TestPressureConversionUsesExactInchOfMercuryFactor protects the stated
// 1 inHg = 33.8638866667 hPa factor against a hand-written float64 oracle.
func TestPressureConversionUsesExactInchOfMercuryFactor(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		value string
		want  float64
	}{
		{"29.046", 29.046 * 33.8638866667},
		{"0", 0},
		{"29.921", 29.921 * 33.8638866667},
		{"29.104", 29.104 * 33.8638866667},
	} {
		t.Run(testCase.value, func(t *testing.T) {
			t.Parallel()
			state, err := decodeInchOfMercuryPressure(testCase.value)
			if err != nil {
				t.Fatalf("decode %q: %v", testCase.value, err)
			}
			if state.Kind != quantityPressure {
				t.Fatalf("kind = %d, want pressure", state.Kind)
			}
			assertClose(t, state.Value, testCase.want)
		})
	}
}

// TestSpeedConversionUsesExactMilesPerHourFactor protects the stated
// 1 mph = 0.44704 m/s factor.
func TestSpeedConversionUsesExactMilesPerHourFactor(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		value string
		want  float64
	}{
		{"3.13", 3.13 * 0.44704},
		{"4.03", 4.03 * 0.44704},
		{"6.04", 6.04 * 0.44704},
		{"0", 0},
	} {
		t.Run(testCase.value, func(t *testing.T) {
			t.Parallel()
			state, err := decodeMilesPerHourSpeed(testCase.value)
			if err != nil {
				t.Fatalf("decode %q: %v", testCase.value, err)
			}
			if state.Kind != quantitySpeed {
				t.Fatalf("kind = %d, want speed", state.Kind)
			}
			assertClose(t, state.Value, testCase.want)
		})
	}
}

// TestRainConversionUsesExactMillimetreFactor protects the stated
// 1 inch = 25.4 mm factor through the catalog's inch-scaled decoder, including
// the identical rate factor for inches per hour.
func TestRainConversionUsesExactMillimetreFactor(t *testing.T) {
	t.Parallel()

	decodeRain := decodeInchScaledNumeric(10000000)
	for _, testCase := range []struct {
		value string
		want  float64
	}{
		{"0.150", 0.150 * 25.4},
		{"32.866", 32.866 * 25.4},
		{"1.390", 1.390 * 25.4},
		{"0.000", 0},
	} {
		t.Run(testCase.value, func(t *testing.T) {
			t.Parallel()
			state, err := decodeRain(testCase.value)
			if err != nil {
				t.Fatalf("decode %q: %v", testCase.value, err)
			}
			if state.Kind != quantityNumericSensor {
				t.Fatalf("kind = %d, want numeric sensor", state.Kind)
			}
			assertClose(t, state.Value, testCase.want)
		})
	}
	rate := decodeInchScaledNumeric(10000)
	state, err := rate("12.5")
	if err != nil {
		t.Fatalf("decode rain rate: %v", err)
	}
	assertClose(t, state.Value, 12.5*25.4)
}

// TestCanonicalNumericConversionChecksEnvelope protects the generic numeric
// sensor envelope.
func TestCanonicalNumericConversionChecksEnvelope(t *testing.T) {
	t.Parallel()

	decode := decodeCanonicalNumeric(360)
	state, err := decode("44")
	if err != nil {
		t.Fatalf("decode 44: %v", err)
	}
	if state.Value != 44 {
		t.Fatalf("decoded value = %v, want 44", state.Value)
	}
	if _, err = decode("400"); !errors.Is(err, errOutOfEnvelope) {
		t.Fatalf("decode 400 error = %v, want errOutOfEnvelope", err)
	}
	if _, err = decode("-1"); !errors.Is(err, errOutOfEnvelope) {
		t.Fatalf("decode -1 error = %v, want errOutOfEnvelope", err)
	}
	if _, err = decode("3.5"); err != nil {
		t.Fatalf("decode fractional value: %v", err)
	}
}

// TestCanonicalFloatEnvelopeIsInclusiveAndExact protects the closed support
// envelope of canonicalFloat. The upper bound is inclusive, and it is decided
// against the exact rational before any float64 conversion, so a value outside
// the envelope by less than one float64 step is rejected rather than rounded
// onto the boundary.
func TestCanonicalFloatEnvelopeIsInclusiveAndExact(t *testing.T) {
	t.Parallel()

	// One part in 10^30: far below the float64 resolution at these magnitudes.
	tenToThirtieth := new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil)
	tiny := new(big.Rat).SetFrac(big.NewInt(1), tenToThirtieth)

	for _, testCase := range []struct {
		name    string
		value   *big.Rat
		maximum float64
		want    float64
		wantErr error
	}{
		{name: "zero", value: new(big.Rat), maximum: 100, want: 0},
		{name: "exact upper humidity bound", value: big.NewRat(100, 1), maximum: 100, want: 100},
		{name: "exact upper pressure bound", value: big.NewRat(2000, 1), maximum: 2000, want: 2000},
		{name: "exact upper rain bound", value: big.NewRat(10000000, 1), maximum: 10000000, want: 10000000},
		{
			name:    "just inside upper humidity bound",
			value:   new(big.Rat).Sub(big.NewRat(100, 1), tiny),
			maximum: 100,
			want:    100,
		},
		{
			name:    "just outside upper humidity bound",
			value:   new(big.Rat).Add(big.NewRat(100, 1), tiny),
			maximum: 100,
			wantErr: errOutOfEnvelope,
		},
		{
			name:    "just below zero",
			value:   new(big.Rat).Neg(tiny),
			maximum: 100,
			wantErr: errOutOfEnvelope,
		},
		{name: "negative", value: big.NewRat(-1, 1), maximum: 2000, wantErr: errOutOfEnvelope},
		{
			name:    "far above upper bound overflows float64",
			value:   new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(400), nil)),
			maximum: 2000,
			wantErr: errOutOfEnvelope,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got, err := canonicalFloat(testCase.value, testCase.maximum)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("canonicalFloat(%v, %v) error = %v, want %v",
					testCase.value, testCase.maximum, err, testCase.wantErr)
			}
			if err != nil {
				if got != 0 {
					t.Fatalf("rejected value returned %v, want the zero value", got)
				}
				return
			}
			if got != testCase.want {
				t.Fatalf("canonicalFloat(%v, %v) = %v, want %v", testCase.value, testCase.maximum, got, testCase.want)
			}
		})
	}
}

// TestCanonicalFloatRejectsOutsideValueThatRoundsOntoBoundary is the regression
// guard for the envelope check order. Comparing the converted float64 to the
// bound would accept this value, because it rounds to exactly 100.
func TestCanonicalFloatRejectsOutsideValueThatRoundsOntoBoundary(t *testing.T) {
	t.Parallel()

	const maximum = 100
	denominator := new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil)
	numerator := new(big.Int).Add(
		new(big.Int).Mul(big.NewInt(maximum), denominator),
		big.NewInt(1),
	)
	outside := new(big.Rat).SetFrac(numerator, denominator)
	rounded, _ := outside.Float64()
	if rounded != maximum {
		t.Fatalf("fixture value rounds to %v, want %v; the test no longer probes the rounding gap", rounded, maximum)
	}
	if got, err := canonicalFloat(outside, maximum); !errors.Is(err, errOutOfEnvelope) || got != 0 {
		t.Fatalf("canonicalFloat(%v, %v) = %v, %v; want rejection before rounding", outside, maximum, got, err)
	}
}

// TestCatalogDecodersEnforceExactInclusiveEnvelope protects the exact inclusive
// envelope through real catalog decoders. Each just-outside case converts to
// exactly the bound as a float64, so only an exact-rational envelope check can
// reject it, and each just-inside case stays accepted.
func TestCatalogDecodersEnforceExactInclusiveEnvelope(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		decode  func(string) (normalizedState, error)
		value   string
		want    float64
		wantErr error
	}{
		{name: "humidity at inclusive upper bound", decode: decodeRelativeHumidityPercent, value: "100", want: 100},
		{name: "humidity just outside upper bound", decode: decodeRelativeHumidityPercent, value: "100.0000000000000000001", wantErr: errOutOfEnvelope},
		{name: "wind direction at inclusive upper bound", decode: decodeCanonicalNumeric(360), value: "360", want: 360},
		{name: "wind direction just outside upper bound", decode: decodeCanonicalNumeric(360), value: "360.0000000000000000001", wantErr: errOutOfEnvelope},
		{name: "rain total just inside inch-scaled bound", decode: decodeInchScaledNumeric(10000000), value: "393700.7874015748", want: 10000000},
		{name: "rain total just outside inch-scaled bound", decode: decodeInchScaledNumeric(10000000), value: "393700.78740157481", wantErr: errOutOfEnvelope},
		{name: "rain rate just inside inch-scaled bound", decode: decodeInchScaledNumeric(10000), value: "393.7007874015748", want: 10000},
		{name: "rain rate just outside inch-scaled bound", decode: decodeInchScaledNumeric(10000), value: "393.70078740157482", wantErr: errOutOfEnvelope},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			state, err := testCase.decode(testCase.value)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("decode %q error = %v, want %v", testCase.value, err, testCase.wantErr)
			}
			if err != nil {
				return
			}
			if state.Value != testCase.want {
				t.Fatalf("decode %q = %v, want %v", testCase.value, state.Value, testCase.want)
			}
		})
	}
}

// TestCatalogDecodersRejectNonDecimalAndOutOfEnvelopeValues protects per-Entity
// isolation at the decoder boundary: a bad value is an error, never a clamped
// or zero measurement.
func TestCatalogDecodersRejectNonDecimalAndOutOfEnvelopeValues(t *testing.T) {
	t.Parallel()

	plans := catalog(t)
	for _, testCase := range []struct {
		key     string
		value   string
		wantErr error
	}{
		{"indoor-temperature", "not-a-number", errSourceValue},
		{"indoor-temperature", "", errSourceValue},
		{"indoor-temperature", " 71.96", errSourceValue},
		{"indoor-temperature", "1e2", errSourceValue},
		{"outdoor-temperature", "2000", errOutOfEnvelope},
		{"indoor-humidity", "-1", errOutOfEnvelope},
		{"indoor-humidity", "100.1", errOutOfEnvelope},
		{"relative-pressure", "-1", errOutOfEnvelope},
		{"absolute-pressure", "100", errOutOfEnvelope},
		{"wind-speed", "500", errOutOfEnvelope},
		{"wind-gust", "not-a-number", errSourceValue},
		{"maximum-daily-gust", "5000", errOutOfEnvelope},
		{"wind-direction", "400", errOutOfEnvelope},
		{"solar-radiation", "10000.1", errOutOfEnvelope},
		{"uv-index", "1e1", errSourceValue},
		{"rain-rate", "10000.1", errOutOfEnvelope},
		{"event-rain", "-0.1", errOutOfEnvelope},
		{"yearly-rain", "1000000", errOutOfEnvelope},
	} {
		t.Run(testCase.key+"/"+testCase.value, func(t *testing.T) {
			t.Parallel()
			plan := planByKey(t, plans, testCase.key)
			state, err := plan.Decode(testCase.value)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("%s decode %q error = %v, want %v", testCase.key, testCase.value, err, testCase.wantErr)
			}
			if state.Value != 0 || state.MilliCelsius != 0 {
				t.Fatalf("%s decoded %#v from a rejected value", testCase.key, state)
			}
		})
	}
}

// assertClose compares a converted value against an independently written
// float64 oracle, allowing only the last-bit difference between exact rational
// arithmetic and native float64 multiplication.
func assertClose(t *testing.T, got, want float64) {
	t.Helper()
	tolerance := 1e-9 * math.Max(1, math.Abs(want))
	if math.Abs(got-want) > tolerance {
		t.Fatalf("converted value = %v, want %v (tolerance %v)", got, want, tolerance)
	}
}
