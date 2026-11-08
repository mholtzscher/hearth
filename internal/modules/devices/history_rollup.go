package devices

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/numstats"
)

// HistoryRollupCellDuration is the fixed rollup resolution. Cells start at
// Unix-time multiples of it, in UTC, and are half-open.
const HistoryRollupCellDuration = time.Minute

// HistoryRollupBucket aggregates the cells whose start falls in one requested
// bucket. Sum, Min, and Max are meaningful only when Count > 0.
type HistoryRollupBucket struct {
	numstats.Stats

	Index int // zero-based bucket position from the request's From
}

type HistoryRollupRequest struct {
	EntityID EntityID
	Field    string        // validated against the Entity's type by the reader
	From     time.Time     // on the cell grid
	End      time.Time     // the cell boundary at or after the requested To
	Bucket   time.Duration // zero means overall; otherwise a positive whole-cell duration
}

// HistoryRollupReader returns the non-empty buckets in ascending Index order,
// from one read snapshot. It returns ErrEntityNotFound for an unknown Entity,
// unsupported_source for a stateless type, and invalid_query on select_field
// when Field is not one of HistoryRollupFields for the Entity's type.
type HistoryRollupReader interface {
	ReadHistoryRollupBuckets(context.Context, HistoryRollupRequest) ([]HistoryRollupBucket, error)
}

// HistoryRollupFields returns the catalogued numeric field paths for typeID.
func HistoryRollupFields(typeID EntityTypeID) []string {
	var paths []string
	for _, field := range builtinStateFields[typeID] {
		if _, numeric := field.Value.(NumericField); numeric {
			paths = append(paths, field.Path)
		}
	}
	return paths
}

// renderHistoryRollups applies the null policy to the read buckets and encodes
// each requested metric. A bucket without numeric samples renders null.
//
//nolint:gocognit // The policy checks, metric variants, and result budget share one pass.
func renderHistoryRollups(plan historyPlan, buckets []HistoryRollupBucket) (HistoryResult, error) {
	read := make([]*HistoryRollupBucket, len(plan.ranges))
	for i := range buckets {
		bucket := &buckets[i]
		if bucket.Index < 0 || bucket.Index >= len(read) || read[bucket.Index] != nil {
			return nil, fmt.Errorf("history rollup bucket index %d is outside the query", bucket.Index)
		}
		if bucket.Invalid > 0 || bucket.Nulls > 0 && plan.query.Nulls == HistoryNullReject {
			// Name the first metric, as every metric consumes the same values.
			return nil, historyError("value_type", "result.metrics."+plan.names[0])
		}
		read[bucket.Index] = bucket
	}
	rendered := make([]HistorySummaryBucket, len(plan.ranges))
	resultBytes := 0
	for i, bucketRange := range plan.ranges {
		values := make(HistoryMetricValues, len(plan.names))
		bucket := read[i]
		for _, name := range plan.names {
			value := json.RawMessage("null")
			if bucket != nil && bucket.Count > 0 && bucket.Nulls == 0 {
				var err error
				switch plan.query.Metrics[name].(type) {
				case HistoryMinimum:
					value, err = bucket.Min.Encode()
				case HistoryMaximum:
					value, err = bucket.Max.Encode()
				case HistorySampleMean:
					value = bucket.Mean()
				}
				if err != nil {
					return nil, err
				}
			}
			resultBytes += len(name) + len(value)
			if resultBytes > HistoryMaxResultBytes {
				return nil, historyError("query_limit", "result")
			}
			values[name] = value
		}
		rendered[i] = HistorySummaryBucket{Range: bucketRange, Metrics: values}
	}
	if plan.query.Bucket == nil {
		return HistorySummaryResult{Metrics: rendered[0].Metrics}, nil
	}
	return HistoryBucketedSummaryResult{Bucket: *plan.query.Bucket, Buckets: rendered}, nil
}
