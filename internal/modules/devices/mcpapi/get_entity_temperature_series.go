package mcpapi

import (
	"context"
	"errors"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerGetEntityTemperatureSeries(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[GetEntityTemperatureSeriesInput, TemperatureSeriesBody]{
			Name:        "get_entity_temperature_series",
			Description: "Read retained temperature observations in 288 fixed buckets.",
			Handler:     h.getEntityTemperatureSeries,
		},
	)
}

type GetEntityTemperatureSeriesInput struct {
	EntityID entityID `json:"entity_id"`
	From     string   `json:"from"`
	To       string   `json:"to"`
}
type TemperatureBucketBody struct {
	Start            string   `json:"start"`
	End              string   `json:"end"`
	Count            int64    `json:"count"`
	MinMilliCelsius  *int64   `json:"min_milli_celsius"`
	MaxMilliCelsius  *int64   `json:"max_milli_celsius"`
	MeanMilliCelsius *float64 `json:"mean_milli_celsius"`
}
type TemperatureSeriesBody struct {
	EntityID string                  `json:"entity_id"`
	From     string                  `json:"from"`
	To       string                  `json:"to"`
	Buckets  []TemperatureBucketBody `json:"buckets"`
}

func (h *handler) getEntityTemperatureSeries(
	ctx context.Context,
	input GetEntityTemperatureSeriesInput,
) (TemperatureSeriesBody, error) {
	from, err := time.Parse(time.RFC3339Nano, input.From)
	if err != nil {
		return TemperatureSeriesBody{}, invalidRequestFailure("invalid time window")
	}
	to, err := time.Parse(time.RFC3339Nano, input.To)
	if err != nil {
		return TemperatureSeriesBody{}, invalidRequestFailure("invalid time window")
	}
	series, err := h.devices.GetTemperatureSeries(
		ctx,
		devices.TemperatureSeriesParams{EntityID: devices.EntityID(input.EntityID), From: from, To: to},
	)
	switch {
	case errors.Is(err, devices.ErrInvalidTemperatureSeries):
		return TemperatureSeriesBody{}, invalidRequestFailure("invalid temperature series request")
	case errors.Is(err, devices.ErrEntityNotFound):
		return TemperatureSeriesBody{}, toolError(failureEntityNotFound, "entity not found")
	case err != nil:
		return TemperatureSeriesBody{}, mcptools.InternalToolError(err)
	}
	out := TemperatureSeriesBody{
		EntityID: string(series.EntityID),
		From:     formatTime(series.From),
		To:       formatTime(series.To),
		Buckets:  make([]TemperatureBucketBody, len(series.Buckets)),
	}
	for i, b := range series.Buckets {
		out.Buckets[i] = TemperatureBucketBody{
			formatTime(b.Start),
			formatTime(b.End),
			b.Count,
			b.MinMilliCelsius,
			b.MaxMilliCelsius,
			b.MeanMilliCelsius,
		}
	}
	return out, nil
}
