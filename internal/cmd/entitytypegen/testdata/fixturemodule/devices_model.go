// Package devices is minimal test-only catalog scaffolding for generated
// entity-type fixture execution. It provides the plain model types the
// generated catalog references; catalog behavior comes from a runtime copy of
// the production catalog.go, so no production semantics are duplicated here.
package devices

import (
	"encoding/json"
	"time"
)

type EntityID string

type EntityTypeID string

type OperationName string

type OutcomeKind string

const (
	OutcomeObserved   OutcomeKind = "observed"
	OutcomeDispatched OutcomeKind = "dispatched"
)

type EntitySupport json.RawMessage

type Value json.RawMessage

type CommandParameters json.RawMessage

type Entity struct {
	ID      EntityID
	TypeID  EntityTypeID
	Support EntitySupport
}

type CommandRecord struct {
	OperationName OperationName
	Parameters    CommandParameters
	Deadline      time.Time
}
