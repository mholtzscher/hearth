package pirateweather //nolint:testpackage // Exercises the private measurement catalog.

import "testing"

func TestMeasurementQuantityKinds(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"wind-direction": "wind_direction", "precipitation-rate": "precipitation_rate",
		"precipitation-probability": "precipitation_probability", "cloud-cover": "cloud_cover", "uv-index": "uv_index",
	}
	for _, measurement := range measurements() {
		if measurement.quantityKind != want[measurement.key] {
			t.Errorf("%s kind = %q", measurement.key, measurement.quantityKind)
		}
		if measurement.kind == numeric {
			support := measurement.numericSupport()
			if support.State.QuantityKind == nil || *support.State.QuantityKind != want[measurement.key] {
				t.Errorf("%s support = %+v", measurement.key, support)
			}
		}
	}
}
