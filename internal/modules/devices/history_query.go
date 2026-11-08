package devices

import (
	"context"
	"errors"
)

// QueryHistory summarizes one numeric field from rollup cells. The final
// bucket counts the whole cell that contains To, so it can include up to one
// cell of reports after To.
func (service *Service) QueryHistory(ctx context.Context, query HistoryQuery) (HistoryQueryResult, error) {
	plan, err := prepareHistoryQuery(query, service.dependencies.Now())
	if err != nil {
		return HistoryQueryResult{}, err
	}
	if service.stores.HistoryRollups == nil {
		return HistoryQueryResult{}, errors.New("history rollup reader not configured")
	}
	buckets, err := service.stores.HistoryRollups.ReadHistoryRollupBuckets(ctx, HistoryRollupRequest{
		EntityID: query.EntityID,
		Field:    query.Field,
		From:     query.Range.From,
		End:      plan.end,
		Bucket:   plan.width(),
	})
	if err != nil {
		return HistoryQueryResult{}, err
	}
	data, err := renderHistoryRollups(plan, buckets)
	if err != nil {
		return HistoryQueryResult{}, err
	}
	return HistoryQueryResult{EntityID: query.EntityID, Range: query.Range, Data: data}, nil
}
