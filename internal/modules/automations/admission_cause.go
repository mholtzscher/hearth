package automations

// AdmissionCause is the immutable evidence for one admission attempt.
//
//sumtype:decl
type AdmissionCause interface{ isAdmissionCause() }

// ManualCause identifies an operator invocation with no matched Triggers.
type ManualCause struct{}

// DeviceFactCause retains the complete owned Fact that matched the definition.
type DeviceFactCause struct{ Fact DeviceFact }

// HeldStateCause retains the elapsed window and its matched Trigger.
type HeldStateCause struct{ Evidence HeldStateEvidence }

// ScheduleCause identifies a sampled household-local schedule match.
type ScheduleCause struct{}

func (ManualCause) isAdmissionCause()     {}
func (DeviceFactCause) isAdmissionCause() {}
func (HeldStateCause) isAdmissionCause()  {}
func (ScheduleCause) isAdmissionCause()   {}
