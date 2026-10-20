package scripted_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func objectSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}

func echoFunctionSpec() scripted.FunctionSpec {
	return scripted.FunctionSpec{
		Key: "inspect", Description: "Inspect the arguments",
		InputSchema: objectSchema(), OutputSchema: objectSchema(),
		EchoArguments: true,
	}
}

func terminalFunctionSpec(outcome, reason string) scripted.FunctionSpec {
	return scripted.FunctionSpec{
		Key: outcome, Description: "Return " + outcome,
		InputSchema: objectSchema(), OutputSchema: objectSchema(),
		Outcome: outcome, ReasonCode: reason,
	}
}

// Factory validation is an independent boundary: configs are freely constructed,
// so the constructor must reject the same shapes the config loader rejects.
func TestFunctionRuntimeValidatesConfiguration(t *testing.T) {
	t.Parallel()
	valid := map[string]scripted.FunctionSpec{
		"default success": {
			Key:          "inspect",
			Description:  "Inspect",
			InputSchema:  objectSchema(),
			OutputSchema: objectSchema(),
		},
		"echo":         echoFunctionSpec(),
		"failed":       terminalFunctionSpec(scripted.FunctionOutcomeFailed, "simulated_failure"),
		"not executed": terminalFunctionSpec(scripted.FunctionOutcomeNotExecuted, "simulated_unavailable"),
		"unknown":      terminalFunctionSpec(scripted.FunctionOutcomeUnknown, "simulated_unknown"),
		"held": {
			Key:          "hold",
			Description:  "Hold",
			InputSchema:  objectSchema(),
			OutputSchema: objectSchema(),
			Hold:         true,
		},
		"unicode maximum": {
			Key:          "inspect",
			Description:  strings.Repeat("界", 2000),
			InputSchema:  objectSchema(),
			OutputSchema: objectSchema(),
		},
		"explicit timeout": {
			Key:          "inspect",
			Description:  "Inspect",
			TimeoutMS:    60000,
			InputSchema:  objectSchema(),
			OutputSchema: objectSchema(),
		},
	}
	for name, spec := range valid {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := scripted.NewFunctionRuntime([]scripted.FunctionSpec{spec}); err != nil {
				t.Fatalf("valid function rejected: %v", err)
			}
		})
	}
	invalid := map[string]scripted.FunctionSpec{
		"missing key": {Description: "Inspect", InputSchema: objectSchema(), OutputSchema: objectSchema()},
		"key too long": {
			Key:          strings.Repeat("a", 64),
			Description:  "Inspect",
			InputSchema:  objectSchema(),
			OutputSchema: objectSchema(),
		},
		"blank description": {
			Key:          "inspect",
			Description:  "  ",
			InputSchema:  objectSchema(),
			OutputSchema: objectSchema(),
		},
		"description too long": {
			Key:          "inspect",
			Description:  strings.Repeat("界", 2001),
			InputSchema:  objectSchema(),
			OutputSchema: objectSchema(),
		},
		"timeout too low": {
			Key:          "inspect",
			Description:  "Inspect",
			TimeoutMS:    99,
			InputSchema:  objectSchema(),
			OutputSchema: objectSchema(),
		},
		"timeout too high": {
			Key:          "inspect",
			Description:  "Inspect",
			TimeoutMS:    60001,
			InputSchema:  objectSchema(),
			OutputSchema: objectSchema(),
		},
		"missing input":  {Key: "inspect", Description: "Inspect", OutputSchema: objectSchema()},
		"missing output": {Key: "inspect", Description: "Inspect", InputSchema: objectSchema()},
		"non-object schema": {
			Key:          "inspect",
			Description:  "Inspect",
			InputSchema:  map[string]any{"type": "array"},
			OutputSchema: objectSchema(),
		},
		"unsupported keyword": {
			Key:          "inspect",
			Description:  "Inspect",
			InputSchema:  map[string]any{"type": "object", "patternProperties": map[string]any{}},
			OutputSchema: objectSchema(),
		},
		"result mismatch": {
			Key:          "inspect",
			Description:  "Inspect",
			InputSchema:  objectSchema(),
			OutputSchema: map[string]any{"type": "object", "required": []any{"ready"}},
			Result:       map[string]any{"other": true},
		},
		"result and echo": {
			Key:           "inspect",
			Description:   "Inspect",
			InputSchema:   objectSchema(),
			OutputSchema:  objectSchema(),
			Result:        map[string]any{},
			EchoArguments: true,
		},
		"reason on success": {
			Key:          "inspect",
			Description:  "Inspect",
			InputSchema:  objectSchema(),
			OutputSchema: objectSchema(),
			ReasonCode:   "simulated",
		},
		"missing reason":  terminalFunctionSpec(scripted.FunctionOutcomeFailed, ""),
		"invalid reason":  terminalFunctionSpec(scripted.FunctionOutcomeFailed, "Bad Reason"),
		"unknown outcome": terminalFunctionSpec("finished", "simulated"),
		"result on failure": {
			Key:          "fail",
			Description:  "Fail",
			InputSchema:  objectSchema(),
			OutputSchema: objectSchema(),
			Outcome:      scripted.FunctionOutcomeFailed,
			ReasonCode:   "simulated",
			Result:       map[string]any{},
		},
		"echo on failure": {
			Key:           "fail",
			Description:   "Fail",
			InputSchema:   objectSchema(),
			OutputSchema:  objectSchema(),
			Outcome:       scripted.FunctionOutcomeUnknown,
			ReasonCode:    "simulated",
			EchoArguments: true,
		},
	}
	for name, spec := range invalid {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := scripted.NewFunctionRuntime([]scripted.FunctionSpec{spec}); err == nil {
				t.Fatal("invalid function accepted")
			}
		})
	}
	duplicate := []scripted.FunctionSpec{echoFunctionSpec(), echoFunctionSpec()}
	if err := scripted.ValidateFunctions(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate function keys = %v", err)
	}
	tooMany := make([]scripted.FunctionSpec, 65)
	for index := range tooMany {
		tooMany[index] = echoFunctionSpec()
		tooMany[index].Key = "inspect" + strings.Repeat("a", index%5)
	}
	if err := scripted.ValidateFunctions(tooMany); err == nil || !strings.Contains(err.Error(), "at most 64") {
		t.Fatalf("oversized catalog = %v", err)
	}
}

func TestFunctionRuntimeDescriptorsArePrepared(t *testing.T) {
	t.Parallel()
	if _, err := scripted.NewFunctionRuntime([]scripted.FunctionSpec{{
		Key: " inspect ", Description: "Inspect",
		InputSchema: objectSchema(), OutputSchema: objectSchema(), EchoArguments: true,
	}}); err == nil {
		t.Fatal("key with whitespace accepted")
	}
	runtime, err := scripted.NewFunctionRuntime([]scripted.FunctionSpec{{
		Key: "inspect", Description: "  Inspect the arguments  ",
		InputSchema: objectSchema(), OutputSchema: objectSchema(), EchoArguments: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	functions := runtime.Functions()
	if len(functions) != 1 {
		t.Fatalf("functions = %d", len(functions))
	}
	descriptor := functions[0].Descriptor
	if descriptor.Key != "inspect" || descriptor.Description != "Inspect the arguments" ||
		descriptor.TimeoutMS != 1000 {
		t.Fatalf("prepared descriptor = %+v", descriptor)
	}
	var input, output map[string]any
	if json.Unmarshal(descriptor.InputSchema, &input) != nil ||
		json.Unmarshal(descriptor.OutputSchema, &output) != nil {
		t.Fatalf("schemas are not JSON: %s %s", descriptor.InputSchema, descriptor.OutputSchema)
	}
	if input["type"] != "object" || output["type"] != "object" {
		t.Fatalf("normalized schemas changed their root type: %s %s", descriptor.InputSchema, descriptor.OutputSchema)
	}
	if functions[0].Handler == nil {
		t.Fatal("prepared function has no handler")
	}
}

func TestFunctionRuntimeReturnsConfiguredOutcomes(t *testing.T) {
	t.Parallel()
	arguments := json.RawMessage(`{"sequence":9007199254740993,"tiny":1e-10000}`)
	cases := map[string]struct {
		spec scripted.FunctionSpec
		run  func(t *testing.T, result adapter.FunctionResult)
	}{
		"static success": {
			spec: scripted.FunctionSpec{
				Key:         "ready",
				Description: "Ready",
				InputSchema: objectSchema(),
				OutputSchema: map[string]any{
					"type":                 "object",
					"required":             []any{"ready"},
					"properties":           map[string]any{"ready": map[string]any{"type": "boolean"}},
					"additionalProperties": false,
				},
				Result: map[string]any{"ready": true},
			},
			run: func(t *testing.T, result adapter.FunctionResult) {
				succeeded, ok := result.(adapter.FunctionSucceeded)
				if !ok || string(succeeded.Result) != `{"ready":true}` {
					t.Fatalf("static success = %#v", result)
				}
			},
		},
		"echo preserves exact arguments": {
			spec: echoFunctionSpec(),
			run: func(t *testing.T, result adapter.FunctionResult) {
				succeeded, ok := result.(adapter.FunctionSucceeded)
				if !ok || string(succeeded.Result) != string(arguments) {
					t.Fatalf("echo = %#v", result)
				}
			},
		},
		"failed": {
			spec: terminalFunctionSpec(scripted.FunctionOutcomeFailed, "simulated_failure"),
			run: func(t *testing.T, result adapter.FunctionResult) {
				failed, ok := result.(adapter.FunctionFailed)
				if !ok || failed.ReasonCode != "simulated_failure" {
					t.Fatalf("failed = %#v", result)
				}
			},
		},
		"not executed": {
			spec: terminalFunctionSpec(scripted.FunctionOutcomeNotExecuted, "simulated_unavailable"),
			run: func(t *testing.T, result adapter.FunctionResult) {
				notExecuted, ok := result.(adapter.FunctionNotExecuted)
				if !ok || notExecuted.ReasonCode != "simulated_unavailable" {
					t.Fatalf("not executed = %#v", result)
				}
			},
		},
		"unknown": {
			spec: terminalFunctionSpec(scripted.FunctionOutcomeUnknown, "simulated_unknown"),
			run: func(t *testing.T, result adapter.FunctionResult) {
				unknown, ok := result.(adapter.FunctionUnknown)
				if !ok || unknown.ReasonCode != "simulated_unknown" {
					t.Fatalf("unknown = %#v", result)
				}
			},
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runtime, err := scripted.NewFunctionRuntime([]scripted.FunctionSpec{testCase.spec})
			if err != nil {
				t.Fatal(err)
			}
			result := runtime.Functions()[0].Handler(context.Background(), adapter.FunctionRequest{
				InvocationID: "afi_test", FunctionKey: testCase.spec.Key, Arguments: arguments,
			})
			testCase.run(t, result)
		})
	}
}

// A held function blocks until released. Release stays idempotent only while
// the handler is still running; Core, not this runtime, owns completion history.
func TestFunctionRuntimeHoldRelease(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		runtime, err := scripted.NewFunctionRuntime([]scripted.FunctionSpec{{
			Key: "hold", Description: "Hold", InputSchema: objectSchema(), OutputSchema: objectSchema(), Hold: true,
		}})
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan adapter.FunctionResult, 1)
		go func() {
			result <- runtime.Functions()[0].Handler(context.Background(), adapter.FunctionRequest{InvocationID: "afi_held"})
		}()
		synctest.Wait()
		if err = runtime.Release("afi_held"); err != nil {
			t.Fatal(err)
		}
		if err = runtime.Release("afi_held"); err != nil {
			t.Fatalf("repeated release while held = %v", err)
		}
		synctest.Wait()
		succeeded, ok := (<-result).(adapter.FunctionSucceeded)
		if !ok || string(succeeded.Result) != `{}` {
			t.Fatalf("released result = %#v", succeeded)
		}
		if err = runtime.Release("afi_held"); !errors.Is(err, scripted.ErrUnknownFunctionInvocation) {
			t.Fatalf("release after completion = %v", err)
		}
	})
}

// Cancellation releases an admitted handler without an external release and
// resolves it as unknown, because the simulated work may have already begun.
func TestFunctionRuntimeHoldCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		runtime, err := scripted.NewFunctionRuntime([]scripted.FunctionSpec{{
			Key: "hold", Description: "Hold", InputSchema: objectSchema(), OutputSchema: objectSchema(), Hold: true,
		}})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan adapter.FunctionResult, 1)
		go func() {
			result <- runtime.Functions()[0].Handler(ctx, adapter.FunctionRequest{InvocationID: "afi_held"})
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		unknown, ok := (<-result).(adapter.FunctionUnknown)
		if !ok || unknown.ReasonCode != "outcome_timeout" {
			t.Fatalf("cancelled result = %#v", unknown)
		}
		if err = runtime.Release("afi_held"); !errors.Is(err, scripted.ErrUnknownFunctionInvocation) {
			t.Fatalf("release after cancellation = %v", err)
		}
	})
}

// App shutdown must join held handlers before the Session closes. Stop unblocks
// every hold once and resolves it as unknown, because admitted work may have
// begun.
func TestFunctionRuntimeStopReleasesHeldInvocations(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		runtime, err := scripted.NewFunctionRuntime([]scripted.FunctionSpec{{
			Key: "hold", Description: "Hold", InputSchema: objectSchema(), OutputSchema: objectSchema(), Hold: true,
		}})
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan adapter.FunctionResult, 1)
		go func() {
			result <- runtime.Functions()[0].Handler(context.Background(), adapter.FunctionRequest{InvocationID: "afi_held"})
		}()
		synctest.Wait()
		runtime.Stop()
		runtime.Stop()
		synctest.Wait()
		unknown, ok := (<-result).(adapter.FunctionUnknown)
		if !ok || unknown.ReasonCode != "adapter_stopping" {
			t.Fatalf("stopped result = %#v", unknown)
		}
		if err = runtime.Release("afi_held"); !errors.Is(err, scripted.ErrUnknownFunctionInvocation) {
			t.Fatalf("release after stop = %v", err)
		}
	})
}

func TestFunctionRuntimePendingHolds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		runtime, err := scripted.NewFunctionRuntime([]scripted.FunctionSpec{{
			Key: "hold", Description: "Hold", InputSchema: objectSchema(), OutputSchema: objectSchema(), Hold: true,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if pending := runtime.Pending(); len(pending) != 0 {
			t.Fatalf("initial pending holds = %v", pending)
		}
		first, second := make(chan adapter.FunctionResult, 1), make(chan adapter.FunctionResult, 1)
		go func() {
			first <- runtime.Functions()[0].Handler(context.Background(), adapter.FunctionRequest{InvocationID: "afi_first"})
		}()
		go func() {
			second <- runtime.Functions()[0].Handler(context.Background(), adapter.FunctionRequest{InvocationID: "afi_second"})
		}()
		synctest.Wait()
		if pending := runtime.Pending(); !slices.Equal(pending, []string{"afi_first", "afi_second"}) {
			t.Fatalf("pending holds = %v", pending)
		}
		if err = runtime.Release("afi_first"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if pending := runtime.Pending(); !slices.Equal(pending, []string{"afi_second"}) {
			t.Fatalf("pending after first release = %v", pending)
		}
		if err = runtime.Release("afi_second"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if pending := runtime.Pending(); len(pending) != 0 {
			t.Fatalf("pending after second release = %v", pending)
		}
	})
}

func TestFunctionRuntimeConcurrentHoldsReleaseIndependently(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		runtime, err := scripted.NewFunctionRuntime([]scripted.FunctionSpec{{
			Key: "hold", Description: "Hold", InputSchema: objectSchema(), OutputSchema: objectSchema(), Hold: true,
		}})
		if err != nil {
			t.Fatal(err)
		}
		first, second := make(chan adapter.FunctionResult, 1), make(chan adapter.FunctionResult, 1)
		go func() {
			first <- runtime.Functions()[0].Handler(context.Background(), adapter.FunctionRequest{InvocationID: "afi_first"})
		}()
		go func() {
			second <- runtime.Functions()[0].Handler(context.Background(), adapter.FunctionRequest{InvocationID: "afi_second"})
		}()
		synctest.Wait()
		if err = runtime.Release("afi_first"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if _, ok := (<-first).(adapter.FunctionSucceeded); !ok {
			t.Fatal("first hold did not complete")
		}
		select {
		case <-second:
			t.Fatal("second hold completed without its own release")
		default:
		}
		if err = runtime.Release("afi_second"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if _, ok := (<-second).(adapter.FunctionSucceeded); !ok {
			t.Fatal("second hold did not complete")
		}
		if err = runtime.Release("afi_missing"); !errors.Is(err, scripted.ErrUnknownFunctionInvocation) {
			t.Fatalf("unknown release = %v", err)
		}
	})
}

func TestFunctionRuntimeEmptyCatalogIsValid(t *testing.T) {
	t.Parallel()
	runtime, err := scripted.NewFunctionRuntime(nil)
	if err != nil {
		t.Fatal(err)
	}
	if functions := runtime.Functions(); len(functions) != 0 {
		t.Fatalf("empty catalog = %d functions", len(functions))
	}
	if err = runtime.Release("afi_missing"); !errors.Is(err, scripted.ErrUnknownFunctionInvocation) {
		t.Fatalf("release with empty catalog = %v", err)
	}
}
