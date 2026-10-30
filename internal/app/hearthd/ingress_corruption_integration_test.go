//go:build integration

package hearthd //nolint:testpackage // Selected real ingress/readiness assembly, not a full Core process.

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
)

const corruptionRuntime = "run_01890f47-7a6b-7c4d-8e9f-0123456789ab"

type ingressCorruptionFixture struct {
	readinessFixture

	service   *devices.Service
	entity    devices.EntityID
	validator *contractsv1.Validator
}

func newIngressCorruptionFixture(t *testing.T) ingressCorruptionFixture {
	t.Helper()
	f := newReadinessFixture(t)
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	adapterRepo := adapterssqlite.NewAdapterRepository(f.database)
	at := time.Now().UTC()
	if err = adapterRepo.ClaimAdapterRuntime(
		t.Context(),
		adapters.ClaimRuntimeWrite{
			AdapterID:       "poison-test",
			RuntimeID:       adapters.RuntimeID(corruptionRuntime),
			SoftwareName:    "test",
			SoftwareVersion: "1",
			ClaimedAt:       at,
			LeaseExpiresAt:  at.Add(15 * time.Second),
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err = adapterRepo.RecordAdapterHeartbeat(
		t.Context(),
		adapters.HeartbeatWrite{
			AdapterID:        "poison-test",
			RuntimeID:        adapters.RuntimeID(corruptionRuntime),
			ExternalStatus:   adapters.AdapterHealthHealthy,
			SourceObservedAt: at,
			ReceivedAt:       at,
			LeaseExpiresAt:   at.Add(15 * time.Second),
		},
	); err != nil {
		t.Fatal(err)
	}
	repo := devicessqlite.NewDeviceRepository(f.database, catalog)
	service := devices.NewService(
		devices.Stores{
			Registration: repo,
			AdapterReads: adapterRepo,
			Observations: repo,
			Availability: repo,
			Reads:        repo,
		},
		nil,
		catalog,
		devices.Dependencies{DeviceFacts: f.relay},
	)
	binding, err := service.Register(
		t.Context(),
		"poison-test",
		adapters.RuntimeID(corruptionRuntime),
		devices.Registration{
			BindingKey: "power",
			Device:     devices.DeviceDescriptor{Name: "Synthetic power", Kind: devices.DeviceKindLight},
			Entities: []devices.EntityDescriptor{
				{
					Key:        "power",
					ExternalID: "synthetic.power",
					Name:       "Power",
					TypeID:     devices.EntityTypePowerV1,
					Support:    devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return ingressCorruptionFixture{
		readinessFixture: f,
		service:          service,
		entity:           binding.Entities[0].EntityID,
		validator:        validator,
	}
}

func TestCorruptObservationBlocksItsPendingSlotWithoutFailingRuntimeReadiness(t *testing.T) {
	t.Parallel()
	f := newIngressCorruptionFixture(t)
	// Replace the readiness fixture's discard projector with the actual service.
	f.consumer.Stop()
	<-f.consumer.Closed()
	durable, err := f.jetstream.Consumer(
		t.Context(),
		devicesnats.ObservationStreamName,
		devicesnats.ObservationConsumerName,
	)
	if err != nil {
		t.Fatal(err)
	}
	projector := &corruptionProjector{service: f.service, results: make(chan corruptionProjection, 4)}
	running, err := devicesnats.StartObservationConsumer(
		t.Context(),
		durable,
		f.validator,
		projector,
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { running.Stop(); <-running.Closed() })
	f.readiness.observationConsumer = running
	if _, err = f.database.ExecContext(
		t.Context(),
		`UPDATE entities SET support_json='{}' WHERE id=?`,
		string(f.entity),
	); err != nil {
		t.Fatal(err)
	}
	first := "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	second := "obs_01890f47-7a6b-7c4d-8e9f-0123456789ac"
	publishCorruptionObservation(t, f, first)
	initial := nextCorruptionProjection(t, projector.results, 5*time.Second)
	if string(initial.id) != first || initial.err == nil {
		t.Fatalf("stored corruption did not reach projection failure: %+v", initial)
	}
	publishCorruptionObservation(t, f, second)
	info, err := durable.Info(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if info.NumAckPending != 1 || info.NumPending != 1 || info.Delivered.Stream != 1 {
		t.Fatalf("pending-slot contract changed: %+v", info)
	}
	if err = f.readiness.Check(t.Context()); err != nil {
		t.Fatalf("processing fault unexpectedly failed runtime readiness: %v", err)
	}
	assertCorruptionObservationCount(t, f, 0)
	// Repair only this isolated fixture, not production data. The original bytes
	// must redeliver after the unchanged real AckWait, before the queued message.
	if _, err = f.database.ExecContext(
		t.Context(),
		`UPDATE entities SET support_json=? WHERE id=?`,
		`{"state":{},"operations":{"set":{}}}`,
		string(f.entity),
	); err != nil {
		t.Fatal(err)
	}
	replay := nextCorruptionProjection(t, projector.results, devicesnats.ObservationAckWait+10*time.Second)
	if string(replay.id) != first || replay.err != nil || replay.disposition != devices.DispositionApplied {
		t.Fatalf("same-ID replay after fixture repair failed: %+v", replay)
	}
	queued := nextCorruptionProjection(t, projector.results, 5*time.Second)
	if string(queued.id) != second || queued.err != nil || queued.disposition != devices.DispositionUnchanged {
		t.Fatalf("queued input failed after release: %+v", queued)
	}
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		current, readErr := durable.Info(t.Context())
		return readErr == nil && current.NumAckPending == 0 && current.AckFloor.Stream == 2, readErr
	})
	assertCorruptionObservationCount(t, f, 2)
	if err = f.readiness.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptAvailabilityReceiptGetsNoReplyWithoutFailingRuntimeReadiness(t *testing.T) {
	t.Parallel()
	f := newIngressCorruptionFixture(t)
	reporter := &corruptionAvailabilityReporter{service: f.service, results: make(chan error, 4)}
	server, err := devicesnats.StartEntityAvailabilityServer(
		f.connection,
		f.validator,
		reporter,
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	requestID := "avl_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	source := time.Now().UTC().Format(time.RFC3339Nano)
	request := corruptionAvailabilityRequest(t, f, requestID, source)
	if _, err = f.connection.RequestMsg(request, 2*time.Second); err != nil {
		t.Fatalf("valid control failed: %v", err)
	}
	if reportErr := <-reporter.results; reportErr != nil {
		t.Fatal(reportErr)
	}
	var reportedAt string
	if err = f.database.QueryRowContext(t.Context(), `SELECT reported_at FROM entity_availability_receipts WHERE request_id=?`, requestID).
		Scan(&reportedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = f.database.ExecContext(
		t.Context(),
		`UPDATE entity_availability_receipts SET reported_at='SENSITIVE-corrupt-time' WHERE request_id=?`,
		requestID,
	); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = f.connection.RequestMsg(request, 250*time.Millisecond); !errors.Is(err, natsgo.ErrTimeout) {
			t.Fatalf("corrupt receipt response=%v want no reply", err)
		}
		select {
		case reportErr := <-reporter.results:
			if reportErr == nil {
				t.Fatal("request timed out without the intended storage fault")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("availability callback did not reach the real service")
		}
	}
	if err = f.readiness.Check(t.Context()); err != nil {
		t.Fatalf("no-reply fault failed runtime readiness: %v", err)
	}
	// Restore only the receipt bytes captured from this fixture's accepted request.
	if _, err = f.database.ExecContext(
		t.Context(),
		`UPDATE entity_availability_receipts SET reported_at=? WHERE request_id=?`,
		reportedAt,
		requestID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = f.connection.RequestMsg(request, 2*time.Second); err != nil {
		t.Fatalf("same request failed after fixture repair: %v", err)
	}
	if reportErr := <-reporter.results; reportErr != nil {
		t.Fatal(reportErr)
	}
}

type corruptionAvailabilityReporter struct {
	service *devices.Service
	results chan error
}

func (reporter *corruptionAvailabilityReporter) ReportEntityAvailability(
	ctx context.Context,
	id, adapter string,
	runtime adapters.RuntimeID,
	reports []devices.EntityAvailabilityReport,
) (time.Time, error) {
	at, err := reporter.service.ReportEntityAvailability(ctx, id, adapter, runtime, reports)
	reporter.results <- err
	return at, err
}

type corruptionProjection struct {
	id          devices.ObservationID
	err         error
	disposition devices.ObservationDisposition
}
type corruptionProjector struct {
	service *devices.Service
	results chan corruptionProjection
}

func (projector *corruptionProjector) ProjectObservation(
	ctx context.Context,
	adapter string,
	runtime adapters.RuntimeID,
	observation devices.Observation,
	at time.Time,
) (devices.ProjectionResult, error) {
	result, err := projector.service.ProjectObservation(ctx, adapter, runtime, observation, at)
	projector.results <- corruptionProjection{id: observation.ID, err: err, disposition: result.Disposition}
	return result, err
}

func nextCorruptionProjection(
	t *testing.T,
	results <-chan corruptionProjection,
	timeout time.Duration,
) corruptionProjection {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(timeout):
		t.Fatal("timed out waiting for actual broker delivery")
		return corruptionProjection{}
	}
}

func publishCorruptionObservation(t *testing.T, f ingressCorruptionFixture, id string) {
	t.Helper()
	subject, err := natswire.ObservationSubject("poison-test", corruptionRuntime, string(f.entity))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := natswire.Encode(
		f.validator,
		contractsv1.ObservationSchemaID,
		natswire.Envelope[map[string]any]{
			ID:            id,
			Schema:        contractsv1.ObservationSchemaID,
			EmittedAt:     time.Now().UTC().Format(time.RFC3339Nano),
			CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			Data: map[string]any{
				"entity_id":           string(f.entity),
				"value":               true,
				"adapter_received_at": time.Now().UTC().Format(time.RFC3339Nano),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.jetstream.PublishMsg(
		t.Context(),
		&natsgo.Msg{Subject: subject, Header: natsgo.Header{natsgo.MsgIdHdr: []string{id}}, Data: payload},
	); err != nil {
		t.Fatal(err)
	}
}

func corruptionAvailabilityRequest(t *testing.T, f ingressCorruptionFixture, id, source string) *natsgo.Msg {
	t.Helper()
	subject, err := natswire.EntityAvailabilitySubject("poison-test", corruptionRuntime)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := natswire.Encode(
		f.validator,
		contractsv1.EntityAvailabilityRequestSchemaID,
		natswire.Envelope[map[string]any]{
			ID:            id,
			Schema:        contractsv1.EntityAvailabilityRequestSchemaID,
			EmittedAt:     source,
			CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			Data: map[string]any{
				"entities": []any{
					map[string]any{"entity_id": string(f.entity), "status": "available", "source_observed_at": source},
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return &natsgo.Msg{Subject: subject, Data: payload}
}

func assertCorruptionObservationCount(t *testing.T, f ingressCorruptionFixture, want int) {
	t.Helper()
	var count int
	if err := f.database.QueryRowContext(t.Context(), `SELECT count(*) FROM observations WHERE entity_id=?`, string(f.entity)).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("observation rows=%d want=%d", count, want)
	}
}
