package sqlite

import (
	"database/sql"
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type temperatureSeriesRow struct {
	bucket, count, invalid, minimum, maximum sql.NullInt64
	mean                                     sql.NullFloat64
}

// SQL groups only accepted observations; invalid counts detect noncanonical or
// out-of-schema retained values before the aggregate is made available.
func temperatureSeriesBucketFromRow(buckets []devices.TemperatureBucket, row temperatureSeriesRow) error {
	if !row.bucket.Valid || row.bucket.Int64 < 0 || row.bucket.Int64 >= int64(len(buckets)) ||
		!row.count.Valid || row.count.Int64 < 1 || !row.invalid.Valid || row.invalid.Int64 != 0 ||
		!row.minimum.Valid || !row.maximum.Valid || !row.mean.Valid {
		return errors.New("invalid retained temperature series evidence")
	}
	item := &buckets[row.bucket.Int64]
	item.Count = row.count.Int64
	item.MinMilliCelsius = &row.minimum.Int64
	item.MaxMilliCelsius = &row.maximum.Int64
	item.MeanMilliCelsius = &row.mean.Float64
	return nil
}
