package httpapi //nolint:testpackage // Exercises the registered operation and local fixtures.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestTemperatureSeriesHTTP(t *testing.T) {
	t.Parallel()
	stub := &stubDevices{
		getTemperatureSeries: func(_ context.Context, p devices.TemperatureSeriesParams) (devices.TemperatureSeries, error) {
			if p.EntityID != apiEntityID {
				t.Fatalf("entity: %s", p.EntityID)
			}
			return devices.TemperatureSeries{
				EntityID: p.EntityID,
				From:     p.From,
				To:       p.To,
				Buckets:  []devices.TemperatureBucket{{Start: p.From, End: p.From.Add(75e9)}},
			}, nil
		},
	}
	router, _ := testAPI(t, stub)
	path := "/v1/entities/" + string(
		apiEntityID,
	) + "/temperature-series?from=2026-08-22T00:00:00Z&to=2026-08-22T06:00:00Z"
	response := performRequest(router, path)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"mean_milli_celsius":null`) {
		t.Fatalf("response %d: %s", response.Code, response.Body.String())
	}
	stub.getTemperatureSeries = func(context.Context, devices.TemperatureSeriesParams) (devices.TemperatureSeries, error) {
		return devices.TemperatureSeries{}, errors.New("private evidence")
	}
	response = performRequest(router, path)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "private evidence") {
		t.Fatalf("unsafe error %d: %s", response.Code, response.Body.String())
	}
	response = performRequest(router, "/v1/entities/"+string(apiEntityID)+"/temperature-series?from=no&to=no")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid window status %d", response.Code)
	}
	response = performRequest(router, "/v1/entities/"+string(apiEntityID)+"/temperature-series")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing window status %d", response.Code)
	}
}
