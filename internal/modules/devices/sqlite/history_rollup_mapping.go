package sqlite

import (
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
