package automations

import (
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Stable domain error classes for transport mapping without inspecting error text.
var (
	// ErrAutomationNotFound reports that no current Automation definition exists
	// for the requested identity.
	ErrAutomationNotFound = errors.New("automation not found")
	// ErrHistoryNotFound reports that no retained Run or Skip exists for the
	// requested identity within the Automation.
	ErrHistoryNotFound = errors.New("automation history not found")
	// ErrInvalidAutomation reports definition, identifier, comparison, or
	// admission input that cannot be accepted.
	ErrInvalidAutomation = errors.New("invalid automation")
	// ErrRevisionConflict reports an expected revision that is not the current one.
	ErrRevisionConflict = errors.New("automation revision conflict")
	// ErrAutomationBusy reports that the Automation already has a running Run.
	ErrAutomationBusy = errors.New("automation is busy")
	// ErrAdmissionUnavailable reports that automation or Command admission is already closed.
	ErrAdmissionUnavailable = errors.New("automation admission is unavailable")
	// ErrInvalidDeviceFact reports one Device Fact whose family, identity, or
	// payload is contradictory or malformed.
	ErrInvalidDeviceFact = errors.New("invalid device fact")
	// ErrExecutorFault reports that a Step start, completion, or ownership check failed.
	ErrExecutorFault = errors.New("automation executor fault")
	// ErrConditionSnapshotRequired reports an Entity missing from the Service's
	// pre-read State snapshot.
	ErrConditionSnapshotRequired = errors.New("automation condition snapshot coverage is incomplete")
	// ErrAutomationConditionsBlocked reports that manual admission committed a
	// Condition Skip instead of a Run.
	ErrAutomationConditionsBlocked = errors.New("automation conditions prevented manual admission")
)

// ConditionSnapshotRequiredError reports a definition-edit race between the
// Service's State pre-read and the admission transaction. RequiredEntityIDs
// carries the transaction's complete required set.
type ConditionSnapshotRequiredError struct {
	RequiredEntityIDs []devices.EntityID
}

// Error reports the fixed class message without echoing Entity identities.
func (*ConditionSnapshotRequiredError) Error() string {
	return "automation condition snapshot coverage is incomplete"
}

// Is matches [ErrConditionSnapshotRequired] for [errors.Is].
func (*ConditionSnapshotRequiredError) Is(target error) bool {
	return target == ErrConditionSnapshotRequired
}

// ConditionsBlockedError reports a manual admission that already committed a
// Condition Skip; the Service constructs it after the transaction commits.
type ConditionsBlockedError struct {
	AutomationID AutomationID
	SkipID       SkipID
	Reason       SkipReason
}

// Error reports the fixed class message without echoing definitions or values.
func (*ConditionsBlockedError) Error() string {
	return "automation conditions prevented manual admission"
}

// Is matches [ErrAutomationConditionsBlocked] for [errors.Is].
func (*ConditionsBlockedError) Is(target error) bool {
	return target == ErrAutomationConditionsBlocked
}
