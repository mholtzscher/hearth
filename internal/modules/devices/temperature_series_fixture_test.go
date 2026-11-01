package devices //nolint:testpackage // Extends package-local repository fixtures.

import "context"

func (*commandRepository) GetTemperatureSeries(
	context.Context,
	TemperatureSeriesParams,
) (TemperatureSeriesSnapshot, error) {
	return TemperatureSeriesSnapshot{}, nil
}

func (*readRepository) GetTemperatureSeries(
	context.Context,
	TemperatureSeriesParams,
) (TemperatureSeriesSnapshot, error) {
	return TemperatureSeriesSnapshot{}, nil
}

func (*inputContractRepository) GetTemperatureSeries(
	context.Context,
	TemperatureSeriesParams,
) (TemperatureSeriesSnapshot, error) {
	return TemperatureSeriesSnapshot{}, nil
}
