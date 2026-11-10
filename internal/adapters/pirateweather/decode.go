package pirateweather

import (
	"encoding/json"
	"errors"
	"math"
	"time"
	"unicode/utf8"
)

var errInvalidResponse = errors.New("invalid response")

const jsonNull = "null"

type object = map[string]json.RawMessage

func asObject(raw json.RawMessage) (object, error) {
	var value object
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, errInvalidResponse
	}
	return value, nil
}

func providerDocument(body []byte) (object, error) {
	root, err := asObject(body)
	if err != nil {
		return nil, err
	}
	flags, err := asObject(root["flags"])
	if err != nil {
		return nil, err
	}
	var units string
	if json.Unmarshal(flags["units"], &units) != nil || units != "si" {
		return nil, errInvalidResponse
	}
	return root, nil
}

func sourceTime(raw json.RawMessage) (time.Time, error) {
	var seconds int64
	if len(raw) == 0 || string(raw) == jsonNull || json.Unmarshal(raw, &seconds) != nil {
		return time.Time{}, errInvalidResponse
	}
	t := time.Unix(seconds, 0).UTC()
	if t.Year() < 1 || t.Year() > 9999 {
		return time.Time{}, errInvalidResponse
	}
	return t, nil
}

func number(o object, field string, minimum, maximum, scale float64) *float64 {
	raw := o[field]
	var value float64
	if len(raw) == 0 || string(raw) == jsonNull || json.Unmarshal(raw, &value) != nil || math.IsNaN(value) ||
		math.IsInf(value, 0) ||
		value == -999 ||
		value < minimum ||
		value > maximum {
		return nil
	}
	value *= scale
	return &value
}

func text(o object, field string, maximum int) *string {
	var value string
	raw := o[field]
	if len(raw) == 0 || string(raw) == jsonNull || !utf8.Valid(raw) || json.Unmarshal(raw, &value) != nil ||
		!utf8.ValidString(value) ||
		utf8.RuneCountInString(value) > maximum {
		return nil
	}
	return &value
}

//nolint:mnd // Provider ranges and text limits are contract data.
func weather(o object) ForecastWeather {
	return ForecastWeather{
		Summary:           text(o, "summary", 512),
		Icon:              text(o, "icon", 64),
		PrecipitationType: text(o, "precipType", 32),
		HumidityPercent:   number(o, "humidity", 0, 1, 100),
		PressureHPa:       number(o, "pressure", 0, 2000, 1),
		WindSpeedMPS:      number(o, "windSpeed", 0, 200, 1),
		WindGustMPS:       number(o, "windGust", 0, 200, 1),
		WindDirectionDegrees: number(
			o,
			"windBearing",
			0,
			360,
			1,
		),
		PrecipitationRateMMH: number(o, "precipIntensity", 0, 10000, 1),
		PrecipitationProbability: number(
			o,
			"precipProbability",
			0,
			1,
			100,
		),
		CloudCoverPercent: number(o, "cloudCover", 0, 1, 100),
		UVIndex:           number(o, "uvIndex", 0, 100, 1),
	}
}

func weatherHasNumber(w ForecastWeather) bool {
	return w.HumidityPercent != nil || w.PressureHPa != nil || w.WindSpeedMPS != nil || w.WindGustMPS != nil ||
		w.WindDirectionDegrees != nil ||
		w.PrecipitationRateMMH != nil ||
		w.PrecipitationProbability != nil ||
		w.CloudCoverPercent != nil ||
		w.UVIndex != nil
}

func decodeCurrent(body []byte, received time.Time, interval time.Duration) (currentResult, error) {
	root, err := providerDocument(body)
	if err != nil {
		return currentResult{}, err
	}
	o, err := asObject(root["currently"])
	if err != nil {
		return currentResult{}, err
	}
	t, err := sourceTime(o["time"])
	if err != nil || t.After(received.Add(time.Minute)) || t.Before(received.Add(-2*interval)) {
		return currentResult{}, errInvalidResponse
	}
	result := currentResult{SourceTime: t, ReceivedAt: received.UTC(), Values: make(map[string]float64)}
	for _, m := range measurements() {
		value := m.decode(o)
		if value != nil {
			result.Values[m.key] = *value
		}
	}
	if len(result.Values) == 0 {
		return currentResult{}, errInvalidResponse
	}
	return result, nil
}

//nolint:mnd // Timezone length is contract data.
func forecastBlock(body []byte, block string, count int) (string, *time.Location, []json.RawMessage, error) {
	root, err := providerDocument(body)
	if err != nil {
		return "", nil, nil, err
	}
	zone := text(root, "timezone", 128)
	if zone == nil || *zone == "" {
		return "", nil, nil, errInvalidResponse
	}
	location, err := time.LoadLocation(*zone)
	if err != nil || *zone == "Local" {
		return "", nil, nil, errInvalidResponse
	}
	o, err := asObject(root[block])
	if err != nil {
		return "", nil, nil, err
	}
	var periods []json.RawMessage
	if json.Unmarshal(o["data"], &periods) != nil || len(periods) == 0 {
		return "", nil, nil, errInvalidResponse
	}
	if len(periods) > count {
		periods = periods[:count]
	}
	return *zone, location, periods, nil
}

//nolint:mnd // Temperature bounds are contract data.
func decodeHourly(body []byte, name string, received time.Time, count int) (HourlyForecast, error) {
	zone, _, raw, err := forecastBlock(body, "hourly", count)
	if err != nil {
		return HourlyForecast{}, err
	}
	result := HourlyForecast{
		ForecastMetadata: ForecastMetadata{name, received.UTC(), zone},
		Periods:          make([]HourlyPeriod, 0, len(raw)),
	}
	var previous time.Time
	for index, item := range raw {
		o, e := asObject(item)
		if e != nil {
			return HourlyForecast{}, e
		}
		t, e := sourceTime(o["time"])
		if e != nil || (index > 0 && !t.After(previous)) {
			return HourlyForecast{}, errInvalidResponse
		}
		p := HourlyPeriod{
			ForecastWeather:      weather(o),
			StartsAt:             t,
			TemperatureC:         number(o, "temperature", -273.15, 1000, 1),
			ApparentTemperatureC: number(o, "apparentTemperature", -273.15, 1000, 1),
		}
		if !weatherHasNumber(p.ForecastWeather) && p.TemperatureC == nil && p.ApparentTemperatureC == nil {
			return HourlyForecast{}, errInvalidResponse
		}
		result.Periods = append(result.Periods, p)
		previous = t
	}
	return result, nil
}

//nolint:mnd // Temperature bounds are contract data.
func decodeDaily(body []byte, name string, received time.Time, count int) (DailyForecast, error) {
	zone, location, raw, err := forecastBlock(body, "daily", count)
	if err != nil {
		return DailyForecast{}, err
	}
	result := DailyForecast{
		ForecastMetadata: ForecastMetadata{name, received.UTC(), zone},
		Periods:          make([]DailyPeriod, 0, len(raw)),
	}
	var previous time.Time
	for index, item := range raw {
		o, e := asObject(item)
		if e != nil {
			return DailyForecast{}, e
		}
		t, e := sourceTime(o["time"])
		if e != nil || (index > 0 && !t.After(previous)) {
			return DailyForecast{}, errInvalidResponse
		}
		p := DailyPeriod{
			ForecastWeather: weather(o),
			StartsAt:        t,
			Date:            t.In(location).Format("2006-01-02"),
			TemperatureMinC: number(o, "temperatureMin", -273.15, 1000, 1),
			TemperatureMaxC: number(o, "temperatureMax", -273.15, 1000, 1),
		}
		if !weatherHasNumber(p.ForecastWeather) && p.TemperatureMinC == nil && p.TemperatureMaxC == nil {
			return DailyForecast{}, errInvalidResponse
		}
		result.Periods = append(result.Periods, p)
		previous = t
	}
	return result, nil
}
