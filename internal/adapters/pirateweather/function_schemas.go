package pirateweather

import (
	"encoding/json"
	"errors"

	"github.com/mholtzscher/hearth/contracts/functionschema"
)

//nolint:mnd,goconst,funlen // Schema literals describe the published contract.
func compileFunctionSchemas() (*functionschema.Schema, *functionschema.Schema, *functionschema.Schema, *functionschema.Schema, error) {
	input := func(key string, maximum int) map[string]any {
		return map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{key},
			"properties": map[string]any{
				key: map[string]any{"type": "integer", "minimum": 1, "maximum": maximum},
			},
		}
	}
	stringSchema := func(maximum int) map[string]any { return map[string]any{"type": "string", "maxLength": maximum} }
	numeric := func(minimum, maximum float64, description string) map[string]any {
		return map[string]any{
			"type":        "number",
			"minimum":     minimum,
			"maximum":     maximum,
			"description": description + " Omitted when unavailable.",
		}
	}
	output := func(daily bool) map[string]any {
		props := map[string]any{
			"starts_at": stringSchema(
				30,
			),
			"summary":            stringSchema(512),
			"icon":               stringSchema(64),
			"precipitation_type": stringSchema(32),
			"humidity":           numeric(0, 100, "Relative humidity in percent, from 0 to 100."),
			"pressure":           numeric(0, 2000, "Sea-level pressure in hPa."),
			"wind_speed":         numeric(0, 200, "Wind speed in meters per second."),
			"wind_gust":          numeric(0, 200, "Wind gust speed in meters per second."),
			"wind_direction":     numeric(0, 360, "Direction the wind comes from, in degrees."),
			"precipitation_rate": numeric(
				0,
				10000,
				"Liquid-water-equivalent rate in mm/h; not an accumulation.",
			),
			"precipitation_probability": numeric(0, 100, "Chance of precipitation in percent, from 0 to 100."),
			"cloud_cover":               numeric(0, 100, "Cloud cover in percent, from 0 to 100."),
			"uv_index":                  numeric(0, 100, "Dimensionless UV index."),
		}
		required := []string{"starts_at"}
		maximum := 48
		if daily {
			props["date"] = stringSchema(10)
			props["temperature_min"] = numeric(
				-273.15,
				1000,
				"Lowest temperature from midnight to midnight, in degrees Celsius.",
			)
			props["temperature_max"] = numeric(
				-273.15,
				1000,
				"Highest temperature from midnight to midnight, in degrees Celsius.",
			)
			props["precipitation_probability"] = numeric(
				0,
				100,
				"The day's maximum chance of precipitation in percent, from 0 to 100; today's value may cover only the remaining hours.",
			)
			required = append(required, "date")
			maximum = 8
		} else {
			props["temperature"] = numeric(-273.15, 1000, "Air temperature in degrees Celsius.")
			props["apparent_temperature"] = numeric(-273.15, 1000, "Feels-like temperature in degrees Celsius.")
		}
		return map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"location_name", "retrieved_at", "timezone", "periods"},
			"properties": map[string]any{
				"location_name": stringSchema(128),
				"retrieved_at":  stringSchema(30),
				"timezone":      stringSchema(128),
				"periods": map[string]any{
					"type":     "array",
					"minItems": 1,
					"maxItems": maximum,
					"items": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"required":             required,
						"properties":           props,
					},
				},
			},
		}
	}
	schemas := make([]*functionschema.Schema, 0, 4)
	for _, doc := range []map[string]any{input("hours", 48), input("days", 8), output(false), output(true)} {
		encoded, err := json.Marshal(doc)
		if err != nil {
			return nil, nil, nil, nil, errors.New("invalid forecast schema")
		}
		compiled, err := functionschema.Compile(encoded)
		if err != nil {
			return nil, nil, nil, nil, errors.New("invalid forecast schema")
		}
		schemas = append(schemas, compiled)
	}
	return schemas[0], schemas[1], schemas[2], schemas[3], nil
}
