package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// A single statement reads both the Entity and all aggregate rows under one SQLite snapshot.
// Canonical integer checks precede every numeric aggregate; corrupt values are counted
// separately so SQLite's permissive CAST can never silently turn them into samples.
const temperatureSeriesSQL = `
WITH samples AS (
 SELECT (unixepoch(substr(observed_at, 1, 19) || 'Z') - ?4) / ?5 AS bucket,
        state_value_json AS value,
        CASE WHEN state_value_json = CAST(CAST(state_value_json AS INTEGER) AS TEXT)
             AND CAST(state_value_json AS INTEGER) BETWEEN -273150 AND 1000000
             THEN CAST(state_value_json AS INTEGER) END AS number
 FROM observations
 WHERE entity_id = ?1 AND observed_at >= ?2 AND observed_at < ?3
   AND disposition IN ('applied', 'unchanged')
   AND EXISTS (SELECT 1 FROM entities WHERE id = ?1 AND type_id = 'hearth.temperature/v1')
), grouped AS (
 SELECT bucket, count(*) AS count, count(*) - count(number) AS invalid,
        min(number) AS minimum, max(number) AS maximum, avg(number) AS mean
 FROM samples GROUP BY bucket
)
SELECT e.type_id, g.bucket, g.count, g.invalid, g.minimum, g.maximum, g.mean
FROM entities e LEFT JOIN grouped g ON true WHERE e.id = ?1 ORDER BY g.bucket`

func (repository *DeviceRepository) GetTemperatureSeries(
	ctx context.Context,
	params devices.TemperatureSeriesParams,
) (devices.TemperatureSeriesSnapshot, error) {
	if err := devices.ValidateTemperatureSeriesParams(params); err != nil {
		return devices.TemperatureSeriesSnapshot{}, err
	}
	params.From, params.To = params.From.UTC(), params.To.UTC()
	width := int64(params.To.Sub(params.From) / time.Second / devices.TemperatureSeriesBucketCount)
	rows, err := repository.database.QueryContext(
		ctx,
		temperatureSeriesSQL,
		string(params.EntityID),
		formatTime(params.From),
		formatTime(params.To),
		params.From.Unix(),
		width,
	)
	if err != nil {
		return devices.TemperatureSeriesSnapshot{}, fmt.Errorf("query temperature series: %w", err)
	}
	defer rows.Close()
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
	found := false
	for rows.Next() {
		found = true
		var kind string
		var row temperatureSeriesRow
		if scanErr := rows.Scan(
			&kind,
			&row.bucket,
			&row.count,
			&row.invalid,
			&row.minimum,
			&row.maximum,
			&row.mean,
		); scanErr != nil {
			return devices.TemperatureSeriesSnapshot{}, fmt.Errorf("scan temperature series: %w", scanErr)
		}
		result.TypeID = devices.EntityTypeID(kind)
		if !row.bucket.Valid {
			if row.count.Valid {
				return devices.TemperatureSeriesSnapshot{}, fmt.Errorf("invalid retained temperature timestamp")
			}
			continue
		}
		if mapErr := temperatureSeriesBucketFromRow(result.Series.Buckets, row); mapErr != nil {
			return devices.TemperatureSeriesSnapshot{}, mapErr
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return devices.TemperatureSeriesSnapshot{}, fmt.Errorf("read temperature series: %w", rowsErr)
	}
	if !found {
		return devices.TemperatureSeriesSnapshot{}, devices.ErrEntityNotFound
	}
	return result, nil
}
