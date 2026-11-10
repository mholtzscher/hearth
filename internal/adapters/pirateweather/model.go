// Package pirateweather implements the Pirate Weather provider boundary.
package pirateweather

import (
	"errors"
	"math"
	"strings"
	"time"
	_ "time/tzdata" // Forecast dates must work in minimal companion images.
	"unicode/utf8"
)

type Config struct {
	LocationName string
	Latitude     float64
	Longitude    float64
	PollInterval time.Duration
}

// Validate checks the runtime configuration independently of application decoding.
func (c Config) Validate() error {
	if !utf8.ValidString(c.LocationName) || strings.TrimSpace(c.LocationName) != c.LocationName ||
		utf8.RuneCountInString(c.LocationName) < 1 ||
		utf8.RuneCountInString(c.LocationName) > 128 {
		return errors.New("invalid location name")
	}
	if math.IsNaN(c.Latitude) || math.IsInf(c.Latitude, 0) || c.Latitude < -90 || c.Latitude > 90 ||
		math.IsNaN(c.Longitude) ||
		math.IsInf(c.Longitude, 0) ||
		c.Longitude < -180 ||
		c.Longitude > 180 {
		return errors.New("invalid coordinates")
	}
	if c.PollInterval < 15*time.Minute || c.PollInterval > 24*time.Hour {
		return errors.New("invalid poll interval")
	}
	return nil
}

// currentResult contains canonical values keyed by the catalog's Entity keys.
// Temperatures are rounded integer milli-Celsius represented as float64.
type currentResult struct {
	SourceTime time.Time
	ReceivedAt time.Time
	Values     map[string]float64
}

// pollResult contains one response; invalid or missing forecast blocks are nil.
type pollResult struct {
	Current currentResult
	Hourly  *HourlyForecast
	Daily   *DailyForecast
}

type ForecastMetadata struct {
	LocationName string    `json:"location_name"`
	RetrievedAt  time.Time `json:"retrieved_at"`
	Timezone     string    `json:"timezone"`
}

type ForecastWeather struct {
	Summary                  *string  `json:"summary,omitempty"`
	Icon                     *string  `json:"icon,omitempty"`
	PrecipitationType        *string  `json:"precipitation_type,omitempty"`
	HumidityPercent          *float64 `json:"humidity_percent,omitempty"`
	PressureHPa              *float64 `json:"pressure_hpa,omitempty"`
	WindSpeedMPS             *float64 `json:"wind_speed_mps,omitempty"`
	WindGustMPS              *float64 `json:"wind_gust_mps,omitempty"`
	WindDirectionDegrees     *float64 `json:"wind_direction_degrees,omitempty"`
	PrecipitationRateMMH     *float64 `json:"precipitation_rate_mmh,omitempty"`
	PrecipitationProbability *float64 `json:"precipitation_probability_percent,omitempty"`
	CloudCoverPercent        *float64 `json:"cloud_cover_percent,omitempty"`
	UVIndex                  *float64 `json:"uv_index,omitempty"`
}

type HourlyPeriod struct {
	ForecastWeather

	StartsAt             time.Time `json:"starts_at"`
	TemperatureC         *float64  `json:"temperature_c,omitempty"`
	ApparentTemperatureC *float64  `json:"apparent_temperature_c,omitempty"`
}

type DailyPeriod struct {
	ForecastWeather

	StartsAt        time.Time `json:"starts_at"`
	Date            string    `json:"date"`
	TemperatureMinC *float64  `json:"temperature_min_c,omitempty"`
	TemperatureMaxC *float64  `json:"temperature_max_c,omitempty"`
}

type HourlyForecast struct {
	ForecastMetadata

	Periods []HourlyPeriod `json:"periods"`
}

type DailyForecast struct {
	ForecastMetadata

	Periods []DailyPeriod `json:"periods"`
}
