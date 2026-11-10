//go:build e2e

package hearthd //nolint:testpackage // Exercises production Core and SDK boundaries.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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

	check := func(core *functionCoreRun, expected float64, unit string, minimum, maximum float64) {
		t.Helper()
		httpBody := successfulFunctionCoreRequest(t, client, core, http.MethodGet, "/v1/entities/"+entity, "")
		mcp := connectAutomationFunctionsMCP(t, core)
		mcpBody := automationFunctionsMCP(t, mcp, "get_entity", `{"entity_id":"`+entity+`"}`)
		type entityRead struct {
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
		var httpRead, mcpRead entityRead
		require.NoError(t, json.Unmarshal(httpBody, &httpRead))
		require.NoError(t, json.Unmarshal(mcpBody, &mcpRead))
		value, parseErr := httpRead.State.Value.Float64()
		require.NoError(t, parseErr)
		require.InDelta(t, expected, value, 1e-9)
		require.Equal(t, httpRead.State.Value, mcpRead.State.Value)
		require.Equal(t, httpRead.Quantities, mcpRead.Quantities)
		require.Equal(t, unit, httpRead.Quantities[""].Unit)
		require.InDelta(t, minimum, httpRead.Quantities[""].Range.Minimum, 1e-9)
		require.InDelta(t, maximum, httpRead.Quantities[""].Range.Maximum, 1e-9)
		canonical := successfulFunctionCoreRequest(
			t,
			client,
			core,
			http.MethodGet,
			"/v1/entities/"+entity+"?units=canonical",
			"",
		)
		canonicalMCP := automationFunctionsMCP(t, mcp, "get_entity", `{"entity_id":"`+entity+`","units":"canonical"}`)
		for _, raw := range [][]byte{canonical, canonicalMCP} {
			var value entityRead
			require.NoError(t, json.Unmarshal(raw, &value))
			require.Equal(t, json.Number("21500"), value.State.Value)
			require.Equal(t, "mCel", value.Quantities[""].Unit)
		}
		audit := successfulFunctionCoreRequest(
			t,
			client,
			core,
			http.MethodGet,
			"/v1/entities/"+entity+"/state/history?disposition=all",
			"",
		)
		require.Contains(t, string(audit), `"value":21500`)
	}
	check(core, 70.7, "[degF]", -148, 212)
	require.NoError(t, session.Close())
	core.stop()
	core.join(t)
	config.HouseholdUnitSystem = "metric"
	core = startFunctionCore(t, config)
	check(core, 21.5, "Cel", -100, 100)
}
