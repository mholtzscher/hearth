package dbload

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// numericWrites measures accepted numeric reports, including rollup updates.
// Its input and checks are identical when backported to the pre-rollup binary.
func (b workloadBuilder) numericWrites(ctx context.Context) ([]Workload, error) {
	const numericAppendBase = 20000
	var entity string
	if err := b.db.QueryRowContext(ctx,
		`SELECT e.id FROM entities e JOIN adapter_entity_mappings m ON m.entity_id=e.id
		 WHERE e.type_id=? AND m.adapter_id=? ORDER BY e.id LIMIT 1`,
		string(devices.EntityTypeTemperatureV1), b.m.AdapterID,
	).Scan(&entity); err != nil {
		return nil, err
	}
	return []Workload{{
		Name: "devices.observation.append-numeric", Class: classWrite, Mutates: true,
		Queries: []string{
			"devices/GetObservation",
			"devices/GetAdapterRuntime",
			queryDevicesGetEntity,
			"devices/InsertObservation",
			"devices/GetActiveAdapterRuntime",
			"devices/UpsertEntityState",
			"devices/InsertObservationDeviceFact",
		},
		Run: func(ctx context.Context, sequence int) (int64, error) {
			at := b.m.Clock.Add(time.Duration(sequence) * time.Millisecond)
			result, err := b.d.ProjectObservation(ctx, devices.ProjectObservationParams{
				AdapterID: b.m.AdapterID, RuntimeID: adapters.RuntimeID(b.m.RuntimeID),
				Observation: devices.Observation{
					ID: devices.ObservationID(
						fmt.Sprintf("obs_019a4a00-0000-7000-9000-%012x", sequence+observationSequenceBase),
					),
					EntityID: devices.EntityID(entity),
					Value:    devices.Value(strconv.Itoa(numericAppendBase + sequence%700)),
					CorrelationID: devices.CorrelationID(
						"cor_019a4a00-0000-7000-8000-000000000001",
					),
					AdapterReceivedAt: at,
				}, ObservedAt: at, Now: func() time.Time { return at },
			})
			if err == nil &&
				(result.Disposition == devices.DispositionDuplicate || result.Disposition == devices.DispositionRejected ||
					result.State == nil || result.PendingFactID == nil) {
				err = errors.New("numeric append did not project State and enqueue fact")
			}
			return checkedWork(1, 1, err)
		},
	}}, nil
}
