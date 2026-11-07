package devices

import (
	"context"
	"fmt"

	"github.com/mholtzscher/hearth/internal/platform/exprvalue"
)

type historySelected struct {
	row   EntityStateHistoryEntry
	value exprvalue.Value
}

// historyFold reduces one ascending traversal of retained rows into a timeline
// or summary, holding only its own bounded state between reads.
type historyFold interface {
	add(row EntityStateHistoryEntry) error
	finish() (HistoryResult, error)
}

// QueryHistory evaluates a bounded retained-observation query. Expressions run
// only after each reader snapshot closes.
func (service *Service) QueryHistory(ctx context.Context, query HistoryQuery) (HistoryQueryResult, error) {
	plan, err := prepareHistoryQuery(query, service.dependencies.Now())
	if err != nil {
		return HistoryQueryResult{}, err
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return HistoryQueryResult{}, contextErr
	}
	if service.stores.History == nil {
		return HistoryQueryResult{}, fmt.Errorf("history reader not configured")
	}
	evaluator := &historyEvaluator{ctx: ctx}
	result := HistoryQueryResult{EntityID: query.EntityID, Range: query.Range}
	if observations, ok := plan.query.Result.(HistoryObservations); ok {
		result.Data, err = service.queryHistoryObservations(ctx, plan, evaluator, observations)
	} else {
		result.Data, err = service.foldHistory(ctx, plan, evaluator)
	}
	if err != nil {
		return HistoryQueryResult{}, err
	}
	return result, nil
}

// foldHistory reads the range in ascending chunks, each from its own snapshot
// pinned to the first read's high-water mark. The evaluation budget bounds the
// whole traversal because every row costs at least its selection.
func (service *Service) foldHistory(
	ctx context.Context,
	plan historyPlan,
	evaluator *historyEvaluator,
) (HistoryResult, error) {
	request := HistoryReadRequest{
		EntityID:    plan.query.EntityID,
		Range:       plan.query.Range,
		Order:       HistoryAscending,
		IncludeSeed: plan.temporal,
	}
	var fold historyFold
	for {
		window, err := service.stores.History.ReadHistoryWindow(ctx, request)
		if err != nil {
			return nil, err
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		if fold == nil {
			if fold, err = newHistoryFold(plan, evaluator, window.Seed); err != nil {
				return nil, err
			}
		}
		for _, row := range window.Rows {
			if err = fold.add(row); err != nil {
				return nil, err
			}
		}
		if !window.HasMore {
			return fold.finish()
		}
		if len(window.Rows) == 0 {
			// The seed alone filled the read budget, so no cursor can advance.
			return nil, historyError("query_limit", "range")
		}
		last := window.Rows[len(window.Rows)-1]
		request.IncludeSeed = false
		request.Cursor = &HistoryReadCursor{
			ObservedAt:   last.ObservedAt,
			ReceiveOrder: last.ReceiveOrder,
			HighWater:    window.HighWater,
		}
	}
}

func newHistoryFold(plan historyPlan, evaluator *historyEvaluator, seed *EntityStateHistoryEntry) (historyFold, error) {
	if _, ok := plan.query.Result.(HistoryTimeline); ok {
		return newHistoryTimeline(plan, evaluator, seed)
	}
	return newHistorySummary(plan, evaluator, seed)
}

//nolint:gocognit // One page keeps selection, filtering, its budget, and its cursor together.
func (service *Service) queryHistoryObservations(
	ctx context.Context,
	plan historyPlan,
	evaluator *historyEvaluator,
	observations HistoryObservations,
) (HistoryResult, error) {
	request := HistoryReadRequest{
		EntityID: plan.query.EntityID,
		Range:    plan.query.Range,
		Order:    observations.Order,
	}
	if plan.filter == nil {
		request.SourceRowLimit = observations.Limit + 1
	}
	var err error
	request.Cursor, err = decodeHistoryCursor(plan.query, observations)
	if err != nil {
		return nil, err
	}
	window, err := service.stores.History.ReadHistoryWindow(ctx, request)
	if err != nil {
		return nil, err
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	items := make([]HistoryObservation, 0, observations.Limit)
	scanned := -1
	valueBytes := 0
	for i, row := range window.Rows {
		value, selectErr := evaluator.selectValue(plan.selectProgram, row)
		if selectErr != nil {
			return nil, selectErr
		}
		scanned = i
		if plan.filter != nil {
			match, predicateErr := evaluator.predicate(plan.filter, "result.where_expr", value)
			if predicateErr != nil {
				return nil, predicateErr
			}
			if !match {
				continue
			}
		}
		valueBytes += len(value.JSON)
		if valueBytes > HistoryMaxResultBytes {
			return nil, historyError("query_limit", "result")
		}
		items = append(
			items,
			HistoryObservation{
				ObservationID:     row.ObservationID,
				ObservedAt:        row.ObservedAt.UTC(),
				AdapterReceivedAt: row.AdapterReceivedAt.UTC(),
				SourceUpdatedAt:   row.SourceUpdatedAt,
				Disposition:       row.Disposition,
				Value:             value.JSON,
			},
		)
		if len(items) == observations.Limit {
			break
		}
	}
	data := HistoryObservationsResult{Order: observations.Order, Items: items}
	if scanned >= 0 && (scanned+1 < len(window.Rows) || window.HasMore) {
		data.NextCursor = encodeHistoryCursor(plan.query, observations, window.Rows[scanned], window.HighWater)
	}
	return data, nil
}
