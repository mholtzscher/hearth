package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type CatalogID string
type InvocationID string
type FunctionKey string
type ContractFingerprint string

const (
	MaximumFunctions           = 64
	FunctionPersistenceTimeout = 5 * time.Second
	MinimumFunctionTimeoutMS   = 100
	MaximumFunctionTimeoutMS   = 60000
)

var (
	ErrInvalidFunctionInput    = errors.New("invalid function input")
	ErrFunctionNotFound        = errors.New("function not found")
	ErrInvocationNotFound      = errors.New("function invocation not found")
	ErrCatalogChanged          = errors.New("function catalog changed")
	ErrCatalogConflict         = errors.New("function catalog conflict")
	ErrAdapterUnavailable      = errors.New("adapter unavailable")
	ErrFunctionAdmissionClosed = errors.New("function admission closed")
	ErrFunctionPersistence     = errors.New("function persistence failed")
	ErrFunctionContractChanged = errors.New("function contract changed")
	ErrInvocationIDConflict    = errors.New("function invocation ID conflict")
)

type Descriptor struct {
	Key          FunctionKey     `json:"key"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
	TimeoutMS    int             `json:"timeout_ms"`
}

type Catalog struct {
	ID        CatalogID
	AdapterID string
	RuntimeID RuntimeID
	Functions []Descriptor
}

type Function struct {
	AdapterID           string
	CatalogRevision     CatalogID
	Descriptor          Descriptor
	Callable            bool
	ContractFingerprint ContractFingerprint
}

type ContractInput struct {
	AdapterID           string
	FunctionKey         FunctionKey
	ContractFingerprint ContractFingerprint
	Arguments           json.RawMessage
}

type ReservedInvokeInput struct {
	ContractInput

	InvocationID InvocationID
}

type InvokeInput struct {
	AdapterID       string
	FunctionKey     FunctionKey
	CatalogRevision CatalogID
	Arguments       json.RawMessage
}

type RouteSnapshot struct {
	AdapterID string
	RuntimeID RuntimeID
}

// Evidence records what can be established, never work to retry or replay.
//
//sumtype:decl
type Evidence interface{ functionEvidence() }
type Succeeded struct{ Result json.RawMessage }
type Failed struct{ ReasonCode string }
type NotExecuted struct{ ReasonCode string }
type Unknown struct{ ReasonCode string }

func (Succeeded) functionEvidence()   {}
func (Failed) functionEvidence()      {}
func (NotExecuted) functionEvidence() {}
func (Unknown) functionEvidence()     {}

type Completion struct {
	At       time.Time
	Evidence Evidence
}

type Invocation struct {
	ID              InvocationID
	CatalogRevision CatalogID
	Function        Descriptor
	Route           RouteSnapshot
	Arguments       json.RawMessage
	StartedAt       time.Time
	Deadline        time.Time
	Completion      *Completion
}

// InvocationError preserves the diagnostic identity without exposing payloads.
type InvocationError struct {
	ID  InvocationID
	Err error
}

func (err *InvocationError) Error() string { return "function invocation persistence failed" }
func (err *InvocationError) Unwrap() error { return err.Err }

type PageInput struct {
	Limit  int
	Cursor string
}
type FunctionPage struct {
	Items      []Function
	NextCursor string
}
type InvocationFilter struct {
	PageInput

	AdapterID   string
	FunctionKey FunctionKey
}
type InvocationPage struct {
	Items      []Invocation
	NextCursor string
}

type FunctionInvoker interface {
	Invoke(context.Context, Invocation) Evidence
}

type FunctionRepository interface {
	RegisterCatalog(context.Context, Catalog) error
	ListFunctions(context.Context, string, PageInput) (FunctionPage, error)
	GetFunction(context.Context, string, FunctionKey) (Function, error)
	Admit(context.Context, PreparedInvocation) (Invocation, error)
	Complete(context.Context, InvocationID, Evidence, time.Time) (Invocation, error)
	GetInvocation(context.Context, InvocationID) (Invocation, error)
	ListInvocations(context.Context, InvocationFilter) (InvocationPage, error)
	Recover(context.Context, time.Time) error
	DeleteHistoryBefore(context.Context, time.Time, int) (int64, error)
}
