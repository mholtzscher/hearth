package exprvalue_test

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
)

func evaluate(t *testing.T, source string, vars map[string]any) exprvalue.Value {
	t.Helper()
	program, err := exprvalue.Compile(source)
	if err != nil {
		t.Fatalf("compile %q: %v", source, err)
	}
	value, err := exprvalue.Evaluate(program, exprvalue.Environment{Vars: vars})
	if err != nil {
		t.Fatalf("evaluate %q: %v", source, err)
	}
	return value
}

func failureCode(t *testing.T, err error, code string) *exprvalue.Failure {
	t.Helper()
	var failure *exprvalue.Failure
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("error = %v; want %s", err, code)
	}
	if err.Error() != code {
		t.Fatalf("unsafe failure text: %q", err.Error())
	}
	return failure
}

func TestNativeSemantics(t *testing.T) {
	t.Parallel()
	vars := map[string]any{"present": nil, "key": "missing", "object": map[string]any{}}
	for source, want := range map[string]string{
		`vars.missing != true`: `true`, `vars[vars.key]`: `null`,
		`vars.missing ?? false`: `false`, `vars.present ?? false`: `false`,
		`"present" in vars`: `true`, `"missing" in vars`: `false`,
		`steps.forecast?.output ?? []`: `[]`, `vars.missing?.nested`: `null`,
		`false && vars.missing`: `false`, `true || vars.missing`: `true`,
		`let target = 2; {value: target ** 3}`: `{"value":8}`,
		`map(filter([{rain: true, temperature: 4}, {rain: false, temperature: 7}], {.rain}), {.temperature})`: `[4]`,
		`all([1, 2], {# > 0})`: `true`, `map([5, 7], {#index + #})`: `[5,8]`,
		`all([1], {all([1], {all([1], {true})})})`: `true`,
		`int("2") + float("2.5")`:                  `4.5`, `upper("hello")`: `"HELLO"`,
		`split("a,b", ",")`: `["a","b"]`, `1..3`: `[1,2,3]`,
		`sort([3,1,2])`: `[1,2,3]`, `len(vars.object)`: `0`,
		`nil`: `null`, `fromJSON('{"value": 2}')`: `{"value":2}`,
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			if got := string(evaluate(t, source, vars).JSON); got != want {
				t.Fatalf("got %s; want %s", got, want)
			}
		})
	}
	for _, source := range []string{`get(vars, "x", 1)`, `has(vars, "x")`, `now()`, `::now()`, `date("2026-01-01")`, `timezone("UTC")`, `vars.x = 1`} {
		if _, err := exprvalue.Compile(source); err == nil {
			t.Fatalf("accepted unavailable source: %s", source)
		}
	}
}

func TestInitialEnvironment(t *testing.T) {
	t.Parallel()
	for _, source := range []string{`vars.x`, `steps.read.output`, `false ? vars.x : 1`, `$env.vars.x`} {
		if _, err := exprvalue.CompileInitial(source); err == nil {
			t.Fatalf("initial context exposed execution roots: %s", source)
		}
	}
	if _, err := exprvalue.CompileInitial(`let vars = 1; vars + 1`); err != nil {
		t.Fatalf("local name mistaken for an environment dependency: %v", err)
	}
	program, err := exprvalue.CompileInitial(`{id: run.id, kind: trigger.kind}`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = exprvalue.Evaluate(
		program,
		exprvalue.Environment{Run: map[string]any{"id": "r"}, Trigger: map[string]any{"kind": "manual"}},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestExactJSONIngress(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]any{
		`9223372036854775807`: int64(math.MaxInt64), `-9223372036854775808`: int64(math.MinInt64),
		`9007199254740993`: int64(9007199254740993), `1.0`: float64(1), `1e3`: float64(1000),
		`1.5`: float64(1.5), `0e1000000000`: float64(0), `1.2300`: float64(1.23),
		`9007199254740993.0`: float64(9007199254740992), `9.007199254740993e15`: float64(9007199254740992),
		`9223372036854775808.0`: float64(9223372036854775808),
	} {
		value, err := exprvalue.DecodeValue(json.RawMessage(raw))
		if err != nil || !reflect.DeepEqual(value.Data, want) {
			t.Fatalf("decode %s = %#v, %v", raw, value.Data, err)
		}
		result := evaluate(t, `vars.n`, map[string]any{"n": value.Data})
		if !reflect.DeepEqual(result.Data, want) || string(result.JSON) != string(value.JSON) {
			t.Fatalf("lost numeric type/value: %+v", result)
		}
	}
	for _, raw := range []string{`9223372036854775808`, `-9223372036854775809`, `1e1000000000`, `1e-1000000000`, `1e-400`, `1e309`, `NaN`, `01`, `1 2`, `{"x":1,"x":2}`, `{"a":{"x":1,"x":2}}`} {
		_, err := exprvalue.DecodeValue(json.RawMessage(raw))
		failureCode(t, err, "expression_value_invalid")
	}
	for _, source := range []string{`1 / 0`, `0 / 0`, `1e308 * 2`} {
		program, err := exprvalue.Compile(source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = exprvalue.Evaluate(program, exprvalue.Environment{})
		failureCode(t, err, "expression_value_invalid")
	}
	// Arithmetic is native Expr, not a guarded Hearth numeric dialect.
	if got := evaluate(t, `9223372036854775807 + 1`, nil).Data; got != int64(math.MinInt64) {
		t.Fatalf("native wrap = %v", got)
	}
	if evaluate(t, `(1 / 0) > 0`, nil).Data != true {
		t.Fatal("intermediate native numeric semantics changed")
	}
}

func TestOwnershipAndConcurrentReuse(t *testing.T) {
	t.Parallel()
	vars := map[string]any{"array": []any{map[string]any{"value": int64(7)}}}
	value := evaluate(t, `vars.array`, vars)
	value.Data.([]any)[0].(map[string]any)["value"] = int64(100)
	if vars["array"].([]any)[0].(map[string]any)["value"] != int64(7) {
		t.Fatal("result aliases caller environment")
	}
	program, err := exprvalue.Compile(`map(vars.array, {.value + 1})`)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			got, evalErr := exprvalue.Evaluate(program, exprvalue.Environment{Vars: vars})
			if evalErr != nil || string(got.JSON) != `[8]` {
				t.Errorf("concurrent result: %s %v", got.JSON, evalErr)
			}
		})
	}
	wg.Wait()
	for _, host := range []any{func() {}, struct{ X int }{1}, math.Inf(1), math.NaN()} {
		_, err = exprvalue.Evaluate(program, exprvalue.Environment{Vars: map[string]any{"host": host}})
		failureCode(t, err, "expression_value_invalid")
	}
}

func TestCompileFailureSpansUseBytes(t *testing.T) {
	t.Parallel()
	source := `"é" == "é" ? unknown : nil`
	_, err := exprvalue.Compile(source)
	failure := failureCode(t, err, "expression_type")
	if failure.Start < 0 || failure.End > len(source) || failure.Start >= failure.End ||
		!strings.Contains(source[failure.Start:failure.End], "unknown") {
		t.Fatalf("invalid byte span: %+v", failure)
	}
}

func TestMalformedCorpus(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "{", "[", "null x", "\xff", `{"x":}`, `[1,]`} {
		if _, err := exprvalue.DecodeValue(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted malformed JSON %q", raw)
		}
	}
	for _, source := range []string{"", "  ", "\xff", "(", "[", "vars[", "all([], {", strings.Repeat("(", 100) + "1"} {
		if _, err := exprvalue.Compile(source); err == nil {
			t.Fatalf("accepted malformed expression %q", source)
		}
	}
}
