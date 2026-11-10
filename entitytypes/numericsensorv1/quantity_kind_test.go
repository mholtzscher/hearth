package numericsensorv1_test

import (
	"testing"

	"github.com/mholtzscher/hearth/entitytypes/numericsensorv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdk "github.com/mholtzscher/hearth/sdk/adapter/numericsensorv1"
)

func TestQuantityKindSupportValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind, unit string
		valid      bool
	}{
		{"", "opaque", true}, {"battery_level", "%", true}, {"battery_level", "V", false}, {"unknown", "%", false},
	} {
		support := numericsensorv1.Support{State: numericsensorv1.StateSupport{Minimum: 0, Maximum: 100, Unit: tc.unit}}
		if tc.kind != "" {
			support.State.QuantityKind = &tc.kind
		}
		if err := numericsensorv1.ValidateSupport(support); (err == nil) != tc.valid {
			t.Errorf("contract %s/%s: %v", tc.kind, tc.unit, err)
		}
		_, err := sdk.NewEntityDescriptor(
			adapter.EntityMetadata{Key: "sensor", ExternalID: "sensor", Name: "Sensor"},
			support,
		)
		if (err == nil) != tc.valid {
			t.Errorf("SDK %s/%s: %v", tc.kind, tc.unit, err)
		}
	}
}
