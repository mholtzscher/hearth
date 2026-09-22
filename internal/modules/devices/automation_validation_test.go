package devices //nolint:testpackage // Tests the private JSON Pointer selector directly.

import "testing"

// This test protects State-shape pointer validation and fails if API wrapper
// paths can select from scalar values or valid nested paths are rejected.
func TestObservationValuePointerExists(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		value   any
		pointer string
		want    bool
	}{
		{name: "scalar root", value: float64(21600), pointer: "", want: true},
		{name: "scalar API wrapper", value: float64(21600), pointer: "/state/value", want: false},
		{
			name: "object member", value: map[string]any{"temperature": float64(21600)},
			pointer: "/temperature", want: true,
		},
		{
			name: "missing object member", value: map[string]any{"temperature": float64(21600)},
			pointer: "/state/value", want: false,
		},
		{name: "array member", value: []any{"off", "on"}, pointer: "/1", want: true},
		{name: "noncanonical array index", value: []any{"off", "on"}, pointer: "/01", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := observationValuePointerExists(test.value, test.pointer); got != test.want {
				t.Fatalf(
					"observationValuePointerExists(%#v, %q) = %t, want %t",
					test.value, test.pointer, got, test.want,
				)
			}
		})
	}
}
