package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func registerGetEntityTemperatureSeries(api huma.API, h *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "get-entity-temperature-series",
		Method:      http.MethodGet,
		Path:        "/entities/{entity_id}/temperature-series",
		Summary:     "Get retained temperature series",
		Tags:        []string{entitiesTag},
		Errors:      []int{400, 404, 500},
		// The handler validates all parameters and returns 400 rather than Huma's 422.
		SkipValidateParams: true,
	}, h.getEntityTemperatureSeries)
}

type GetEntityTemperatureSeriesInput struct {
	EntityID string `path:"entity_id"`
	From     string `                 query:"from" required:"true" format:"date-time"`
	To       string `                 query:"to"   required:"true" format:"date-time"`
}
type TemperatureBucketBody struct {
	Start            string   `json:"start"`
	End              string   `json:"end"`
	Count            int64    `json:"count"`
	MinMilliCelsius  *int64   `json:"min_milli_celsius"`
	MaxMilliCelsius  *int64   `json:"max_milli_celsius"`
	MeanMilliCelsius *float64 `json:"mean_milli_celsius"`
}
type GetEntityTemperatureSeriesOutput struct {
	Body struct {
		EntityID string                  `json:"entity_id"`
		From     string                  `json:"from"`
		To       string                  `json:"to"`
		Buckets  []TemperatureBucketBody `json:"buckets"`
	}
}

func (h *handler) getEntityTemperatureSeries(
	ctx context.Context,
	input *GetEntityTemperatureSeriesInput,
) (*GetEntityTemperatureSeriesOutput, error) {
	id, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid entity ID")
	}
	from, err := time.Parse(time.RFC3339Nano, input.From)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid time window")
	}
	to, err := time.Parse(time.RFC3339Nano, input.To)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid time window")
	}
	series, err := h.devices.GetTemperatureSeries(
		ctx,
		devices.TemperatureSeriesParams{EntityID: id, From: from, To: to},
	)
	switch {
	case errors.Is(err, devices.ErrInvalidTemperatureSeries):
		return nil, huma.Error400BadRequest("invalid temperature series request")
	case errors.Is(err, devices.ErrEntityNotFound):
		return nil, huma.Error404NotFound("entity not found")
	case err != nil:
		return nil, h.internalAPIError(ctx, "get-entity-temperature-series", "service", err)
	}
	out := &GetEntityTemperatureSeriesOutput{}
	out.Body.EntityID, out.Body.From, out.Body.To = string(
		series.EntityID,
	), formatTime(
		series.From,
	), formatTime(
		series.To,
	)
	out.Body.Buckets = make([]TemperatureBucketBody, len(series.Buckets))
	for i, b := range series.Buckets {
		out.Body.Buckets[i] = TemperatureBucketBody{
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
