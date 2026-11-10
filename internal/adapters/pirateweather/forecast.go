package pirateweather

import (
	"time"

	dailytype "github.com/mholtzscher/hearth/entitytypes/dailyforecastv1"
	hourlytype "github.com/mholtzscher/hearth/entitytypes/hourlyforecastv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	"github.com/mholtzscher/hearth/sdk/adapter/dailyforecastv1"
	"github.com/mholtzscher/hearth/sdk/adapter/hourlyforecastv1"
)

func forecastCondition(icon *string) *string {
	if icon == nil {
		return nil
	}
	switch *icon {
	case "clear-day", "clear-night", "partly-cloudy-day", "partly-cloudy-night", "cloudy",
		"rain", "snow", "sleet", "thunderstorm", "wind", "fog":
		return icon
	default:
		return nil
	}
}

// Entity State requires nonempty summaries; Function results preserve provider text.
func forecastSummary(summary *string) *string {
	if summary == nil || *summary == "" {
		return nil
	}
	return summary
}

func hourlyObservation(id string, forecast HourlyForecast) (adapter.Observation, error) {
	state := hourlyforecastv1.State{Periods: make(hourlytype.StatePeriods, 0, len(forecast.Periods))}
	for _, p := range forecast.Periods {
		state.Periods = append(state.Periods, hourlytype.StatePeriodsItem{
			StartsAt: p.StartsAt.UTC().Format(time.RFC3339), Condition: forecastCondition(p.Icon),
			Summary: forecastSummary(p.Summary), Temperature: p.TemperatureC,
			ApparentTemperature: p.ApparentTemperatureC, Humidity: p.HumidityPercent,
			Pressure: p.PressureHPa, WindSpeed: p.WindSpeedMPS, WindGust: p.WindGustMPS,
			WindDirection: p.WindDirectionDegrees, PrecipitationRate: p.PrecipitationRateMMH,
			PrecipitationProbability: p.PrecipitationProbability, CloudCover: p.CloudCoverPercent,
			UvIndex: p.UVIndex,
		})
	}
	return hourlyforecastv1.NewObservation(hourlyforecastv1.ObservationInput{
		EntityID: id, Support: hourlyforecastv1.Support{}, State: state, AdapterReceivedAt: forecast.RetrievedAt,
	})
}

func dailyObservation(id string, forecast DailyForecast) (adapter.Observation, error) {
	state := dailyforecastv1.State{Periods: make(dailytype.StatePeriods, 0, len(forecast.Periods))}
	for _, p := range forecast.Periods {
		state.Periods = append(state.Periods, dailytype.StatePeriodsItem{
			StartsAt: p.StartsAt.UTC().Format(time.RFC3339), Date: p.Date, Condition: forecastCondition(p.Icon),
			Summary: forecastSummary(p.Summary), TemperatureMin: p.TemperatureMinC, TemperatureMax: p.TemperatureMaxC,
			Humidity: p.HumidityPercent, Pressure: p.PressureHPa, WindSpeed: p.WindSpeedMPS,
			WindGust: p.WindGustMPS, WindDirection: p.WindDirectionDegrees,
			PrecipitationRate: p.PrecipitationRateMMH, PrecipitationProbability: p.PrecipitationProbability,
			CloudCover: p.CloudCoverPercent, UvIndex: p.UVIndex,
		})
	}
	return dailyforecastv1.NewObservation(dailyforecastv1.ObservationInput{
		EntityID: id, Support: dailyforecastv1.Support{}, State: state, AdapterReceivedAt: forecast.RetrievedAt,
	})
}
