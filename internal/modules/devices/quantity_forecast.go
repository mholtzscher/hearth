package devices

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/mholtzscher/hearth/entitytypes/quantitykind"
)

// Forecast source schemas use unit-suffixed properties. Only the household read
// representation renames them; canonical reads and retained audit stay source-shaped.
type forecastQuantity struct {
	source string
	field  string
	unit   string
	kind   string
}

func forecastQuantities() []forecastQuantity {
	return []forecastQuantity{
		{"temperature_c", "temperature", unitCelsius, quantitykind.Temperature},
		{"apparent_temperature_c", "apparent_temperature", unitCelsius, quantitykind.Temperature},
		{"temperature_min_c", "temperature_min", unitCelsius, quantitykind.Temperature},
		{"temperature_max_c", "temperature_max", unitCelsius, quantitykind.Temperature},
		{"precipitation_probability_percent", "precipitation_probability", "%", quantitykind.PrecipitationProbability},
		{"precipitation_rate_mmh", "precipitation_rate", "mm/h", quantitykind.PrecipitationRate},
		{"humidity_percent", "humidity", "%", quantitykind.RelativeHumidity},
		{"pressure_hpa", "pressure", unitHectopascal, quantitykind.AtmosphericPressure},
		{"wind_speed_mps", "wind_speed", unitMetersPerSecond, quantitykind.Speed},
		{"wind_gust_mps", "wind_gust", unitMetersPerSecond, quantitykind.Speed},
		{"wind_direction_degrees", "wind_direction", unitDegrees, quantitykind.WindDirection},
		{"cloud_cover_percent", "cloud_cover", "%", quantitykind.CloudCover},
		{"uv_index", "uv_index", "index", quantitykind.UVIndex},
	}
}

func (presenter *Presenter) forecast(
	view EntityWithState,
	units ReadUnits,
) (EntityWithState, map[string]QuantityMetadata, error) {
	metadata := make(map[string]QuantityMetadata)
	if view.State == nil {
		return view, metadata, nil
	}
	var snapshot struct {
		Periods []map[string]json.RawMessage `json:"periods"`
	}
	if err := json.Unmarshal(view.State.Value, &snapshot); err != nil {
		return EntityWithState{}, nil, err
	}
	for _, quantity := range forecastQuantities() {
		if err := presenter.forecastField(quantity, snapshot.Periods, units, metadata); err != nil {
			return EntityWithState{}, nil, err
		}
	}
	if units == UnitsCanonical {
		return view, metadata, nil
	}
	value, err := json.Marshal(snapshot)
	if err != nil {
		return EntityWithState{}, nil, err
	}
	state := *view.State
	state.Value = value
	view.State = &state
	return view, metadata, nil
}

func (presenter *Presenter) forecastField(
	quantity forecastQuantity,
	periods []map[string]json.RawMessage,
	units ReadUnits,
	metadata map[string]QuantityMetadata,
) error {
	presentation, err := presenter.field(NumericField{
		ValueType: NumericNumber, Unit: FixedUnit{Code: quantity.unit}, Kind: FixedKind{Code: quantity.kind},
	}, nil, units)
	if err != nil {
		return err
	}
	// Forecast cells intentionally use whole degrees and probabilities,
	// while the API retains the full converted numeric value.
	if quantity.kind == quantitykind.Temperature || quantity.kind == quantitykind.PrecipitationProbability {
		presentation.metadata.FractionDigits = 0
	}
	field := quantity.source
	if units != UnitsCanonical {
		field = quantity.field
	}
	for index, period := range periods {
		raw, present := period[quantity.source]
		if !present {
			continue
		}
		if units != UnitsCanonical {
			converted, conversionErr := presentation.number(raw)
			if conversionErr != nil {
				return fmt.Errorf("project forecast field %s: %w", field, conversionErr)
			}
			delete(period, quantity.source)
			period[field] = converted
		}
		metadata["/periods/"+strconv.Itoa(index)+"/"+field] = presentation.metadata
	}
	return nil
}
