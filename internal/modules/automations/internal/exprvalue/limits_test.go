package exprvalue_test

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
)

func TestSourceAndASTBounds(t *testing.T) {
	t.Parallel()
	for _, count := range []int{4096, 4097} {
		_, err := exprvalue.Compile("1" + strings.Repeat(" ", count-1))
		if count == 4096 {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			failureCode(t, err, "expression_limit")
		}
	}
	// Array plus 128 integer children is 129 nodes. A balanced expression has
	// shallow depth so the 256-node ceiling, rather than depth, is decisive.
	var balanced func(int) string
	balanced = func(leaves int) string {
		if leaves == 1 {
			return "1"
		}
		return "(" + balanced(leaves/2) + "+" + balanced(leaves-leaves/2) + ")"
	}
	if _, err := exprvalue.Compile("[" + balanced(127) + ",+1]"); err != nil {
		t.Fatalf("256 nodes rejected: %v", err)
	}
	if _, err := exprvalue.Compile("[" + balanced(128) + ",1]"); err == nil {
		t.Fatal("258 nodes accepted")
	} else {
		failureCode(t, err, "expression_limit")
	}
	if got := string(evaluate(t, `map(map([1], {#}), {#})`, nil).JSON); got != `[1]` {
		t.Fatal(got)
	}
}

//nolint:gocognit // The public decoder is tested at and beyond each independent value bound.
func TestValueBounds(t *testing.T) {
	t.Parallel()
	for _, count := range []int{128, 129} {
		array := "[" + strings.Repeat("0,", count-1) + "0]"
		program, compileErr := exprvalue.Compile(array)
		if compileErr != nil {
			t.Fatal(compileErr)
		}
		_, outputErr := exprvalue.Evaluate(program, exprvalue.Environment{})
		if count == 128 && outputErr != nil {
			t.Fatal(outputErr)
		} else if count == 129 {
			failureCode(t, outputErr, "expression_limit")
		}
		_, err := exprvalue.DecodeValue(json.RawMessage(array))
		if count == 128 {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			failureCode(t, err, "expression_limit")
		}
		members := make([]string, count)
		for i := range members {
			members[i] = fmt.Sprintf(`"k%d":0`, i)
		}
		_, err = exprvalue.DecodeValue(json.RawMessage("{" + strings.Join(members, ",") + "}"))
		if count == 128 {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			failureCode(t, err, "expression_limit")
		}
	}
	for _, depth := range []int{16, 17} {
		raw := strings.Repeat("[", depth-1) + "0" + strings.Repeat("]", depth-1)
		_, err := exprvalue.DecodeValue(json.RawMessage(raw))
		if depth == 16 {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			failureCode(t, err, "expression_limit")
		}
	}
	for _, size := range []int{65536, 65537} {
		_, err := exprvalue.DecodeValue(json.RawMessage(`"` + strings.Repeat("x", size-2) + `"`))
		if size == 65536 {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			failureCode(t, err, "expression_limit")
		}
	}
	// Exactly 8192 value nodes: root + 64 arrays + 8127 leaves.
	for _, leaves := range []int{8127, 8128} {
		groups := make([]any, 64)
		remaining := leaves
		for i := range groups {
			count := min(128, remaining)
			remaining -= count
			items := make([]any, count)
			for j := range items {
				items[j] = int64(0)
			}
			groups[i] = items
		}
		raw, _ := json.Marshal(groups)
		_, err := exprvalue.DecodeValue(raw)
		if leaves == 8127 {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			failureCode(t, err, "expression_limit")
		}
	}
}

func TestEnvironmentEnvelopesPreserveValueBounds(t *testing.T) {
	t.Parallel()
	large := strings.Repeat("x", 65534)
	deep := any(int64(1))
	for range 15 {
		deep = []any{deep}
	}
	for source, env := range map[string]exprvalue.Environment{
		`vars.value`:         {Vars: map[string]any{"value": large}},
		`vars.deep`:          {Vars: map[string]any{"deep": deep}},
		`steps.state.output`: {Steps: map[string]any{"state": map[string]any{"output": large}}},
	} {
		program, err := exprvalue.Compile(source)
		if err != nil {
			t.Fatal(err)
		}
		value, evalErr := exprvalue.Evaluate(program, env)
		if evalErr != nil {
			t.Fatalf("context envelope consumed fragment allowance: %v", evalErr)
		}
		if source != `vars.deep` && len(value.JSON) != 65536 {
			t.Fatalf("value bytes = %d", len(value.JSON))
		}
	}
	program, err := exprvalue.Compile(`true`)
	if err != nil {
		t.Fatal(err)
	}
	root := map[string]any{"x": strings.Repeat("x", 65519)}
	env := exprvalue.Environment{Run: root, Trigger: root, Vars: root, Steps: root}
	encoded, err := json.Marshal(map[string]any{"run": root, "trigger": root, "vars": root, "steps": root})
	if err != nil || len(encoded) != 262144 {
		t.Fatalf("bad exact-bound fixture: %d %v", len(encoded), err)
	}
	if _, evalErr := exprvalue.Evaluate(program, env); evalErr != nil {
		t.Fatal(evalErr)
	}
	env.Vars = map[string]any{"x": strings.Repeat("x", 65520)}
	_, err = exprvalue.Evaluate(program, env)
	failureCode(t, err, "expression_limit")
}

//nolint:gocognit // Construction and string expansion paths share exact-size positive and negative controls.
func TestOutputBoundsNotIntermediateGuards(t *testing.T) {
	t.Parallel()
	vars := map[string]any{"a": strings.Repeat("x", 32760), "b": strings.Repeat("x", 32760)}
	for _, source := range []string{`len(vars.a + vars.b + "xxxxxxxxxxxxxxxx")`, `len(join([vars.a, vars.b], "xxxxxxxxxxxxxxxx"))`,
		`all([1], {len([vars.a, vars.b, "xxxxxxxxxxxxxxxx"]) > 0})`,
		`len(filter([1], {len({x: vars.a, y: vars.b, z: "xxxxxxxxxxxxxxxx"}) == 0}))`,
	} {
		program, err := exprvalue.Compile(source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = exprvalue.Evaluate(program, exprvalue.Environment{Vars: vars}); err != nil {
			t.Fatalf("intermediate value was incorrectly treated as an output: %v", err)
		}
	}
	// Exact encoded output boundary, including escaping rather than raw length.
	for _, source := range []string{`vars.a + vars.b`, `join(["", "", ""], vars.a)`, `string(vars.a) + vars.b`} {
		for _, length := range []int{32767, 32768} {
			program, err := exprvalue.Compile(source)
			if err != nil {
				t.Fatal(err)
			}
			value, err := exprvalue.Evaluate(
				program,
				exprvalue.Environment{
					Vars: map[string]any{"a": strings.Repeat("x", length), "b": strings.Repeat("x", length)},
				},
			)
			if length == 32767 {
				if err != nil || len(value.JSON) != 65536 {
					t.Fatalf("exact bound %s: %d %+v", source, len(value.JSON), err)
				}
			} else {
				failureCode(t, err, "expression_limit")
			}
		}
	}
	program, err := exprvalue.Compile(`len(vars.a + vars.a)`)
	if err != nil {
		t.Fatal(err)
	}
	value, err := exprvalue.Evaluate(
		program,
		exprvalue.Environment{Vars: map[string]any{"a": strings.Repeat("<", 6000)}},
	)
	if err != nil || value.Data != int64(12000) {
		t.Fatalf("native len = %v, %v", value.Data, err)
	}
	// Predicate intermediates do not enter the live context either.
	program, err = exprvalue.Compile(`len(filter([1], {len(map(vars.items, {[#, #]})) == 0}))`)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]any, 128)
	for i := range items {
		items[i] = strings.Repeat("x", 256)
	}
	value, err = exprvalue.Evaluate(program, exprvalue.Environment{Vars: map[string]any{"items": items}})
	if err != nil || value.Data != int64(0) {
		t.Fatalf("native predicate = %v, %v", value.Data, err)
	}
}

func TestEnvironmentAndVMBounds(t *testing.T) {
	t.Parallel()
	program, err := exprvalue.Compile(`true`)
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]any{}
	for i := range 64 {
		vars[strconv.Itoa(i)] = int64(0)
	}
	if _, evalErr := exprvalue.Evaluate(program, exprvalue.Environment{Vars: vars}); evalErr != nil {
		t.Fatal(evalErr)
	}
	vars["65"] = int64(0)
	_, err = exprvalue.Evaluate(program, exprvalue.Environment{Vars: vars})
	failureCode(t, err, "expression_limit")
	root := func(size int) map[string]any { return map[string]any{"x": strings.Repeat("a", size)} }
	// All fragments fit, but four roots collectively exceed the environment cap.
	if _, evalErr := exprvalue.Evaluate(
		program,
		exprvalue.Environment{Run: root(65000), Trigger: root(65000), Vars: root(65000), Steps: root(65000)},
	); evalErr != nil {
		t.Fatal(evalErr)
	}
	_, err = exprvalue.Evaluate(
		program,
		exprvalue.Environment{Run: root(65530), Trigger: root(65530), Vars: root(65530), Steps: root(65530)},
	)
	failureCode(t, err, "expression_limit")
	// VM allocation accounting is additional to individual intermediate bounds.
	program, err = exprvalue.Compile(`all(vars.items, {all(vars.items, {len([1,2,3]) > 0})})`)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]any, 128)
	for i := range items {
		items[i] = int64(1)
	}
	_, err = exprvalue.Evaluate(program, exprvalue.Environment{Vars: map[string]any{"items": items}})
	failureCode(t, err, "expression_limit")
}

func TestOwnedValueChecksActualEscapedSizeAndAggregateStrings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		data any
		valid bool
	}{
		{"escaped string fits", strings.Repeat("<", 10922), true},
		{"escaped string exceeds", strings.Repeat("<", 10923), false},
		{"escaped key exceeds", map[string]any{strings.Repeat("\x00", 10923): nil}, false},
		{"aggregate strings exceed", []any{strings.Repeat("x", 32768), strings.Repeat("y", 32769)}, false},
		{"keys and values share bound", map[string]any{strings.Repeat("k", 32768): strings.Repeat("v", 32769)}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value, err := exprvalue.OwnValue(test.data)
			if !test.valid {
				failureCode(t, err, "expression_limit")
				return
			}
			want, marshalErr := json.Marshal(test.data)
			if err != nil || marshalErr != nil || string(value.JSON) != string(want) {
				t.Fatalf("owned value differs from JSON encoding: %v %v", err, marshalErr)
			}
		})
	}
}
