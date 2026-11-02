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

// QueryHistory evaluates a bounded retained-observation query after the reader's snapshot closes.
//
//nolint:gocognit,nestif,funlen // The orchestration keeps the two bounded result paths visible together.
func (service *Service) QueryHistory(ctx context.Context, query HistoryQuery) (HistoryQueryResult, error) {
	plan, err := prepareHistoryQuery(query, service.dependencies.Now())
	if err != nil {
		return HistoryQueryResult{}, err
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return HistoryQueryResult{}, contextErr
	}
	request := HistoryReadRequest{
		EntityID:    query.EntityID,
		Range:       query.Range,
		Order:       HistoryAscending,
		IncludeSeed: plan.temporal,
	}
	if observations, ok := plan.query.Result.(HistoryObservations); ok {
		request.Order = observations.Order
		request.Cursor, err = decodeHistoryCursor(plan.query, observations)
		if err != nil {
			return HistoryQueryResult{}, err
		}
	}
	if service.stores.History == nil {
		return HistoryQueryResult{}, fmt.Errorf("history reader not configured")
	}
	window, err := service.stores.History.ReadHistoryWindow(ctx, request)
	if err != nil {
		return HistoryQueryResult{}, err
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return HistoryQueryResult{}, contextErr
	}
	if plan.temporal || isHistorySummary(plan.query.Result) {
		if window.HasMore {
			return HistoryQueryResult{}, historyError("query_limit", "result")
		}
	}
	evaluator := &historyEvaluator{ctx: ctx}
	result := HistoryQueryResult{EntityID: query.EntityID, Range: query.Range}
	if observations, ok := plan.query.Result.(HistoryObservations); ok {
		items := make([]HistoryObservation, 0, observations.Limit)
		scanned := -1
		valueBytes := 0
		for i, row := range window.Rows {
			value, selectErr := evaluator.selectValue(plan.selectProgram, row)
			if selectErr != nil {
				return HistoryQueryResult{}, selectErr
			}
			scanned = i
			if plan.filter != nil {
				match, predicateErr := evaluator.predicate(plan.filter, "result.where_expr", value)
				if predicateErr != nil {
					return HistoryQueryResult{}, predicateErr
				}
				if !match {
					continue
				}
			}
			valueBytes += len(value.JSON)
			if valueBytes > HistoryMaxResultBytes {
				return HistoryQueryResult{}, historyError("query_limit", "result")
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
		result.Data = data
		return result, nil
	}
	if _, ok := plan.query.Result.(HistoryTimeline); ok {
		timeline, timelineErr := makeHistoryTimeline(plan, evaluator, window)
		if timelineErr != nil {
			return HistoryQueryResult{}, timelineErr
		}
		result.Data = timeline
		return result, nil
	}
	result.Data, err = makeHistorySummary(plan, evaluator, window)
	if err != nil {
		return HistoryQueryResult{}, err
	}
	return result, nil
}

func isHistorySummary(result HistoryResultSpec) bool { _, ok := result.(HistorySummary); return ok }
