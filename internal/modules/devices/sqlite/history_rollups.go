package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

// upsertObservationRollups adds an accepted (applied or unchanged) report to
// the cell of every catalogued numeric field of its Entity's type; rejected
// reports change no cell. It runs inside the Observation's projection
// transaction, so cells and evidence commit together.
func upsertObservationRollups(
	ctx context.Context,
	queries *dbsqlc.Queries,
	entity devices.Entity,
	disposition devices.ObservationDisposition,
	value devices.Value,
	observedAt time.Time,
) error {
	if disposition != devices.DispositionApplied && disposition != devices.DispositionUnchanged {
		return nil
	}
	cellStart := historyRollupCellStart(observedAt)
	for _, field := range devices.HistoryRollupFields(entity.TypeID) {
		sample, err := devices.ClassifyHistoryRollupSample(value, field)
		if err != nil {
			return fmt.Errorf("classify history rollup sample: %w", err)
		}
		if err = queries.UpsertObservationRollup(
			ctx, observationRollupParams(entity.ID, field, cellStart, sample),
		); err != nil {
			return fmt.Errorf("upsert observation rollup: %w", err)
		}
	}
	return nil
}
