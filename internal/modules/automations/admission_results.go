package automations

import "github.com/mholtzscher/hearth/internal/modules/devices"

// AdmissionOutcome reports what one automatic admission attempt decided.
type AdmissionOutcome struct {
	MatchedAutomations int
	StartedRuns        int
	RecordedSkips      int
	DuplicateOutcomes  int
}

// AdmissionResult contains committed admission outcomes.
type AdmissionResult struct {
	Outcome     AdmissionOutcome
	StartedRuns []Run
	Skips       []AdmissionSkip
}

// ManualAdmissionResult is exactly one committed Run or Skip, returned only
// after the transaction commits.
type ManualAdmissionResult struct {
	Run  *Run
	Skip *Skip
}

// AdmissionSkip carries committed Skip identity, admission source, reason, and
// nullable Fact identity for logging. FactID, Family, and Variant are set only
// for a device-fact Skip.
type AdmissionSkip struct {
	SkipID       SkipID
	AutomationID AutomationID
	Revision     int64
	Source       RunSource
	Reason       SkipReason
	FactID       *devices.DeviceFactID
	Family       DeviceFactFamily
	Variant      string
}
