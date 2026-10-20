package scripted

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	// FunctionOutcomeSucceeded returns a retained successful result.
	FunctionOutcomeSucceeded = "succeeded"
	// FunctionOutcomeFailed returns a retained failed terminal outcome.
	FunctionOutcomeFailed = "failed"
	// FunctionOutcomeNotExecuted returns a retained not-executed terminal outcome.
	FunctionOutcomeNotExecuted = "not_executed"
	// FunctionOutcomeUnknown returns a retained unknown terminal outcome.
	FunctionOutcomeUnknown = "unknown"

	defaultFunctionTimeoutMS    = 1000
	maxFunctionCatalog          = 64
	maxFunctionDescriptionRunes = 2000
)

var functionReasonPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,127}$`)

// ErrUnknownFunctionInvocation reports a release for an invocation this runtime
// never held, already released, or already completed. Core retains authoritative
// invocation history.
var ErrUnknownFunctionInvocation = errors.New("unknown scripted function invocation")

// FunctionSpec is one configured Adapter Function. InputSchema and OutputSchema
// are YAML values converted to JSON and compiled with the same bounded profile
// Core and the SDK use. The runtime never interprets a function's name.
type FunctionSpec struct {
	Key           string `yaml:"key"`
	Description   string `yaml:"description"`
	TimeoutMS     int    `yaml:"timeout_ms"`
	InputSchema   any    `yaml:"input_schema"`
	OutputSchema  any    `yaml:"output_schema"`
	Outcome       string `yaml:"outcome"`
	ReasonCode    string `yaml:"reason_code"`
	Result        any    `yaml:"result"`
	EchoArguments bool   `yaml:"echo_arguments"`
	Hold          bool   `yaml:"hold"`
}

// scriptedFunction is one validated function. Its descriptor and result are
// immutable after construction.
type scriptedFunction struct {
	descriptor adapter.FunctionDescriptor
	outcome    string
	reasonCode string
	result     json.RawMessage
	echo       bool
	hold       bool
}

// ValidateFunctions is the static config boundary. It validates and prepares
// every function without a Session so checked-in examples and config files fail
// before the simulator connects to NATS.
func ValidateFunctions(specs []FunctionSpec) error {
	if len(specs) > maxFunctionCatalog {
		return fmt.Errorf("functions must contain at most %d entries", maxFunctionCatalog)
	}
	seen := make(map[string]struct{}, len(specs))
	for index, spec := range specs {
		if _, err := buildFunction(spec); err != nil {
			return fmt.Errorf("functions[%d]: %w", index, err)
		}
		if _, duplicate := seen[spec.Key]; duplicate {
			return fmt.Errorf("functions[%d]: duplicate function key %q", index, spec.Key)
		}
		seen[spec.Key] = struct{}{}
	}
	return nil
}

// buildFunction validates one function and prepares its descriptor. Constructor
// and config validation share it, so both boundaries enforce the same rules.
func buildFunction(spec FunctionSpec) (scriptedFunction, error) {
	descriptor, err := prepareFunctionDescriptor(spec)
	if err != nil {
		return scriptedFunction{}, err
	}
	function := scriptedFunction{descriptor: descriptor, hold: spec.Hold}
	return applyFunctionBehavior(function, spec)
}

func prepareFunctionDescriptor(spec FunctionSpec) (adapter.FunctionDescriptor, error) {
	if err := platformconfig.ValidateSlug("key", spec.Key); err != nil {
		return adapter.FunctionDescriptor{}, err
	}
	description := strings.TrimSpace(spec.Description)
	if !utf8.ValidString(description) || description == "" ||
		utf8.RuneCountInString(description) > maxFunctionDescriptionRunes {
		return adapter.FunctionDescriptor{}, fmt.Errorf(
			"description must contain 1-%d characters",
			maxFunctionDescriptionRunes,
		)
	}
	timeoutMS := spec.TimeoutMS
	if timeoutMS == 0 {
		timeoutMS = defaultFunctionTimeoutMS
	}
	if timeoutMS < 100 || timeoutMS > 60000 {
		return adapter.FunctionDescriptor{}, fmt.Errorf("timeout_ms must be between 100 and 60000")
	}
	if spec.InputSchema == nil || spec.OutputSchema == nil {
		return adapter.FunctionDescriptor{}, fmt.Errorf("input_schema and output_schema are required")
	}
	input, err := compileFunctionSchema("input_schema", spec.InputSchema)
	if err != nil {
		return adapter.FunctionDescriptor{}, err
	}
	output, err := compileFunctionSchema("output_schema", spec.OutputSchema)
	if err != nil {
		return adapter.FunctionDescriptor{}, err
	}
	return adapter.FunctionDescriptor{
		Key: spec.Key, Description: description, TimeoutMS: timeoutMS,
		InputSchema: input.Document(), OutputSchema: output.Document(),
	}, nil
}

func applyFunctionBehavior(function scriptedFunction, spec FunctionSpec) (scriptedFunction, error) {
	outcome := spec.Outcome
	if outcome == "" {
		outcome = FunctionOutcomeSucceeded
	}
	switch outcome {
	case FunctionOutcomeSucceeded:
		if spec.ReasonCode != "" {
			return scriptedFunction{}, fmt.Errorf(
				"reason_code is only valid for %q, %q, or %q",
				FunctionOutcomeFailed, FunctionOutcomeNotExecuted, FunctionOutcomeUnknown,
			)
		}
		if spec.EchoArguments && spec.Result != nil {
			return scriptedFunction{}, fmt.Errorf("result and echo_arguments are mutually exclusive")
		}
		function.outcome = outcome
		function.echo = spec.EchoArguments
		if function.echo {
			return function, nil
		}
		return prepareStaticResult(function, spec.Result)
	case FunctionOutcomeFailed, FunctionOutcomeNotExecuted, FunctionOutcomeUnknown:
		if spec.EchoArguments || spec.Result != nil {
			return scriptedFunction{}, fmt.Errorf(
				"result and echo_arguments require the %q outcome",
				FunctionOutcomeSucceeded,
			)
		}
		if !functionReasonPattern.MatchString(spec.ReasonCode) {
			return scriptedFunction{}, fmt.Errorf("reason_code must match %s", functionReasonPattern.String())
		}
		function.outcome = outcome
		function.reasonCode = spec.ReasonCode
		return function, nil
	default:
		return scriptedFunction{}, fmt.Errorf(
			"outcome must be %q, %q, %q, or %q",
			FunctionOutcomeSucceeded, FunctionOutcomeFailed, FunctionOutcomeNotExecuted, FunctionOutcomeUnknown,
		)
	}
}

func prepareStaticResult(function scriptedFunction, value any) (scriptedFunction, error) {
	raw := value
	if raw == nil {
		raw = map[string]any{}
	}
	result, err := toJSON(raw)
	if err != nil {
		return scriptedFunction{}, fmt.Errorf("invalid result: %w", err)
	}
	output, err := functionschema.Compile(function.descriptor.OutputSchema)
	if err != nil {
		return scriptedFunction{}, fmt.Errorf("invalid output_schema")
	}
	normalized, err := output.Normalize(result)
	if err != nil {
		return scriptedFunction{}, fmt.Errorf("result does not match output_schema")
	}
	function.result = normalized
	return function, nil
}

func compileFunctionSchema(field string, value any) (*functionschema.Schema, error) {
	raw, err := toJSON(value)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", field, err)
	}
	compiled, err := functionschema.Compile(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid %s", field)
	}
	return compiled, nil
}

// FunctionRuntime owns one Adapter's configured Functions and the release signal
// for held invocations. It does not own SDK serving, registration I/O, or when
// shutdown begins.
type FunctionRuntime struct {
	mutex     sync.Mutex
	functions []scriptedFunction
	pending   map[string]*pendingFunctionInvocation
	stopped   chan struct{}
	stopOnce  sync.Once
}

type pendingFunctionInvocation struct {
	release  chan struct{}
	released bool
}

// NewFunctionRuntime validates and prepares one Adapter's functions. An empty
// list is valid and registers no catalog.
func NewFunctionRuntime(specs []FunctionSpec) (*FunctionRuntime, error) {
	if err := ValidateFunctions(specs); err != nil {
		return nil, err
	}
	functions := make([]scriptedFunction, 0, len(specs))
	for _, spec := range specs {
		function, err := buildFunction(spec)
		if err != nil {
			return nil, err
		}
		functions = append(functions, function)
	}
	return &FunctionRuntime{
		functions: functions,
		pending:   make(map[string]*pendingFunctionInvocation),
		stopped:   make(chan struct{}),
	}, nil
}

// Functions returns the SDK catalog in configuration order. The SDK sorts
// descriptors for registration. Handlers may run concurrently for one key.
func (runtime *FunctionRuntime) Functions() []adapter.Function {
	functions := make([]adapter.Function, 0, len(runtime.functions))
	for index := range runtime.functions {
		function := runtime.functions[index]
		functions = append(functions, adapter.Function{
			Descriptor: function.descriptor,
			Handler:    runtime.handler(function),
		})
	}
	return functions
}

// Release releases one held invocation. It is idempotent while the handler is
// still running and reports [ErrUnknownFunctionInvocation] after it returns.
func (runtime *FunctionRuntime) Release(invocationID string) error {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	entry := runtime.pending[invocationID]
	if entry == nil {
		return ErrUnknownFunctionInvocation
	}
	if !entry.released {
		close(entry.release)
		entry.released = true
	}
	return nil
}

// Stop unblocks every held handler so app shutdown can join them before the
// Session closes. A handler released by Stop returns an unknown outcome because
// admitted work may have begun. Stop is idempotent.
func (runtime *FunctionRuntime) Stop() {
	runtime.stopOnce.Do(func() { close(runtime.stopped) })
}

// Pending returns the invocation IDs currently waiting on a hold, sorted for
// stable control output. It exposes admission, not invocation arguments.
func (runtime *FunctionRuntime) Pending() []string {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	ids := make([]string, 0, len(runtime.pending))
	for invocationID := range runtime.pending {
		ids = append(ids, invocationID)
	}
	sort.Strings(ids)
	return ids
}

func (runtime *FunctionRuntime) handler(function scriptedFunction) adapter.FunctionHandler {
	return func(ctx context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
		if function.hold {
			entry, admitted := runtime.admitHold(request.InvocationID)
			if !admitted {
				return adapter.FunctionNotExecuted{ReasonCode: "adapter_busy"}
			}
			defer runtime.finishHold(request.InvocationID)
			select {
			case <-entry.release:
			case <-runtime.stopped:
				return adapter.FunctionUnknown{ReasonCode: "adapter_stopping"}
			case <-ctx.Done():
				return adapter.FunctionUnknown{ReasonCode: "outcome_timeout"}
			}
		}
		return function.resultFor(request.Arguments)
	}
}

func (runtime *FunctionRuntime) admitHold(invocationID string) (*pendingFunctionInvocation, bool) {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	if _, exists := runtime.pending[invocationID]; exists {
		return nil, false
	}
	entry := &pendingFunctionInvocation{release: make(chan struct{})}
	runtime.pending[invocationID] = entry
	return entry, true
}

func (runtime *FunctionRuntime) finishHold(invocationID string) {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	delete(runtime.pending, invocationID)
}

// resultFor returns the configured terminal result. Echo returns the SDK's
// normalized argument bytes so exact literals round-trip.
func (function scriptedFunction) resultFor(arguments json.RawMessage) adapter.FunctionResult {
	switch function.outcome {
	case FunctionOutcomeSucceeded:
		if function.echo {
			return adapter.FunctionSucceeded{Result: append(json.RawMessage(nil), arguments...)}
		}
		return adapter.FunctionSucceeded{Result: function.result}
	case FunctionOutcomeFailed:
		return adapter.FunctionFailed{ReasonCode: function.reasonCode}
	case FunctionOutcomeNotExecuted:
		return adapter.FunctionNotExecuted{ReasonCode: function.reasonCode}
	case FunctionOutcomeUnknown:
		return adapter.FunctionUnknown{ReasonCode: function.reasonCode}
	default:
		return adapter.FunctionUnknown{ReasonCode: "invalid_response"}
	}
}
