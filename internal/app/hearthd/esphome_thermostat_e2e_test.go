//go:build e2e

package hearthd //nolint:testpackage // Exercises production Core, the real Adapter, and public protocols.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/nativeapitest"
	esphomeapp "github.com/mholtzscher/hearth/internal/app/esphome"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices/httpapi"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// This synthetic API 1.19 peer models the cached 2026.10.2 capabilities; it
// does not establish that physical equipment accepts or applies these writes.
//
//nolint:tparallel,gocognit // A single ordered journey owns its Adapter and evidence.
func TestESPHomeThermostatThroughProductionCore(t *testing.T) {
	t.Parallel()
	broker := startCoreNATSServer(t)
	core := startFunctionCore(t, Config{
		HouseholdTimezone: "UTC", HouseholdUnitSystem: "us", NATSURL: broker.ClientURL(),
		SQLitePath: dbtest.MigratedPath(t), Agent: requiredAgentConfig(t),
	})
	client := newNonPoolingHTTPClient(t)
	// Core belongs to the parent and remains alive for child Adapter release.
	t.Run("scripted native peer", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		config, connected := thermostatJourneyPeer(t)
		runCtx, stop := context.WithCancel(ctx)
		finished := make(chan error, 1)
		logger, records := withRecording(slog.LevelInfo)
		go func() {
			finished <- esphomeapp.Run(runCtx, esphomeapp.Config{
				AdapterID: "esphome-thermostat", NATSURL: broker.ClientURL(),
				Devices: []esphomeapp.DeviceConfig{config},
			}, logger)
		}()
		t.Cleanup(func() {
			stop()
			select {
			case err := <-finished:
				require.NoError(t, err)
				require.Len(t, recordsWithEvent(records.snapshot(), "adapter.session_released"), 1)
			case <-time.After(5 * time.Second):
				t.Error("ESPHome adapter did not join")
			}
		})
		var peer *nativeapitest.Session
		select {
		case peer = <-connected:
			t.Cleanup(peer.Close)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}

		entities := make(map[string]httpapi.EntityBody)
		thermostatJourneyWait(t, 10*time.Second, func() (bool, error) {
			raw := successfulFunctionCoreRequest(t, client, core, http.MethodGet, "/v1/entities?limit=50", "")
			var page httpapi.EntityCollectionBody
			if err := json.Unmarshal(raw, &page); err != nil {
				return false, err
			}
			for _, entity := range page.Items {
				entities[entity.Type] = entity
			}
			for _, kind := range []string{"hearth.thermostat/v1", "hearth.numericsetting/v1", "hearth.enumsetting/v1"} {
				if entities[kind].State == nil || entities[kind].Availability.Status != "available" {
					return false, nil
				}
			}
			return true, nil
		})
		thermostat := entities["hearth.thermostat/v1"]
		number := entities["hearth.numericsetting/v1"]
		selection := entities["hearth.enumsetting/v1"]
		require.Equal(t, "Climate", thermostat.Name)
		require.Equal(t, "Humidification target", number.Name)
		require.Equal(t, "Fan duty", selection.Name)
		require.NotEmpty(t, thermostat.ID)
		require.NotEmpty(t, number.ID)
		require.NotEmpty(t, selection.ID)
		require.NotEqual(t, thermostat.ID, number.ID)
		require.NotEqual(t, number.ID, selection.ID)
		require.Equal(t, thermostat.DeviceID, number.DeviceID)
		require.Equal(t, thermostat.DeviceID, selection.DeviceID)
		var device httpapi.DeviceBody
		require.NoError(t, json.Unmarshal(successfulFunctionCoreRequest(t, client, core, http.MethodGet,
			"/v1/devices/"+thermostat.DeviceID, ""), &device))
		require.Equal(t, "thermostat", device.Kind)

		canonical := thermostatJourneyRead(t, client, core, thermostat.ID, "?units=canonical")
		require.Equal(t, "mCel", canonical.Quantities["/current_temperature"].Unit)
		require.JSONEq(t, `{"mode":"heat_cool","activity":"idle","current_temperature":20000,
			"target_temperature_low":17000,"target_temperature_high":24000,"fan_mode":"Automatic",
			"current_humidity":45,"target_humidity":50}`, thermostatJourneyJSON(t, canonical.State.Value))
		require.Equal(t, "[degF]", thermostat.Quantities["/current_temperature"].Unit)
		projected := thermostat.State.Value.(map[string]any)
		require.InDelta(t, 68, projected["current_temperature"], 1e-9)
		require.InDelta(t, 62.6, projected["target_temperature_low"], 1e-9)
		require.InDelta(t, 75.2, projected["target_temperature_high"], 1e-9)
		require.NotContains(t, projected, "target_temperature")
		require.Equal(t, "1", number.Quantities["/value"].Unit)
		require.JSONEq(t, `{"minimum":10,"maximum":80,"choices":[]}`, thermostatJourneyJSON(t, number.Support["state"]))
		require.JSONEq(t, `{"mode":"value","value":40}`, thermostatJourneyJSON(t, number.State.Value))
		require.JSONEq(t, `"Auto"`, thermostatJourneyJSON(t, selection.State.Value))
		support := canonical.Support
		require.JSONEq(t, `{"modes":["off","heat_cool","cool","heat","fan_only"],
			"fan_modes":["Automatic","Speed 1 (Low)"],"temperature_minimum":10000,"temperature_maximum":32222,
			"humidity_minimum":10,"humidity_maximum":80}`, thermostatJourneyJSON(t, support["state"]))
		operations := support["operations"].(map[string]any)
		require.Contains(t, operations, "set_range")
		require.NotContains(t, operations, "set_target")
		require.Contains(t, operations, "set_fan_mode")
		require.Contains(t, operations, "set_humidity")

		mcpClient := connectAutomationFunctionsMCP(t, core)
		mcpRead := automationFunctionsMCP(t, mcpClient, "get_entity",
			`{"entity_id":"`+thermostat.ID+`","units":"canonical"}`)
		var mcpEntity struct {
			EntityID string `json:"entity_id"`
			DeviceID string `json:"device_id"`
			Type     string `json:"type"`
			State    *struct {
				Value         json.RawMessage `json:"value"`
				ObservationID string          `json:"observation_id"`
			} `json:"state"`
		}
		require.NoError(t, json.Unmarshal(mcpRead, &mcpEntity))
		require.Equal(t, thermostat.ID, mcpEntity.EntityID)
		require.Equal(t, thermostat.DeviceID, mcpEntity.DeviceID)
		require.Equal(t, thermostat.Type, mcpEntity.Type)
		require.NotNil(t, mcpEntity.State)
		require.Equal(t, canonical.State.ObservationID, mcpEntity.State.ObservationID)
		require.JSONEq(t, thermostatJourneyJSON(t, canonical.State.Value), string(mcpEntity.State.Value))
		runtime := thermostatJourneyRuntime(ctx, t, core.address)
		thermostatJourneySubscription(t, broker, runtime, thermostat.ID, "set_range")
		thermostatJourneySubscription(t, broker, runtime, thermostat.ID, "set_fan_mode")
		thermostatJourneySubscription(t, broker, runtime, thermostat.ID, "set_humidity")
		thermostatJourneySubscription(t, broker, runtime, thermostat.ID, "set_mode")
		thermostatJourneySubscription(t, broker, runtime, number.ID, "set")
		thermostatJourneySubscription(t, broker, runtime, selection.ID, "set")

		response := coverJourneyCommand(ctx, client, core.address, thermostat.ID,
			`{"operation":"set_range","parameters":{"low":18000,"high":22000}}`)
		thermostatJourneyFrame(ctx, t, peer, &apipb.ClimateCommandRequest{
			Key: 1, HasTargetTemperatureLow: true, TargetTemperatureLow: 18,
			HasTargetTemperatureHigh: true, TargetTemperatureHigh: 22,
		})
		intermediate := thermostatJourneyClimate(18, 24, "Automatic", apipb.ClimateMode_CLIMATE_MODE_HEAT_COOL)
		require.NoError(t, peer.Push(intermediate))
		var intermediateID string
		thermostatJourneyWait(t, 5*time.Second, func() (bool, error) {
			entity := thermostatJourneyRead(t, client, core, thermostat.ID, "?units=canonical")
			value := entity.State.Value.(map[string]any)
			if value["target_temperature_low"] != float64(18000) || value["target_temperature_high"] != float64(24000) {
				return false, nil
			}
			intermediateID = entity.State.ObservationID
			return true, nil
		})
		select {
		case result := <-response:
			t.Fatalf("one matching bound completed range: %+v", result)
		default:
		}
		require.NoError(
			t,
			peer.Push(thermostatJourneyClimate(18, 22, "Automatic", apipb.ClimateMode_CLIMATE_MODE_HEAT_COOL)),
		)
		rangeResult := awaitCoverJourneyResult(ctx, t, response)
		require.Equal(t, "satisfied", rangeResult.Status)
		require.NotNil(t, rangeResult.ObservationID)
		require.NotEqual(t, intermediateID, *rangeResult.ObservationID)
		thermostatJourneyHistory(
			t,
			client,
			core,
			thermostat.ID,
			rangeResult.CommandID,
			rangeResult.ObservationID,
			"set_range",
			`{"low":18000,"high":22000}`,
			`{"mode":"heat_cool","activity":"idle","current_temperature":20000,
			"target_temperature_low":18000,"target_temperature_high":22000,"fan_mode":"Automatic",
			"current_humidity":45,"target_humidity":50}`,
		)

		// MCP must wait for the custom fan report, not a standard fan frame.
		fanResponse := thermostatJourneyMCPCommand(ctx, mcpClient, thermostat.ID)
		thermostatJourneyFrame(ctx, t, peer, &apipb.ClimateCommandRequest{
			Key: 1, HasCustomFanMode: true, CustomFanMode: "Speed 1 (Low)",
		})
		require.NoError(
			t,
			peer.Push(thermostatJourneyClimate(18, 22, "Speed 1 (Low)", apipb.ClimateMode_CLIMATE_MODE_HEAT_COOL)),
		)
		fanResult := thermostatJourneyMCPResult(ctx, t, fanResponse)
		require.Equal(t, "satisfied", fanResult.Status)
		require.NotNil(t, fanResult.ObservationID)
		require.JSONEq(t, `{"mode":"heat_cool","activity":"idle","current_temperature":20000,
			"target_temperature_low":18000,"target_temperature_high":22000,"fan_mode":"Speed 1 (Low)",
			"current_humidity":45,"target_humidity":50}`, string(fanResult.Value))
		thermostatJourneyHistory(
			t,
			client,
			core,
			thermostat.ID,
			fanResult.CommandID,
			fanResult.ObservationID,
			"set_fan_mode",
			`{"fan_mode":"Speed 1 (Low)"}`,
			`{"mode":"heat_cool","activity":"idle","current_temperature":20000,
			"target_temperature_low":18000,"target_temperature_high":22000,"fan_mode":"Speed 1 (Low)",
			"current_humidity":45,"target_humidity":50}`,
		)

		humidityResponse := coverJourneyCommand(ctx, client, core.address, thermostat.ID,
			`{"operation":"set_humidity","parameters":{"humidity":60}}`)
		thermostatJourneyFrame(ctx, t, peer, &apipb.ClimateCommandRequest{
			Key: 1, HasTargetHumidity: true, TargetHumidity: 60,
		})
		humidityState := thermostatJourneyClimate(18, 22, "Speed 1 (Low)", apipb.ClimateMode_CLIMATE_MODE_HEAT_COOL)
		humidityState.TargetHumidity = 60
		require.NoError(t, peer.Push(humidityState))
		humidityResult := awaitCoverJourneyResult(ctx, t, humidityResponse)
		require.Equal(t, "satisfied", humidityResult.Status)
		require.NotNil(t, humidityResult.ObservationID)
		thermostatJourneyHistory(
			t,
			client,
			core,
			thermostat.ID,
			humidityResult.CommandID,
			humidityResult.ObservationID,
			"set_humidity",
			`{"humidity":60}`,
			`{"mode":"heat_cool","activity":"idle","current_temperature":20000,
			"target_temperature_low":18000,"target_temperature_high":22000,"fan_mode":"Speed 1 (Low)",
			"current_humidity":45,"target_humidity":60}`,
		)

		numberResponse := coverJourneyCommand(ctx, client, core.address, number.ID,
			`{"operation":"set","parameters":{"mode":"value","value":55}}`)
		thermostatJourneyFrame(ctx, t, peer, &apipb.NumberCommandRequest{Key: 2, State: 55})
		require.NoError(t, peer.Push(&apipb.NumberStateResponse{Key: 2, State: 55}))
		numberResult := awaitCoverJourneyResult(ctx, t, numberResponse)
		require.Equal(t, "satisfied", numberResult.Status)
		require.NotNil(t, numberResult.ObservationID)
		thermostatJourneyHistory(t, client, core, number.ID, numberResult.CommandID, numberResult.ObservationID, "set",
			`{"mode":"value","value":55}`, `{"mode":"value","value":55}`)

		selectResponse := coverJourneyCommand(ctx, client, core.address, selection.ID,
			`{"operation":"set","parameters":{"value":"Continuous"}}`)
		thermostatJourneyFrame(ctx, t, peer, &apipb.SelectCommandRequest{Key: 3, State: "Continuous"})
		require.NoError(t, peer.Push(&apipb.SelectStateResponse{Key: 3, State: "Continuous"}))
		selectResult := awaitCoverJourneyResult(ctx, t, selectResponse)
		require.Equal(t, "satisfied", selectResult.Status)
		require.NotNil(t, selectResult.ObservationID)
		thermostatJourneyHistory(
			t,
			client,
			core,
			selection.ID,
			selectResult.CommandID,
			selectResult.ObservationID,
			"set",
			`{"value":"Continuous"}`,
			`"Continuous"`,
		)

		definition := fmt.Sprintf(`{"name":"Thermostat mode","enabled":false,"triggers":[],
			"steps":[{"id":"mode","kind":"command","entity_id":%q,"operation":"set_mode",
			"parameters":{"mode":"heat"}}]}`, thermostat.ID)
		automationID := createBranchingAutomation(ctx, t, core.address, definition)
		manual := conditionsManualRun(ctx, t, core.address, automationID, "")
		require.Equal(t, http.StatusAccepted, manual.StatusCode)
		runID := conditionsLocationRunID(t, manual)
		require.NoError(t, manual.Body.Close())
		thermostatJourneyFrame(ctx, t, peer, &apipb.ClimateCommandRequest{
			Key: 1, HasMode: true, Mode: apipb.ClimateMode_CLIMATE_MODE_HEAT,
		})
		modeState := thermostatJourneyClimate(18, 22, "Speed 1 (Low)", apipb.ClimateMode_CLIMATE_MODE_HEAT)
		modeState.TargetHumidity = 60
		require.NoError(t, peer.Push(modeState))
		thermostatJourneyWait(t, 20*time.Second, func() (bool, error) {
			raw := successfulFunctionCoreRequest(t, client, core, http.MethodGet,
				"/v1/automations/"+automationID+"/history", "")
			var page struct {
				Items []struct {
					ID     string `json:"id"`
					Kind   string `json:"kind"`
					Status string `json:"status"`
				} `json:"items"`
			}
			if err := json.Unmarshal(raw, &page); err != nil {
				return false, err
			}
			return len(page.Items) == 1 && page.Items[0].ID == runID &&
				page.Items[0].Kind == "run" && page.Items[0].Status != "running", nil
		})
		run := readBranchingRun(t, sliceRequest(ctx, t, http.MethodGet, core.address,
			"/v1/automations/"+automationID+"/history/"+runID, nil))
		require.Equal(t, "succeeded", run.Status)
		require.Len(t, run.Executions, 1)
		commandID := requireVerifiedCommandLink(t, run.Executions, "mode")
		var linked httpapi.CommandRecordBody
		require.NoError(t, json.Unmarshal(successfulFunctionCoreRequest(t, client, core, http.MethodGet,
			"/v1/commands/"+commandID, ""), &linked))
		require.Equal(t, commandID, linked.ID)
		require.Equal(t, thermostat.ID, linked.EntityID)
		require.Equal(t, "set_mode", linked.Operation)
		require.JSONEq(t, `{"mode":"heat"}`, thermostatJourneyJSON(t, linked.Parameters))
		require.Equal(t, "satisfied", linked.Status)
		require.NotNil(t, linked.OutcomeObservationID)
		thermostatJourneyHistory(t, client, core, thermostat.ID, commandID, linked.OutcomeObservationID,
			"set_mode", `{"mode":"heat"}`, `{"mode":"heat","activity":"idle","current_temperature":20000,
			"target_temperature_low":18000,"target_temperature_high":22000,"fan_mode":"Speed 1 (Low)",
			"current_humidity":45,"target_humidity":60}`)
		select {
		case extra := <-peer.Received():
			t.Fatalf("unexpected additional native command: %v", extra.Message)
		default:
		}
	})
}

func thermostatJourneyWait(t *testing.T, timeout time.Duration, condition func() (bool, error)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		matched, err := condition()
		require.NoError(t, err)
		if matched {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("timed out waiting for journey condition")
		}
	}
}

func thermostatJourneyRuntime(ctx context.Context, t *testing.T, address string) string {
	t.Helper()
	var runtimeID string
	thermostatJourneyWait(t, 10*time.Second, func() (bool, error) {
		response := sliceRequest(ctx, t, http.MethodGet, address, "/v1/adapters/esphome-thermostat", nil)
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return false, nil
		}
		var body struct {
			Health struct {
				Runtime *struct {
					ID string `json:"id"`
				} `json:"runtime"`
			} `json:"health"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			return false, err
		}
		if body.Health.Runtime == nil || body.Health.Runtime.ID == "" {
			return false, nil
		}
		runtimeID = body.Health.Runtime.ID
		return true, nil
	})
	return runtimeID
}

func thermostatJourneySubscription(t *testing.T, server *natsserver.Server, runtime, entity, operation string) {
	t.Helper()
	subject, err := natswire.CommandSubject("esphome-thermostat", runtime, entity, operation)
	require.NoError(t, err)
	thermostatJourneyWait(t, 10*time.Second, func() (bool, error) {
		subscriptions, subscriptionsErr := server.Subsz(&natsserver.SubszOptions{Subscriptions: true, Test: subject})
		if subscriptionsErr != nil {
			return false, subscriptionsErr
		}
		return subscriptions.Total > 0, nil
	})
}

func thermostatJourneyRead(
	t *testing.T,
	client *http.Client,
	core *functionCoreRun,
	id, query string,
) httpapi.EntityBody {
	t.Helper()
	var entity httpapi.EntityBody
	require.NoError(t, json.Unmarshal(successfulFunctionCoreRequest(t, client, core, http.MethodGet,
		"/v1/entities/"+id+query, ""), &entity))
	require.NotNil(t, entity.State)
	return entity
}

func thermostatJourneyJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return string(raw)
}

func thermostatJourneyFrame(ctx context.Context, t *testing.T, peer *nativeapitest.Session, expected proto.Message) {
	t.Helper()
	actual := awaitCoverJourneyFrame(ctx, t, peer)
	require.True(t, proto.Equal(expected, actual), "native frame: got %v, want %v", actual, expected)
}

type thermostatJourneyMCPResponse struct {
	result *mcp.CallToolResult
	err    error
}

type thermostatJourneyMCPReceipt struct {
	CommandID     string          `json:"command_id"`
	Status        string          `json:"status"`
	ObservationID *string         `json:"observation_id"`
	Value         json.RawMessage `json:"value"`
}

func thermostatJourneyMCPCommand(
	ctx context.Context,
	client *mcp.ClientSession,
	entityID string,
) <-chan thermostatJourneyMCPResponse {
	response := make(chan thermostatJourneyMCPResponse, 1)
	go func() {
		result, err := client.CallTool(ctx, &mcp.CallToolParams{
			Name: "execute_entity_command",
			Arguments: json.RawMessage(`{"entity_id":"` + entityID +
				`","operation":"set_fan_mode","parameters":{"fan_mode":"Speed 1 (Low)"}}`),
		})
		response <- thermostatJourneyMCPResponse{result: result, err: err}
	}()
	return response
}

func thermostatJourneyMCPResult(
	ctx context.Context,
	t *testing.T,
	response <-chan thermostatJourneyMCPResponse,
) thermostatJourneyMCPReceipt {
	t.Helper()
	select {
	case received := <-response:
		require.NoError(t, received.err)
		require.NotNil(t, received.result)
		require.False(t, received.result.IsError)
		for _, content := range received.result.Content {
			if text, ok := content.(*mcp.TextContent); ok && json.Valid([]byte(text.Text)) {
				var result thermostatJourneyMCPReceipt
				require.NoError(t, json.Unmarshal([]byte(text.Text), &result))
				return result
			}
		}
		t.Fatal("MCP command returned no JSON text")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return thermostatJourneyMCPReceipt{}
}

func thermostatJourneyHistory(t *testing.T, client *http.Client, core *functionCoreRun, entityID string,
	commandID string, observationID *string, operation, parameters, state string,
) {
	t.Helper()
	require.NotEmpty(t, commandID)
	require.NotNil(t, observationID)
	var record httpapi.CommandRecordBody
	require.NoError(t, json.Unmarshal(successfulFunctionCoreRequest(t, client, core, http.MethodGet,
		"/v1/commands/"+commandID, ""), &record))
	require.Equal(t, commandID, record.ID)
	require.Equal(t, entityID, record.EntityID)
	require.Equal(t, operation, record.Operation)
	require.JSONEq(t, parameters, thermostatJourneyJSON(t, record.Parameters))
	require.Equal(t, "satisfied", record.Status)
	require.Equal(t, observationID, record.OutcomeObservationID)
	var history httpapi.EntityStateHistoryCollectionBody
	require.NoError(t, json.Unmarshal(successfulFunctionCoreRequest(t, client, core, http.MethodGet,
		"/v1/entities/"+entityID+"/state/history", ""), &history))
	for _, observation := range history.Items {
		if observation.ObservationID == *observationID {
			require.Equal(t, "applied", observation.Disposition)
			require.JSONEq(t, state, thermostatJourneyJSON(t, observation.Value))
			return
		}
	}
	t.Fatalf("outcome observation %q missing from history", *observationID)
}

func thermostatJourneyClimate(low, high float32, fan string, mode apipb.ClimateMode) *apipb.ClimateStateResponse {
	return &apipb.ClimateStateResponse{
		Key: 1, Mode: mode, Action: apipb.ClimateAction_CLIMATE_ACTION_IDLE,
		CurrentTemperature: 20, TargetTemperatureLow: low, TargetTemperatureHigh: high,
		CustomFanMode: fan, CurrentHumidity: 45, TargetHumidity: 50,
	}
}

func thermostatJourneyPeer(t *testing.T) (esphomeapp.DeviceConfig, <-chan *nativeapitest.Session) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	connected := make(chan *nativeapitest.Session, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		connected <- (nativeapitest.Device{
			Plaintext: true,
			Info: &apipb.DeviceInfoResponse{
				MacAddress: "02:00:00:00:00:02", Name: "synthetic-thermostat", EsphomeVersion: "2026.10.2",
			},
			Entities: []proto.Message{
				&apipb.ListEntitiesClimateResponse{
					Key: 1, Name: "Climate", FeatureFlags: 59,
					SupportedModes: []apipb.ClimateMode{
						apipb.ClimateMode_CLIMATE_MODE_OFF, apipb.ClimateMode_CLIMATE_MODE_HEAT_COOL,
						apipb.ClimateMode_CLIMATE_MODE_COOL, apipb.ClimateMode_CLIMATE_MODE_HEAT,
						apipb.ClimateMode_CLIMATE_MODE_FAN_ONLY,
					},
					SupportedCustomFanModes: []string{"Automatic", "Speed 1 (Low)"}, VisualMinTemperature: 10,
					VisualMaxTemperature: 32.22222, VisualTargetTemperatureStep: 0.5,
					VisualCurrentTemperatureStep: 0.1, VisualMinHumidity: 10, VisualMaxHumidity: 80,
				},
				&apipb.ListEntitiesNumberResponse{Key: 2, Name: "Humidification target", MinValue: 10, MaxValue: 80, Step: 1},
				&apipb.ListEntitiesSelectResponse{Key: 3, Name: "Fan duty", Options: []string{"Auto", "Continuous"}},
			},
			States: []proto.Message{
				thermostatJourneyClimate(17, 24, "Automatic", apipb.ClimateMode_CLIMATE_MODE_HEAT_COOL),
				&apipb.NumberStateResponse{Key: 2, State: 40},
				&apipb.SelectStateResponse{Key: 3, State: "Auto"},
			},
		}).Serve(connection)
	}()
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return esphomeapp.DeviceConfig{
		MAC: "02:00:00:00:00:02", Address: host, Port: &port, InsecurePlaintext: true,
	}, connected
}
