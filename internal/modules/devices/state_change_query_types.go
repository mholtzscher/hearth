package devices

import "encoding/json"

// StateChangeQuery reads one catalogued yes/no or text field's recorded
// changes over a half-open range. A nil Bucket selects a held-value timeline;
// a Bucket selects a summary of a yes/no field.
type StateChangeQuery struct {
	EntityID EntityID
	// Field is a catalogued boolean or string State field path: "" or "/name".
	Field  string
	Range  HistoryRange
	Bucket *HistoryBucket
}

// StateChangeQueryResult describes retained changes of unverified completeness.
// Seeded reports whether a change before the range set its opening value.
type StateChangeQueryResult struct {
	EntityID EntityID
	Field    string
	Range    HistoryRange
	Seeded   bool
	Data     StateChangeData
}

// StateChangeData is either a timeline or a bucketed summary.
//
//sumtype:decl
type StateChangeData interface{ stateChangeData() }

// StateChangeTimeline covers the range with contiguous intervals; neighbors
// never hold equal values.
type StateChangeTimeline struct {
	Intervals []StateChangeInterval
}

// StateChangeInterval holds Value over Range. Known is false before the first
// change when no seed exists; a known Value of null means the field was absent.
type StateChangeInterval struct {
	Range HistoryRange
	Known bool
	Value json.RawMessage
}

type StateChangeSummary struct {
	Bucket  HistoryBucket
	Buckets []StateChangeBucket
}

// StateChangeBucket reports time held true, established changes into true
// (from false or null, never from unknown), and time with a known value.
type StateChangeBucket struct {
	Range               HistoryRange
	SecondsTrue         float64
	TransitionsIntoTrue int
	KnownSeconds        float64
}

func (StateChangeTimeline) stateChangeData() {}
func (StateChangeSummary) stateChangeData()  {}

// StateChangeMaxRows bounds the changes one query reads inside its range.
const StateChangeMaxRows = 100_000
