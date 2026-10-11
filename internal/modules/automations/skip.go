package automations

import (
	"time"
)

// SkipID is the durable identity of one recorded Skip (ask_ UUIDv7).
type SkipID string

// SkipReason identifies why a matching Fact did not start a Run.
type SkipReason string

const (
	// SkipBusy marks a matching Automation that already had a running Run.
	SkipBusy SkipReason = "automation_busy"
	// SkipStaleFact marks a matching Fact older than the freshness bound.
	SkipStaleFact SkipReason = "stale_fact"
	// SkipConditionsFalse marks a matching Automation whose Conditions
	// evaluated false.
	SkipConditionsFalse SkipReason = "conditions_false"
	// SkipConditionsUnknown marks a matching Automation whose Conditions
	// evaluated unknown.
	SkipConditionsUnknown SkipReason = "conditions_unknown"
)

// Skip is one recorded non-Run outcome with its admission provenance,
// immutable matching Trigger snapshots, and an admission Condition decision. A
// device-fact Skip carries complete Fact evidence; a held-state Skip carries
// hold evidence and one matched Trigger; a manual Skip carries neither.
type Skip struct {
	ID                SkipID
	AutomationID      AutomationID
	AutomationName    string
	Revision          int64
	Cause             AdmissionCause
	MatchedTriggers   []Trigger // nonempty iff Source is RunSourceDeviceFact
	Reason            SkipReason
	ConditionDecision ConditionDecision
	SkippedAt         time.Time
}
