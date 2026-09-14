package automations_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func comparison(
	pointer string,
	operator automations.ComparisonOperator,
	operand string,
) automations.ObservationComparison {
	return automations.ObservationComparison{
		Pointer:  pointer,
		Operator: operator,
		Operand:  json.RawMessage(operand),
	}
}

func match(t *testing.T, target automations.ObservationComparison, value string) bool {
	t.Helper()
	matched, err := automations.MatchObservationComparison(target, devices.Value(value))
	if err != nil {
		t.Fatal(err)
	}
	return matched
}

// Matching must preserve JSON Pointer and typed equality semantics, including
// false for missing paths under ne, unordered objects, and ordered arrays.
func TestObservationComparisonSemantics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		value    string
		pointer  string
		operator automations.ComparisonOperator
		operand  string
		want     bool
	}{
		{"root selects whole value", `20`, ``, automations.ComparisonEqual, `20`, true},
		{"root object order irrelevant", `{"a":1,"b":2}`, ``, automations.ComparisonEqual, `{"b":2,"a":1}`, true},
		{"root array order significant", `[1,2]`, ``, automations.ComparisonEqual, `[2,1]`, false},
		{"nested object member", `{"a":{"b":true}}`, `/a/b`, automations.ComparisonEqual, `true`, true},
		{"escaped slash", `{"a/b":1}`, `/a~1b`, automations.ComparisonEqual, `1`, true},
		{"escaped tilde", `{"a~b":1}`, `/a~0b`, automations.ComparisonEqual, `1`, true},
		{"missing path eq false", `{"a":1}`, `/b`, automations.ComparisonEqual, `1`, false},
		{"missing path ne false", `{"a":1}`, `/b`, automations.ComparisonNotEqual, `1`, false},
		{"missing nested path ne false", `{"a":{"b":1}}`, `/a/c`, automations.ComparisonNotEqual, `2`, false},
		{"descend into scalar false", `{"a":1}`, `/a/b`, automations.ComparisonEqual, `1`, false},
		{"type mismatch eq false", `{"a":1}`, `/a`, automations.ComparisonEqual, `"1"`, false},
		{"type mismatch ne false", `{"a":1}`, `/a`, automations.ComparisonNotEqual, `"1"`, false},
		{"null equals null", `{"a":null}`, `/a`, automations.ComparisonEqual, `null`, true},
		{"null differs from false", `{"a":null}`, `/a`, automations.ComparisonEqual, `false`, false},
		{
			"nested null equals nested null",
			`{"a":[null]}`,
			`/a`,
			automations.ComparisonEqual,
			`[null]`,
			true,
		},
		{
			"nested null differs from string",
			`{"a":[null]}`,
			`/a`,
			automations.ComparisonEqual,
			`["x"]`,
			false,
		},
		{
			"nested null differs from number",
			`{"a":[null,1]}`,
			`/a`,
			automations.ComparisonEqual,
			`[1,1]`,
			false,
		},
		{
			"nested member null differs from number",
			`{"a":{"b":null}}`,
			`/a`,
			automations.ComparisonEqual,
			`{"b":0}`,
			false,
		},
		{
			"deep nested null differs from boolean",
			`{"a":[{"b":null}]}`,
			`/a`,
			automations.ComparisonEqual,
			`[{"b":false}]`,
			false,
		},
		{
			"nested null operand differs from value",
			`{"a":["x"]}`,
			`/a`,
			automations.ComparisonEqual,
			`[null]`,
			false,
		},
		{
			"nested null array ne is true for unequal contents",
			`{"a":[null]}`,
			`/a`,
			automations.ComparisonNotEqual,
			`["x"]`,
			true,
		},
		{"value ne present", `{"a":1}`, `/a`, automations.ComparisonNotEqual, `2`, true},
		{"array index", `[10,20,30]`, `/1`, automations.ComparisonEqual, `20`, true},
		{"array index out of range", `[10]`, `/2`, automations.ComparisonEqual, `10`, false},
		{"array index leading zero", `[10,20]`, `/01`, automations.ComparisonEqual, `20`, false},
		{"array dash is invalid read", `[10,20]`, `/-`, automations.ComparisonEqual, `20`, false},
		{"array negative index", `[10,20]`, `/-1`, automations.ComparisonEqual, `10`, false},
		{"nested array index", `{"a":[{"b":2}]}`, `/a/0/b`, automations.ComparisonEqual, `2`, true},
		{"number eq by value", `{"a":1.0}`, `/a`, automations.ComparisonEqual, `1`, true},
		{"number eq exponent", `{"a":1e3}`, `/a`, automations.ComparisonEqual, `1000`, true},
		{"number ne", `{"a":1}`, `/a`, automations.ComparisonNotEqual, `1.0000000000000001`, true},
		{
			"large integer precision",
			`{"a":9007199254740993}`,
			`/a`,
			automations.ComparisonEqual,
			`9007199254740992`,
			false,
		},
		{"decimal rounding", `{"a":0.30000000000000004}`, `/a`, automations.ComparisonGreaterThan, `0.3`, true},
		{"integer ordering", `{"a":2}`, `/a`, automations.ComparisonLessThan, `10`, true},
		{"negative ordering", `{"a":-0.5}`, `/a`, automations.ComparisonLessThan, `0`, true},
		{"huge exponent ordering", `{"a":1e1000}`, `/a`, automations.ComparisonGreaterThan, `1e999`, true},
		{"ordering requires numbers", `{"a":"2"}`, `/a`, automations.ComparisonLessThan, `10`, false},
		{"ordering requires numeric operand", `{"a":2}`, `/a`, automations.ComparisonLessThan, `"10"`, false},
		{"lte equal", `{"a":2}`, `/a`, automations.ComparisonLessThanOrEqual, `2`, true},
		{"gte equal", `{"a":2}`, `/a`, automations.ComparisonGreaterThanOrEqual, `2`, true},
		{"lt equal is false", `{"a":2}`, `/a`, automations.ComparisonLessThan, `2`, false},
		{"gt equal is false", `{"a":2}`, `/a`, automations.ComparisonGreaterThan, `2`, false},
		{
			"object member order irrelevant",
			`{"a":{"x":1,"y":2}}`,
			`/a`,
			automations.ComparisonEqual,
			`{"y":2,"x":1}`,
			true,
		},
		{"object extra member differs", `{"a":{"x":1}}`, `/a`, automations.ComparisonEqual, `{"x":1,"y":2}`, false},
		{"nested array order significant", `{"a":[1,2,3]}`, `/a`, automations.ComparisonEqual, `[3,2,1]`, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			target := comparison(test.pointer, test.operator, test.operand)
			if got := match(t, target, test.value); got != test.want {
				t.Fatalf("match(%s, %s %s %s) = %v, want %v",
					test.value, test.pointer, test.operator, test.operand, got, test.want)
			}
		})
	}
}

// Undecodable input is an error, not an ordinary non-match.
func TestMatchObservationComparisonRejectsUndecodableInput(t *testing.T) {
	t.Parallel()
	_, err := automations.MatchObservationComparison(
		comparison("/a", automations.ComparisonEqual, `1 2`),
		devices.Value(`{"a":1}`),
	)
	if err == nil {
		t.Fatal("operand with trailing content was accepted")
	}
	_, err = automations.MatchObservationComparison(
		comparison("/a", automations.ComparisonEqual, `1`),
		devices.Value(`{`),
	)
	if err == nil {
		t.Fatal("undecodable value was accepted")
	}
}

// Pointer syntax accepts root and RFC 6901 escapes within the 256-byte bound.
func TestValidateJSONPointer(t *testing.T) {
	t.Parallel()
	valid := []string{"", "/", "/a", "/a/b", "/a~0b", "/a~1b", "/0", "/-", "/a~0~1b"}
	for _, pointer := range valid {
		if err := automations.ValidateJSONPointer(pointer); err != nil {
			t.Fatalf("pointer %q: %v", pointer, err)
		}
	}
	invalid := []string{"a", "a/b", "/a~", "/a~2b", "/~x", "/" + strings.Repeat("a", 256)}
	for _, pointer := range invalid {
		err := automations.ValidateJSONPointer(pointer)
		if err == nil {
			t.Fatalf("pointer %q was accepted", pointer)
		}
		if !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Fatalf("pointer %q error = %v, want ErrInvalidAutomation", pointer, err)
		}
	}
}

// Ordering requires numeric operands; equality accepts any JSON value.
// Unknown operators must fail validation.
func TestValidateObservationComparison(t *testing.T) {
	t.Parallel()
	accepted := []automations.ObservationComparison{
		comparison("", automations.ComparisonEqual, `null`),
		comparison("/a", automations.ComparisonNotEqual, `{"x":[1,2]}`),
		comparison("/a", automations.ComparisonGreaterThan, `-1.5e3`),
		comparison("/a", automations.ComparisonLessThanOrEqual, `0`),
	}
	for _, target := range accepted {
		if err := automations.ValidateObservationComparison(target); err != nil {
			t.Fatalf("comparison %+v: %v", target, err)
		}
	}
	rejected := []automations.ObservationComparison{
		comparison("/a", automations.ComparisonGreaterThan, `"20"`),
		comparison("/a", automations.ComparisonLessThan, `null`),
		comparison("/a", automations.ComparisonGreaterThanOrEqual, `[1]`),
		comparison("/a", automations.ComparisonOperator("between"), `1`),
		comparison("bad", automations.ComparisonEqual, `1`),
		comparison("/a", automations.ComparisonEqual, `1 2`),
	}
	for _, target := range rejected {
		err := automations.ValidateObservationComparison(target)
		if err == nil {
			t.Fatalf("comparison %+v was accepted", target)
		}
		if !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Fatalf("comparison %+v error = %v, want ErrInvalidAutomation", target, err)
		}
	}
}

// All six operators must agree with integer comparison, including values above
// 2^53 that binary floating point cannot distinguish.
func TestObservationComparisonOrderingMatchesIntegerOracle(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		left := rapid.Int64().Draw(t, "left")
		right := rapid.Int64().Draw(t, "right")
		value := devices.Value(fmt.Sprintf(`{"v":%d}`, left))
		operand := json.RawMessage(strconv.FormatInt(right, 10))
		cases := []struct {
			operator automations.ComparisonOperator
			want     bool
		}{
			{automations.ComparisonEqual, left == right},
			{automations.ComparisonNotEqual, left != right},
			{automations.ComparisonLessThan, left < right},
			{automations.ComparisonLessThanOrEqual, left <= right},
			{automations.ComparisonGreaterThan, left > right},
			{automations.ComparisonGreaterThanOrEqual, left >= right},
		}
		for _, test := range cases {
			got, err := automations.MatchObservationComparison(automations.ObservationComparison{
				Pointer: "/v", Operator: test.operator, Operand: operand,
			}, value)
			if err != nil {
				t.Fatalf("left=%d right=%d operator=%s: %v", left, right, test.operator, err)
			}
			if got != test.want {
				t.Fatalf("left=%d right=%d operator=%s: got %v want %v", left, right, test.operator, got, test.want)
			}
		}
	})
}
