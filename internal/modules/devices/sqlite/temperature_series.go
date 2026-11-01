package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func (repository *DeviceRepository) GetTemperatureSeries(
	ctx context.Context,
	params devices.TemperatureSeriesParams,
) (devices.TemperatureSeriesSnapshot, error) {
	if err := devices.ValidateTemperatureSeriesParams(params); err != nil {
		return devices.TemperatureSeriesSnapshot{}, err
	}
	params.From, params.To = params.From.UTC(), params.To.UTC()
	width := int64(params.To.Sub(params.From) / time.Second / devices.TemperatureSeriesBucketCount)
	rows, err := repository.queries.GetTemperatureSeries(ctx, dbsqlc.GetTemperatureSeriesParams{
		EntityID: string(params.EntityID), FromTime: formatTime(params.From), ToTime: formatTime(params.To),
		StartUnix: params.From.Unix(), BucketSeconds: width,
	})
	if err != nil {
		return devices.TemperatureSeriesSnapshot{}, fmt.Errorf("query temperature series: %w", err)
	}
	result := devices.TemperatureSeriesSnapshot{
		Series: devices.TemperatureSeries{
			EntityID: params.EntityID,
			From:     params.From,
			To:       params.To,
			Buckets:  make([]devices.TemperatureBucket, devices.TemperatureSeriesBucketCount),
		},
	}
	for i := range result.Series.Buckets {
		start := params.From.Add(time.Duration(int64(i)*width) * time.Second)
		result.Series.Buckets[i] = devices.TemperatureBucket{
			Start: start,
			End:   start.Add(time.Duration(width) * time.Second),
		}
	}
	for _, row := range rows {
		result.TypeID = devices.EntityTypeID(row.TypeID)
		if !row.Bucket.Valid {
			if row.Count.Valid {
				return devices.TemperatureSeriesSnapshot{}, fmt.Errorf("invalid retained temperature timestamp")
			}
			continue
		}
		if mapErr := temperatureSeriesBucketFromRow(result.Series.Buckets, row); mapErr != nil {
			return devices.TemperatureSeriesSnapshot{}, mapErr
		}
	}
	if len(rows) == 0 {
		return devices.TemperatureSeriesSnapshot{}, devices.ErrEntityNotFound
	}
	return result, nil
}
