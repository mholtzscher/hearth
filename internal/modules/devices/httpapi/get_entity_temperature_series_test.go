package httpapi //nolint:testpackage // Exercises the registered operation and local fixtures.

import (
	"context"
	"encoding/json"
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

func TestTemperatureSeriesQueryContract(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{})
	response := performRequest(router, "/openapi.json")
	if response.Code != http.StatusOK {
		t.Fatalf("OpenAPI status = %d", response.Code)
	}
	var document struct {
		Paths map[string]struct {
			Get struct {
				Parameters []struct {
					Name     string `json:"name"`
					In       string `json:"in"`
					Required bool   `json:"required"`
					Schema   struct {
						Type   string `json:"type"`
						Format string `json:"format"`
					} `json:"schema"`
				} `json:"parameters"`
			} `json:"get"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	operation, exists := document.Paths["/v1/entities/{entity_id}/temperature-series"]
	if !exists {
		t.Fatal("temperature series operation missing from OpenAPI")
	}
	seen := make(map[string]bool)
	for _, parameter := range operation.Get.Parameters {
		if parameter.Name != "from" && parameter.Name != "to" {
			continue
		}
		if seen[parameter.Name] || parameter.In != "query" || !parameter.Required ||
			parameter.Schema.Type != "string" ||
			parameter.Schema.Format != "date-time" {
			t.Fatalf("unexpected parameter: %+v", parameter)
		}
		seen[parameter.Name] = true
	}
	if !seen["from"] || !seen["to"] {
		t.Fatalf("missing required query parameters: %v", seen)
	}
	path := "/v1/entities/" + string(apiEntityID) + "/temperature-series"
	for _, query := range []string{
		"", "?from=2026-08-22T00:00:00Z", "?to=2026-08-22T06:00:00Z",
		"?from=invalid&to=2026-08-22T06:00:00Z", "?from=2026-08-22T00:00:00Z&to=invalid",
	} {
		result := performRequest(router, path+query)
		if result.Code != http.StatusBadRequest {
			t.Errorf("query %q: status %d, body %s", query, result.Code, result.Body.String())
		}
	}
}
