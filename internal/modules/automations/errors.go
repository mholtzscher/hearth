package automations

import "errors"

// Stable domain error classes for transport mapping without inspecting error text.
var (
	// ErrAutomationNotFound reports that no current Automation definition exists
	// for the requested identity, including one that was hard-deleted.
	ErrAutomationNotFound = errors.New("automation not found")
	// ErrHistoryNotFound reports that no retained Run or Skip exists for the
	// requested identity within the requested Automation.
	ErrHistoryNotFound = errors.New("automation history not found")
	// ErrInvalidAutomation reports a definition, identifier, comparison, or
	// admission input that cannot be accepted. It is the permanent input class
	// and never covers a retryable storage failure.
	ErrInvalidAutomation = errors.New("invalid automation")
	// ErrRevisionConflict reports that a replacement or deletion was supplied an
	// expected revision that is not the current one.
	ErrRevisionConflict = errors.New("automation revision conflict")
	// ErrAutomationBusy reports that the Automation already has a running Run, so
	// a new admission cannot create another one.
	ErrAutomationBusy = errors.New("automation is busy")
	// ErrAdmissionUnavailable reports that automation or Command admission is
	// already closed, so no Run may start.
	ErrAdmissionUnavailable = errors.New("automation admission is unavailable")
	// ErrInvalidDeviceFact reports one Device Fact that cannot be admitted because
	// its family, identity, or payload is contradictory or malformed.
	ErrInvalidDeviceFact = errors.New("invalid device fact")
	// ErrExecutorFault reports that an admitted Run cannot be advanced truthfully,
	// because a Step start, completion, or ownership check failed. It closes
	// automation admission rather than guessing progress.
	ErrExecutorFault = errors.New("automation executor fault")
)
