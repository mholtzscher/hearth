package esphome

import (
	"math"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/entitytypes/quantitykind"
)

// routeKind is the Hearth Entity type and translation one route provides.
type routeKind uint8

const (
	routeTemperature routeKind = iota + 1
	routeHumidity
	routePressure
	routeNumeric
	routeBinary
	routeSwitchPower
	routeLightPower
	routeLightBrightness
	routeLightColor
	routeLightEffect
	routeText
	routeCover
	routeLock
	routeNumber
	routeSelect
	routeThermostat
)

// unitConversion converts an upstream sensor value into the route's canonical unit.
type unitConversion uint8

const (
	convertIdentity unitConversion = iota
	convertCelsiusToMilli
	convertFahrenheitToMilli
	convertPascalToHecto
	convertKiloPascalToHecto
)

const (
	// maximumUnitRunes is the support schema's unit length, counted in characters.
	maximumUnitRunes = 32
	// genericBound is the presentation and admission bound of an unclassified numeric sensor.
	genericBound = 1e12
	// dimensionlessUnit is Hearth's unit code for a reading with no unit.
	dimensionlessUnit = "1"
)

// sensorMapping is one catalog row's translation for a sensor.
type sensorMapping struct {
	kind       routeKind
	conversion unitConversion
	// unit, quantityKind, minimum, and maximum describe numeric sensor support. quantityKind is empty for the
	// generic row.
	unit         string
	quantityKind string
	minimum      float64
	maximum      float64
}

type catalogKey struct {
	deviceClass string
	unit        string
}

// sensorCatalog maps an ESPHome device class and unit to a typed Hearth Entity.
//
//nolint:gochecknoglobals // Immutable catalog initialized once and shared by all device plans.
var sensorCatalog = func() map[catalogKey]sensorMapping {
	catalog := map[catalogKey]sensorMapping{
		{"temperature", "°C"}: {kind: routeTemperature, conversion: convertCelsiusToMilli},
		{"temperature", "°F"}: {kind: routeTemperature, conversion: convertFahrenheitToMilli},
		{"humidity", "%"}:     {kind: routeHumidity},
	}
	for _, deviceClass := range []string{"pressure", "atmospheric_pressure"} {
		catalog[catalogKey{deviceClass, "hPa"}] = sensorMapping{kind: routePressure}
		catalog[catalogKey{deviceClass, "Pa"}] = sensorMapping{kind: routePressure, conversion: convertPascalToHecto}
		catalog[catalogKey{deviceClass, "kPa"}] = sensorMapping{
			kind:       routePressure,
			conversion: convertKiloPascalToHecto,
		}
	}
	numeric := func(unit, kind string, minimum, maximum float64) sensorMapping {
		return sensorMapping{kind: routeNumeric, unit: unit, quantityKind: kind, minimum: minimum, maximum: maximum}
	}
	catalog[catalogKey{"carbon_dioxide", "ppm"}] = numeric(
		"ppm",
		quantitykind.CarbonDioxide,
		0,
		40000, //nolint:mnd // Catalog bounds are defined per quantity and unit.
	)
	for _, deviceClass := range []string{"pm1", "pm25", "pm4", "pm10"} {
		catalog[catalogKey{deviceClass, "µg/m³"}] = numeric(
			"µg/m³",
			quantitykind.ParticulateMatter,
			0,
			1000, //nolint:mnd // Catalog bounds are defined per quantity and unit.
		)
	}
	catalog[catalogKey{"aqi", ""}] = numeric(
		"index",
		quantitykind.AirQualityIndex,
		0,
		500, //nolint:mnd // Catalog bounds are defined per quantity and unit.
	)
	catalog[catalogKey{"illuminance", "lx"}] = numeric(
		"lx",
		quantitykind.Illuminance,
		0,
		200000, //nolint:mnd // Catalog bounds are defined per quantity and unit.
	)
	catalog[catalogKey{"battery", "%"}] = numeric(
		"%",
		quantitykind.BatteryLevel,
		0,
		100, //nolint:mnd // Catalog bounds are defined per quantity and unit.
	)
	catalog[catalogKey{"voltage", "V"}] = numeric(
		"V",
		quantitykind.Voltage,
		0,
		1e6, //nolint:mnd // Catalog bounds are defined per quantity and unit.
	)
	catalog[catalogKey{"current", "A"}] = numeric(
		"A",
		quantitykind.ElectricCurrent,
		0,
		1e6, //nolint:mnd // Catalog bounds are defined per quantity and unit.
	)
	catalog[catalogKey{"power", "W"}] = numeric(
		"W",
		quantitykind.ElectricalPower,
		-1e9,
		1e9, //nolint:mnd // Catalog bounds are defined per quantity and unit.
	)
	catalog[catalogKey{"energy", "kWh"}] = numeric(
		"kWh",
		quantitykind.Energy,
		0,
		1e15, //nolint:mnd // Catalog bounds are defined per quantity and unit.
	)
	return catalog
}()

// catalogSensor returns the mapping for one sensor. ok is false when even the generic row cannot carry the unit.
func catalogSensor(deviceClass, unit string) (sensorMapping, bool) {
	if mapping, found := sensorCatalog[catalogKey{deviceClass, unit}]; found {
		return mapping, true
	}
	if utf8.RuneCountInString(unit) > maximumUnitRunes {
		return sensorMapping{}, false
	}
	if unit == "" {
		unit = dimensionlessUnit
	}
	return sensorMapping{kind: routeNumeric, unit: unit, minimum: -genericBound, maximum: genericBound}, true
}

// convert applies a unit conversion. Temperature conversions round to whole milli-Celsius.
func (conversion unitConversion) convert(value float64) float64 {
	switch conversion {
	case convertIdentity:
		return value
	case convertCelsiusToMilli:
		return math.Round(value * 1000) //nolint:mnd // Milli-Celsius per Celsius.
	case convertFahrenheitToMilli:
		return math.Round((value - 32) * 5000 / 9) //nolint:mnd // Fahrenheit to milli-Celsius.
	case convertPascalToHecto:
		return value / 100 //nolint:mnd // Pascals per hectopascal.
	case convertKiloPascalToHecto:
		return value * 10 //nolint:mnd // Hectopascals per kilopascal.
	default:
		return value
	}
}
