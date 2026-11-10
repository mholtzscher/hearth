package pirateweather

import (
	"encoding/json"
	"errors"

	"github.com/mholtzscher/hearth/contracts/functionschema"
)

//nolint:mnd,goconst // Schema literals describe the published contract.
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
	numeric := func(minimum, maximum float64) map[string]any {
		return map[string]any{
			"type":        "number",
			"minimum":     minimum,
			"maximum":     maximum,
			"description": "Omitted when unavailable.",
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
			"humidity_percent": numeric(
				0,
				100,
			),
			"pressure_hpa":   numeric(0, 2000),
			"wind_speed_mps": numeric(0, 200),
			"wind_gust_mps":  numeric(0, 200),
			"wind_direction_degrees": numeric(
				0,
				360,
			),
			"precipitation_rate_mmh":            numeric(0, 10000),
			"precipitation_probability_percent": numeric(0, 100),
			"cloud_cover_percent":               numeric(0, 100),
			"uv_index":                          numeric(0, 100),
		}
		required := []string{"starts_at"}
		maximum := 48
		if daily {
			props["date"] = stringSchema(10)
			props["temperature_min_c"] = numeric(-273.15, 1000)
			props["temperature_max_c"] = numeric(-273.15, 1000)
			required = append(required, "date")
			maximum = 8
		} else {
			props["temperature_c"] = numeric(-273.15, 1000)
			props["apparent_temperature_c"] = numeric(-273.15, 1000)
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
