package devices

import (
	"context"
	"errors"
	"time"

	"github.com/mholtzscher/hearth/entitytypes/temperaturev1"
)

var ErrInvalidTemperatureSeries = errors.New("invalid temperature series request")

const TemperatureSeriesBucketCount = 288

type TemperatureSeriesParams struct {
	EntityID EntityID
	From     time.Time
	To       time.Time
}

type TemperatureBucket struct {
	Start            time.Time
	End              time.Time
	Count            int64
	MinMilliCelsius  *int64
	MaxMilliCelsius  *int64
	MeanMilliCelsius *float64
}

type TemperatureSeries struct {
	EntityID EntityID
	From     time.Time
	To       time.Time
	Buckets  []TemperatureBucket
}

// TemperatureSeriesSnapshot includes the Entity type from the same snapshot as the observations.
type TemperatureSeriesSnapshot struct {
	TypeID EntityTypeID
	Series TemperatureSeries
}

func ValidateTemperatureSeriesParams(params TemperatureSeriesParams) error {
	if _, err := ParseEntityID(string(params.EntityID)); err != nil {
		return ErrInvalidTemperatureSeries
	}
	_, fromOffset := params.From.Zone()
	_, toOffset := params.To.Zone()
	if params.From.IsZero() || params.To.IsZero() || fromOffset != 0 || toOffset != 0 ||
		params.From.Nanosecond() != 0 ||
		params.To.Nanosecond() != 0 {
		return ErrInvalidTemperatureSeries
	}
	duration := params.To.Sub(params.From)
	if duration != 6*time.Hour && duration != 24*time.Hour {
		return ErrInvalidTemperatureSeries
	}
	return nil
}

func (service *Service) GetTemperatureSeries(
	ctx context.Context,
	params TemperatureSeriesParams,
) (TemperatureSeries, error) {
	if err := ValidateTemperatureSeriesParams(params); err != nil {
		return TemperatureSeries{}, err
	}
	params.From, params.To = params.From.UTC(), params.To.UTC()
	snapshot, err := service.stores.Reads.GetTemperatureSeries(ctx, params)
	if err != nil {
		return TemperatureSeries{}, err
	}
	if snapshot.TypeID != EntityTypeID(temperaturev1.TypeID) {
		return TemperatureSeries{}, ErrInvalidTemperatureSeries
	}
	return snapshot.Series, nil
}
