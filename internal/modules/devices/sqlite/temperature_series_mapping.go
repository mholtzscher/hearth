package sqlite

import (
	"database/sql"
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

// SQL groups only accepted observations; invalid counts detect noncanonical or
// out-of-schema retained values before the aggregate is made available.
func temperatureSeriesBucketFromRow(buckets []devices.TemperatureBucket, row dbsqlc.GetTemperatureSeriesRow) error {
	var minimum, maximum sql.NullInt64
	if err := minimum.Scan(row.Minimum); err != nil {
		return err
	}
	if err := maximum.Scan(row.Maximum); err != nil {
		return err
	}
	if !row.Bucket.Valid || row.Bucket.Int64 < 0 || row.Bucket.Int64 >= int64(len(buckets)) ||
		!row.Count.Valid || row.Count.Int64 < 1 || !row.Invalid.Valid || row.Invalid.Int64 != 0 ||
		!minimum.Valid || !maximum.Valid || !row.Mean.Valid {
		return errors.New("invalid retained temperature series evidence")
	}
	item := &buckets[row.Bucket.Int64]
	item.Count = row.Count.Int64
	item.MinMilliCelsius = &minimum.Int64
	item.MaxMilliCelsius = &maximum.Int64
	item.MeanMilliCelsius = &row.Mean.Float64
	return nil
}
