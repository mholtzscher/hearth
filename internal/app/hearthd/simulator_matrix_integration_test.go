package hearthd

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	simulatoradapter "github.com/mholtzscher/hearth/internal/adapters/simulator"
	simulatorapp "github.com/mholtzscher/hearth/internal/app/simulator"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/api"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const simulatorMatrixAdapterID = "simulator"

type simulatorMatrixOptions struct {
	dependencies         devices.Dependencies
	ackWait              time.Duration
	observationProjector func(*devices.Service, devicesnats.ObservationProjector) devicesnats.ObservationProjector
}

type observationProjectorFunc func(
	context.Context,
	string,
	devices.Observation,
	time.Time,
) (devices.ProjectionResult, error)

func (projector observationProjectorFunc) ProjectObservation(
	ctx context.Context,
	adapterID string,
	observation devices.Observation,
	observedAt time.Time,
) (devices.ProjectionResult, error) {
	return projector(ctx, adapterID, observation, observedAt)
}

type simulatorMatrixHarness struct {
	test            *testing.T
	ctx             context.Context
	cancel          context.CancelFunc
	database        *sql.DB
	repository      *devices.SQLiteRepository
	service         *devices.Service
	server          *natsserver.Server
	connection      *natsgo.Conn
	jetstream       jetstream.JetStream
	durable         jetstream.Consumer
	consumer        *devicesnats.ObservationConsumer
	registrations   *devicesnats.RegistrationServer
	validator       *contractsv1.Validator
	httpServer      *httptest.Server
	logs            *lockedBuffer
	simulatorErrors chan error
	entityID        devices.EntityID
	closeOnce       sync.Once
}

type lockedBuffer struct {
	mutex sync.Mutex
	bytes.Buffer
}

func (buffer *lockedBuffer) Write(value []byte) (int, error) {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return buffer.Buffer.Write(value)
}

func (buffer *lockedBuffer) String() string {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return buffer.Buffer.String()
}

func newSimulatorMatrixHarness(t *testing.T, scenario string, options simulatorMatrixOptions) *simulatorMatrixHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	harness := &simulatorMatrixHarness{
		test: t, ctx: ctx, cancel: cancel, logs: &lockedBuffer{}, simulatorErrors: make(chan error, 1),
	}
	logger := slog.New(slog.NewJSONHandler(harness.logs, nil))

	var err error
	harness.database, err = platformdb.Open(ctx, t.TempDir()+"/hearth.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := platformdb.Migrate(ctx, harness.database); err != nil {
		t.Fatal(err)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	harness.repository = devices.NewSQLiteRepository(harness.database, catalog)

	harness.server, err = natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go harness.server.Start()
	if !harness.server.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server did not become ready")
	}
	harness.connection, err = natsgo.Connect(harness.server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	harness.jetstream, err = jetstream.New(harness.connection)
	if err != nil {
		t.Fatal(err)
	}
	harness.durable, err = devicesnats.ProvisionObservationResources(ctx, harness.jetstream)
	if err != nil {
		t.Fatal(err)
	}
	if options.ackWait > 0 {
		info, err := harness.durable.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		config := info.Config
		config.AckWait = options.ackWait
		harness.durable, err = harness.jetstream.UpdateConsumer(ctx, devicesnats.ObservationStreamName, config)
		if err != nil {
			t.Fatal(err)
		}
	}
	harness.validator, err = contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	harness.service = devices.NewService(
		harness.repository,
		devicesnats.NewCommandSender(harness.connection, harness.validator),
		catalog,
		options.dependencies,
	)
	harness.registrations, err = devicesnats.StartRegistrationServer(
		harness.connection, harness.validator, harness.service, logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	var projector devicesnats.ObservationProjector = harness.service
	if options.observationProjector != nil {
		projector = options.observationProjector(harness.service, projector)
	}
	harness.consumer, err = devicesnats.StartObservationConsumer(
		ctx,
		harness.durable,
		harness.validator,
		projector,
		logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	httpHandler, _ := NewHTTPHandler(harness.service, nil)
	harness.httpServer = httptest.NewServer(httpHandler)

	go func() {
		harness.simulatorErrors <- simulatorapp.Run(ctx, simulatorapp.Config{
			AdapterID: simulatorMatrixAdapterID, NATSURL: harness.server.ClientURL(),
			BindingKey: "simulated-light", Scenario: scenario,
		}, logger)
	}()

	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		var entityID string
		err := harness.database.QueryRowContext(ctx, `SELECT id FROM entities LIMIT 1`).Scan(&entityID)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		harness.entityID = devices.EntityID(entityID)
		return true, nil
	})
	t.Cleanup(harness.Close)
	return harness
}

func (harness *simulatorMatrixHarness) Close() {
	harness.closeOnce.Do(func() {
		harness.cancel()
		if harness.httpServer != nil {
			harness.httpServer.Close()
		}
		select {
		case err := <-harness.simulatorErrors:
			if err != nil && !errors.Is(err, context.Canceled) {
				harness.test.Errorf("simulator stopped: %v", err)
			}
		case <-time.After(time.Second):
			harness.test.Error("simulator did not stop")
		}
		if harness.consumer != nil {
			harness.consumer.Stop()
			select {
			case <-harness.consumer.Closed():
			case <-time.After(time.Second):
			}
		}
		if harness.registrations != nil {
			_ = harness.registrations.Drain()
		}
		if harness.connection != nil {
			harness.connection.Close()
		}
		if harness.database != nil {
			_ = harness.database.Close()
		}
		if harness.server != nil {
			harness.server.Shutdown()
			harness.server.WaitForShutdown()
		}
	})
}

func (harness *simulatorMatrixHarness) waitForState(t *testing.T) devices.State {
	t.Helper()
	var state devices.State
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		view, err := harness.service.GetEntity(harness.ctx, harness.entityID)
		if err != nil {
			return false, err
		}
		if view.State == nil {
			return false, nil
		}
		state = *view.State
		return true, nil
	})
	return state
}

func (harness *simulatorMatrixHarness) postCommand(ctx context.Context, value bool) (int, []byte, error) {
	payload := fmt.Sprintf(`{"operation":"set","parameters":{"value":%t}}`, value)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		harness.httpServer.URL+"/v1/entities/"+string(harness.entityID)+"/commands",
		strings.NewReader(payload),
	)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return response.StatusCode, body, err
}

func TestSimulatorObservationFailureMatrix(t *testing.T) {
	tests := []struct {
		name     string
		scenario string
		assert   func(*testing.T, *simulatorMatrixHarness)
	}{
		{
			name: "duplicate", scenario: simulatoradapter.ScenarioDuplicate,
			assert: func(t *testing.T, harness *simulatorMatrixHarness) {
				state := harness.waitForState(t)
				if string(state.Value) != "false" {
					t.Fatalf("state = %#v", state)
				}
				assertMatrixCount(t, harness.database, "observation_receipts", 1)
				stream, err := harness.jetstream.Stream(harness.ctx, devicesnats.ObservationStreamName)
				if err != nil {
					t.Fatal(err)
				}
				info, err := stream.Info(harness.ctx)
				if err != nil {
					t.Fatal(err)
				}
				if info.State.Msgs != 1 {
					t.Fatalf("stream messages = %d", info.State.Msgs)
				}
			},
		},
		{
			name: "delayed source time", scenario: simulatoradapter.ScenarioDelayedSourceTime,
			assert: func(t *testing.T, harness *simulatorMatrixHarness) {
				state := harness.waitForState(t)
				if state.SourceUpdatedAt == nil ||
					!state.SourceUpdatedAt.Before(state.AdapterReceivedAt.Add(-23*time.Hour)) {
					t.Fatalf("state timestamps = %#v", state)
				}
			},
		},
		{
			name: "future clock skew", scenario: simulatoradapter.ScenarioFutureClockSkew,
			assert: func(t *testing.T, harness *simulatorMatrixHarness) {
				state := harness.waitForState(t)
				if !state.AdapterReceivedAt.After(state.ObservedAt.Add(time.Minute)) {
					t.Fatalf("state timestamps = %#v", state)
				}
				waitForMatrixCondition(t, time.Second, func() (bool, error) {
					return strings.Contains(harness.logs.String(), "adapter observation clock is ahead"), nil
				})
			},
		},
		{
			name: "malformed", scenario: simulatoradapter.ScenarioMalformed,
			assert: func(t *testing.T, harness *simulatorMatrixHarness) {
				waitForMatrixCondition(t, 3*time.Second, func() (bool, error) {
					return strings.Contains(harness.logs.String(), "acknowledging invalid observation"), nil
				})
				view, err := harness.service.GetEntity(harness.ctx, harness.entityID)
				if err != nil {
					t.Fatal(err)
				}
				if view.State != nil {
					t.Fatalf("malformed observation created state: %#v", view.State)
				}
				assertMatrixCount(t, harness.database, "observation_receipts", 0)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newSimulatorMatrixHarness(t, test.scenario, simulatorMatrixOptions{})
			test.assert(t, harness)
		})
	}
}

func TestSimulatorCommandHTTPFailureMatrix(t *testing.T) {
	tests := []struct {
		name        string
		scenario    string
		options     simulatorMatrixOptions
		prepare     func(*testing.T, *simulatorMatrixHarness)
		wantStatus  int
		wantDetail  string
		wantCommand devices.CommandStatus
		wantFailure devices.CommandFailureCode
	}{
		{
			name: "unavailable adapter", scenario: simulatoradapter.ScenarioUnavailableAdapter,
			wantStatus: http.StatusServiceUnavailable, wantDetail: "adapter unavailable",
			wantCommand: devices.CommandStatusAdapterUnavailable, wantFailure: devices.CommandFailureAdapterUnavailable,
		},
		{
			name: "upstream rejection", scenario: simulatoradapter.ScenarioUpstreamRejection,
			wantStatus: http.StatusBadGateway, wantDetail: "upstream rejected command",
			wantCommand: devices.CommandStatusRejected, wantFailure: devices.CommandFailureUpstreamRejected,
		},
		{
			name: "outcome timeout", scenario: simulatoradapter.ScenarioOutcomeTimeout,
			options: simulatorMatrixOptions{dependencies: devices.Dependencies{
				Now: func() time.Time { return time.Now().UTC().Add(-9500 * time.Millisecond) },
			}},
			wantStatus: http.StatusGatewayTimeout, wantDetail: "command outcome timed out",
			wantCommand: devices.CommandStatusOutcomeTimeout, wantFailure: devices.CommandFailureOutcomeTimeout,
		},
		{
			name: "unexpected response", scenario: simulatoradapter.ScenarioUnavailableAdapter,
			prepare: func(t *testing.T, harness *simulatorMatrixHarness) {
				subject, err := natswire.CommandSubject(simulatorMatrixAdapterID, string(harness.entityID), "set")
				if err != nil {
					t.Fatal(err)
				}
				subscription, err := harness.connection.Subscribe(subject, func(message *natsgo.Msg) {
					_ = message.Respond([]byte("{"))
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = subscription.Drain() })
				if err := harness.connection.Flush(); err != nil {
					t.Fatal(err)
				}
			},
			wantStatus: http.StatusInternalServerError, wantDetail: "internal error",
			wantCommand: devices.CommandStatusInternalFailure, wantFailure: devices.CommandFailureInternalError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newSimulatorMatrixHarness(t, test.scenario, test.options)
			if test.prepare != nil {
				test.prepare(t, harness)
			}
			status, body, err := harness.postCommand(harness.ctx, true)
			if err != nil {
				t.Fatal(err)
			}
			if status != test.wantStatus {
				t.Fatalf("status = %d, body = %s", status, body)
			}
			var response huma.ErrorModel
			if err := json.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			if response.Status != test.wantStatus || response.Detail != test.wantDetail {
				t.Fatalf("error body = %#v", response)
			}
			history, err := harness.repository.ListEntityCommands(harness.ctx, devices.ListEntityCommandsParams{
				EntityID: harness.entityID, Limit: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(history.Items) != 1 {
				t.Fatalf("command history = %#v", history)
			}
			command := history.Items[0]
			if command.Status != test.wantCommand || command.FailureCode == nil ||
				*command.FailureCode != test.wantFailure {
				t.Fatalf("stored command = %#v", command)
			}
		})
	}
}

func TestSimulatorNoOpAndOverlappingCommands(t *testing.T) {
	t.Run("no-op refresh", func(t *testing.T) {
		harness := newSimulatorMatrixHarness(t, simulatoradapter.ScenarioNoOpRefresh, simulatorMatrixOptions{})
		initial := harness.waitForState(t)
		status, body, err := harness.postCommand(harness.ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		if status != http.StatusOK {
			t.Fatalf("status = %d, body = %s", status, body)
		}
		var result devicesapi.CommandResultBody
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		state := harness.waitForState(t)
		if result.Value != false || state.ObservationID == initial.ObservationID ||
			string(state.Value) != "false" || state.ObservationID != devices.ObservationID(result.ObservationID) {
			t.Fatalf("initial = %#v, result = %#v, state = %#v", initial, result, state)
		}
		commandID, err := devices.ParseCommandID(result.CommandID)
		if err != nil {
			t.Fatal(err)
		}
		command, err := harness.repository.GetCommand(harness.ctx, commandID)
		if err != nil {
			t.Fatal(err)
		}
		if command.Status != devices.CommandStatusSatisfied || command.OutcomeObservationID == nil ||
			*command.OutcomeObservationID != state.ObservationID {
			t.Fatalf("command = %#v", command)
		}
	})

	t.Run("overlapping opposite commands", func(t *testing.T) {
		harness := newSimulatorMatrixHarness(t, simulatoradapter.ScenarioOverlappingCommands, simulatorMatrixOptions{})
		harness.waitForState(t)
		type outcome struct {
			status int
			body   []byte
			err    error
		}
		outcomes := make(chan outcome, 2)
		for _, value := range []bool{true, false} {
			go func() {
				status, body, err := harness.postCommand(harness.ctx, value)
				outcomes <- outcome{status: status, body: body, err: err}
			}()
		}
		commandIDs := make(map[string]struct{})
		observationIDs := make(map[string]struct{})
		for range 2 {
			outcome := <-outcomes
			if outcome.err != nil {
				t.Fatal(outcome.err)
			}
			if outcome.status != http.StatusOK {
				t.Fatalf("status = %d, body = %s", outcome.status, outcome.body)
			}
			var result devicesapi.CommandResultBody
			if err := json.Unmarshal(outcome.body, &result); err != nil {
				t.Fatal(err)
			}
			commandIDs[result.CommandID] = struct{}{}
			observationIDs[result.ObservationID] = struct{}{}
		}
		if len(commandIDs) != 2 || len(observationIDs) != 2 {
			t.Fatalf("command IDs = %v, observation IDs = %v", commandIDs, observationIDs)
		}
		assertMatrixCount(t, harness.database, "commands", 2)
		var satisfied, correlations int
		if err := harness.database.QueryRowContext(harness.ctx,
			`SELECT count(*), count(DISTINCT correlation_id) FROM commands WHERE status = 'satisfied'`,
		).Scan(&satisfied, &correlations); err != nil {
			t.Fatal(err)
		}
		if satisfied != 2 || correlations != 2 {
			t.Fatalf("satisfied = %d, correlations = %d", satisfied, correlations)
		}
		state := harness.waitForState(t)
		if _, ok := observationIDs[string(state.ObservationID)]; !ok {
			t.Fatalf("final state is not from either linked outcome: %#v", state)
		}
	})
}

func TestHTTPDisconnectLeavesCommandLifecycleActive(t *testing.T) {
	harness := newSimulatorMatrixHarness(t, simulatoradapter.ScenarioOutcomeTimeout, simulatorMatrixOptions{
		dependencies: devices.Dependencies{
			Now: func() time.Time { return time.Now().UTC().Add(-9 * time.Second) },
		},
	})
	harness.waitForState(t)
	requestContext, cancelRequest := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, _, err := harness.postCommand(requestContext, true)
		result <- err
	}()
	var commandID devices.CommandID
	waitForMatrixCondition(t, 3*time.Second, func() (bool, error) {
		var id, status string
		err := harness.database.QueryRowContext(harness.ctx,
			`SELECT id, status FROM commands ORDER BY requested_at DESC LIMIT 1`,
		).Scan(&id, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if status != string(devices.CommandStatusAccepted) {
			return false, nil
		}
		commandID = devices.CommandID(id)
		return true, nil
	})
	cancelRequest()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("disconnected request unexpectedly completed")
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP request did not observe cancellation")
	}
	waitForMatrixCondition(t, 2*time.Second, func() (bool, error) {
		command, err := harness.repository.GetCommand(harness.ctx, commandID)
		if err != nil {
			return false, err
		}
		return command.Status == devices.CommandStatusOutcomeTimeout && command.FailureCode != nil &&
			*command.FailureCode == devices.CommandFailureOutcomeTimeout, nil
	})
}

func TestSimulatorInterruptedCommandSurvivesLateLinkedObservation(t *testing.T) {
	harness := newSimulatorMatrixHarness(t, simulatoradapter.ScenarioInterruptedCommand, simulatorMatrixOptions{})
	harness.waitForState(t)
	commandID, err := devices.NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	correlationID, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := devices.CommandRecord{
		ID: commandID, EntityID: harness.entityID, AdapterID: simulatorMatrixAdapterID,
		OperationName: devices.OperationNameSet, Parameters: devices.CommandParameters(`{"value":true}`),
		CorrelationID: correlationID, Status: devices.CommandStatusRequested,
		RequestedAt: now, DeadlineAt: now.Add(10 * time.Second),
	}
	if _, err := harness.repository.CreateCommand(harness.ctx, record); err != nil {
		t.Fatal(err)
	}
	acceptance, err := devicesnats.NewCommandSender(harness.connection, harness.validator).Send(
		harness.ctx, simulatorMatrixAdapterID, devices.CommandRequest{
			ID:            commandID,
			CorrelationID: correlationID,
			EntityID:      harness.entityID,
			OperationName: devices.OperationNameSet,
			Parameters:    devices.CommandParameters(`{"value":true}`),
			Deadline:      record.DeadlineAt,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !acceptance.Accepted {
		t.Fatal("simulator did not accept interrupted command")
	}
	if err := harness.repository.MarkCommandAccepted(harness.ctx, commandID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := harness.repository.InterruptActiveCommands(harness.ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	observationID := publishMatrixLinkedObservation(t, harness, commandID, correlationID, true)
	waitForMatrixCondition(t, 3*time.Second, func() (bool, error) {
		view, err := harness.service.GetEntity(harness.ctx, harness.entityID)
		if err != nil {
			return false, err
		}
		return view.State != nil && view.State.ObservationID == observationID, nil
	})
	command, err := harness.repository.GetCommand(harness.ctx, commandID)
	if err != nil {
		t.Fatal(err)
	}
	if command.Status != devices.CommandStatusInterrupted || command.FailureCode == nil ||
		*command.FailureCode != devices.CommandFailureCoreRestarted || command.OutcomeObservationID != nil {
		t.Fatalf("interrupted command = %#v", command)
	}
}

func TestSimulatorRestartBeforeAckRedeliversWithoutChangingStateOrCommand(t *testing.T) {
	committed := make(chan struct{})
	var failOnce atomic.Bool
	harness := newSimulatorMatrixHarness(t, simulatoradapter.ScenarioRestartBeforeAck, simulatorMatrixOptions{
		ackWait: 500 * time.Millisecond,
		observationProjector: func(_ *devices.Service, base devicesnats.ObservationProjector) devicesnats.ObservationProjector {
			return observationProjectorFunc(func(
				ctx context.Context,
				adapterID string,
				observation devices.Observation,
				observedAt time.Time,
			) (devices.ProjectionResult, error) {
				result, err := base.ProjectObservation(ctx, adapterID, observation, observedAt)
				if err != nil {
					return result, err
				}
				if observation.RefreshForCommand != nil && failOnce.CompareAndSwap(false, true) {
					close(committed)
					return result, errors.New("simulated core exit before acknowledgement")
				}
				return result, nil
			})
		},
	})
	harness.waitForState(t)
	status, body, err := harness.postCommand(harness.ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	var result devicesapi.CommandResultBody
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	select {
	case <-committed:
	case <-time.After(time.Second):
		t.Fatal("linked observation did not commit before simulated exit")
	}
	commandID, err := devices.ParseCommandID(result.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	beforeCommand, err := harness.repository.GetCommand(harness.ctx, commandID)
	if err != nil {
		t.Fatal(err)
	}
	beforeState := harness.waitForState(t)
	assertMatrixCount(t, harness.database, "observation_receipts", 2)

	harness.consumer.Stop()
	select {
	case <-harness.consumer.Closed():
	case <-time.After(time.Second):
		t.Fatal("observation consumer did not stop")
	}
	redelivered := make(chan struct{})
	var redeliveryOnce sync.Once
	harness.consumer, err = devicesnats.StartObservationConsumer(
		harness.ctx, harness.durable, harness.validator,
		observationProjectorFunc(func(
			ctx context.Context,
			adapterID string,
			observation devices.Observation,
			observedAt time.Time,
		) (devices.ProjectionResult, error) {
			projected, err := harness.service.ProjectObservation(ctx, adapterID, observation, observedAt)
			if observation.ID == devices.ObservationID(result.ObservationID) {
				redeliveryOnce.Do(func() { close(redelivered) })
			}
			return projected, err
		}),
		slog.New(slog.NewJSONHandler(harness.logs, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-redelivered:
	case <-time.After(3 * time.Second):
		t.Fatal("unacknowledged linked observation was not redelivered")
	}
	waitForMatrixCondition(t, time.Second, func() (bool, error) {
		info, err := harness.durable.Info(harness.ctx)
		return err == nil && info.NumAckPending == 0, err
	})
	afterCommand, err := harness.repository.GetCommand(harness.ctx, commandID)
	if err != nil {
		t.Fatal(err)
	}
	afterState := harness.waitForState(t)
	if afterCommand.Status != beforeCommand.Status || afterCommand.OutcomeObservationID == nil ||
		beforeCommand.OutcomeObservationID == nil || *afterCommand.OutcomeObservationID != *beforeCommand.OutcomeObservationID ||
		afterCommand.CompletedAt == nil || beforeCommand.CompletedAt == nil || !afterCommand.CompletedAt.Equal(*beforeCommand.CompletedAt) {
		t.Fatalf("command changed on redelivery: before = %#v, after = %#v", beforeCommand, afterCommand)
	}
	if afterState.ObservationID != beforeState.ObservationID || afterState.ReceiveOrder != beforeState.ReceiveOrder ||
		string(afterState.Value) != string(beforeState.Value) {
		t.Fatalf("state changed on redelivery: before = %#v, after = %#v", beforeState, afterState)
	}
	assertMatrixCount(t, harness.database, "observation_receipts", 2)
}

func publishMatrixLinkedObservation(
	t *testing.T,
	harness *simulatorMatrixHarness,
	commandID devices.CommandID,
	correlationID devices.CorrelationID,
	value bool,
) devices.ObservationID {
	t.Helper()
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	commandIDString := string(commandID)
	payload, err := natswire.Encode(harness.validator, contractsv1.ObservationSchemaID,
		natswire.Envelope[adapter.Observation]{
			ID: string(observationID), Schema: contractsv1.ObservationSchemaID, EmittedAt: now,
			CorrelationID: string(correlationID), CausationID: &commandIDString,
			Data: adapter.Observation{
				EntityID: string(harness.entityID), Value: json.RawMessage(strconv.FormatBool(value)),
				AdapterReceivedAt: now, RefreshForCommand: &commandIDString,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := natswire.ObservationSubject(simulatorMatrixAdapterID, string(harness.entityID))
	if err != nil {
		t.Fatal(err)
	}
	message := &natsgo.Msg{Subject: subject, Data: payload, Header: make(natsgo.Header)}
	message.Header.Set(natsgo.MsgIdHdr, string(observationID))
	if _, err := harness.jetstream.PublishMsg(harness.ctx, message); err != nil {
		t.Fatal(err)
	}
	return observationID
}

func assertMatrixCount(t *testing.T, database *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := database.QueryRow(`SELECT count(*) FROM ` + table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}

func waitForMatrixCondition(t *testing.T, timeout time.Duration, condition func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		matched, err := condition()
		if err != nil {
			t.Fatal(err)
		}
		if matched {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	matched, err := condition()
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("timed out waiting for condition")
	}
}
