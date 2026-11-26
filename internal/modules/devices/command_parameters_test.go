package devices //nolint:testpackage // Uses the private generated catalog behavior through ResolveCommand.

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/entitytypes"
)

// TestCommandParametersErrorsLocateRejectedFields covers the model failure
// that motivated the diagnostics: a thermostat target sent under its State
// field name. Each rejection says what to fix without echoing input.
func TestCommandParametersErrorsLocateRejectedFields(t *testing.T) {
	t.Parallel()
	catalog, err := NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	thermostat := Entity{ID: "ent_thermostat", TypeID: EntityTypeThermostatV1, Support: EntitySupport(
		`{"state":{"modes":["heat"],"fan_modes":["Auto"],"temperature_minimum":7000,"temperature_maximum":32000,` +
			`"humidity_minimum":10,"humidity_maximum":80},"operations":{"set_target":{},"set_range":{}}}`,
	)}
	for _, test := range []struct {
		name       string
		operation  OperationName
		parameters string
		want       []entitytypes.SchemaDiagnostic
	}{
		{
			name: "state field name instead of parameter", operation: "set_target",
			parameters: `{"target_temperature":21000}`,
			want: []entitytypes.SchemaDiagnostic{
				{Code: entitytypes.DiagnosticUnknownProperty, Allowed: []string{"temperature"}},
				{Code: entitytypes.DiagnosticMissingProperty, Path: "/temperature"},
			},
		},
		{
			name: "every missing field", operation: "set_range", parameters: `{}`,
			want: []entitytypes.SchemaDiagnostic{
				{Code: entitytypes.DiagnosticMissingProperty, Path: "/high"},
				{Code: entitytypes.DiagnosticMissingProperty, Path: "/low"},
			},
		},
		{
			name: "wrong type", operation: "set_target", parameters: `{"temperature":"21"}`,
			want: []entitytypes.SchemaDiagnostic{{Code: entitytypes.DiagnosticTypeMismatch, Path: "/temperature"}},
		},
		{
			name: "outside the Entity's bounds", operation: "set_target", parameters: `{"temperature":21}`,
			want: []entitytypes.SchemaDiagnostic{{Code: DiagnosticOutsideSupport}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, resolveErr := catalog.ResolveCommand(thermostat, test.operation, CommandParameters(test.parameters))
			rejection, ok := errors.AsType[*CommandParametersError](resolveErr)
			if !ok {
				t.Fatalf("ResolveCommand() error = %v, want CommandParametersError", resolveErr)
			}
			if !slices.EqualFunc(rejection.Diagnostics, test.want, equalDiagnostic) {
				t.Fatalf("diagnostics = %+v, want %+v", rejection.Diagnostics, test.want)
			}
			for _, diagnostic := range rejection.Diagnostics {
				if strings.Contains(diagnostic.Path+strings.Join(diagnostic.Allowed, ","), "target_temperature") {
					t.Fatalf("diagnostic echoes submitted key: %+v", diagnostic)
				}
			}
		})
	}
}

func equalDiagnostic(a, b entitytypes.SchemaDiagnostic) bool {
	return a.Code == b.Code && a.Path == b.Path && slices.Equal(a.Allowed, b.Allowed)
}
