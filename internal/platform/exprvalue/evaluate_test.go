package exprvalue_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mholtzscher/hearth/internal/platform/exprvalue"
)

func TestSelection(t *testing.T) {
	t.Parallel()
	input, err := exprvalue.DecodeValue(json.RawMessage(`{"n":9007199254740993,"present":null}`))
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]any{"sample": map[string]any{"value": input.Data}}
	for source, want := range map[string]string{
		`sample.value.n`: `9007199254740993`,
		`{number: sample.value.n, missing: sample.value.absent}`: `{"missing":null,"number":9007199254740993}`,
		`sample.value.present`:          `null`,
		`sample.value?.n`:               `9007199254740993`,
		`sample.value?.absent`:          `null`,
		`sample.value?.absent?.nested`:  `null`,
		`sample.value?.present?.nested`: `null`,
	} {
		program, compileErr := exprvalue.CompileJSON(source, "sample")
		if compileErr != nil {
			t.Fatal(compileErr)
		}
		value, evalErr := exprvalue.Evaluate(program, env)
		if evalErr != nil || string(value.JSON) != want {
			t.Fatalf("%s = %s, %v", source, value.JSON, evalErr)
		}
		if source == `sample.value.n` && value.Data != int64(9007199254740993) {
			t.Fatalf("lost integer: %#v", value.Data)
		}
	}
	if _, err = exprvalue.CompileJSON(`run.id`, "sample"); err == nil {
		t.Fatal("unexpected root accepted")
	}
}

func TestPredicate(t *testing.T) {
	t.Parallel()
	predicate, err := exprvalue.CompileJSON(`value == true`, "value")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []bool{true, false} {
		value, evalErr := exprvalue.Evaluate(predicate, map[string]any{"value": expected})
		if evalErr != nil || value.Data != expected {
			t.Fatalf("predicate = %#v, %v", value.Data, evalErr)
		}
	}
	calculation, err := exprvalue.CompileJSON(`value + 1`, "value")
	if err != nil {
		t.Fatal(err)
	}
	value, err := exprvalue.Evaluate(calculation, map[string]any{"value": int64(2)})
	if err != nil || value.Data != int64(3) {
		t.Fatalf("dynamic value arithmetic: %#v, %v", value.Data, err)
	}
}

func TestOwnershipAndConcurrentEvaluation(t *testing.T) {
	t.Parallel()
	program, err := exprvalue.Compile(`sample.value`, map[string]any{"sample": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]any{"sample": map[string]any{"value": []any{int64(7)}}}
	value, err := exprvalue.Evaluate(program, env)
	if err != nil {
		t.Fatal(err)
	}
	value.Data.([]any)[0] = int64(8)
	if !reflect.DeepEqual(env["sample"].(map[string]any)["value"], []any{int64(7)}) {
		t.Fatal("result aliases input")
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			got, evalErr := exprvalue.Evaluate(program, env)
			if evalErr != nil || string(got.JSON) != `[7]` {
				t.Errorf("concurrent evaluation: %s %v", got.JSON, evalErr)
			}
		})
	}
	wg.Wait()
}

func TestSafeFailures(t *testing.T) {
	t.Parallel()
	program, err := exprvalue.Compile(`sample.value`, map[string]any{"sample": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		input any
		code  string
	}{
		{func() {}, "expression_value_invalid"},
		{strings.Repeat("x", 65536), "expression_limit"},
	} {
		_, evalErr := exprvalue.Evaluate(program, map[string]any{"sample": test.input})
		var failure *exprvalue.Failure
		if !errors.As(evalErr, &failure) || failure.Code != test.code || evalErr.Error() != test.code {
			t.Fatalf("unsafe or wrong failure: %v", evalErr)
		}
	}
	_, err = exprvalue.Compile(`unknown + "secret"`, map[string]any{"value": any(nil)})
	var failure *exprvalue.Failure
	if !errors.As(err, &failure) || failure.Code != "expression_type" || strings.Contains(err.Error(), "secret") ||
		!failure.Located {
		t.Fatalf("compile failure: %v", err)
	}
}

func TestVMBudgetAndSafeRuntimeFailure(t *testing.T) {
	t.Parallel()
	schema := map[string]any{"items": []any{}, "value": any(nil)}
	program, err := exprvalue.Compile(`all(items, {all(items, {len([1,2,3]) > 0})})`, schema)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]any, 128)
	for i := range items {
		items[i] = int64(1)
	}
	_, err = exprvalue.Evaluate(program, map[string]any{"items": items})
	var failure *exprvalue.Failure
	if !errors.As(err, &failure) || failure.Code != "expression_limit" {
		t.Fatalf("VM budget failure: %v", err)
	}
	program, err = exprvalue.Compile(`sample.value + 1`, map[string]any{"sample": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = exprvalue.Evaluate(program, map[string]any{"sample": map[string]any{"value": "private"}})
	if !errors.As(err, &failure) || failure.Code != "expression_evaluation" || err.Error() != "expression_evaluation" {
		t.Fatalf("runtime failure: %v", err)
	}
}
