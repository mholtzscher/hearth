//go:build e2e

package hearthd //nolint:testpackage // Uses production Core assembly with public HTTP, MCP, SDK, and Native API.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/nativeapitest"
	esphomeapp "github.com/mholtzscher/hearth/internal/app/esphome"
	"github.com/mholtzscher/hearth/internal/modules/devices/httpapi"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

//nolint:tparallel,gocognit // The serial child owns one causal journey and joins its adapter before Core stops.
func TestESPHomeCoverThroughProductionCore(t *testing.T) {
	t.Parallel()
	broker := startCoreNATSServer(t)
	core := startFunctionCore(t, Config{
		HouseholdTimezone: "UTC", NATSURL: broker.ClientURL(),
		SQLitePath: dbtest.MigratedPath(t), Agent: requiredAgentConfig(t),
	})
	client := newNonPoolingHTTPClient(t)
	// Core belongs to the parent: cancelling the child cannot remove its session-release responder.
	t.Run("native commands", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		config, connected := serveCoverJourneyPeer(t)
		runCtx, stop := context.WithCancel(ctx)
		finished := make(chan error, 1)
		logger, adapterRecords := withRecording(slog.LevelInfo)
		go func() {
			finished <- esphomeapp.Run(runCtx, esphomeapp.Config{
				AdapterID: "esphome-cover", NATSURL: broker.ClientURL(), Devices: []esphomeapp.DeviceConfig{config},
			}, logger)
		}()
		t.Cleanup(func() {
			stop()
			select {
			case err := <-finished:
				require.NoError(t, err)
				require.Len(t, recordsWithEvent(adapterRecords.snapshot(), "adapter.session_released"), 1)
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
		var door, lock httpapi.EntityBody
		waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
			raw := successfulFunctionCoreRequest(t, client, core, http.MethodGet, "/v1/entities?limit=50", "")
			var page httpapi.EntityCollectionBody
			if err := json.Unmarshal(raw, &page); err != nil {
				return false, err
			}
			for _, entity := range page.Items {
				switch entity.Type {
				case "hearth.cover/v1":
					door = entity
				case "hearth.lock/v1":
					lock = entity
				}
			}
			return door.State != nil && lock.State != nil && door.Availability.Status == "available", nil
		})
		require.Equal(t, "Lock remotes", lock.Name)
		runtime := waitForAdapterRuntime(ctx, t, core.address, "esphome-cover")
		waitForCommandSubscription(t, broker, "esphome-cover", runtime, door.ID)

		response := coverJourneyCommand(
			ctx,
			client,
			core.address,
			door.ID,
			`{"operation":"set","parameters":{"position":100}}`,
		)
		frame := awaitCoverJourneyFrame(ctx, t, peer)
		require.True(t, proto.Equal(&apipb.CoverCommandRequest{Key: 1, HasPosition: true, Position: 1}, frame))
		require.NoError(t, peer.Push(&apipb.CoverStateResponse{
			Key: 1, Position: 0.42, CurrentOperation: apipb.CoverOperation_COVER_OPERATION_IS_OPENING,
		}))
		waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
			raw := successfulFunctionCoreRequest(t, client, core, http.MethodGet, "/v1/entities/"+door.ID, "")
			var entity httpapi.EntityBody
			if err := json.Unmarshal(raw, &entity); err != nil {
				return false, err
			}
			value, err := json.Marshal(entity.State.Value)
			return string(value) == `{"motion":"opening","position":42}`, err
		})
		select {
		case result := <-response:
			t.Fatalf("nonmatching position completed command: %+v", result)
		default:
		}
		require.NoError(t, peer.Push(&apipb.CoverStateResponse{
			Key: 1, Position: 0.99, CurrentOperation: apipb.CoverOperation_COVER_OPERATION_IS_OPENING,
		}))
		positionResult := awaitCoverJourneyResult(ctx, t, response)
		require.Equal(t, "satisfied", positionResult.Status)
		require.NotNil(t, positionResult.ObservationID)

		mcpClient := connectAutomationFunctionsMCP(t, core)
		stopRaw := automationFunctionsMCP(t, mcpClient, "execute_entity_command",
			`{"entity_id":"`+door.ID+`","operation":"stop","parameters":{}}`)
		var stopResult struct {
			CommandID     string  `json:"command_id"`
			Status        string  `json:"status"`
			ObservationID *string `json:"observation_id"`
		}
		require.NoError(t, json.Unmarshal(stopRaw, &stopResult))
		require.Equal(t, "dispatched", stopResult.Status)
		require.Nil(t, stopResult.ObservationID)
		require.True(
			t,
			proto.Equal(&apipb.CoverCommandRequest{Key: 1, Stop: true}, awaitCoverJourneyFrame(ctx, t, peer)),
		)
		// The peer emits nothing for Stop. Neither State nor its evidence identity changes.
		stateRaw := automationFunctionsMCP(t, mcpClient, "get_entity", `{"entity_id":"`+door.ID+`"}`)
		var read struct {
			Type  string `json:"type"`
			State struct {
				Value json.RawMessage `json:"value"`
			} `json:"state"`
		}
		require.NoError(t, json.Unmarshal(stateRaw, &read))
		require.Equal(t, "hearth.cover/v1", read.Type)
		require.JSONEq(t, `{"position":99,"motion":"opening"}`, string(read.State.Value))
		afterRaw := successfulFunctionCoreRequest(t, client, core, http.MethodGet, "/v1/entities/"+door.ID, "")
		var after httpapi.EntityBody
		require.NoError(t, json.Unmarshal(afterRaw, &after))
		require.Equal(t, *positionResult.ObservationID, after.State.ObservationID)

		lockResponse := coverJourneyCommand(ctx, client, core.address, lock.ID,
			`{"operation":"set","parameters":{"value":"locked"}}`)
		require.True(t, proto.Equal(&apipb.LockCommandRequest{Key: 2, Command: apipb.LockCommand_LOCK_LOCK},
			awaitCoverJourneyFrame(ctx, t, peer)))
		// Like the researched firmware, this synthetic peer reports the requested lockout State.
		require.NoError(t, peer.Push(&apipb.LockStateResponse{Key: 2, State: apipb.LockState_LOCK_STATE_LOCKED}))
		require.Equal(t, "satisfied", awaitCoverJourneyResult(ctx, t, lockResponse).Status)

		historyRaw := successfulFunctionCoreRequest(t, client, core, http.MethodGet,
			"/v1/entities/"+door.ID+"/commands", "")
		var history httpapi.CommandCollectionBody
		require.NoError(t, json.Unmarshal(historyRaw, &history))
		require.Len(t, history.Items, 2)
		records := map[string]httpapi.CommandRecordBody{}
		for _, record := range history.Items {
			records[record.ID] = record
		}
		require.Equal(t, "satisfied", records[positionResult.CommandID].Status)
		require.Equal(t, positionResult.ObservationID, records[positionResult.CommandID].OutcomeObservationID)
		require.Equal(t, "dispatched", records[stopResult.CommandID].Status)
		require.Nil(t, records[stopResult.CommandID].OutcomeObservationID)
	})
}

type coverJourneyResponse struct {
	status int
	raw    []byte
	err    error
}

func coverJourneyCommand(
	ctx context.Context,
	client *http.Client,
	address, entityID, body string,
) <-chan coverJourneyResponse {
	response := make(chan coverJourneyResponse, 1)
	go func() {
		status, raw, err := functionNetworkRequest(ctx, client, address, http.MethodPost,
			"/v1/entities/"+entityID+"/commands", body)
		response <- coverJourneyResponse{status: status, raw: raw, err: err}
	}()
	return response
}

func awaitCoverJourneyResult(
	ctx context.Context,
	t *testing.T,
	response <-chan coverJourneyResponse,
) httpapi.CommandResultBody {
	t.Helper()
	select {
	case result := <-response:
		require.NoError(t, result.err)
		require.Equal(t, http.StatusOK, result.status, string(result.raw))
		var body httpapi.CommandResultBody
		require.NoError(t, json.Unmarshal(result.raw, &body))
		return body
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return httpapi.CommandResultBody{}
	}
}

func awaitCoverJourneyFrame(ctx context.Context, t *testing.T, peer *nativeapitest.Session) proto.Message {
	t.Helper()
	select {
	case frame := <-peer.Received():
		return frame.Message
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return nil
	}
}

func serveCoverJourneyPeer(t *testing.T) (esphomeapp.DeviceConfig, <-chan *nativeapitest.Session) {
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
		// Synthetic 2026.10.2 protocol peer; not a claim about physical ratgdo behavior.
		connected <- (nativeapitest.Device{
			Plaintext: true, Info: &apipb.DeviceInfoResponse{MacAddress: "02:00:00:00:00:01", Name: "garage"},
			Entities: []proto.Message{
				&apipb.ListEntitiesCoverResponse{Key: 1, Name: "Door", SupportsPosition: true, SupportsStop: true},
				&apipb.ListEntitiesLockResponse{Key: 2, Name: "Lock remotes"},
			},
			States: []proto.Message{
				&apipb.CoverStateResponse{Key: 1},
				&apipb.LockStateResponse{Key: 2, State: apipb.LockState_LOCK_STATE_UNLOCKED},
			},
		}).Serve(connection)
	}()
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return esphomeapp.DeviceConfig{
		MAC: "02:00:00:00:00:01", Address: host, Port: &port, InsecurePlaintext: true,
	}, connected
}
