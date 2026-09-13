package main

import (
	"encoding/json"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSupportLeafMutationFullInt64RangeReportsNoMutation protects the full-range
// guard: a leaf spanning MinInt64 to MaxInt64 accepts every Go-representable
// value, so minimum-1 would wrap to MaxInt64 and stay valid. It must report
// no mutation so traversal can try the next constrained leaf.
func TestSupportLeafMutationFullInt64RangeReportsNoMutation(t *testing.T) {
	t.Parallel()
	leaf := schemaNode{
		Type:    string(kindInteger),
		Minimum: jsonNumber(strconv.FormatInt(math.MinInt64, 10)),
		Maximum: jsonNumber(strconv.FormatInt(math.MaxInt64, 10)),
	}
	if _, ok := findSupportLeafMutation(leaf, "Maximum", integerLeafMutation); ok {
		t.Fatal("full int64 range leaf reported a mutation; want no mutation")
	}
}

// TestSupportLeafMutationSkipsFullRangeLeafForConstrainedLeaf protects nested
// traversal: a full-range leaf sorted first must not block a constrained
// leaf sorted later.
func TestSupportLeafMutationSkipsFullRangeLeafForConstrainedLeaf(t *testing.T) {
	t.Parallel()
	schema := schemaNode{
		Type:     schemaTypeObject,
		Required: []string{"aaa", "zzz"},
		Properties: map[string]schemaNode{
			"aaa": {
				Type:    string(kindInteger),
				Minimum: jsonNumber(strconv.FormatInt(math.MinInt64, 10)),
				Maximum: jsonNumber(strconv.FormatInt(math.MaxInt64, 10)),
			},
			"zzz": {
				Type:    string(kindInteger),
				Minimum: jsonNumber("0"),
				Maximum: jsonNumber("100"),
			},
		},
	}
	mutation, ok := findSupportLeafMutation(schema, "Support", integerLeafMutation)
	if !ok {
		t.Fatal("nested traversal reported no mutation; want the constrained leaf")
	}
	if mutation.Selector != "Support.Zzz" || mutation.Expression != "101" {
		t.Fatalf("mutation = %s=%s, want Support.Zzz=101", mutation.Selector, mutation.Expression)
	}
}

// TestSupportLeafMutationIntegerBoundaryEndpoints pins the boundary arithmetic
// on either side of the full-range guard.
func TestSupportLeafMutationIntegerBoundaryEndpoints(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		minimum string
		maximum string
		value   int64
		ok      bool
	}{
		{name: "bounded maximum", minimum: "0", maximum: "100", value: 101, ok: true},
		{name: "equivalent exponent maximum", minimum: "0", maximum: "1e2", value: 101, ok: true},
		{name: "fractional maximum", minimum: "0", maximum: "100.0", value: 101, ok: true},
		{name: "fractional minimum", minimum: "0.5", maximum: strconv.FormatInt(math.MaxInt64, 10), value: 0, ok: true},
		{
			name:    "maximum at MaxInt64 falls back to minimum",
			minimum: "0",
			maximum: strconv.FormatInt(math.MaxInt64, 10),
			value:   -1,
			ok:      true,
		},
		{
			name:    "minimum just above MinInt64 stays representable",
			minimum: strconv.FormatInt(math.MinInt64+1, 10),
			maximum: strconv.FormatInt(math.MaxInt64, 10),
			value:   math.MinInt64,
			ok:      true,
		},
		{
			name:    "full range reports no mutation",
			minimum: strconv.FormatInt(math.MinInt64, 10),
			maximum: strconv.FormatInt(math.MaxInt64, 10),
			ok:      false,
		},
		{
			name:    "fractional full range reports no mutation",
			minimum: "-9223372036854775808.5",
			maximum: "9223372036854775807.5",
			ok:      false,
		},
	}
	for _, example := range cases {
		t.Run(example.name, func(t *testing.T) {
			t.Parallel()
			leaf := schemaNode{
				Type:    string(kindInteger),
				Minimum: jsonNumber(example.minimum),
				Maximum: jsonNumber(example.maximum),
			}
			mutation, ok := findSupportLeafMutation(leaf, "Maximum", integerLeafMutation)
			if ok != example.ok {
				t.Fatalf("ok = %v, want %v", ok, example.ok)
			}
			if !example.ok {
				return
			}
			want := strconv.FormatInt(example.value, 10)
			if mutation.Selector != "Maximum" || mutation.Expression != want {
				t.Fatalf(
					"mutation = %s=%s, want Maximum=%d",
					mutation.Selector,
					mutation.Expression,
					example.value,
				)
			}
		})
	}
}

// TestStringConstLeafMutationRendersWrongString protects the fixed-value
// mutation: a required string const must render a Go string literal that
// differs from the authoritative value so the schema rejects it.
func TestStringConstLeafMutationRendersWrongString(t *testing.T) {
	t.Parallel()
	schema := schemaNode{Type: string(kindString), Const: json.RawMessage(`"celsius"`)}
	mutation, ok := stringConstLeafMutation(schema, "State.Unit")
	if !ok {
		t.Fatal("string const leaf reported no mutation")
	}
	if mutation.Selector != "State.Unit" || mutation.Expression != `"celsius-invalid"` {
		t.Fatalf("mutation = %s=%s, want State.Unit=\"celsius-invalid\"", mutation.Selector, mutation.Expression)
	}
}

// TestStringConstLeafMutationSkipsUnusableConsts protects the leaf predicate:
// only a required JSON string const can render a string mutation, so absent or
// non-string consts and non-string schemas must report no mutation so traversal
// falls through to another leaf.
func TestStringConstLeafMutationSkipsUnusableConsts(t *testing.T) {
	t.Parallel()
	cases := map[string]schemaNode{
		"missing const": {Type: string(kindString)},
		"numeric const": {Type: string(kindString), Const: json.RawMessage(`1`)},
		"non-string type": {
			Type:  string(kindInteger),
			Const: json.RawMessage(`"1"`),
		},
	}
	for name, schema := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if mutation, ok := stringConstLeafMutation(schema, "State.Unit"); ok {
				t.Fatalf("unusable const reported mutation %+v", mutation)
			}
		})
	}
}

// TestInvalidSupportMutationPrefersStringConstOverInteger protects the global
// preference: a fixed string value is the strongest immutability claim, so even
// when a bounded integer sibling sorts first the string const must be chosen.
func TestInvalidSupportMutationPrefersStringConstOverInteger(t *testing.T) {
	t.Parallel()
	model := entityTypeModel{SupportSchema: schemaNode{
		Type:     schemaTypeObject,
		Required: []string{"state", "operations"},
		Properties: map[string]schemaNode{
			"state": {
				Type:     schemaTypeObject,
				Required: []string{"maximum", "unit"},
				Properties: map[string]schemaNode{
					"maximum": {Type: string(kindInteger), Minimum: jsonNumber("0"), Maximum: jsonNumber("100")},
					"unit":    {Type: string(kindString), Const: json.RawMessage(`"celsius"`)},
				},
			},
			"operations": {Type: schemaTypeObject},
		},
	}}
	mutation, ok := invalidSupportMutation(model)
	if !ok {
		t.Fatal("support mutation reported no mutation")
	}
	if mutation.Selector != "State.Unit" || mutation.Expression != `"celsius-invalid"` {
		t.Fatalf(
			"mutation = %s=%s, want the string const State.Unit",
			mutation.Selector,
			mutation.Expression,
		)
	}
}

// TestSupportZeroValueRejectedForRequiredStringConst protects empty-support
// elicitation: only a required string const with a non-empty value makes the Go
// zero value schema-invalid, so the generated empty-support probe appears
// exactly when it can fail.
func TestSupportZeroValueRejectedForRequiredStringConst(t *testing.T) {
	t.Parallel()
	stringConst := func(value string) schemaNode {
		return schemaNode{Type: string(kindString), Const: json.RawMessage(strconv.Quote(value))}
	}
	cases := []struct {
		name   string
		schema schemaNode
		want   bool
	}{
		{
			name: "required non-empty const",
			want: true,
			schema: schemaNode{
				Type: schemaTypeObject, Required: []string{"unit"},
				Properties: map[string]schemaNode{"unit": stringConst("celsius")},
			},
		},
		{
			name: "nested under required object",
			want: true,
			schema: schemaNode{
				Type: schemaTypeObject, Required: []string{"state"},
				Properties: map[string]schemaNode{"state": {
					Type: schemaTypeObject, Required: []string{"unit"},
					Properties: map[string]schemaNode{"unit": stringConst("celsius")},
				}},
			},
		},
		{
			name: "required empty const",
			want: false,
			schema: schemaNode{
				Type: schemaTypeObject, Required: []string{"unit"},
				Properties: map[string]schemaNode{"unit": stringConst("")},
			},
		},
		{
			name: "optional const",
			want: false,
			schema: schemaNode{
				Type:       schemaTypeObject,
				Properties: map[string]schemaNode{"unit": stringConst("celsius")},
			},
		},
		{
			name: "required string without const",
			want: false,
			schema: schemaNode{
				Type: schemaTypeObject, Required: []string{"unit"},
				Properties: map[string]schemaNode{"unit": {Type: string(kindString)}},
			},
		},
	}
	for _, example := range cases {
		t.Run(example.name, func(t *testing.T) {
			t.Parallel()
			model := entityTypeModel{SupportSchema: example.schema}
			if got := supportZeroValueRejected(model); got != example.want {
				t.Fatalf("supportZeroValueRejected = %v, want %v", got, example.want)
			}
		})
	}
}

// TestFacadeConformanceMutatesRequiredStringConst protects the generated SDK
// facade test for a fixed support value: descriptor and observation checks must
// mutate the required string const to a wrong string, and both must separately
// probe the schema-invalid zero value so omission is rejected too.
func TestFacadeConformanceMutatesRequiredStringConst(t *testing.T) {
	t.Parallel()
	model := fixedUnitFacadeModel(true)
	text := string(renderFacadeConformanceTest(model, "example.test").content)
	for _, required := range []string{
		`invalidSupport.State.Unit = "celsius-invalid"`,
		"descriptor with empty Entity support was accepted",
		"Observation with empty Entity support was accepted",
		"NewEntityDescriptor(metadata, emptySupport)",
		"Support: emptySupport, State: state",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("facade conformance omits %q:\n%s", required, text)
		}
	}
}

// TestFacadeConformanceKeepsBoundedIntegerMutation protects existing coverage:
// a support schema whose only constrained leaf is a bounded integer still
// renders the integer mutation and must not claim an empty-support probe it
// cannot substantiate.
func TestFacadeConformanceKeepsBoundedIntegerMutation(t *testing.T) {
	t.Parallel()
	model := fixedUnitFacadeModel(false)
	text := string(renderFacadeConformanceTest(model, "example.test").content)
	if !strings.Contains(text, "invalidSupport.State.Maximum = 101") {
		t.Errorf("facade conformance omits the bounded integer mutation:\n%s", text)
	}
	if strings.Contains(text, "emptySupport") {
		t.Errorf("bounded integer support emits an unsubstantiated empty support probe:\n%s", text)
	}
}

// fixedUnitFacadeModel returns a renderable model mirroring a semantic support
// schema. When fixedUnit is true the support fixes a non-empty string const;
// otherwise it is replaced by a bounded integer leaf, preserving the existing
// integer mutation path for comparison.
func fixedUnitFacadeModel(fixedUnit bool) entityTypeModel {
	stateSupport := schemaNode{
		Type:     schemaTypeObject,
		Required: []string{"unit"},
		Properties: map[string]schemaNode{
			"unit": {Type: string(kindString), Const: json.RawMessage(`"celsius"`)},
		},
	}
	support := json.RawMessage(`{"state":{"unit":"celsius"},"operations":{"set":{}}}`)
	if !fixedUnit {
		stateSupport = schemaNode{
			Type:     schemaTypeObject,
			Required: []string{"maximum"},
			Properties: map[string]schemaNode{
				"maximum": {Type: string(kindInteger), Minimum: jsonNumber("0"), Maximum: jsonNumber("100")},
			},
		}
		support = json.RawMessage(`{"state":{"maximum":80},"operations":{"set":{}}}`)
	}
	return entityTypeModel{
		Package: "examplev1",
		TypeID:  "example.value/v1",
		SupportSchema: schemaNode{
			Type:     schemaTypeObject,
			Required: []string{"state", "operations"},
			Properties: map[string]schemaNode{
				"state":      stateSupport,
				"operations": {Type: schemaTypeObject},
			},
		},
		Operations: []operationModel{{Name: "set", GoName: "Set", Required: true}},
		Examples: examplesFile{Cases: []exampleCase{{
			Name:    "fixed",
			Support: support,
			States: []validityExample{
				{Value: json.RawMessage(`true`), Valid: true},
				{Value: json.RawMessage(`1`), Valid: false},
			},
			Operations: map[string]operationExamples{"set": {
				Parameters: []validityExample{
					{Value: json.RawMessage(`{"value":1}`), Valid: true},
					{Value: json.RawMessage(`{"value":"on"}`), Valid: false},
				},
			}},
		}}},
	}
}

// TestFixtureConstFacadeMutationCoversRequiredStringConst protects the authored
// fixture end to end: the fixture's required string const must win over its
// bounded integer sibling, its Go zero value must be schema-invalid, and the
// rendered facade test must carry both the wrong-value and empty-support probes.
func TestFixtureConstFacadeMutationCoversRequiredStringConst(t *testing.T) {
	t.Parallel()
	model, err := loadModel(filepath.Join(fixtureRoot(t), "fixtureconstv1", "entitytype.json"))
	if err != nil {
		t.Fatal(err)
	}
	mutation, ok := invalidSupportMutation(model)
	if !ok {
		t.Fatal("fixture const support reported no mutation")
	}
	if mutation.Selector != "State.Unit" || mutation.Expression != `"celsius-invalid"` {
		t.Fatalf(
			"mutation = %s=%s, want the string const State.Unit",
			mutation.Selector,
			mutation.Expression,
		)
	}
	if !supportZeroValueRejected(model) {
		t.Fatal("fixture const support does not reject its zero value")
	}
	text := string(renderFacadeConformanceTest(model, "example.test").content)
	for _, required := range []string{
		`invalidSupport.State.Unit = "celsius-invalid"`,
		"descriptor with empty Entity support was accepted",
		"Observation with empty Entity support was accepted",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("rendered fixture facade omits %q:\n%s", required, text)
		}
	}
}

// TestFixtureFreeIntegerMutationRenders protects the bounded-integer path on a
// real fixture: a support schema without a string const still renders its
// bounded integer mutation and must not claim an empty-support probe that its
// zero value would satisfy.
func TestFixtureFreeIntegerMutationRenders(t *testing.T) {
	t.Parallel()
	model, err := loadModel(filepath.Join(fixtureRoot(t), "fixturefreev1", "entitytype.json"))
	if err != nil {
		t.Fatal(err)
	}
	mutation, ok := invalidSupportMutation(model)
	if !ok {
		t.Fatal("fixture free support reported no mutation")
	}
	if mutation.Selector != "State.Maximum" || mutation.Expression != "101" {
		t.Fatalf(
			"mutation = %s=%s, want the bounded integer State.Maximum=101",
			mutation.Selector,
			mutation.Expression,
		)
	}
	if supportZeroValueRejected(model) {
		t.Fatal("fixture free support surprise-rejects its zero value")
	}
	text := string(renderFacadeConformanceTest(model, "example.test").content)
	if !strings.Contains(text, "invalidSupport.State.Maximum = 101") {
		t.Errorf("rendered fixture facade omits the bounded integer mutation:\n%s", text)
	}
	if strings.Contains(text, "emptySupport") {
		t.Errorf("rendered fixture facade emits an unsubstantiated empty support probe:\n%s", text)
	}
}

// TestUnknownOperationCandidateAvoidsDeclaredNames protects the unknown
// route check: the candidate must be absent from the complete declared name
// set, not just differ from one operation. It fails if the generator emits
// an unknown-operation probe that collides with a real operation.
func TestUnknownOperationCandidateAvoidsDeclaredNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		operations []string
		want       string
	}{
		{name: "empty", operations: nil, want: "unknown"},
		{name: "unrelated", operations: []string{"set"}, want: "unknown"},
		{name: "unknown taken", operations: []string{"unknown"}, want: "unknown-operation"},
		{name: "only suffixed taken", operations: []string{"unknown-operation"}, want: "unknown"},
		{
			name:       "both reserved",
			operations: []string{"unknown", "unknown-operation"},
			want:       "unknown-operation-2",
		},
		{
			name:       "suffixed chain",
			operations: []string{"unknown", "unknown-operation", "unknown-operation-2"},
			want:       "unknown-operation-3",
		},
		{name: "order independent", operations: []string{"unknown-operation", "unknown"}, want: "unknown-operation-2"},
	}
	for _, example := range cases {
		t.Run(example.name, func(t *testing.T) {
			t.Parallel()
			operations := make([]operationModel, 0, len(example.operations))
			for _, name := range example.operations {
				operations = append(operations, operationModel{Name: name})
			}
			if got := unknownOperationCandidate(operations); got != example.want {
				t.Fatalf("candidate = %q, want %q", got, example.want)
			}
		})
	}
}

// TestFacadeConformanceCoversRelationalInvalidSupports protects the SDK
// conformance seam for schema-valid but relationally invalid supports (for
// example a numericsetting minimum above its maximum). The schema-mutation
// probe cannot construct such values, so the generator must also emit
// rejection checks from the independently authored invalid_supports examples
// for both descriptor and command construction. It fails if either check is
// missing.
func TestFacadeConformanceCoversRelationalInvalidSupports(t *testing.T) {
	t.Parallel()
	model := entityTypeModel{
		Package: "examplev1",
		TypeID:  "example.value/v1",
		Operations: []operationModel{
			{Name: "set", GoName: "Set", Required: true},
		},
		Examples: examplesFile{
			Cases: []exampleCase{{
				Name:    "basic",
				Support: json.RawMessage(`{"state":{},"operations":{"set":{}}}`),
				States: []validityExample{
					{Value: json.RawMessage(`true`), Valid: true},
					{Value: json.RawMessage(`1`), Valid: false},
				},
				Operations: map[string]operationExamples{
					"set": {Parameters: []validityExample{
						{Value: json.RawMessage(`{"value":1}`), Valid: true},
						{Value: json.RawMessage(`{"value":"on"}`), Valid: false},
					}},
				},
			}},
			InvalidSupports: []json.RawMessage{
				json.RawMessage(`{"state":{"minimum":454,"maximum":142},"operations":{"set":{}}}`),
			},
		},
	}
	rendered := renderFacadeConformanceTest(
		model,
		"example.test",
	)
	text := string(rendered.content)
	for _, required := range []string{
		"func TestGeneratedCommandInvalidSupport(t *testing.T) {",
		"descriptor with invalid Entity support 1 was accepted",
		"command handler with invalid Entity support 1 was accepted",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("facade conformance omits %q", required)
		}
	}
}

// TestInvalidSupportOperationsMatchesEnabledOperations protects handler
// construction for invalid-support command checks: required operations are
// always present while optional ones follow the authored value, so only
// support validity decides the construction outcome.
func TestInvalidSupportOperationsMatchesEnabledOperations(t *testing.T) {
	t.Parallel()
	model := entityTypeModel{Operations: []operationModel{
		{Name: "set", GoName: "Set", Required: true},
		{Name: "toggle", GoName: "Toggle"},
	}}
	enabled := invalidSupportOperations(
		model,
		json.RawMessage(`{"state":{},"operations":{"toggle":{}}}`),
	)
	if len(enabled) != 2 || enabled[0].Name != "set" || enabled[1].Name != "toggle" {
		t.Fatalf("enabled operations = %+v, want required set plus enabled toggle", enabled)
	}
	disabled := invalidSupportOperations(
		model,
		json.RawMessage(`{"state":{},"operations":{}}`),
	)
	if len(disabled) != 1 || disabled[0].Name != "set" {
		t.Fatalf("enabled operations = %+v, want required set only", disabled)
	}
}
