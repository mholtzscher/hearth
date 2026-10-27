package exprvalue_test

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
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

func TestStrictLookupsAndLaziness(t *testing.T) {
	t.Parallel()
	vars := map[string]any{"present": nil, "key": "missing", "array": []any{int64(7)}, "object": map[string]any{}}
	for _, source := range []string{
		`vars.missing != true`, `steps.forecast.output`, `vars.object.missing`, `vars[vars.key]`,
		`vars.missing ?? false`, `get(vars, "x", vars.missing)`, `get(vars.missing, "x", false)`,
		`all([{ok: true}, {}], {.ok})`, `map([{x: 1}, {}], {#.x})`,
		`filter([{}], {get(#, "x", .missing)})`, `vars.array[1]`,
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			program, err := exprvalue.Compile(source)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			_, err = exprvalue.Evaluate(program, exprvalue.Environment{Vars: vars})
			failure := failureCode(t, err, "expression_missing")
			if failure.Start < 0 || failure.End > len(source) || failure.End <= failure.Start {
				t.Fatalf("invalid source span: %+v", failure)
			}
		})
	}
	for source, want := range map[string]string{
		`has(vars, "present")`: `true`, `get(vars, "present", false)`: `null`,
		`vars.present ?? false`: `false`, `get(vars, "missing", false) != true`: `true`,
		`false && vars.missing`: `false`, `true || vars.missing`: `true`,
		`has(steps, "forecast") ? steps.forecast.output : []`: `[]`,
		`get(get(vars, "missing", null), "nested", 3)`:        `3`,
		`has(vars.array, 0)`:                                  `true`, `get(vars.array, 1, 9)`: `9`,
		`map(filter([{rain: true, temperature: 4}, {rain: false, temperature: 7}], {.rain}), {.temperature})`: `[4]`,
		`all([1, 2], {# > 0})`: `true`, `map([5, 7], {#index + #})`: `[5,8]`,
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			if got := string(evaluate(t, source, vars).JSON); got != want {
				t.Fatalf("got %s; want %s", got, want)
			}
		})
	}
	for _, source := range []string{`vars.present.x`, `vars.array["x"]`, `get(4, "x", null)`, `has(vars.array, -1)`} {
		program, err := exprvalue.Compile(source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = exprvalue.Evaluate(program, exprvalue.Environment{Vars: vars})
		failureCode(t, err, "expression_type")
	}
}

//nolint:gocognit // Positive typed results and explicit rejection cases form one numeric contract matrix.
func TestNumericContract(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]any{
		`9223372036854775807`: int64(math.MaxInt64), `-9223372036854775808`: int64(math.MinInt64),
		`9007199254740993`: int64(9007199254740993), `1.0`: int64(1), `1e3`: int64(1000),
		`1.5`: float64(1.5), `0e1000000000`: int64(0), `1.2300`: float64(1.23),
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			value, err := exprvalue.DecodeValue(json.RawMessage(raw))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(value.Data, want) {
				t.Fatalf("got %#v (%T); want %#v", value.Data, value.Data, want)
			}
			result := evaluate(t, `vars.n`, map[string]any{"n": value.Data})
			if !reflect.DeepEqual(result.Data, want) || string(result.JSON) != string(value.JSON) {
				t.Fatalf("lost numeric type/value: %+v", result)
			}
		})
	}
	for _, raw := range []string{`9223372036854775808`, `-9223372036854775809`, `9223372036854775808.0`, `1e1000000000`, `1e-1000000000`, `1e-400`, `1e309`, `NaN`, `01`, `1 2`, `{"x":1,"x":2}`, `{"a":{"x":1,"x":2}}`} {
		_, err := exprvalue.DecodeValue(json.RawMessage(raw))
		failureCode(t, err, "expression_value_invalid")
	}
	for source, want := range map[string]any{
		`9223372036854775807 + 1`: int64(math.MinInt64), `-9223372036854775807 - 2`: int64(math.MaxInt64),
		`9223372036854775807 * 2`: int64(-2), `-(-9223372036854775807 - 1)`: int64(math.MinInt64),
		`sum([9223372036854775807, 1])`: int64(math.MinInt64), `3 / 1`: float64(3),
		`sum([{x: 9223372036854775807}, {x: 1}], {.x})`: int64(math.MinInt64),
		`5 % 2`: int64(1), `3 + 0.5`: float64(3.5), `int(-3.9)`: int64(-3),
		`round(3.2)`: float64(3), `int(round(3.7))`: int64(4), `float(3)`: float64(3),
		`min(9007199254740993, 9007199254740992)`: int64(9007199254740992),
		`max(1, 3, 2)`: int64(3), `len([1, 2])`: int64(2),
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			if got := evaluate(t, source, nil).Data; !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v (%T); want %#v (%T)", got, got, want, want)
			}
		})
	}
	for _, source := range []string{`9223372036854775808`, `-9223372036854775808`} {
		if _, err := exprvalue.Compile(source); err == nil {
			t.Fatalf("accepted integer overflow: %s", source)
		}
	}
	for _, source := range []string{`1 / 0`, `0 / 0`, `1 % 0`, `int(9223372036854775807.0)`, `int("2")`, `float("2")`, `1e308 * 2`, `(1 / 0) > 0`, `(1e308 * 2) == (1e308 * 2)`} {
		program, err := exprvalue.Compile(source)
		if err != nil {
			t.Fatalf("compile %s: %v", source, err)
		}
		if _, evalErr := exprvalue.Evaluate(program, exprvalue.Environment{}); evalErr == nil {
			t.Fatalf("accepted invalid numeric result: %s", source)
		}
	}
}

func TestLanguagePolicy(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`let x = 1; x`, `abs(-1)`, `ceil(1.2)`, `floor(1.2)`, `lower("A")`, `upper("a")`,
		`trim(" a ")`, `split("a", ",")`, `replace("a", "a", "b")`, `count([1])`,
		`any([true], {#})`, `none([true], {#})`, `one([true], {#})`, `now()`, `date("2020-01-01")`,
		`toJSON(vars)`, `fromJSON("{}")`, `sort([2,1])`, `groupBy([1], {#})`, `flatten([[1]])`,
		`unknown`, `$env`, `$env.vars`, `__hearth_0(vars, "x")`, `vars.f()`, `vars["f"]()`,
		`vars?.x`, `vars.x?.y`, `[1, 2][0:1]`, `1..128`, `2 ** 3`, `2 ^ 3`,
		`"a" matches "a"`, `::len([])`, `string`, `get`, `vars.x = 1`,
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			if _, err := exprvalue.Compile(source); err == nil {
				t.Fatal("forbidden source accepted")
			}
		})
	}
	if got := string(
		evaluate(t, `join([string(2), string(true), "let ?. :: $env"], ":")`, nil).JSON,
	); got != `"2:true:let ?. :: $env"` {
		t.Fatal(got)
	}
	program, err := exprvalue.Compile(`get(vars, "x", null) ?? steps["forecast"].output`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(program.Roots(), []string{"steps", "vars"}) ||
		!reflect.DeepEqual(program.StepReferences(), []string{"forecast"}) {
		t.Fatalf("wrong inspection: %v %v", program.Roots(), program.StepReferences())
	}
	program.Roots()[0] = "corrupt"
	if program.Roots()[0] != "steps" {
		t.Fatal("inspection leaked mutable storage")
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
	for _, host := range []any{func() {}, struct{ X int }{1}, []string{"x"}, math.Inf(1), math.NaN()} {
		_, err = exprvalue.Evaluate(program, exprvalue.Environment{Vars: map[string]any{"host": host}})
		failureCode(t, err, "expression_value_invalid")
	}
}

func TestFailureSpansUseBytes(t *testing.T) {
	t.Parallel()
	source := `"é" == "é" ? vars.secret : null`
	program, err := exprvalue.Compile(source)
	if err != nil {
		t.Fatal(err)
	}
	_, err = exprvalue.Evaluate(program, exprvalue.Environment{})
	failure := failureCode(t, err, "expression_missing")
	if got := source[failure.Start:failure.End]; got != "secret" {
		t.Fatalf("span = %q (%+v)", got, failure)
	}
}

//nolint:gocognit // The workload-size matrix includes exact result checks, not just timed calls.
func BenchmarkCollectionComposition(b *testing.B) {
	for name, source := range map[string]string{
		"NestedAll": `all(vars.items, {all(vars.items, {.ok})})`,
		"FilterMap": `map(filter(vars.items, {.ok}), {.n * 2})`,
		"NestedMap": `all(vars.items, {len(map(vars.items, {.n + 1})) == len(vars.items)})`,
	} {
		for _, size := range []int{1, 16, 128} {
			b.Run(name+"/"+strconv.Itoa(size), func(b *testing.B) {
				// Prepared program, fresh VM and owned/validated environment each call;
				// largest admitted two-predicate composition does size² element visits.
				program, err := exprvalue.Compile(source)
				if err != nil {
					b.Fatal(err)
				}
				items := make([]any, size)
				for i := range items {
					items[i] = map[string]any{"ok": true, "n": int64(1)}
				}
				env := exprvalue.Environment{Vars: map[string]any{"items": items}}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					value, evalErr := exprvalue.Evaluate(program, env)
					if evalErr != nil {
						b.Fatalf("evaluation = %v, %v", value.Data, evalErr)
					}
					if name == "FilterMap" {
						array, ok := value.Data.([]any)
						if !ok || len(array) != size || array[0] != int64(2) {
							b.Fatalf("wrong mapped result: %v", value.Data)
						}
						for _, item := range array {
							if item != int64(2) {
								b.Fatalf("wrong mapped item: %v", item)
							}
						}
					} else if value.Data != true {
						b.Fatalf("wrong predicate result: %v", value.Data)
					}
				}
			})
		}
	}
}

func BenchmarkNestedGuardedConstruction(b *testing.B) {
	for _, size := range []int{1, 16, 128} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			// Near-cap valid input repeatedly enters a guarded singleton array,
			// then len discards it. This exercises value scans as well as size²
			// predicate visits, not just the cheap boolean-predicate workload.
			program, err := exprvalue.Compile(`all(vars.items, {all(vars.items, {len([vars.payload]) == 1})})`)
			if err != nil {
				b.Fatal(err)
			}
			items := make([]any, size)
			for i := range items {
				items[i] = true
			}
			env := exprvalue.Environment{Vars: map[string]any{"items": items, "payload": strings.Repeat("x", 65529)}}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				value, evalErr := exprvalue.Evaluate(program, env)
				if evalErr != nil || value.Data != true {
					b.Fatalf("evaluation = %v, %v", value.Data, evalErr)
				}
			}
		})
	}
}

// Deterministic malformed-input corpus replaces a maintained fuzz target per
// docs/testing.md; each input exercises the public decoder/compiler boundary.
func TestMalformedCorpus(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "{", "[", "null x", "\xff", `{"x":}`, `[1,]`, `"\u0000"`} {
		_, err := exprvalue.DecodeValue(json.RawMessage(raw))
		if raw == `"\u0000"` {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("accepted malformed JSON %q", raw)
		}
	}
	for _, source := range []string{"", "  ", "\xff", "(", "[", "vars[", "all([], {", strings.Repeat("(", 100) + "1"} {
		if _, err := exprvalue.Compile(source); err == nil {
			t.Fatalf("accepted malformed expression %q", source)
		}
	}
}
