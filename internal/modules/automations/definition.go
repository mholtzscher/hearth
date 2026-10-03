package automations

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// AutomationID is the durable identity of one Automation definition (aut_ UUIDv7).
type AutomationID string

// StepID is an author-supplied subject-safe slug identifying one Step within its own definition.
type StepID string

// Step is one identified Command, If, or Choose node in an execution-ordered tree.
type Step struct {
	ID            StepID
	Kind          StepKind
	EntityID      devices.EntityID
	OperationName devices.OperationName
	Parameters    devices.CommandParameters
	If            *IfStep
	Choose        *ChooseStep
}

// Definition is one complete, normalized Automation document. Conditions is
// optional; explicit JSON null is invalid.
type Definition struct {
	Name       string // 1–200 runes, trimmed; not unique
	Enabled    bool
	Triggers   []Trigger // 1–32, IDs unique
	Conditions *Condition
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
