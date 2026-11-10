package numericsettingv1_test

import (
	"testing"

	"github.com/mholtzscher/hearth/entitytypes/numericsettingv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdk "github.com/mholtzscher/hearth/sdk/adapter/numericsettingv1"
)

func TestQuantityKindSupportValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind, unit string
		valid      bool
	}{
		{"", "", true}, {"color_temperature", "mired", true}, {"brightness", "%", true},
		{"brightness", "", false}, {"color_temperature", "%", false}, {"unknown", "%", false},
	} {
		support := numericsettingv1.Support{
			State: numericsettingv1.StateSupport{Minimum: 0, Maximum: 100, Choices: []string{}},
		}
		if tc.kind != "" {
			support.State.QuantityKind = &tc.kind
		}
		if tc.unit != "" {
			support.State.Unit = &tc.unit
		}
		if err := numericsettingv1.ValidateSupport(support); (err == nil) != tc.valid {
			t.Errorf("contract %s/%s: %v", tc.kind, tc.unit, err)
		}
		_, err := sdk.NewEntityDescriptor(
			adapter.EntityMetadata{Key: "setting", ExternalID: "setting", Name: "Setting"},
			support,
		)
		if (err == nil) != tc.valid {
			t.Errorf("SDK %s/%s: %v", tc.kind, tc.unit, err)
		}
	}
}
