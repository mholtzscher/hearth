package devices

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// MinimumObservationRetention is the shortest non-current observation retention
// the devices module will prune with. It matches the application's
// `observation_retention` configuration floor and keeps the Core window above
// the seven-day JetStream retention, preserving redelivery deduplication. A shorter or
// unconfigured retention makes PruneHistory fail instead of deleting too much.
const MinimumObservationRetention = 8 * 24 * time.Hour

// PruneHistory performs one devices history retention pass at the supplied UTC
// sweep time. It deletes non-current observations older than the
// Dependencies.ObservationRetention window and retained Entity Events older
// than the fixed EntityEventHistoryRetention window, always attempting both
// deletions and joining their failures so one failing deletion never suppresses
// the other. Observations that anchor current Entity State are retained by the
// observation deletion itself.
//
// The sweep time must be non-zero and the injected retention at least
// MinimumObservationRetention; otherwise nothing is deleted and the
// misconfiguration is reported. The caller owns the schedule and the failure
// logging.
func (service *Service) PruneHistory(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		return errors.New("devices history prune time is required")
	}
	retention := service.dependencies.ObservationRetention
	if retention < MinimumObservationRetention {
		return fmt.Errorf(
			"observation retention %s is below the minimum %s",
			retention, MinimumObservationRetention,
		)
	}
	observationErr := service.DeleteExpiredObservations(ctx, now, retention)
	entityEventErr := service.DeleteExpiredEntityEvents(ctx, now)
	return errors.Join(observationErr, entityEventErr)
}
