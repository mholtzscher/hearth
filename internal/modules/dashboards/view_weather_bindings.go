package dashboards

import "encoding/json"

const (
	hourlyForecastType = "hearth.hourlyforecast/v1"
	dailyForecastType  = "hearth.dailyforecast/v1"
)

func forecastBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Source binding `json:"source"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	props.Source.ValueKind = forecastValueKind
	return []binding{props.Source}, nil
}

func validateBindingShape(b binding) error {
	if b.ValueKind == forecastValueKind {
		if b.Field != "" || (b.EntityType != hourlyForecastType && b.EntityType != dailyForecastType) {
			return invalidDefinition("a Forecast reads a whole hourly or daily forecast Entity")
		}
		return nil
	}
	return validateFieldBinding(b)
}

// Stored units of the numeric-sensor slots in weather cards.
const (
	unitRainRate  = "mm/h"
	unitRainDepth = "mm"
	unitDegrees   = "deg"
)

// weatherUnitReasons names the rule a weather slot violates when its Entity
// stores another unit.
//
//nolint:gochecknoglobals // Immutable rejection reasons keyed by required unit.
var weatherUnitReasons = map[string]string{
	unitRainRate:  "weather rain rates need a numeric sensor in mm/h",
	unitRainDepth: "weather rain totals need a numeric sensor in mm",
	unitDegrees:   "weather wind directions need a numeric sensor in deg",
}

// weatherReading marks a weather slot as a numeric read, pinned to unit when
// the slot accepts a generic numeric sensor.
func weatherReading(b binding, unit string) binding {
	b.ValueKind, b.RequiredUnit = numericValueKind, unit
	return b
}

// weatherHistory is the history read a weather card makes for one slot.
func weatherHistory(b binding, window string) binding {
	b.Kind, b.Window = seriesBindingKind, window
	return b
}

// appendOptional adds a bound optional slot; schemas make every slot a root read.
func appendOptional(result []binding, b *binding, unit string) []binding {
	if b == nil {
		return result
	}
	return append(result, weatherReading(*b, unit))
}

func outsideNowBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Temperature   binding  `json:"temperature"`
		Humidity      *binding `json:"humidity"`
		Wind          *binding `json:"wind"`
		WindDirection *binding `json:"wind_direction"`
		RainRate      *binding `json:"rain_rate"`
		RainToday     *binding `json:"rain_today"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	temperature := weatherReading(props.Temperature, "")
	result := []binding{temperature, weatherHistory(temperature, "24h")}
	result = appendOptional(result, props.Humidity, "")
	result = appendOptional(result, props.Wind, "")
	result = appendOptional(result, props.WindDirection, unitDegrees)
	result = appendOptional(result, props.RainRate, unitRainRate)
	return appendOptional(result, props.RainToday, unitRainDepth), nil
}

func rainBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Rate   binding  `json:"rate"`
		Storm  *binding `json:"storm"`
		Totals *struct {
			Day   *binding `json:"day"`
			Week  *binding `json:"week"`
			Month *binding `json:"month"`
			Year  *binding `json:"year"`
		} `json:"totals"`
		Trend string `json:"trend"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	rate := weatherReading(props.Rate, unitRainRate)
	result := []binding{rate}
	if props.Trend != "" {
		result = append(result, weatherHistory(rate, props.Trend))
	}
	result = appendOptional(result, props.Storm, unitRainDepth)
	if totals := props.Totals; totals != nil {
		for _, total := range []*binding{totals.Day, totals.Week, totals.Month, totals.Year} {
			result = appendOptional(result, total, unitRainDepth)
		}
	}
	return result, nil
}

func windBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Speed     binding  `json:"speed"`
		Gust      *binding `json:"gust"`
		Direction *binding `json:"direction"`
		MaxGust   *binding `json:"max_gust"`
		Trend     string   `json:"trend"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	speed := weatherReading(props.Speed, "")
	result := []binding{speed}
	if props.Trend != "" {
		result = append(result, weatherHistory(speed, props.Trend))
	}
	result = appendOptional(result, props.Gust, "")
	result = appendOptional(result, props.Direction, unitDegrees)
	return appendOptional(result, props.MaxGust, ""), nil
}

func pressureBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Source binding `json:"source"`
		Trend  string  `json:"trend"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	pressure := weatherReading(props.Source, "")
	// The three-hour tendency always reads the last six hours.
	result := []binding{pressure, weatherHistory(pressure, "6h")}
	if props.Trend != "" && props.Trend != "6h" {
		result = append(result, weatherHistory(pressure, props.Trend))
	}
	return result, nil
}
