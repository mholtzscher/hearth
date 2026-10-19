package adapters

import (
	"encoding/json"
	"regexp"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/contracts/functionschema"
)

var functionReasonPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,127}$`)

// PreparedInvocation transfers immutable, schema-checked input to transactional
// admission. Accessors copy mutable data; the zero value is not a preparation.
type PreparedInvocation struct {
	function  Function
	arguments json.RawMessage
	startedAt time.Time
	deadline  time.Time
	prepared  bool
}

func PrepareInvocation(function Function, input InvokeInput, start time.Time) (PreparedInvocation, error) {
	if ValidateInvokeScope(input) != nil || function.AdapterID != input.AdapterID ||
		function.Descriptor.Key != input.FunctionKey || !ValidFunctionTime(start) {
		return PreparedInvocation{}, ErrInvalidFunctionInput
	}
	if function.CatalogRevision != input.CatalogRevision {
		return PreparedInvocation{}, ErrCatalogChanged
	}
	descriptor, schema, _, err := PrepareDescriptor(function.Descriptor)
	if err != nil {
		return PreparedInvocation{}, err
	}
	arguments, err := schema.Normalize(input.Arguments)
	if err != nil {
		return PreparedInvocation{}, ErrInvalidFunctionInput
	}
	function.Descriptor = descriptor
	deadline := start.Add(time.Duration(descriptor.TimeoutMS) * time.Millisecond)
	if !ValidFunctionTime(deadline) {
		return PreparedInvocation{}, ErrInvalidFunctionInput
	}
	return PreparedInvocation{function: function, arguments: arguments, startedAt: start,
		deadline: deadline, prepared: true}, nil
}

func (prepared PreparedInvocation) Function() Function {
	function := prepared.function
	function.Descriptor = copyDescriptor(function.Descriptor)
	return function
}
func (prepared PreparedInvocation) Arguments() json.RawMessage {
	return slices.Clone(prepared.arguments)
}
func (prepared PreparedInvocation) StartedAt() time.Time { return prepared.startedAt }
func (prepared PreparedInvocation) Deadline() time.Time  { return prepared.deadline }
func (prepared PreparedInvocation) Ready() bool          { return prepared.prepared }

func ValidateInvokeScope(input InvokeInput) error {
	if ValidateAdapterID(input.AdapterID) != nil || !ValidFunctionKey(input.FunctionKey) ||
		validateID(string(input.CatalogRevision), "afc") != nil || len(input.Arguments) == 0 {
		return ErrInvalidFunctionInput
	}
	return nil
}

func ValidFunctionTime(at time.Time) bool {
	_, offset := at.Zone()
	return !at.IsZero() && offset == 0 && at.Year() >= 1 && at.Year() <= 9999
}

// PrepareFunctionEvidence validates evidence against an already compiled snapshot
// output schema. Its consumer owns descriptor preparation within its boundary.
func PrepareFunctionEvidence(
	evidence Evidence,
	output *functionschema.Schema,
	start, deadline, at time.Time,
) (Evidence, error) {
	if !ValidFunctionTime(at) || at.Before(start) {
		return nil, ErrInvalidFunctionInput
	}
	switch outcome := evidence.(type) {
	case Succeeded:
		if !at.Before(deadline) {
			return nil, ErrInvalidFunctionInput
		}
		result, err := output.Normalize(outcome.Result)
		if err != nil {
			return nil, ErrInvalidFunctionInput
		}
		return Succeeded{Result: result}, nil
	case Failed:
		if !at.Before(deadline) || !functionReasonPattern.MatchString(outcome.ReasonCode) {
			return nil, ErrInvalidFunctionInput
		}
		return outcome, nil
	case NotExecuted:
		if !functionReasonPattern.MatchString(outcome.ReasonCode) {
			return nil, ErrInvalidFunctionInput
		}
		return outcome, nil
	case Unknown:
		if !functionReasonPattern.MatchString(outcome.ReasonCode) {
			return nil, ErrInvalidFunctionInput
		}
		return outcome, nil
	default:
		return nil, ErrInvalidFunctionInput
	}
}
