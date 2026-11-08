package sqlite

import (
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

// historyRollupCellStart floors an Observation time to its rollup cell, in
// Unix seconds.
func historyRollupCellStart(observedAt time.Time) int64 {
	cell := int64(devices.HistoryRollupCellDuration / time.Second)
	seconds := observedAt.Unix()
	return seconds - ((seconds%cell)+cell)%cell
}

// observationRollupParams encodes one sample as a single-sample cell delta.
func observationRollupParams(
	entityID devices.EntityID,
	field string,
	cellStart int64,
	sample devices.HistoryRollupSample,
) dbsqlc.UpsertObservationRollupParams {
	params := dbsqlc.UpsertObservationRollupParams{
		EntityID: string(entityID), FieldPath: field, CellStart: cellStart,
	}
	switch sample.Kind {
	case devices.HistoryRollupNumeric:
		params.SampleCount = 1
		params.Value = historyNumberColumn(sample.Number)
	case devices.HistoryRollupNull:
		params.NullCount = 1
	case devices.HistoryRollupInvalid:
		params.InvalidCount = 1
	}
	return params
}

// historyNumberColumn binds the number with its SQLite storage class: INTEGER
// for integers and REAL for reals, which the untyped rollup columns retain.
func historyNumberColumn(number devices.HistoryNumber) any {
	if number.IsReal {
		return number.Float
	}
	return number.Int
}

// historyRollupBucketFromRow validates what SQL cannot: the untyped sum and
// extrema must be INTEGER or REAL whenever the bucket holds numeric samples.
func historyRollupBucketFromRow(row dbsqlc.HistoryRollupBucketsRow) (devices.HistoryRollupBucket, error) {
	bucket := devices.HistoryRollupBucket{
		Index: int(row.BucketIndex), Count: row.SampleCount, Nulls: row.NullCount, Invalid: row.InvalidCount,
	}
	if bucket.Count == 0 {
		return bucket, nil
	}
	var err error
	if bucket.Sum, err = historyNumberFromColumn(row.SumValue); err != nil {
		return devices.HistoryRollupBucket{}, fmt.Errorf("decode rollup sum: %w", err)
	}
	if bucket.Min, err = historyNumberFromColumn(row.MinValue); err != nil {
		return devices.HistoryRollupBucket{}, fmt.Errorf("decode rollup minimum: %w", err)
	}
	if bucket.Max, err = historyNumberFromColumn(row.MaxValue); err != nil {
		return devices.HistoryRollupBucket{}, fmt.Errorf("decode rollup maximum: %w", err)
	}
	return bucket, nil
}

func historyNumberFromColumn(value any) (devices.HistoryNumber, error) {
	switch number := value.(type) {
	case int64:
		return devices.HistoryIntNumber(number), nil
	case float64:
		return devices.HistoryRealNumber(number), nil
	default:
		return devices.HistoryNumber{}, fmt.Errorf("stored rollup number has type %T", value)
	}
}
