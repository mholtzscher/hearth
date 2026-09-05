package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

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
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

const (
	simulatorMatrixAdapterID      = "simulator"
	simulatorMatrixDuplicateFault = "duplicate"
	simulatorMatrixMalformedFault = "malformed"
)

type wireObservation struct {
	EntityID          string          `json:"entity_id"`
	Value             json.RawMessage `json:"value"`
	AdapterReceivedAt string          `json:"adapter_received_at"`
	SourceUpdatedAt   *string         `json:"source_updated_at,omitempty"`
	RefreshForCommand *string         `json:"refresh_for_command_id,omitempty"`
}

type simulatorMatrixOptions struct {
	dependencies devices.Dependencies
	manual       bool
	ackWait      time.Duration
	// logLevel selects the shared JSON log threshold; the zero value keeps
	// Info so existing matrix tests never capture Debug transport progress.
	logLevel             slog.Level
	observationProjector func(*devices.Service, devicesnats.ObservationProjector) devicesnats.ObservationProjector
}

type observationProjectorFunc func(
	context.Context,
	string,
	devices.RuntimeID,
	devices.Observation,
	time.Time,
) (devices.ProjectionResult, error)

func (projector observationProjectorFunc) ProjectObservation(
	ctx context.Context,
	adapterID string,
	runtimeID devices.RuntimeID,
	observation devices.Observation,
	observedAt time.Time,
) (devices.ProjectionResult, error) {
	return projector(ctx, adapterID, runtimeID, observation, observedAt)
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
	sessions        *devicesnats.SessionServer
	availability    *devicesnats.EntityAvailabilityServer
	registrations   *devicesnats.RegistrationServer
	enablement      *devicesnats.EntityEnablementServer
	validator       *contractsv1.Validator
	httpServer      *httptest.Server
	logs            *lockedBuffer
	simulatorErrors chan error
	hasSimulator    bool
	entityID        devices.EntityID
	closeOnce       sync.Once
}

type lockedBuffer struct {
	bytes.Buffer

	mutex sync.Mutex
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

//nolint:gocognit,gocyclo,cyclop // Integration harness setup keeps resource ownership visible in one place.
func newSimulatorMatrixHarness(t *testing.T, scenario string, options simulatorMatrixOptions) *simulatorMatrixHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	harness := &simulatorMatrixHarness{
		test: t, ctx: ctx, cancel: cancel, logs: &lockedBuffer{}, simulatorErrors: make(chan error, 1),
	}
	logger := slog.New(slog.NewJSONHandler(harness.logs, &slog.HandlerOptions{Level: options.logLevel}))

	var err error
	harness.database, err = platformdb.Open(ctx, t.TempDir()+"/hearth.db")
	if err != nil {
		t.Fatal(err)
	}
	if migrateErr := platformdb.Migrate(ctx, harness.database); migrateErr != nil {
		t.Fatal(migrateErr)
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
		info, infoErr := harness.durable.Info(ctx)
		if infoErr != nil {
			t.Fatal(infoErr)
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
	// Capture service command evidence in the shared log buffer by default so
	// cross-app tests can follow one command across core and SDK records.
	// Like core assembly, the service receives the devices component child.
	// Explicit dependency overrides (Now, Logger) are preserved.
	serviceDependencies := options.dependencies
	if serviceDependencies.Logger == nil {
		serviceDependencies.Logger = logger.With("component", "devices")
	}
	harness.service = devices.NewService(
		devices.SQLiteStores(harness.repository),
		devicesnats.NewCommandSender(harness.connection, harness.validator),
		catalog,
		serviceDependencies,
	)
	harness.sessions, err = devicesnats.StartSessionServer(
		harness.connection, harness.validator, harness.service, harness.service, logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	harness.availability, err = devicesnats.StartEntityAvailabilityServer(
		harness.connection, harness.validator, harness.service, logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	harness.registrations, err = devicesnats.StartRegistrationServer(
		harness.connection, harness.validator, harness.service, logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	harness.enablement, err = devicesnats.StartEntityEnablementServer(
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

	if options.manual {
		t.Cleanup(harness.Close)
		return harness
	}
	harness.hasSimulator = true
	go func() {
		if scenario == simulatorMatrixDuplicateFault || scenario == simulatorMatrixMalformedFault {
			harness.simulatorErrors <- harness.runObservationFaultAdapter(ctx, scenario, logger)
			return
		}
		harness.simulatorErrors <- simulatorapp.Run(ctx, simulatorapp.Config{
			AdapterID: simulatorMatrixAdapterID, NATSURL: harness.server.ClientURL(),
			BindingKey: "simulated-light", Scenario: scenario,
		}, logger)
	}()

	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		var entityID string
		queryErr := harness.database.QueryRowContext(ctx, `SELECT id FROM entities LIMIT 1`).Scan(&entityID)
		if errors.Is(queryErr, sql.ErrNoRows) {
			return false, nil
		}
		if queryErr != nil {
			return false, queryErr
		}
		harness.entityID = devices.EntityID(entityID)
		return true, nil
	})
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		instance, adapterErr := harness.service.GetAdapter(harness.ctx, simulatorMatrixAdapterID)
		if adapterErr != nil {
			return false, adapterErr
		}
		entity, entityErr := harness.service.GetEntity(harness.ctx, harness.entityID)
		if entityErr != nil {
			return false, entityErr
		}
		wantHealth := devices.AdapterHealthHealthy
		wantAvailability := devices.EntityAvailabilityAvailable
		switch scenario {
		case simulatoradapter.ScenarioAdapterUnhealthy:
			wantHealth = devices.AdapterHealthUnhealthy
			wantAvailability = devices.EntityAvailabilityUnavailable
		case simulatoradapter.ScenarioEntityUnavailable:
			wantAvailability = devices.EntityAvailabilityUnavailable
		}
		return instance.Health.Status == wantHealth &&
			entity.Availability.Status == wantAvailability, nil
	})
	if scenario != simulatoradapter.ScenarioAdapterUnhealthy {
		runtimeID := currentMatrixRuntimeID(t, harness)
		commandSubject, subjectErr := natswire.CommandSubject(
			simulatorMatrixAdapterID, string(runtimeID), string(harness.entityID), "set",
		)
		if subjectErr != nil {
			t.Fatal(subjectErr)
		}
		waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
			subscriptions, subscriptionsErr := harness.server.Subsz(&natsserver.SubszOptions{
				Subscriptions: true,
				Test:          commandSubject,
			})
			if subscriptionsErr != nil {
				return false, subscriptionsErr
			}
			return subscriptions.Total > 0, nil
		})
	}
	t.Cleanup(harness.Close)
	return harness
}

//nolint:gocognit // Teardown mirrors the harness resources and preserves their shutdown order.
func (harness *simulatorMatrixHarness) Close() {
	harness.closeOnce.Do(func() {
		harness.cancel()
		if harness.httpServer != nil {
			harness.httpServer.Close()
		}
		if harness.hasSimulator {
			select {
			case err := <-harness.simulatorErrors:
				if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, adapter.ErrRuntimeFenced) {
					harness.test.Errorf("simulator stopped: %v", err)
				}
			case <-time.After(time.Second):
				harness.test.Error("simulator did not stop")
			}
		}
		if harness.consumer != nil {
			harness.consumer.Stop()
			select {
			case <-harness.consumer.Closed():
			case <-time.After(time.Second):
			}
		}
		if harness.enablement != nil {
			_ = harness.enablement.Drain()
		}
		if harness.registrations != nil {
			_ = harness.registrations.Drain()
		}
		if harness.availability != nil {
			_ = harness.availability.Drain()
		}
		if harness.sessions != nil {
			_ = harness.sessions.Drain()
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

func (harness *simulatorMatrixHarness) runObservationFaultAdapter(
	ctx context.Context,
	scenario string,
	logger *slog.Logger,
) error {
	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: simulatorMatrixAdapterID, SoftwareName: "hearth-simulator-test",
		SoftwareVersion: "0.1.0", NATSURL: harness.server.ClientURL(), Logger: logger,
	})
	if err != nil {
		return err
	}
	defer session.Close()
	simulated, err := simulatoradapter.New(session, simulatoradapter.ScenarioHappy)
	if err != nil {
		return err
	}
	descriptor, err := sdkpowerv1.NewEntityDescriptor(adapter.EntityMetadata{
		Key: "power", ExternalID: "simulated-light.power", Name: "Power",
	}, simulated.Support())
	if err != nil {
		return err
	}
	deviceExternalID := "simulated-light"
	binding, err := session.Register(ctx, adapter.Registration{
		BindingKey: "simulated-light",
		Device: adapter.DeviceDescriptor{
			ExternalID: &deviceExternalID, Name: "Simulated light", Kind: "light",
		},
		Entities: []adapter.EntityDescriptor{descriptor},
	})
	if err != nil {
		return err
	}
	var entityID string
	for _, entity := range binding.Entities {
		if entity.Key == "power" {
			entityID = entity.EntityID
			break
		}
	}
	if entityID == "" {
		return errors.New("registration response omitted power Entity")
	}
	now := time.Now().UTC()
	if healthErr := session.SetHealth(ctx, adapter.HealthReport{
		Status: adapter.HealthHealthy, SourceObservedAt: now,
	}); healthErr != nil {
		return healthErr
	}
	if availabilityErr := session.ReportEntityAvailability(ctx, []adapter.EntityAvailabilityReport{{
		EntityID: entityID, Status: adapter.AvailabilityAvailable, SourceObservedAt: now,
	}}); availabilityErr != nil {
		return availabilityErr
	}
	if publishErr := harness.publishObservationFault(ctx, scenario, entityID); publishErr != nil {
		return publishErr
	}
	handler, err := simulated.CommandHandler(entityID)
	if err != nil {
		return err
	}
	serveErr := session.ServeCommands(ctx, handler)
	if errors.Is(serveErr, context.Canceled) || errors.Is(serveErr, adapter.ErrClosed) {
		return nil
	}
	return serveErr
}

func (harness *simulatorMatrixHarness) publishObservationFault(
	ctx context.Context,
	scenario string,
	entityID string,
) error {
	observationID, err := devices.NewObservationID()
	if err != nil {
		return err
	}
	var payload []byte
	switch scenario {
	case simulatorMatrixDuplicateFault:
		correlationID, correlationErr := devices.NewCorrelationID()
		if correlationErr != nil {
			return correlationErr
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		payload, err = natswire.Encode(harness.validator, contractsv1.ObservationSchemaID,
			natswire.Envelope[adapter.Observation]{
				ID: string(observationID), Schema: contractsv1.ObservationSchemaID,
				EmittedAt: now, CorrelationID: string(correlationID),
				Data: adapter.Observation{
					EntityID: entityID, Value: json.RawMessage(`false`), AdapterReceivedAt: now,
				},
			},
		)
		if err != nil {
			return err
		}
	case simulatorMatrixMalformedFault:
		payload = []byte("{")
	default:
		return fmt.Errorf("unknown Observation fault %q", scenario)
	}
	instance, err := harness.service.GetAdapter(ctx, simulatorMatrixAdapterID)
	if err != nil {
		return err
	}
	if instance.Health.Runtime == nil {
		return errors.New("simulator Adapter has no runtime")
	}
	subject, err := natswire.ObservationSubject(
		simulatorMatrixAdapterID, string(instance.Health.Runtime.ID), entityID,
	)
	if err != nil {
		return err
	}
	message := &natsgo.Msg{Subject: subject, Data: payload, Header: make(natsgo.Header)}
	message.Header.Set(natsgo.MsgIdHdr, string(observationID))
	if _, publishErr := harness.jetstream.PublishMsg(ctx, message); publishErr != nil {
		return fmt.Errorf("publish simulator fault Observation: %w", publishErr)
	}
	if scenario != simulatorMatrixDuplicateFault {
		return nil
	}
	acknowledgement, err := harness.jetstream.PublishMsg(ctx, message)
	if err != nil {
		return fmt.Errorf("publish duplicate simulator Observation: %w", err)
	}
	if !acknowledgement.Duplicate {
		return errors.New("duplicate simulator Observation was not deduplicated by JetStream")
	}
	return nil
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
	t.Parallel()
	tests := []struct {
		name     string
		scenario string
		assert   func(*testing.T, *simulatorMatrixHarness)
	}{
		{
			name: "duplicate", scenario: simulatorMatrixDuplicateFault,
			assert: func(t *testing.T, harness *simulatorMatrixHarness) {
				state := harness.waitForState(t)
				if string(state.Value) != "false" {
					t.Fatalf("state = %#v", state)
				}
				assertMatrixCount(t, harness.database, "observations", 1)
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
			name: "malformed", scenario: simulatorMatrixMalformedFault,
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
				assertMatrixCount(t, harness.database, "observations", 0)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			harness := newSimulatorMatrixHarness(t, test.scenario, simulatorMatrixOptions{})
			test.assert(t, harness)
		})
	}
}

//nolint:gocognit // The failure matrix is clearer as one table-driven integration test.
func TestSimulatorCommandHTTPFailureMatrix(t *testing.T) {
	t.Parallel()
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
			name: "unhealthy adapter", scenario: simulatoradapter.ScenarioAdapterUnhealthy,
			wantStatus: http.StatusServiceUnavailable, wantDetail: "adapter unhealthy",
			wantCommand: devices.CommandStatusAdapterUnhealthy, wantFailure: devices.CommandFailureAdapterUnhealthy,
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
			name: "unexpected response", options: simulatorMatrixOptions{manual: true},
			prepare: func(t *testing.T, harness *simulatorMatrixHarness) {
				session, runtimeID := connectMatrixSession(t, harness)
				entityID, _ := registerMatrixEntity(harness.ctx, t, session)
				harness.entityID = entityID
				now := time.Now().UTC()
				if err := session.SetHealth(harness.ctx, adapter.HealthReport{
					Status: adapter.HealthHealthy, SourceObservedAt: now,
				}); err != nil {
					t.Fatal(err)
				}
				if err := session.ReportEntityAvailability(harness.ctx, []adapter.EntityAvailabilityReport{{
					EntityID: string(entityID), Status: adapter.AvailabilityAvailable, SourceObservedAt: now,
				}}); err != nil {
					t.Fatal(err)
				}
				subject, err := natswire.CommandSubject(
					simulatorMatrixAdapterID, string(runtimeID), string(harness.entityID), "set",
				)
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
				if flushErr := harness.connection.Flush(); flushErr != nil {
					t.Fatal(flushErr)
				}
			},
			wantStatus: http.StatusInternalServerError, wantDetail: "internal error",
			wantCommand: devices.CommandStatusInternalFailure, wantFailure: devices.CommandFailureInternalError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
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
			if decodeErr := json.Unmarshal(body, &response); decodeErr != nil {
				t.Fatal(decodeErr)
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
			if test.wantCommand == devices.CommandStatusAdapterUnhealthy && command.RuntimeID != nil {
				t.Fatalf("unhealthy Adapter Command selected runtime %s", *command.RuntimeID)
			}
		})
	}
}

func TestSimulatorUnavailableEntityRecoversThroughDispatchedCommand(t *testing.T) {
	t.Parallel()
	harness := newSimulatorMatrixHarness(t, simulatoradapter.ScenarioEntityUnavailable, simulatorMatrixOptions{})
	before, err := harness.service.GetEntity(harness.ctx, harness.entityID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Availability.Status != devices.EntityAvailabilityUnavailable || before.State != nil {
		t.Fatalf("Entity before recovery Command = %#v", before)
	}
	status, body, err := harness.postCommand(harness.ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	waitForMatrixCondition(t, 3*time.Second, func() (bool, error) {
		after, getErr := harness.service.GetEntity(harness.ctx, harness.entityID)
		if getErr != nil {
			return false, getErr
		}
		return after.Availability.Status == devices.EntityAvailabilityAvailable &&
			after.State != nil && string(after.State.Value) == "true", nil
	})
}

//nolint:gocognit // The overlapping command lifecycle is clearer as one integration test.
func TestSimulatorNoOpAndOverlappingCommands(t *testing.T) {
	t.Parallel()
	t.Run("no-op refresh", func(t *testing.T) {
		t.Parallel()
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
		if decodeErr := json.Unmarshal(body, &result); decodeErr != nil {
			t.Fatal(decodeErr)
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
		t.Parallel()
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
			commandOutcome := <-outcomes
			if commandOutcome.err != nil {
				t.Fatal(commandOutcome.err)
			}
			if commandOutcome.status != http.StatusOK {
				t.Fatalf("status = %d, body = %s", commandOutcome.status, commandOutcome.body)
			}
			var result devicesapi.CommandResultBody
			if err := json.Unmarshal(commandOutcome.body, &result); err != nil {
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
	t.Parallel()
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
	t.Parallel()
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
	created, createErr := harness.repository.CreateCommand(harness.ctx, record)
	if createErr != nil {
		t.Fatal(createErr)
	}
	if created.RuntimeID == nil {
		t.Fatal("created Command omitted runtime ID")
	}
	acceptance, err := devicesnats.NewCommandSender(harness.connection, harness.validator).Send(
		harness.ctx, simulatorMatrixAdapterID, *created.RuntimeID, devices.CommandRequest{
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
	if acceptErr := harness.repository.MarkCommandAccepted(harness.ctx, commandID, time.Now().UTC()); acceptErr != nil {
		t.Fatal(acceptErr)
	}
	if interruptErr := harness.repository.InterruptActiveCommands(harness.ctx, time.Now().UTC()); interruptErr != nil {
		t.Fatal(interruptErr)
	}
	observationID := publishMatrixLinkedObservation(t, harness, commandID, correlationID, true)
	waitForMatrixCondition(t, 3*time.Second, func() (bool, error) {
		view, getErr := harness.service.GetEntity(harness.ctx, harness.entityID)
		if getErr != nil {
			return false, getErr
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

//nolint:gocognit // The restart and redelivery lifecycle is clearer as one integration test.
func TestSimulatorRestartBeforeAckRedeliversWithoutChangingStateOrCommand(t *testing.T) {
	t.Parallel()
	committed := make(chan struct{})
	var failOnce atomic.Bool
	harness := newSimulatorMatrixHarness(t, simulatoradapter.ScenarioRestartBeforeAck, simulatorMatrixOptions{
		ackWait: 500 * time.Millisecond,
		observationProjector: func(_ *devices.Service, base devicesnats.ObservationProjector) devicesnats.ObservationProjector {
			return observationProjectorFunc(func(
				ctx context.Context,
				adapterID string,
				runtimeID devices.RuntimeID,
				observation devices.Observation,
				observedAt time.Time,
			) (devices.ProjectionResult, error) {
				result, err := base.ProjectObservation(ctx, adapterID, runtimeID, observation, observedAt)
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
	if decodeErr := json.Unmarshal(body, &result); decodeErr != nil {
		t.Fatal(decodeErr)
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
	assertMatrixCount(t, harness.database, "observations", 2)

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
			runtimeID devices.RuntimeID,
			observation devices.Observation,
			observedAt time.Time,
		) (devices.ProjectionResult, error) {
			projected, projectionErr := harness.service.ProjectObservation(
				ctx, adapterID, runtimeID, observation, observedAt,
			)
			if observation.ID == devices.ObservationID(result.ObservationID) {
				redeliveryOnce.Do(func() { close(redelivered) })
			}
			return projected, projectionErr
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
		info, infoErr := harness.durable.Info(harness.ctx)
		return infoErr == nil && info.NumAckPending == 0, infoErr
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
	assertMatrixCount(t, harness.database, "observations", 2)
}

//nolint:gocognit // The readiness sequence is easier to audit in chronological order.
func TestSimulatorReadinessRecoveryRestoresPersistedAvailabilityWithoutReport(t *testing.T) {
	t.Parallel()
	harness := newManualSimulatorMatrixHarness(t)
	session, _ := connectMatrixSession(t, harness)
	entityID, _ := registerMatrixEntity(harness.ctx, t, session)
	now := time.Now().UTC()
	if err := session.SetHealth(harness.ctx, adapter.HealthReport{
		Status: adapter.HealthHealthy, SourceObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	report := adapter.EntityAvailabilityReport{
		EntityID: string(entityID), Status: adapter.AvailabilityAvailable, SourceObservedAt: now,
	}
	if err := session.ReportEntityAvailability(harness.ctx, []adapter.EntityAvailabilityReport{report}); err != nil {
		t.Fatal(err)
	}
	beforeEntity, err := harness.service.GetEntity(harness.ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	beforeAdapterHistory, err := harness.service.ListAdapterHealthHistory(
		harness.ctx,
		devices.ListAdapterHealthParams{AdapterID: simulatorMatrixAdapterID, Limit: 200},
	)
	if err != nil {
		t.Fatal(err)
	}
	beforeEntityHistory, err := harness.service.ListEntityAvailabilityHistory(
		harness.ctx,
		devices.ListEntityAvailabilityParams{EntityID: entityID, Limit: 200},
	)
	if err != nil {
		t.Fatal(err)
	}

	readiness := &supervisorReadinessStub{err: errors.New("Core not ready")}
	supervisor := &healthSupervisor{
		readiness: readiness, health: harness.service, logger: slog.New(slog.DiscardHandler),
	}
	recoveredAt := time.Now().UTC()
	supervisor.poll(harness.ctx, recoveredAt.Add(-time.Second))
	readiness.err = nil
	supervisor.poll(harness.ctx, recoveredAt)
	recoveredAdapter, err := harness.service.GetAdapter(harness.ctx, simulatorMatrixAdapterID)
	if err != nil {
		t.Fatal(err)
	}
	recoveredEntity, err := harness.service.GetEntity(harness.ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if recoveredAdapter.Health.Status != devices.AdapterHealthHealthy ||
		recoveredEntity.Availability.Status != devices.EntityAvailabilityAvailable {
		t.Fatalf("recovery views = Adapter %#v, Entity %#v", recoveredAdapter, recoveredEntity)
	}
	if err = session.SetHealth(harness.ctx, adapter.HealthReport{
		Status: adapter.HealthHealthy, SourceObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	waitForMatrixCondition(t, 3*time.Second, func() (bool, error) {
		instance, adapterErr := harness.service.GetAdapter(harness.ctx, simulatorMatrixAdapterID)
		if adapterErr != nil {
			return false, adapterErr
		}
		entity, entityErr := harness.service.GetEntity(harness.ctx, entityID)
		if entityErr != nil {
			return false, entityErr
		}
		return instance.Health.Status == devices.AdapterHealthHealthy &&
			entity.Availability.Status == devices.EntityAvailabilityAvailable, nil
	})
	restoredEntity, err := harness.service.GetEntity(harness.ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if !restoredEntity.Availability.Since.Equal(beforeEntity.Availability.Since) ||
		!restoredEntity.Availability.EvidenceAt.Equal(beforeEntity.Availability.EvidenceAt) ||
		restoredEntity.Availability.SourceObservedAt == nil || beforeEntity.Availability.SourceObservedAt == nil ||
		!restoredEntity.Availability.SourceObservedAt.Equal(*beforeEntity.Availability.SourceObservedAt) {
		t.Fatalf(
			"persisted availability changed during recovery: before = %#v, after = %#v",
			beforeEntity.Availability, restoredEntity.Availability,
		)
	}
	afterAdapterHistory, err := harness.service.ListAdapterHealthHistory(
		harness.ctx,
		devices.ListAdapterHealthParams{AdapterID: simulatorMatrixAdapterID, Limit: 200},
	)
	if err != nil {
		t.Fatal(err)
	}
	afterEntityHistory, err := harness.service.ListEntityAvailabilityHistory(
		harness.ctx,
		devices.ListEntityAvailabilityParams{EntityID: entityID, Limit: 200},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterAdapterHistory.Items) != len(beforeAdapterHistory.Items) ||
		len(afterEntityHistory.Items) != len(beforeEntityHistory.Items) {
		t.Fatalf(
			"recovery created history: Adapter %d -> %d, Entity %d -> %d",
			len(beforeAdapterHistory.Items), len(afterAdapterHistory.Items),
			len(beforeEntityHistory.Items), len(afterEntityHistory.Items),
		)
	}
}

func TestSimulatorGracefulReleaseAllowsImmediateReplacement(t *testing.T) {
	t.Parallel()
	harness := newManualSimulatorMatrixHarness(t)
	first, _ := connectMatrixSession(t, harness)
	if err := first.SetHealth(harness.ctx, adapter.HealthReport{
		Status: adapter.HealthHealthy, SourceObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	waitForMatrixCondition(t, time.Second, func() (bool, error) {
		instance, err := harness.service.GetAdapter(harness.ctx, simulatorMatrixAdapterID)
		return err == nil && instance.Health.Status == devices.AdapterHealthUnhealthy &&
			instance.Health.Reason != nil && instance.Health.Reason.Code == "hearth.stopped" &&
			instance.Health.Runtime != nil && instance.Health.Runtime.Status == "offline", err
	})
	second, secondRuntimeID := connectMatrixSession(t, harness)
	instance, err := harness.service.GetAdapter(harness.ctx, simulatorMatrixAdapterID)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Health.Runtime == nil || instance.Health.Runtime.ID != secondRuntimeID {
		t.Fatalf("replacement Adapter = %#v", instance)
	}
	if err = second.Close(); err != nil {
		t.Fatal(err)
	}
}

//nolint:gocognit,gocyclo,cyclop // This process scenario keeps takeover and every stale-runtime effect in one causal sequence.
func TestSimulatorExpiryTakeoverFencesOldTrafficAndCommands(t *testing.T) {
	t.Parallel()
	harness := newManualSimulatorMatrixHarness(t)
	oldSession, oldRuntimeID := connectMatrixSession(t, harness)
	entityID, registration := registerMatrixEntity(harness.ctx, t, oldSession)
	now := time.Now().UTC()
	if err := oldSession.SetHealth(harness.ctx, adapter.HealthReport{
		Status: adapter.HealthHealthy, SourceObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := oldSession.ReportEntityAvailability(harness.ctx, []adapter.EntityAvailabilityReport{{
		EntityID: string(entityID), Status: adapter.AvailabilityAvailable, SourceObservedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
	capturedCommandID, err := devices.NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	capturedCorrelationID, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	captured, err := harness.repository.CreateCommand(harness.ctx, devices.CommandRecord{
		ID: capturedCommandID, EntityID: entityID, AdapterID: simulatorMatrixAdapterID,
		OperationName: devices.OperationNameSet, Parameters: devices.CommandParameters(`{"value":false}`),
		CorrelationID: capturedCorrelationID, Status: devices.CommandStatusRequested,
		RequestedAt: now, DeadlineAt: now.Add(10 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured.RuntimeID == nil || *captured.RuntimeID != oldRuntimeID {
		t.Fatalf("captured Command runtime = %#v", captured.RuntimeID)
	}

	competingContext, cancelCompeting := context.WithTimeout(context.Background(), 200*time.Millisecond)
	_, err = adapter.Connect(competingContext, adapter.Config{
		AdapterID: simulatorMatrixAdapterID, SoftwareName: "hearth-simulator",
		SoftwareVersion: "0.1.0", NATSURL: harness.server.ClientURL(),
	})
	cancelCompeting()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("claim while first runtime was active = %v", err)
	}
	if err = harness.service.ExpireAdapterLeases(harness.ctx, time.Now().UTC().Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	newSession, newRuntimeID := connectMatrixSession(t, harness)
	newEntityID, _ := registerMatrixEntity(harness.ctx, t, newSession)
	if newEntityID != entityID {
		t.Fatalf("replacement Entity ID = %s, want %s", newEntityID, entityID)
	}
	if err = newSession.SetHealth(harness.ctx, adapter.HealthReport{
		Status: adapter.HealthHealthy, SourceObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err = newSession.ReportEntityAvailability(harness.ctx, []adapter.EntityAvailabilityReport{{
		EntityID: string(entityID), Status: adapter.AvailabilityAvailable, SourceObservedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	var oldCommands atomic.Int64
	var newCommands atomic.Int64
	serveContext, stopServing := context.WithCancel(context.Background())
	defer stopServing()
	oldServe := make(chan error, 1)
	newServe := make(chan error, 1)
	go func() {
		oldServe <- oldSession.ServeCommands(serveContext, func(
			_ context.Context,
			_ adapter.Command,
			responder adapter.Responder,
		) error {
			oldCommands.Add(1)
			return responder.Reject("old runtime received Command")
		})
	}()
	go func() {
		newServe <- newSession.ServeCommands(serveContext, func(
			_ context.Context,
			_ adapter.Command,
			responder adapter.Responder,
		) error {
			newCommands.Add(1)
			return responder.Reject("simulated rejection")
		})
	}()
	waitForMatrixCommandSubscription(t, harness, string(oldRuntimeID), entityID)
	waitForMatrixCommandSubscription(t, harness, string(newRuntimeID), entityID)
	acceptance, err := devicesnats.NewCommandSender(harness.connection, harness.validator).Send(
		harness.ctx,
		simulatorMatrixAdapterID,
		*captured.RuntimeID,
		devices.CommandRequest{
			ID: capturedCommandID, CorrelationID: capturedCorrelationID, EntityID: entityID,
			OperationName: devices.OperationNameSet, Parameters: devices.CommandParameters(`{"value":false}`),
			Deadline: captured.DeadlineAt,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if acceptance.Accepted || oldCommands.Load() != 1 || newCommands.Load() != 0 {
		t.Fatalf(
			"captured Command acceptance = %#v, deliveries = old %d, new %d",
			acceptance, oldCommands.Load(), newCommands.Load(),
		)
	}
	_, err = harness.service.ExecuteCommand(
		harness.ctx,
		entityID,
		devices.OperationNameSet,
		devices.CommandParameters(`{"value":true}`),
	)
	if !errors.Is(err, devices.ErrUpstreamRejected) {
		t.Fatalf("replacement Command error = %v", err)
	}
	if oldCommands.Load() != 1 || newCommands.Load() != 1 {
		t.Fatalf("Command deliveries = old %d, new %d", oldCommands.Load(), newCommands.Load())
	}
	commands, err := harness.repository.ListEntityCommands(harness.ctx, devices.ListEntityCommandsParams{
		EntityID: entityID, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(commands.Items) != 1 || commands.Items[0].RuntimeID == nil ||
		*commands.Items[0].RuntimeID != newRuntimeID {
		t.Fatalf("replacement Command = %#v", commands.Items)
	}

	observationID, err := oldSession.PublishObservation(harness.ctx, adapter.Observation{
		EntityID: string(entityID), Value: json.RawMessage(`false`),
		AdapterReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForMatrixCondition(t, 3*time.Second, func() (bool, error) {
		var rejection string
		queryErr := harness.database.QueryRowContext(
			harness.ctx,
			`SELECT rejection_code FROM observations WHERE observation_id = ?`,
			observationID,
		).Scan(&rejection)
		if errors.Is(queryErr, sql.ErrNoRows) {
			return false, nil
		}
		return rejection == "stale_runtime", queryErr
	})
	err = oldSession.ReportEntityAvailability(harness.ctx, []adapter.EntityAvailabilityReport{{
		EntityID: string(entityID), Status: adapter.AvailabilityAvailable,
		SourceObservedAt: time.Now().UTC(),
	}})
	if !errors.Is(err, adapter.ErrRuntimeFenced) {
		t.Fatalf("stale availability report = %v", err)
	}
	if _, err = oldSession.Register(harness.ctx, registration); !errors.Is(err, adapter.ErrRuntimeFenced) {
		t.Fatalf("stale Registration = %v", err)
	}
	_, err = oldSession.SetEntityEnabled(
		harness.ctx,
		string(entityID),
		false,
	)
	if !errors.Is(err, adapter.ErrRuntimeFenced) {
		t.Fatalf("stale enablement = %v", err)
	}
	stopServing()
	select {
	case serveErr := <-oldServe:
		if !errors.Is(serveErr, adapter.ErrRuntimeFenced) && !errors.Is(serveErr, context.Canceled) {
			t.Fatalf("old command server = %v", serveErr)
		}
	case <-time.After(time.Second):
		t.Fatal("old command server did not stop")
	}
	select {
	case serveErr := <-newServe:
		if !errors.Is(serveErr, context.Canceled) {
			t.Fatalf("new command server = %v", serveErr)
		}
	case <-time.After(time.Second):
		t.Fatal("new command server did not stop")
	}
}

func newManualSimulatorMatrixHarness(t *testing.T) *simulatorMatrixHarness {
	t.Helper()
	return newSimulatorMatrixHarness(t, "", simulatorMatrixOptions{manual: true})
}

func connectMatrixSession(
	t *testing.T,
	harness *simulatorMatrixHarness,
) (*adapter.Session, devices.RuntimeID) {
	t.Helper()
	session, err := adapter.Connect(harness.ctx, adapter.Config{
		AdapterID: simulatorMatrixAdapterID, SoftwareName: "hearth-simulator",
		SoftwareVersion: "0.1.0", NATSURL: harness.server.ClientURL(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := session.Close(); closeErr != nil && !errors.Is(closeErr, adapter.ErrRuntimeFenced) {
			t.Errorf("close simulator Session: %v", closeErr)
		}
	})
	return session, currentMatrixRuntimeID(t, harness)
}

func currentMatrixRuntimeID(t *testing.T, harness *simulatorMatrixHarness) devices.RuntimeID {
	t.Helper()
	instance, err := harness.service.GetAdapter(harness.ctx, simulatorMatrixAdapterID)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Health.Runtime == nil {
		t.Fatal("simulator Adapter has no runtime")
	}
	return instance.Health.Runtime.ID
}

func registerMatrixEntity(
	ctx context.Context,
	t *testing.T,
	session *adapter.Session,
) (devices.EntityID, adapter.Registration) {
	t.Helper()
	simulated, err := simulatoradapter.New(session, simulatoradapter.ScenarioHappy)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := sdkpowerv1.NewEntityDescriptor(adapter.EntityMetadata{
		Key: "power", ExternalID: "simulated-light.power", Name: "Power",
	}, simulated.Support())
	if err != nil {
		t.Fatal(err)
	}
	deviceExternalID := "simulated-light"
	registration := adapter.Registration{
		BindingKey: "simulated-light",
		Device: adapter.DeviceDescriptor{
			ExternalID: &deviceExternalID, Name: "Simulated light", Kind: "light",
		},
		Entities: []adapter.EntityDescriptor{descriptor},
	}
	binding, err := session.Register(ctx, registration)
	if err != nil {
		t.Fatal(err)
	}
	entityID, err := devices.ParseEntityID(binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	return entityID, registration
}

func waitForMatrixCommandSubscription(
	t *testing.T,
	harness *simulatorMatrixHarness,
	runtimeID string,
	entityID devices.EntityID,
) {
	t.Helper()
	subject, err := natswire.CommandSubject(
		simulatorMatrixAdapterID,
		runtimeID,
		string(entityID),
		"set",
	)
	if err != nil {
		t.Fatal(err)
	}
	waitForMatrixCondition(t, time.Second, func() (bool, error) {
		subscriptions, subscriptionsErr := harness.server.Subsz(&natsserver.SubszOptions{
			Subscriptions: true,
			Test:          subject,
		})
		if subscriptionsErr != nil {
			return false, subscriptionsErr
		}
		return subscriptions.Total > 0, nil
	})
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
		natswire.Envelope[wireObservation]{
			ID: string(observationID), Schema: contractsv1.ObservationSchemaID, EmittedAt: now,
			CorrelationID: string(correlationID), CausationID: &commandIDString,
			Data: wireObservation{
				EntityID: string(harness.entityID), Value: json.RawMessage(strconv.FormatBool(value)),
				AdapterReceivedAt: now, RefreshForCommand: &commandIDString,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	runtimeID := currentMatrixRuntimeID(t, harness)
	subject, err := natswire.ObservationSubject(
		simulatorMatrixAdapterID, string(runtimeID), string(harness.entityID),
	)
	if err != nil {
		t.Fatal(err)
	}
	message := &natsgo.Msg{Subject: subject, Data: payload, Header: make(natsgo.Header)}
	message.Header.Set(natsgo.MsgIdHdr, string(observationID))
	if _, publishErr := harness.jetstream.PublishMsg(harness.ctx, message); publishErr != nil {
		t.Fatal(publishErr)
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
