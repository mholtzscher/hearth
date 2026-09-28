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
type HistoryEntry struct {
	Kind HistoryKind
	Run  *Run
	Skip *Skip
}

// HistorySummary is the lightweight listing projection of one retained history record.
type HistorySummary struct {
	ID              string
	Kind            HistoryKind
	AutomationID    AutomationID
	AutomationName  string
	Revision        int64
	RecordedAt      time.Time
	Status          RunStatus          // set iff Kind is HistoryRun
	Reason          SkipReason         // set iff Kind is HistorySkip
	Source          RunSource          // admission provenance
	Fact            *DeviceFactSummary // nil for a manual Run or manual Skip
	HeldState       *HeldStateEvidence // set iff Source is RunSourceHeldState
	ConditionMode   ConditionDecisionMode
	ConditionResult *ConditionResult // set iff Conditions were evaluated
	BypassRequested bool
}

// ListHistoryParams is a descending (recorded_at, id) keyset position scoped to one Automation.
type ListHistoryParams struct {
	AutomationID     AutomationID
	BeforeRecordedAt *time.Time
	BeforeID         *string
	Limit            int
}
