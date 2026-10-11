package automations

import (
	"time"
)

// HistoryKind discriminates a retained Run from a retained Skip.
type HistoryKind string

const (
	// HistoryRun identifies a retained Run.
	HistoryRun HistoryKind = "run"
	// HistorySkip identifies a retained Skip.
	HistorySkip HistoryKind = "skip"
)

// HistoryEntry is exactly one retained history record.
//
//sumtype:decl
type HistoryEntry interface{ isHistoryEntry() }

func (Run) isHistoryEntry()  {}
func (Skip) isHistoryEntry() {}

// HistorySummaryBody is the materialized Run-or-Skip projection.
//
//sumtype:decl
type HistorySummaryBody interface{ isHistorySummaryBody() }

type RunHistorySummary struct{ Status RunStatus }
type SkipHistorySummary struct{ Reason SkipReason }

func (RunHistorySummary) isHistorySummaryBody()  {}
func (SkipHistorySummary) isHistorySummaryBody() {}

// HistorySummary is the lightweight listing projection of one retained history record.
type HistorySummary struct {
	ID               string
	Body             HistorySummaryBody
	AutomationID     AutomationID
	AutomationName   string
	Revision         int64
	RecordedAt       time.Time
	Cause            AdmissionCause
	ConditionSummary ConditionDecisionSummary
}

// ListHistoryParams is a descending (recorded_at, id) keyset position scoped to one Automation.
type ListHistoryParams struct {
	AutomationID     AutomationID
	BeforeRecordedAt *time.Time
	BeforeID         *string
	Limit            int
}
