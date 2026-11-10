package devices

import (
	"errors"
	"math"
)

// Converter resolves only positive-affine conversions, safe for extrema and
// arithmetic sample means. Selection and formatting are not engine concerns.
type Converter interface {
	Affine(from, to string) (Affine, error)
}

type Affine struct {
	Scale  float64
	Offset float64
}

const (
	unitMilliCelsius  = "mCel"
	unitCelsius       = "Cel"
	unitFahrenheit    = "[degF]"
	unitMilesPerHour  = "[mi_i]/h"
	unitInchesMercury = "[in_i'Hg]"
	unitInches        = "[in_i]"
	unitInchesPerHour = "[in_i]/h"
	unitChromaticity  = "10*-4"
)

func (conversion Affine) Apply(value float64) (float64, error) {
	if !finite(conversion.Scale) || conversion.Scale <= 0 || !finite(conversion.Offset) || !finite(value) {
		return 0, errors.New("invalid affine conversion")
	}
	result := value*conversion.Scale + conversion.Offset
	if !finite(result) {
		return 0, errors.New("quantity conversion result is not finite")
	}
	return result, nil
}

// TableConverter implements the bounded display pairs from UCUM definitions.
// It does not replace Adapter ingestion factors or normalization policies.
type TableConverter struct{}

func (TableConverter) Affine(from, to string) (Affine, error) {
	if from == to {
		return Affine{Scale: 1}, nil
	}
	//nolint:mnd // These coefficients are the supported unit definitions.
	switch [2]string{from, to} {
	case [2]string{unitMilliCelsius, unitCelsius}:
		return Affine{Scale: 1.0 / 1000}, nil
	case [2]string{unitMilliCelsius, unitFahrenheit}:
		return Affine{Scale: 9.0 / 5000, Offset: 32}, nil
	case [2]string{"m/s", unitMilesPerHour}:
		return Affine{Scale: 3600 / 1609.344}, nil
	case [2]string{"hPa", unitInchesMercury}:
		return Affine{Scale: 1 / 33.863788}, nil
	case [2]string{"mm", unitInches}, [2]string{"mm/h", unitInchesPerHour}:
		return Affine{Scale: 1 / 25.4}, nil
	case [2]string{unitChromaticity, "1"}:
		return Affine{Scale: 1.0 / 10000}, nil
	default:
		return Affine{}, errors.New("unsupported quantity conversion")
	}
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
