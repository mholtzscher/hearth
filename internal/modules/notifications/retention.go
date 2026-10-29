package notifications

import (
	"context"
	"errors"
	"time"
)

const historyPruneBatchSize = 500

// PruneHistory implements the app's shared retention worker contract. Scheduling
// belongs to the app; notification completion time alone determines eligibility.
func (s *Service) PruneHistory(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		return errors.New("notification history prune time is required")
	}
	cutoff := now.UTC().Add(-Retention)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		deleted, err := s.repo.DeleteHistoryBefore(ctx, cutoff, historyPruneBatchSize)
		if err != nil {
			return err
		}
		if deleted < historyPruneBatchSize {
			return nil
		}
	}
}
