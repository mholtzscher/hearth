package mcpapi //nolint:testpackage // Exercises the operation's transport mapping.

import (
	"context"
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestTemperatureSeriesMCP(t *testing.T) {
	t.Parallel()
	stub := &stubDevices{
		getTemperatureSeries: func(_ context.Context, p devices.TemperatureSeriesParams) (devices.TemperatureSeries, error) {
			return devices.TemperatureSeries{
				EntityID: p.EntityID,
				From:     p.From,
				To:       p.To,
				Buckets:  []devices.TemperatureBucket{{Start: p.From, End: p.From.Add(75e9)}},
			}, nil
		},
	}
	h := &handler{devices: stub}
	input := GetEntityTemperatureSeriesInput{
		EntityID: entityID(apiEntityID),
		From:     "2026-08-22T00:00:00Z",
		To:       "2026-08-22T06:00:00Z",
	}
	result, err := h.getEntityTemperatureSeries(t.Context(), input)
	if err != nil || len(result.Buckets) != 1 || result.Buckets[0].MeanMilliCelsius != nil ||
		result.EntityID != string(apiEntityID) {
		t.Fatalf("result: %+v %v", result, err)
	}
	stub.getTemperatureSeries = func(context.Context, devices.TemperatureSeriesParams) (devices.TemperatureSeries, error) {
		return devices.TemperatureSeries{}, devices.ErrEntityNotFound
	}
	_, err = h.getEntityTemperatureSeries(t.Context(), input)
	var toolErr *mcptools.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "entity_not_found" {
		t.Fatalf("missing entity error: %v", err)
	}
	input.From = "invalid"
	if _, err = h.getEntityTemperatureSeries(t.Context(), input); err == nil {
		t.Fatal("invalid timestamp accepted")
	}
}
