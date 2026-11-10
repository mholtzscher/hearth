package pirateweather

import (
	"errors"
	"math"

	"github.com/mholtzscher/hearth/entitytypes/quantitykind"
	"github.com/mholtzscher/hearth/sdk/adapter"
	"github.com/mholtzscher/hearth/sdk/adapter/numericsensorv1"
	"github.com/mholtzscher/hearth/sdk/adapter/pressurev1"
	"github.com/mholtzscher/hearth/sdk/adapter/relativehumidityv1"
	"github.com/mholtzscher/hearth/sdk/adapter/speedv1"
	"github.com/mholtzscher/hearth/sdk/adapter/temperaturev1"
)

type quantity uint8

const (
	hourlyForecastKey = "hourly-forecast"
	dailyForecastKey  = "daily-forecast"
)

const (
	temperature quantity = iota
	humidity
	pressure
	speed
	numeric
)

// measurement defines current registration, conversion and publication in one place.
type measurement struct {
	key, field, name        string
	kind                    quantity
	unit                    string
	minimum, maximum, scale float64
	quantityKind            string
}

func measurements() []measurement {
	return []measurement{
		{"temperature", "temperature", "Temperature", temperature, "", -273150, 1000000, 1000, ""},
		{
			"apparent-temperature",
			"apparentTemperature",
			"Feels-like Temperature",
			temperature,
			"",
			-273150,
			1000000,
			1000,
			"",
		},
		{"relative-humidity", "humidity", "Relative Humidity", humidity, "", 0, 100, 100, ""},
		{
			"pressure", //nolint:goconst // Entity key and provider field are separate contracts.
			"pressure",
			"Sea-level Pressure",
			pressure,
			"",
			0,
			2000,
			1,
			"",
		},
		{"wind-speed", "windSpeed", "Wind Speed", speed, "", 0, 200, 1, ""},
		{"wind-gust", "windGust", "Wind Gust", speed, "", 0, 200, 1, ""},
		{"wind-direction", "windBearing", "Wind Direction", numeric, "deg", 0, 360, 1, quantitykind.WindDirection},
		{
			"precipitation-rate",
			"precipIntensity",
			"Precipitation Rate (Liquid Equivalent)",
			numeric,
			"mm/h",
			0,
			10000,
			1,
			quantitykind.PrecipitationRate,
		},
		{
			"precipitation-probability",
			"precipProbability",
			"Precipitation Probability",
			numeric,
			"%",
			0,
			100,
			100,
			quantitykind.PrecipitationProbability,
		},
		{"cloud-cover", "cloudCover", "Cloud Cover", numeric, "%", 0, 100, 100, quantitykind.CloudCover},
		{"uv-index", "uvIndex", "UV Index", numeric, "index", 0, 100, 1, quantitykind.UVIndex},
	}
}

func (m measurement) decode(fields object) *float64 {
	value := number(fields, m.field, m.minimum/m.scale, m.maximum/m.scale, m.scale)
	if value != nil && m.kind == temperature {
		*value = math.Round(*value)
	}
	return value
}

func (m measurement) numericSupport() numericsensorv1.Support {
	support := numericsensorv1.Support{
		State: numericsensorv1.StateSupport{Minimum: m.minimum, Maximum: m.maximum, Unit: m.unit},
	}
	if m.quantityKind != "" {
		support.State.QuantityKind = &m.quantityKind
	}
	return support
}

func (m measurement) descriptor() (adapter.EntityDescriptor, error) {
	meta := adapter.EntityMetadata{Key: m.key, ExternalID: "location/" + m.key, Name: m.name}
	switch m.kind {
	case temperature:
		return temperaturev1.NewEntityDescriptor(meta, temperaturev1.Support{})
	case humidity:
		return relativehumidityv1.NewEntityDescriptor(meta, relativehumidityv1.Support{})
	case pressure:
		return pressurev1.NewEntityDescriptor(meta, pressurev1.Support{})
	case speed:
		return speedv1.NewEntityDescriptor(meta, speedv1.Support{})
	case numeric:
		return numericsensorv1.NewEntityDescriptor(meta, m.numericSupport())
	}
	return adapter.EntityDescriptor{}, errors.New("invalid measurement kind")
}
