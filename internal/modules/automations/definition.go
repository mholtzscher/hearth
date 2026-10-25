package automations

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// AutomationID is the durable identity of one Automation definition (aut_ UUIDv7).
type AutomationID string

// StepID is an author-supplied subject-safe slug identifying one Step within its own definition.
type StepID string

// Step is one identified side-effect, branching, or delay node in an execution-ordered tree.
type Step struct {
	ID   StepID
	Body StepBody
}

// StepBody is a supported concrete value describing one Step.
//
//sumtype:decl
type StepBody interface{ isStepBody() }

// CommandStep dispatches one static Entity Operation.
type CommandStep struct {
	EntityID      devices.EntityID
	OperationName devices.OperationName
	Parameters    ValueBinding
}

func (CommandStep) isStepBody()      {}
func (NotificationStep) isStepBody() {}
func (FunctionStep) isStepBody()     {}
func (IfStep) isStepBody()           {}
func (ChooseStep) isStepBody()       {}
func (DelayStep) isStepBody()        {}

// NotificationStep sends one static message to a Core-issued destination identity.
type NotificationStep struct {
	DestinationID notifications.DestinationID
	Title         ValueBinding
	Message       ValueBinding
}

// FunctionStep invokes one Adapter-defined contract with fixed arguments.
type FunctionStep struct {
	AdapterID           string
	FunctionKey         adapters.FunctionKey
	ContractFingerprint adapters.ContractFingerprint
	Arguments           ValueBinding
}

// CommandLeaf pairs a Command with its stable Step identity.
type CommandLeaf struct {
	ID      StepID
	Command CommandStep
}

// Definition is one complete, normalized Automation document. Conditions is
// optional; explicit JSON null is invalid.
type Definition struct {
	Name       string // 1–200 runes, trimmed; not unique
	Enabled    bool
	Triggers   []Trigger // 1–32, IDs unique
	Conditions *Condition
	Variables  map[VariableName]ValueBinding
	Steps      []Step // Bounded recursive sequence; IDs unique across every arm.
}

// Record is one live definition at its current revision, which starts at 1 and increments on replacement.
type Record struct {
	ID         AutomationID
	Revision   int64
	Definition Definition
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ListAutomationsParams is an ascending-ID keyset position.
type ListAutomationsParams struct {
	AfterID *AutomationID
	Limit   int
}
