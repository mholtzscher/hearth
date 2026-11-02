package devices

import (
	"sort"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/exprvalue"
)

type historyPlan struct {
	query         HistoryQuery
	selectProgram *exprvalue.Program
	filter        *exprvalue.Program
	metrics       []historyMetricPlan
	temporal      bool
}

type historyMetricPlan struct {
	name      string
	metric    HistoryMetric
	predicate *exprvalue.Program
}

func historyError(code, field string) error { return &HistoryQueryError{Code: code, Field: field} }

//nolint:gocognit,gocyclo,cyclop,funlen // This boundary validates all closed variants before any read.
func prepareHistoryQuery(query HistoryQuery, now time.Time) (historyPlan, error) {
	plan := historyPlan{query: query}
	if _, err := ParseEntityID(string(query.EntityID)); err != nil {
		return plan, historyError("invalid_query", "entity_id")
	}
	if query.Range.From.Location() != time.UTC || query.Range.To.Location() != time.UTC ||
		!query.Range.From.Before(query.Range.To) ||
		query.Range.To.After(now) {
		return plan, historyError("invalid_query", "range")
	}
	selection := "sample.value"
	if query.SelectExpr != nil {
		selection = *query.SelectExpr
	}
	var err error
	plan.selectProgram, err = compileHistoryExpression(selection, "select_expr", "sample")
	if err != nil {
		return plan, err
	}
	switch result := query.Result.(type) {
	case *HistoryObservations:
		if result == nil {
			return plan, historyError("invalid_query", "result")
		}
		plan.query.Result = *result
	case *HistoryTimeline:
		if result == nil {
			return plan, historyError("invalid_query", "result")
		}
		plan.query.Result = *result
	case *HistorySummary:
		if result == nil {
			return plan, historyError("invalid_query", "result")
		}
		plan.query.Result = *result
	case HistoryObservations, HistoryTimeline, HistorySummary:
	default:
		return plan, historyError("invalid_query", "result")
	}
	switch result := plan.query.Result.(type) {
	case HistoryObservations:
		if result.Order == "" {
			result.Order = HistoryAscending
		}
		if result.Limit == 0 {
			result.Limit = 50
		}
		if result.Order != HistoryAscending && result.Order != HistoryDescending {
			return plan, historyError("invalid_query", "result.order")
		}
		if result.Limit < 1 || result.Limit > 200 {
			return plan, historyError("invalid_query", "result.limit")
		}
		if result.WhereExpr != nil {
			plan.filter, err = compileHistoryExpression(*result.WhereExpr, "result.where_expr", "value")
			if err != nil {
				return plan, err
			}
		}
		plan.query.Result = result
	case HistoryTimeline:
		plan.temporal = true
	case HistorySummary:
		if len(result.Metrics) == 0 || len(result.Metrics) > HistoryMaxMetrics {
			return plan, historyError("invalid_query", "result.metrics")
		}
		if result.Bucket != nil {
			if result.Bucket.Duration <= 0 || result.Bucket.Alignment != "range_start" {
				return plan, historyError("invalid_query", "result.bucket")
			}
			count := 0
			for start := query.Range.From; start.Before(query.Range.To); {
				count++
				if count > HistoryMaxBuckets {
					return plan, historyError("query_limit", "result.bucket")
				}
				next := start.Add(result.Bucket.Duration)
				if !next.After(start) {
					return plan, historyError("invalid_query", "result.bucket")
				}
				start = next
			}
		}
		names := make([]string, 0, len(result.Metrics))
		for name := range result.Metrics {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if name == "" {
				return plan, historyError("invalid_query", "result.metrics")
			}
			metric := result.Metrics[name]
			switch value := metric.(type) {
			case *HistoryMinimum:
				if value == nil {
					return plan, historyError("invalid_query", "result.metrics."+name)
				}
				metric = *value
			case *HistoryMaximum:
				if value == nil {
					return plan, historyError("invalid_query", "result.metrics."+name)
				}
				metric = *value
			case *HistorySampleMean:
				if value == nil {
					return plan, historyError("invalid_query", "result.metrics."+name)
				}
				metric = *value
			case *HistoryDurationWhere:
				if value == nil {
					return plan, historyError("invalid_query", "result.metrics."+name)
				}
				metric = *value
			case *HistoryTransitionsInto:
				if value == nil {
					return plan, historyError("invalid_query", "result.metrics."+name)
				}
				metric = *value
			case HistoryMinimum, HistoryMaximum, HistorySampleMean, HistoryDurationWhere, HistoryTransitionsInto:
			default:
				return plan, historyError("invalid_query", "result.metrics."+name)
			}
			item := historyMetricPlan{name: name, metric: metric}
			field := "result.metrics." + name + ".where_expr"
			switch value := metric.(type) {
			case HistoryDurationWhere:
				if value.Unit != "seconds" {
					return plan, historyError("invalid_query", "result.metrics."+name+".unit")
				}
				plan.temporal = true
				item.predicate, err = compileHistoryExpression(value.WhereExpr, field, "value")
			case HistoryTransitionsInto:
				plan.temporal = true
				item.predicate, err = compileHistoryExpression(value.WhereExpr, field, "value")
			case HistoryMinimum, HistoryMaximum, HistorySampleMean:
			}
			if err != nil {
				return plan, err
			}
			plan.metrics = append(plan.metrics, item)
		}
	}
	return plan, nil
}
