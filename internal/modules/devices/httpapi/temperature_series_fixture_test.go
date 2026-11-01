package httpapi //nolint:testpackage // Extends package-local transport fixtures.

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func (stub *stubDevices) GetTemperatureSeries(
	ctx context.Context,
	params devices.TemperatureSeriesParams,
) (devices.TemperatureSeries, error) {
	if stub.getTemperatureSeries == nil {
		panic("unexpected GetTemperatureSeries call")
	}
	return stub.getTemperatureSeries(ctx, params)
}
