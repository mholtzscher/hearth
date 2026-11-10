package devices

import (
	"encoding/json"
	"time"
)

// HistoryRange is a half-open Observation-time window, [From, To).
type HistoryRange struct {
	From time.Time
	To   time.Time
}

// HistoryQuery summarizes one catalogued numeric field of one Entity's
// retained accepted reports. From must lie on the rollup cell grid; the final
// bucket also counts reports in the rest of the cell that contains To.
type HistoryQuery struct {
	EntityID EntityID
	Range    HistoryRange
	// Field is a catalogued numeric State field path: "" or "/name".
	Field   string
	Metrics map[string]HistoryMetric
	Bucket  *HistoryBucket
	// Nulls defaults to reject when empty.
	Nulls HistoryNullPolicy
}

// HistoryNullPolicy controls known null reports in numeric summary reductions.
// The zero value defaults to rejection; gap nulls every metric in the bucket.
type HistoryNullPolicy string

const (
	HistoryNullReject HistoryNullPolicy = "reject"
	HistoryNullGap    HistoryNullPolicy = "gap"
)

// HistoryMetric is closed to the numeric reductions rollups can answer.
//
//sumtype:decl
type HistoryMetric interface{ historyMetric() }

type HistoryMinimum struct{}
type HistoryMaximum struct{}
type HistorySampleMean struct{}

func (HistoryMinimum) historyMetric()    {}
func (HistoryMaximum) historyMetric()    {}
func (HistorySampleMean) historyMetric() {}

type HistoryBucket struct {
	Duration  time.Duration
	Alignment string
}

// HistoryQueryResult always describes retained evidence of unverified completeness.
type HistoryQueryResult struct {
	EntityID EntityID
	Range    HistoryRange
	Data     HistoryResult
}

// HistoryResult excludes contradictory payloads such as buckets plus overall metrics.
//
//sumtype:decl
type HistoryResult interface{ historyResult() }

// HistoryMetricValues contains exactly the requested labels, each encoded as a
// JSON number or null. Integer extrema never pass through floating-point encoding.
type HistoryMetricValues map[string]json.RawMessage

type HistorySummaryResult struct {
	Metrics HistoryMetricValues
}

type HistoryBucketedSummaryResult struct {
	Bucket  HistoryBucket
	Buckets []HistorySummaryBucket
}

type HistorySummaryBucket struct {
	Range   HistoryRange
	Metrics HistoryMetricValues
}

func (HistorySummaryResult) historyResult()         {}
func (HistoryBucketedSummaryResult) historyResult() {}

const (
	HistoryMaxRequestBytes = 1024 * 1024
	HistoryMaxMetrics      = 16
	HistoryMaxBuckets      = 1000
	HistoryMaxResultBytes  = 4 * 1024 * 1024
)

// HistoryQueryError carries safe query diagnostics. Code is fixed by the owner;
// Field names the request field without embedding State.
type HistoryQueryError struct {
	Code  string
	Field string
}

func (failure *HistoryQueryError) Error() string { return failure.Code }
