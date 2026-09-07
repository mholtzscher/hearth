package main

import (
	"math"
	"strconv"
	"testing"
)

// TestInvalidLeafFullInt64RangeReportsNoMutation protects the full-range
// guard: a leaf spanning MinInt64 to MaxInt64 accepts every Go-representable
// value, so minimum-1 would wrap to MaxInt64 and stay valid. It must report
// no mutation so traversal can try the next constrained leaf.
func TestInvalidLeafFullInt64RangeReportsNoMutation(t *testing.T) {
	t.Parallel()
	leaf := schemaNode{
		Type:    string(kindInteger),
		Minimum: jsonNumber(strconv.FormatInt(math.MinInt64, 10)),
		Maximum: jsonNumber(strconv.FormatInt(math.MaxInt64, 10)),
	}
	if _, _, ok := invalidLeaf(leaf, "Maximum"); ok {
		t.Fatal("full int64 range leaf reported a mutation; want no mutation")
	}
}

// TestInvalidLeafSkipsFullRangeLeafForConstrainedLeaf protects nested
// traversal: a full-range leaf sorted first must not block a constrained
// leaf sorted later.
func TestInvalidLeafSkipsFullRangeLeafForConstrainedLeaf(t *testing.T) {
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
	selector, value, ok := invalidLeaf(schema, "Support")
	if !ok {
		t.Fatal("nested traversal reported no mutation; want the constrained leaf")
	}
	if selector != "Support.Zzz" || value != 101 {
		t.Fatalf("mutation = %s=%d, want Support.Zzz=101", selector, value)
	}
}

// TestInvalidLeafBoundaryEndpoints pins the boundary arithmetic on either
// side of the full-range guard.
func TestInvalidLeafBoundaryEndpoints(t *testing.T) {
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
			selector, value, ok := invalidLeaf(leaf, "Maximum")
			if ok != example.ok {
				t.Fatalf("ok = %v, want %v", ok, example.ok)
			}
			if !example.ok {
				return
			}
			if selector != "Maximum" || value != example.value {
				t.Fatalf("mutation = %s=%d, want Maximum=%d", selector, value, example.value)
			}
		})
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
