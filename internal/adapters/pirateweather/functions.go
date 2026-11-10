package pirateweather

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const reasonInvalidArguments = "invalid_arguments"

//nolint:mnd // The descriptor deadline is part of the Function contract.
func (c *client) functions() []adapter.Function {
	return []adapter.Function{
		{
			Descriptor: adapter.FunctionDescriptor{
				Key:          "get_hourly_forecast",
				Description:  "Get fresh Pirate Weather hourly forecasts. Omitted fields are unavailable.",
				InputSchema:  c.hourlyInput.Document(),
				OutputSchema: c.hourlyOutput.Document(),
				TimeoutMS:    15000,
			},
			Handler: c.hourly,
		},
		{
			Descriptor: adapter.FunctionDescriptor{
				Key:          "get_daily_forecast",
				Description:  "Get fresh Pirate Weather daily forecasts; precipitation probability is the daily maximum. Omitted fields are unavailable.",
				InputSchema:  c.dailyInput.Document(),
				OutputSchema: c.dailyOutput.Document(),
				TimeoutMS:    15000,
			},
			Handler: c.daily,
		},
	}
}

func (c *client) hourly(ctx context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
	if c.hourlyInput.Validate(request.Arguments) != nil {
		return adapter.FunctionNotExecuted{ReasonCode: reasonInvalidArguments}
	}
	var input struct {
		Hours float64 `json:"hours"`
	}
	if json.Unmarshal(request.Arguments, &input) != nil {
		return adapter.FunctionNotExecuted{ReasonCode: reasonInvalidArguments}
	}
	body, received, err := c.request(ctx, "hourly")
	if err != nil {
		return functionFailure(err)
	}
	// Schema validation establishes a bounded integer, including 1.0 and 1e0.
	result, err := decodeHourly(body, c.config.LocationName, received, int(input.Hours))
	if err != nil {
		return adapter.FunctionFailed{ReasonCode: reasonInvalidResponse}
	}
	return forecastSuccess(c.hourlyOutput, result)
}

func (c *client) daily(ctx context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
	if c.dailyInput.Validate(request.Arguments) != nil {
		return adapter.FunctionNotExecuted{ReasonCode: reasonInvalidArguments}
	}
	var input struct {
		Days float64 `json:"days"`
	}
	if json.Unmarshal(request.Arguments, &input) != nil {
		return adapter.FunctionNotExecuted{ReasonCode: reasonInvalidArguments}
	}
	body, received, err := c.request(ctx, "daily")
	if err != nil {
		return functionFailure(err)
	}
	result, err := decodeDaily(body, c.config.LocationName, received, int(input.Days))
	if err != nil {
		return adapter.FunctionFailed{ReasonCode: reasonInvalidResponse}
	}
	return forecastSuccess(c.dailyOutput, result)
}

func functionFailure(err error) adapter.FunctionResult {
	var failure *providerError
	if !errors.As(err, &failure) {
		return adapter.FunctionFailed{ReasonCode: reasonProviderUnavailable}
	}
	if !failure.started {
		return adapter.FunctionNotExecuted{ReasonCode: failure.reason}
	}
	return adapter.FunctionFailed{ReasonCode: failure.reason}
}

func forecastSuccess(schema *functionschema.Schema, result any) adapter.FunctionResult {
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > functionschema.MaxValueBytes || schema.Validate(encoded) != nil {
		return adapter.FunctionFailed{ReasonCode: reasonInvalidResponse}
	}
	return adapter.FunctionSucceeded{Result: encoded}
}
