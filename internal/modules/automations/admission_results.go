package automations

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
//
//sumtype:decl
type ManualAdmissionResult interface{ isManualAdmissionResult() }

func (Run) isManualAdmissionResult()  {}
func (Skip) isManualAdmissionResult() {}

// AdmissionSkip carries committed Skip identity, owned admission evidence, and
// the reason. Logging derives scalar labels without additional reads.
type AdmissionSkip struct {
	SkipID       SkipID
	AutomationID AutomationID
	Revision     int64
	Cause        AdmissionCause
	Reason       SkipReason
}
