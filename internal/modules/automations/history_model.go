package automations

import (
	"fmt"
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

// Page is one keyset page of automation-owned records.
type Page[T any] struct {
	Items   []T
	HasMore bool
}

const (
	// automationDefaultPageLimit is the page size used when a caller omits one.
	automationDefaultPageLimit = 50
	// automationMaximumPageLimit bounds one definition or history page.
	automationMaximumPageLimit = 200
)

// PageLimit resolves one requested page limit to the effective page size. An
// omitted limit becomes automationDefaultPageLimit; anything outside 1 through
// automationMaximumPageLimit is an [ErrInvalidAutomation].
func PageLimit(limit int) (int, error) {
	switch {
	case limit == 0:
		return automationDefaultPageLimit, nil
	case limit < 1 || limit > automationMaximumPageLimit:
		return 0, fmt.Errorf(
			"%w: page limit must be between 1 and %d",
			ErrInvalidAutomation, automationMaximumPageLimit,
		)
	default:
		return limit, nil
	}
}
