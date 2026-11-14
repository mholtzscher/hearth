//go:build e2e

package hearthd //nolint:testpackage // Exercises production Core and SDK boundaries.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func TestQuantityPresentationThroughCore(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	nats := startCoreNATSServer(t)
	path := dbtest.MigratedPath(t)
	config := Config{
		HouseholdTimezone:   "UTC",
		HouseholdUnitSystem: "us",
		NATSURL:             nats.ClientURL(),
		SQLitePath:          path,
		Agent:               requiredAgentConfig(t),
	}
	core := startFunctionCore(t, config)
	client := newNonPoolingHTTPClient(t)
	session, err := adapter.Connect(
		ctx,
		adapter.Config{
			AdapterID:       "simulator",
			SoftwareName:    "quantity-journey",
			SoftwareVersion: "1",
			NATSURL:         nats.ClientURL(),
		},
	)
	require.NoError(t, err)
	binding, err := session.Register(ctx, adapter.Registration{
		BindingKey: "quantity",
		Device:     adapter.DeviceDescriptor{Name: "Quantity sensor", Kind: "sensor"},
		Entities: []adapter.EntityDescriptor{
			{
				Key:        "temperature",
				ExternalID: "quantity.temperature",
				Name:       "Temperature",
				Type:       "hearth.numericsensor/v1",
				Support: json.RawMessage(
					`{"state":{"minimum":-100000,"maximum":100000,"unit":"mCel","quantity_kind":"temperature"},"operations":{}}`,
				),
			},
			{
				Key:        "hourly",
				ExternalID: "quantity.hourly",
				Name:       "Hourly forecast",
				Type:       "hearth.hourlyforecast/v1",
				Support:    json.RawMessage(`{"state":{},"operations":{}}`),
			},
			{
				Key:        "daily",
				ExternalID: "quantity.daily",
				Name:       "Daily forecast",
				Type:       "hearth.dailyforecast/v1",
				Support:    json.RawMessage(`{"state":{},"operations":{}}`),
			},
		},
	})
	require.NoError(t, err)
	entity := string(bindingEntityID(t, binding, "temperature"))
	id, err := session.PublishObservation(
		ctx,
		adapter.Observation{
			EntityID:          entity,
			Value:             json.RawMessage(`21500`),
			AdapterReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
		},
	)
	require.NoError(t, err)
	report := historyJourneyAwait(ctx, t, client, core.address, entity, string(id))
	require.Equal(t, "applied", report.Disposition)
	require.InDelta(t, 21500, report.Value, 0)
	forecastReports := []struct{ key, field, value string }{
		{"hourly", "temperature", `{"periods":[{"starts_at":"2026-11-10T12:00:00Z","temperature":2.150e1}]}`},
		{
			"daily",
			"temperature_min",
			`{"periods":[{"starts_at":"2026-11-10T00:00:00Z","date":"2026-11-10","temperature_min":2.150e1}]}`,
		},
	}
	for _, forecast := range forecastReports {
		_, publishErr := session.PublishObservation(ctx, adapter.Observation{
			EntityID: string(bindingEntityID(t, binding, forecast.key)), Value: json.RawMessage(forecast.value),
			AdapterReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
		require.NoError(t, publishErr)
	}

	check := func(core *functionCoreRun, expected float64, unit string, minimum, maximum float64) {
		t.Helper()
		httpBody := successfulFunctionCoreRequest(t, client, core, http.MethodGet, "/v1/entities/"+entity, "")
		mcp := connectAutomationFunctionsMCP(t, core)
		mcpBody := automationFunctionsMCP(t, mcp, "get_entity", `{"entity_id":"`+entity+`"}`)
		type legacyEntityRead struct {
			State struct {
				Value json.Number `json:"value"`
			} `json:"state"`
			Quantities map[string]struct {
				Unit           string `json:"unit"`
				Label          string `json:"label"`
				FractionDigits int    `json:"fraction_digits"`
				Range          struct {
					Minimum float64 `json:"minimum"`
					Maximum float64 `json:"maximum"`
				} `json:"range"`
			} `json:"quantities"`
		}
		var httpRead struct {
			State struct {
				Canonical struct {
					Value json.Number `json:"value"`
					Unit  string      `json:"unit"`
				} `json:"canonical"`
				Display struct {
					Value          json.Number `json:"value"`
					Unit           string      `json:"unit"`
					Label          string      `json:"label"`
					FractionDigits int         `json:"fraction_digits"`
					Range          struct {
						Minimum float64 `json:"minimum"`
						Maximum float64 `json:"maximum"`
					} `json:"range"`
				} `json:"display"`
			} `json:"state"`
		}
		var mcpRead legacyEntityRead
		require.NoError(t, json.Unmarshal(httpBody, &httpRead))
		require.NoError(t, json.Unmarshal(mcpBody, &mcpRead))
		value, parseErr := httpRead.State.Display.Value.Float64()
		require.NoError(t, parseErr)
		require.InDelta(t, expected, value, 1e-9)
		require.Equal(t, httpRead.State.Display.Value, mcpRead.State.Value)
		require.Equal(t, unit, httpRead.State.Display.Unit)
		require.Equal(t, httpRead.State.Display.Unit, mcpRead.Quantities[""].Unit)
		require.Equal(t, httpRead.State.Display.Label, mcpRead.Quantities[""].Label)
		require.Equal(t, httpRead.State.Display.FractionDigits, mcpRead.Quantities[""].FractionDigits)
		require.InDelta(t, minimum, httpRead.State.Display.Range.Minimum, 1e-9)
		require.InDelta(t, maximum, httpRead.State.Display.Range.Maximum, 1e-9)
		require.Equal(t, httpRead.State.Display.Range, mcpRead.Quantities[""].Range)
		require.Equal(t, json.Number("21500"), httpRead.State.Canonical.Value)
		require.Equal(t, "mCel", httpRead.State.Canonical.Unit)
		canonicalMCP := automationFunctionsMCP(t, mcp, "get_entity", `{"entity_id":"`+entity+`","units":"canonical"}`)
		var canonicalRead legacyEntityRead
		require.NoError(t, json.Unmarshal(canonicalMCP, &canonicalRead))
		require.Equal(t, httpRead.State.Canonical.Value, canonicalRead.State.Value)
		require.Equal(t, httpRead.State.Canonical.Unit, canonicalRead.Quantities[""].Unit)
		audit := successfulFunctionCoreRequest(
			t,
			client,
			core,
			http.MethodGet,
			"/v1/entities/"+entity+"/state/history?disposition=all",
			"",
		)
		require.Contains(t, string(audit), `"value":21500`)
		for _, forecast := range forecastReports {
			forecastEntity := string(bindingEntityID(t, binding, forecast.key))
			type forecastRead struct {
				State *struct {
					Value struct {
						Periods []map[string]json.RawMessage `json:"periods"`
					} `json:"value"`
				} `json:"state"`
				Quantities map[string]devices.QuantityMetadata `json:"quantities"`
			}
			var httpForecast struct {
				State *struct {
					Canonical struct {
						Periods []map[string]json.RawMessage `json:"periods"`
					} `json:"canonical"`
					Display struct {
						Periods []map[string]json.RawMessage `json:"periods"`
					} `json:"display"`
				} `json:"state"`
			}
			var mcpForecast forecastRead
			require.Eventually(t, func() bool {
				raw := successfulFunctionCoreRequest(
					t,
					client,
					core,
					http.MethodGet,
					"/v1/entities/"+forecastEntity,
					"",
				)
				require.NoError(t, json.Unmarshal(raw, &httpForecast))
				return httpForecast.State != nil
			}, 5*time.Second, 10*time.Millisecond)
			raw := automationFunctionsMCP(t, mcp, "get_entity", `{"entity_id":"`+forecastEntity+`"}`)
			require.NoError(t, json.Unmarshal(raw, &mcpForecast))
			field := forecast.field
			require.Len(t, httpForecast.State.Canonical.Periods, 1)
			require.Len(t, httpForecast.State.Display.Periods, 1)
			require.Len(t, mcpForecast.State.Value.Periods, 1)
			canonicalPeriod := httpForecast.State.Canonical.Periods[0]
			displayPeriod := httpForecast.State.Display.Periods[0]
			mcpPeriod := mcpForecast.State.Value.Periods[0]
			require.Len(t, displayPeriod, len(canonicalPeriod))
			require.Len(t, mcpPeriod, len(canonicalPeriod))
			for name, value := range canonicalPeriod {
				require.Contains(t, displayPeriod, name)
				require.Contains(t, mcpPeriod, name)
				if name != field {
					require.JSONEq(t, string(value), string(displayPeriod[name]))
					require.JSONEq(t, string(value), string(mcpPeriod[name]))
				}
			}
			require.Len(t, mcpForecast.Quantities, 1)
			var display, canonical struct {
				Value float64 `json:"value"`
				Unit  string  `json:"unit"`
			}
			require.NoError(t, json.Unmarshal(httpForecast.State.Display.Periods[0][field], &display))
			require.NoError(t, json.Unmarshal(httpForecast.State.Canonical.Periods[0][field], &canonical))
			require.InDelta(t, expected, display.Value, 1e-9)
			require.Equal(t, unit, display.Unit)
			require.InDelta(t, 21.5, canonical.Value, 0)
			require.Equal(t, "Cel", canonical.Unit)
			var mcpValue float64
			require.NoError(t, json.Unmarshal(mcpForecast.State.Value.Periods[0][field], &mcpValue))
			require.InDelta(t, display.Value, mcpValue, 0)
			require.Equal(t, display.Unit, mcpForecast.Quantities["/periods/0/"+field].Unit)
			require.NotContains(t, httpForecast.State.Display.Periods[0], forecast.field+"_c")
			forecastCanonicalMCP := automationFunctionsMCP(
				t,
				mcp,
				"get_entity",
				`{"entity_id":"`+forecastEntity+`","units":"canonical"}`,
			)
			var mcpSource forecastRead
			require.NoError(t, json.Unmarshal(forecastCanonicalMCP, &mcpSource))
			require.Len(t, mcpSource.Quantities, 1)
			require.Contains(t, mcpSource.Quantities, "/periods/0/"+field)
			// Forecast ingestion normalizes the typed float to this canonical token;
			// neither household projection nor the preference restart may rewrite it.
			require.Equal(t, "21.5", string(mcpSource.State.Value.Periods[0][forecast.field]))
			require.Equal(t, canonical.Unit, mcpSource.Quantities["/periods/0/"+forecast.field].Unit)
			forecastAudit := successfulFunctionCoreRequest(
				t,
				client,
				core,
				http.MethodGet,
				"/v1/entities/"+forecastEntity+"/state/history?disposition=all",
				"",
			)
			require.Contains(
				t,
				string(forecastAudit),
				`"`+forecast.field+`":`+string(mcpSource.State.Value.Periods[0][forecast.field]),
			)
		}
	}
	check(core, 70.7, "[degF]", -148, 212)
	require.NoError(t, session.Close())
	core.stop()
	core.join(t)
	config.HouseholdUnitSystem = "metric"
	core = startFunctionCore(t, config)
	check(core, 21.5, "Cel", -100, 100)
}
