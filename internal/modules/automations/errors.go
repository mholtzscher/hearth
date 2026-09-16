package automations

import (
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

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
	// ErrConditionSnapshotRequired reports that the transaction's current eligible
	// Conditions need an Entity absent from the Service's pre-read State snapshot.
	// It is a retryable definition-edit race, never a permanent input error and
	// never a partial admission.
	ErrConditionSnapshotRequired = errors.New("automation condition snapshot coverage is incomplete")
	// ErrAutomationConditionsBlocked reports that manual admission committed a
	// Condition Skip instead of a Run. It refers only to a successfully committed
	// Skip, which is why the Service returns it after the transaction, never from
	// inside it.
	ErrAutomationConditionsBlocked = errors.New("automation conditions prevented manual admission")
)

// ConditionSnapshotRequiredError reports a definition-edit race between the
// Service's State pre-read and the admission transaction. RequiredEntityIDs is
// the affected Automation's complete sorted, deduplicated required set; the
// transaction writes nothing, and transports treat the error as retryable rather
// than input.
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

// ConditionsBlockedError refers only to a manual admission that
// already committed a Condition Skip. The Service constructs it after the
// transaction commits, so returning it never rolls back the retained history.
// The API maps it to 409 with the committed Skip's history reference.
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
