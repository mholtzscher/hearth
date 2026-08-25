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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	simulatoradapter "github.com/mholtzscher/hearth/internal/adapters/simulator"
	simulatorapp "github.com/mholtzscher/hearth/internal/app/simulator"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/api"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const simulatorMatrixAdapterID = "simulator"

type simulatorMatrixOptions struct {
	ackWait            time.Duration
	observationHandler func(*devices.Service, platformnats.ObservationHandler) platformnats.ObservationHandler
}

type simulatorMatrixHarness struct {
	test            *testing.T
	ctx             context.Context
	cancel          context.CancelFunc
	database        *sql.DB
	service         *devices.Service
	moduleCancel    context.CancelFunc
	moduleErrors    chan error
	server          *natsserver.Server
	connection      *natsgo.Conn
	jetstream       jetstream.JetStream
	durable         jetstream.Consumer
	consumer        *platformnats.ObservationConsumer
	registrations   *platformnats.RegistrationServer
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
	harness.service, err = devices.New(ctx, harness.database, logger)
	if err != nil {
		t.Fatal(err)
	}

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
	harness.durable, err = platformnats.ProvisionObservationResources(ctx, harness.jetstream)
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
		harness.durable, err = harness.jetstream.UpdateConsumer(ctx, platformnats.ObservationStreamName, config)
		if err != nil {
			t.Fatal(err)
		}
	}
	harness.validator, err = contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	moduleContext, moduleCancel := context.WithCancel(context.Background())
	harness.moduleCancel = moduleCancel
	harness.moduleErrors = make(chan error, 1)
	go func() {
		harness.moduleErrors <- harness.service.Run(
			moduleContext,
			devicesnats.NewCommandDelivery(platformnats.NewCommandClient(harness.connection, harness.validator)),
		)
	}()
	harness.registrations, err = platformnats.StartRegistrationServer(
		harness.connection, harness.validator, devicesnats.RegistrationHandler(harness.service), logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := devicesnats.ObservationHandler(harness.service)
	if options.observationHandler != nil {
		handler = options.observationHandler(harness.service, handler)
	}
	harness.consumer, err = platformnats.StartObservationConsumer(ctx, harness.durable, harness.validator, handler, logger)
	if err != nil {
		t.Fatal(err)
	}
	httpHandler, _ := NewHTTPHandler(devicesapi.Dependencies{Entities: harness.service, Commands: harness.service}, nil)
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
			<-harness.consumer.Closed()
		}
		if harness.registrations != nil {
			closed := harness.registrations.Closed()
			_ = harness.registrations.Drain()
			<-closed
		}
		if harness.moduleCancel != nil {
			harness.moduleCancel()
			if err := <-harness.moduleErrors; err != nil {
				harness.test.Errorf("Device / Entity module stopped: %v", err)
			}
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
			},
		},
		{
			name: "delayed source time", scenario: simulatoradapter.ScenarioDelayedSourceTime,
			assert: func(t *testing.T, harness *simulatorMatrixHarness) {
				state := harness.waitForState(t)
				if state.SourceUpdatedAt == nil || !state.SourceUpdatedAt.Before(state.AdapterReceivedAt.Add(-23*time.Hour)) {
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
		prepare     func(*testing.T, *simulatorMatrixHarness)
		wantStatus  int
		wantCode    string
		wantCommand string
		wantFailure string
	}{
		{
			name: "unavailable adapter", scenario: simulatoradapter.ScenarioUnavailableAdapter,
			wantStatus: http.StatusServiceUnavailable, wantCode: "adapter_unavailable",
			wantCommand: "adapter_unavailable", wantFailure: "adapter_unavailable",
		},
		{
			name: "upstream rejection", scenario: simulatoradapter.ScenarioUpstreamRejection,
			wantStatus: http.StatusBadGateway, wantCode: "upstream_rejected",
			wantCommand: "rejected", wantFailure: "upstream_rejected",
		},
		{
			name: "outcome timeout", scenario: simulatoradapter.ScenarioOutcomeTimeout,
			wantStatus: http.StatusGatewayTimeout, wantCode: "outcome_timeout",
			wantCommand: "outcome_timeout", wantFailure: "outcome_timeout",
		},
		{
			name: "unexpected response", scenario: simulatoradapter.ScenarioUnavailableAdapter,
			prepare: func(t *testing.T, harness *simulatorMatrixHarness) {
				subject, err := platformnats.CommandSubject(simulatorMatrixAdapterID, string(harness.entityID), "set")
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
			wantStatus: http.StatusInternalServerError, wantCode: "internal_error",
			wantCommand: "internal_failure", wantFailure: "internal_error",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newSimulatorMatrixHarness(t, test.scenario, simulatorMatrixOptions{})
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
			var response devicesapi.ErrorBody
			if err := json.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			if response.Error.Code != test.wantCode || response.Error.CommandID == nil {
				t.Fatalf("error body = %#v", response)
			}
			command := matrixCommand(t, harness.database, *response.Error.CommandID)
			if command.status != test.wantCommand || command.failureCode != test.wantFailure {
				t.Fatalf("stored Command = %#v", command)
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
		command := matrixCommand(t, harness.database, result.CommandID)
		if command.status != "satisfied" || command.outcomeObservationID != result.ObservationID {
			t.Fatalf("Command = %#v", command)
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
			go func(value bool) {
				status, body, err := harness.postCommand(harness.ctx, value)
				outcomes <- outcome{status: status, body: body, err: err}
			}(value)
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
			t.Fatalf("Command IDs = %v, Observation IDs = %v", commandIDs, observationIDs)
		}
		assertMatrixCount(t, harness.database, "commands", 2)
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
	if _, err := harness.database.ExecContext(harness.ctx, `
		INSERT INTO commands (
			id, entity_id, adapter_id, operation, parameters_json, correlation_id,
			status, requested_at, deadline_at, accepted_at, completed_at,
			outcome_observation_id, failure_code
		) VALUES (?, ?, ?, 'set', '{"value":true}', ?, 'requested', ?, ?, NULL, NULL, NULL, NULL)`,
		commandID, harness.entityID, simulatorMatrixAdapterID, correlationID,
		now.Format(time.RFC3339Nano), now.Add(10*time.Second).Format(time.RFC3339Nano),
	); err != nil {
		t.Fatal(err)
	}
	acceptance, err := devicesnats.NewCommandDelivery(
		platformnats.NewCommandClient(harness.connection, harness.validator),
	).Deliver(harness.ctx, simulatorMatrixAdapterID, devices.CommandDispatch{
		ID: commandID, CorrelationID: correlationID, EntityID: harness.entityID,
		OperationName: devices.OperationNameSet, Parameters: devices.CommandParameters(`{"value":true}`),
		Deadline: now.Add(10 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !acceptance.Accepted {
		t.Fatal("simulator did not accept interrupted Command")
	}
	interruptedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := harness.database.ExecContext(harness.ctx, `
		UPDATE commands
		SET status = 'interrupted', accepted_at = ?, completed_at = ?, failure_code = 'core_restarted'
		WHERE id = ?`, interruptedAt, interruptedAt, commandID,
	); err != nil {
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
	command := matrixCommand(t, harness.database, string(commandID))
	if command.status != "interrupted" || command.failureCode != "core_restarted" || command.outcomeObservationID != "" {
		t.Fatalf("interrupted Command = %#v", command)
	}
}

func TestSimulatorRestartBeforeAckRedeliversWithoutChangingStateOrCommand(t *testing.T) {
	committed := make(chan struct{})
	var failOnce atomic.Bool
	harness := newSimulatorMatrixHarness(t, simulatoradapter.ScenarioRestartBeforeAck, simulatorMatrixOptions{
		ackWait: 500 * time.Millisecond,
		observationHandler: func(_ *devices.Service, base platformnats.ObservationHandler) platformnats.ObservationHandler {
			return func(ctx context.Context, delivery platformnats.ObservationDelivery) error {
				if err := base(ctx, delivery); err != nil {
					return err
				}
				if delivery.Envelope.Data.RefreshForCommand != nil && failOnce.CompareAndSwap(false, true) {
					close(committed)
					return errors.New("simulated core exit before acknowledgement")
				}
				return nil
			}
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
		t.Fatal("linked Observation did not commit before simulated exit")
	}
	beforeCommand := matrixCommand(t, harness.database, result.CommandID)
	beforeState := harness.waitForState(t)
	assertMatrixCount(t, harness.database, "observation_receipts", 2)

	harness.consumer.Stop()
	select {
	case <-harness.consumer.Closed():
	case <-time.After(time.Second):
		t.Fatal("Observation consumer did not stop")
	}
	redelivered := make(chan struct{})
	var redeliveryOnce sync.Once
	base := devicesnats.ObservationHandler(harness.service)
	harness.consumer, err = platformnats.StartObservationConsumer(
		harness.ctx, harness.durable, harness.validator,
		func(ctx context.Context, delivery platformnats.ObservationDelivery) error {
			err := base(ctx, delivery)
			if delivery.Envelope.ID == result.ObservationID {
				redeliveryOnce.Do(func() { close(redelivered) })
			}
			return err
		},
		slog.New(slog.NewJSONHandler(harness.logs, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-redelivered:
	case <-time.After(3 * time.Second):
		t.Fatal("unacknowledged linked Observation was not redelivered")
	}
	waitForMatrixCondition(t, time.Second, func() (bool, error) {
		info, err := harness.durable.Info(harness.ctx)
		return err == nil && info.NumAckPending == 0, err
	})
	afterCommand := matrixCommand(t, harness.database, result.CommandID)
	afterState := harness.waitForState(t)
	if afterCommand != beforeCommand {
		t.Fatalf("Command changed on redelivery: before = %#v, after = %#v", beforeCommand, afterCommand)
	}
	if afterState.ObservationID != beforeState.ObservationID || afterState.ReceiveOrder != beforeState.ReceiveOrder ||
		string(afterState.Value) != string(beforeState.Value) {
		t.Fatalf("State changed on redelivery: before = %#v, after = %#v", beforeState, afterState)
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
	payload, err := platformnats.Encode(harness.validator, contractsv1.ObservationSchemaID,
		platformnats.Envelope[platformnats.Observation]{
			ID: string(observationID), Schema: contractsv1.ObservationSchemaID, EmittedAt: now,
			CorrelationID: string(correlationID), CausationID: &commandIDString,
			Data: platformnats.Observation{
				EntityID: string(harness.entityID), Value: json.RawMessage(fmt.Sprintf("%t", value)),
				AdapterReceivedAt: now, RefreshForCommand: &commandIDString,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := platformnats.ObservationSubject(simulatorMatrixAdapterID, string(harness.entityID))
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

type persistedMatrixCommand struct {
	status               string
	failureCode          string
	outcomeObservationID string
	acceptedAt           string
	completedAt          string
}

func matrixCommand(t *testing.T, database *sql.DB, id string) persistedMatrixCommand {
	t.Helper()
	var command persistedMatrixCommand
	var failureCode, outcomeObservationID, acceptedAt, completedAt sql.NullString
	if err := database.QueryRow(
		"SELECT status, failure_code, outcome_observation_id, accepted_at, completed_at FROM commands WHERE id = ?", id,
	).Scan(&command.status, &failureCode, &outcomeObservationID, &acceptedAt, &completedAt); err != nil {
		t.Fatal(err)
	}
	if failureCode.Valid {
		command.failureCode = failureCode.String
	}
	if outcomeObservationID.Valid {
		command.outcomeObservationID = outcomeObservationID.String
	}
	if acceptedAt.Valid {
		command.acceptedAt = acceptedAt.String
	}
	if completedAt.Valid {
		command.completedAt = completedAt.String
	}
	return command
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
