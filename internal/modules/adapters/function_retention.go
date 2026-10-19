package adapters

import (
	"context"
	"time"
)

const FunctionHistoryBatchSize = 500

func (service *FunctionService) PruneHistory(ctx context.Context, now time.Time) error {
	if !ValidFunctionTime(now) {
		return ErrInvalidFunctionInput
	}
	const retention = 30 * 24 * time.Hour
	for {
		count, err := service.repository.DeleteHistoryBefore(ctx, now.Add(-retention), FunctionHistoryBatchSize)
		if err != nil {
			return err
		}
		if count < FunctionHistoryBatchSize {
			return nil
		}
	}
}
