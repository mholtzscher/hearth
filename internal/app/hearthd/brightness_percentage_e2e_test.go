//go:build e2e

package hearthd //nolint:testpackage // Exercises public HTTP and Adapter SDK through production Core.

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/devices/httpapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
	"github.com/mholtzscher/hearth/sdk/adapter/brightnessv1"
)

// A synthetic SDK Adapter supplies actual normalized observations, not hardware
// conversion. Adapter-owned tests separately prove native payloads and rounding.
// Core must retain 52 without satisfying set50, then satisfy with actual51.
func TestBrightnessPercentageCommandThroughCore(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	broker := startCoreNATSServer(t)
	core := startFunctionCore(t, Config{
		HouseholdTimezone: "UTC", NATSURL: broker.ClientURL(),
		SQLitePath: filepath.Join(t.TempDir(), "hearth.db"),
		Agent:      requiredAgentConfig(t),
	})
	client := newNonPoolingHTTPClient(t)
	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: "simulator", SoftwareName: "synthetic-brightness-percentage",
		SoftwareVersion: "1.0", NATSURL: broker.ClientURL(),
	})
	require.NoError(t, err)
	defer func() { _ = session.Close() }()
	support := brightnessv1.Support{
		State:      brightnessv1.StateSupport{},
		Operations: brightnessv1.OperationSupport{Set: brightnessv1.SetSupport{}},
	}
	descriptor, err := brightnessv1.NewEntityDescriptor(adapter.EntityMetadata{
		Key: "brightness", ExternalID: "percentage.brightness", Name: "Brightness",
	}, support)
	require.NoError(t, err)
	binding, err := session.Register(ctx, adapter.Registration{
		BindingKey: "brightness-percentage", Device: adapter.DeviceDescriptor{Name: "Synthetic dimmer", Kind: "light"},
		Entities: []adapter.EntityDescriptor{descriptor},
	})
	require.NoError(t, err)
	entityID := string(bindingEntityID(t, binding, "brightness"))
	now := time.Now().UTC()
	require.NoError(
		t,
		session.SetHealth(ctx, adapter.HealthReport{Status: adapter.HealthHealthy, SourceObservedAt: now}),
	)
	require.NoError(t, session.ReportEntityAvailability(ctx, []adapter.EntityAvailabilityReport{
		{EntityID: entityID, Status: adapter.AvailabilityAvailable, SourceObservedAt: now},
	}))

	type acceptedCommand struct {
		command  brightnessv1.SetCommand
		evidence adapter.CommandEvidence
	}
	accepted := make(chan acceptedCommand, 1)
	finish := make(chan struct{})
	handler, err := brightnessv1.NewCommandHandler(entityID, support, brightnessv1.Handlers{
		Set: func(commandCtx context.Context, command brightnessv1.SetCommand, responder adapter.Responder) error {
			evidence, acceptErr := responder.Accept()
			if acceptErr != nil {
				return acceptErr
			}
			accepted <- acceptedCommand{command: command, evidence: evidence}
			select {
			case <-finish:
				return nil
			case <-commandCtx.Done():
				return commandCtx.Err()
			}
		},
	})
	require.NoError(t, err)
	serveCtx, stopServe := context.WithCancel(ctx)
	served := make(chan error, 1)
	go func() { served <- session.ServeCommands(serveCtx, handler) }()
	defer func() {
		close(finish)
		stopServe()
		require.ErrorIs(t, <-served, context.Canceled)
	}()
	runtimeID := waitForAdapterRuntime(ctx, t, core.address, "simulator")
	waitForCommandSubscription(t, broker, "simulator", runtimeID, entityID)

	type commandResponse struct {
		status int
		raw    []byte
		err    error
	}
	response := make(chan commandResponse, 1)
	go func() {
		status, raw, requestErr := functionNetworkRequest(ctx, client, core.address, http.MethodPost,
			"/v1/entities/"+entityID+"/commands", `{"operation":"set","parameters":{"value":50}}`)
		response <- commandResponse{status: status, raw: raw, err: requestErr}
	}()
	var attempt acceptedCommand
	select {
	case attempt = <-accepted:
	case early := <-response:
		t.Fatalf("command returned before SDK acceptance: %d %s %v", early.status, early.raw, early.err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.Equal(t, int64(50), attempt.command.Parameters.Value)
	commandID := attempt.command.ID
	var lastObservationID string
	for _, actual := range []brightnessv1.State{52, 51} {
		observation, observationErr := brightnessv1.NewObservation(brightnessv1.ObservationInput{
			EntityID: entityID, Support: support, State: actual, AdapterReceivedAt: time.Now().UTC(),
		})
		require.NoError(t, observationErr)
		id, publishErr := attempt.evidence.PublishObservation(ctx, observation)
		require.NoError(t, publishErr)
		lastObservationID = string(id)
		report := historyJourneyAwait(ctx, t, client, core.address, entityID, string(id))
		require.Equal(t, "applied", report.Disposition)
		require.Nil(t, report.Rejection)
		require.Equal(t, float64(actual), report.Value)
		if actual == 52 {
			waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
				raw := successfulFunctionCoreRequest(t, client, core, http.MethodGet, "/v1/commands/"+commandID, "")
				var record httpapi.CommandRecordBody
				require.NoError(t, json.Unmarshal(raw, &record))
				require.NotEqual(t, "satisfied", record.Status)
				require.Nil(t, record.OutcomeObservationID)
				return record.Status == "accepted", nil
			})
		}
	}
	select {
	case result := <-response:
		require.NoError(t, result.err)
		require.Equal(t, http.StatusOK, result.status, string(result.raw))
		var outcome httpapi.CommandResultBody
		require.NoError(t, json.Unmarshal(result.raw, &outcome))
		require.Equal(t, "satisfied", outcome.Status)
		require.Equal(t, commandID, outcome.CommandID)
		require.Equal(t, &lastObservationID, outcome.ObservationID)
		require.NotNil(t, outcome.Value)
		require.Equal(t, float64(51), *outcome.Value)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var record httpapi.CommandRecordBody
	raw := successfulFunctionCoreRequest(t, client, core, http.MethodGet, "/v1/commands/"+commandID, "")
	require.NoError(t, json.Unmarshal(raw, &record))
	require.Equal(t, "satisfied", record.Status)
	require.Equal(t, &lastObservationID, record.OutcomeObservationID)
	var entity httpapi.EntityBody
	raw = successfulFunctionCoreRequest(t, client, core, http.MethodGet, "/v1/entities/"+entityID, "")
	require.NoError(t, json.Unmarshal(raw, &entity))
	require.NotNil(t, entity.State)
	require.Equal(t, float64(51), entity.State.Value)
	require.Equal(t, lastObservationID, entity.State.ObservationID)
}
