package sqlite

import (
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/platform/numstats"
)

func historyRollupCellStart(at time.Time) int64 {
	return at.Truncate(devices.HistoryRollupCellDuration).Unix()
}

// observationRollupParams encodes the complete cell, after exact accumulation.
func observationRollupParams(
	entity devices.EntityID,
	field string,
	start int64,
	stats *numstats.Stats,
) dbsqlc.UpsertObservationRollupParams {
	params := dbsqlc.UpsertObservationRollupParams{
		EntityID: string(entity), FieldPath: field, CellStart: start,
		SampleCount: stats.Count, NullCount: stats.Nulls, InvalidCount: stats.Invalid, SumValue: stats.Sum.RatString(),
	}
	if stats.Count > 0 {
		params.MinValue, params.MaxValue = stats.Min.Value(), stats.Max.Value()
	}
	return params
}

// historyRollupFromRow validates the sum's rational TEXT and the numeric
// storage classes SQL leaves untyped. Counts and presence are SQL-constrained.
func historyRollupFromRow(row dbsqlc.ObservationRollup) (*numstats.Stats, error) {
	stats := &numstats.Stats{Count: row.SampleCount, Nulls: row.NullCount, Invalid: row.InvalidCount}
	if _, ok := stats.Sum.SetString(row.SumValue); !ok {
		return nil, fmt.Errorf("stored rollup sum is not rational")
	}
	if stats.Count == 0 {
		return stats, nil
	}
	var err error
	if stats.Min, err = historyNumberFromColumn(row.MinValue); err != nil {
		return nil, err
	}
	if stats.Max, err = historyNumberFromColumn(row.MaxValue); err != nil {
		return nil, err
	}
	return stats, nil
}

func historyNumberFromColumn(value any) (numstats.Number, error) {
	switch number := value.(type) {
	case int64:
		return numstats.Integer(number), nil
	case float64:
		return numstats.Real(number), nil
	default:
		return numstats.Number{}, fmt.Errorf("stored rollup number has type %T", value)
	}
}
