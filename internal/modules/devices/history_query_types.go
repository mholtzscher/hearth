package devices

import (
	"context"
	"encoding/json"
	"time"
)

// HistoryRange is a half-open Observation-time window, [From, To).
type HistoryRange struct {
	From time.Time
	To   time.Time
}

// HistoryQuery selects values from one Entity's retained accepted reports.
type HistoryQuery struct {
	EntityID   EntityID
	Range      HistoryRange
	SelectExpr *string
	Result     HistoryResultSpec
}

type HistoryOrder string

const (
	HistoryAscending  HistoryOrder = "ascending"
	HistoryDescending HistoryOrder = "descending"
)

// HistoryResultSpec is a closed query shape; only observations support filtering
// and pagination, so a filter cannot accidentally remove timeline evidence.
//
//sumtype:decl
type HistoryResultSpec interface{ historyResultSpec() }

type HistoryObservations struct {
	WhereExpr *string
	Order     HistoryOrder
	Limit     int
	Cursor    string
}

type HistoryTimeline struct{}

type HistorySummary struct {
	Metrics map[string]HistoryMetric
	Bucket  *HistoryBucket
}

func (HistoryObservations) historyResultSpec() {}
func (HistoryTimeline) historyResultSpec()     {}
func (HistorySummary) historyResultSpec()      {}

// HistoryMetric keeps report reductions separate from temporal predicates.
//
//sumtype:decl
type HistoryMetric interface{ historyMetric() }

type HistoryMinimum struct{}
type HistoryMaximum struct{}
type HistorySampleMean struct{}
type HistoryDurationWhere struct {
	WhereExpr string
	Unit      string
}
type HistoryTransitionsInto struct{ WhereExpr string }

func (HistoryMinimum) historyMetric()         {}
func (HistoryMaximum) historyMetric()         {}
func (HistorySampleMean) historyMetric()      {}
func (HistoryDurationWhere) historyMetric()   {}
func (HistoryTransitionsInto) historyMetric() {}

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

type HistoryObservationsResult struct {
	Order      HistoryOrder
	Items      []HistoryObservation
	NextCursor string
}

type HistoryObservation struct {
	ObservationID     ObservationID
	ObservedAt        time.Time
	AdapterReceivedAt time.Time
	SourceUpdatedAt   *time.Time
	Disposition       ObservationDisposition
	Value             json.RawMessage
}

type HistoryTimelineResult struct {
	Coverage  HistoryTemporalCoverage
	Intervals []HistoryInterval
}

// HistoryMetricValues contains exactly the requested labels, each encoded as a
// JSON number or null. Integer extrema never pass through floating-point encoding.
type HistoryMetricValues map[string]json.RawMessage

type HistorySummaryResult struct {
	Coverage *HistoryTemporalCoverage
	Metrics  HistoryMetricValues
}

type HistoryBucketedSummaryResult struct {
	Coverage *HistoryTemporalCoverage
	Bucket   HistoryBucket
	Buckets  []HistorySummaryBucket
}

type HistorySummaryBucket struct {
	Range    HistoryRange
	Metrics  HistoryMetricValues
	Coverage *HistoryDurationCoverage
}

func (HistoryObservationsResult) historyResult()    {}
func (HistoryTimelineResult) historyResult()        {}
func (HistorySummaryResult) historyResult()         {}
func (HistoryBucketedSummaryResult) historyResult() {}

// HistoryInterval distinguishes an observed null value from absent evidence.
//
//sumtype:decl
type HistoryInterval interface{ historyInterval() }

type HistoryValueInterval struct {
	Range HistoryRange
	Value json.RawMessage
}

type HistoryUnknownInterval struct{ Range HistoryRange }

func (HistoryValueInterval) historyInterval()   {}
func (HistoryUnknownInterval) historyInterval() {}

type HistoryDurationCoverage struct {
	InferredKnownSeconds float64
	UnknownSeconds       float64
}

type HistoryTemporalCoverage struct {
	HistoryDurationCoverage

	SeedAvailable bool
}

// HistoryReader owns one coherent, bounded read of Entity eligibility, the seed
// and source rows. No database snapshot remains open while expressions execute.
type HistoryReader interface {
	ReadHistoryWindow(context.Context, HistoryReadRequest) (HistoryWindow, error)
}

type HistoryReadRequest struct {
	EntityID    EntityID
	Range       HistoryRange
	Order       HistoryOrder
	Cursor      *HistoryReadCursor
	IncludeSeed bool
	// SourceRowLimit optionally lowers the source-row budget (including the seed).
	// Zero uses HistoryMaxSourceRows; unfiltered pages need only limit+1 rows.
	SourceRowLimit int
}

// HistoryReadCursor is a strict keyset position and the traversal's fixed initial
// high-water mark. It need not identify a row still retained in SQLite.
type HistoryReadCursor struct {
	ObservedAt   time.Time
	ReceiveOrder int64
	HighWater    int64
}

type HistoryWindow struct {
	Rows      []EntityStateHistoryEntry
	Seed      *EntityStateHistoryEntry
	HighWater int64
	HasMore   bool
}

const (
	HistoryMaxMetrics     = 16
	HistoryMaxBuckets     = 1000
	HistoryMaxSourceRows  = 10000
	HistoryMaxSourceBytes = 16 * 1024 * 1024
	HistoryMaxEvaluations = 100000
	HistoryMaxResultBytes = 4 * 1024 * 1024
)

// HistoryQueryError carries safe query diagnostics. Code is fixed by the owner;
// Field names the request field without embedding expression source or State.
type HistoryQueryError struct {
	Code  string
	Field string
}

func (failure *HistoryQueryError) Error() string { return failure.Code }
