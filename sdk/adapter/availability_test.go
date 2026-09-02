package adapter //nolint:testpackage // Tests exercise package-private availability request behavior.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

func TestHealthValidationAcceptsDocumentedAdapterNamespace(t *testing.T) {
	t.Parallel()
	report := HealthReport{
		Status: HealthUnhealthy, SourceObservedAt: time.Now().UTC(),
		ReasonCode: "adapter.entity_unavailable",
	}
	if err := validateHealthReport(report); err != nil {
		t.Fatalf("documented Adapter reason rejected: %v", err)
	}
	report.ReasonCode = "vendor.offline"
	if err := validateHealthReport(report); err == nil {
		t.Fatal("unsupported reason namespace was accepted")
	}
}

func TestReportEntityAvailabilityRetriesOneEnvelope(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	validator := compileValidator(t)
	requestIDs := make(chan string, 2)
	var attempts atomic.Int32
	if _, err := core.Subscribe(natswire.EntityAvailabilityWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[entityAvailabilityRequest](
			validator, contractsv1.EntityAvailabilityRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode availability request: %v", decodeErr)
			return
		}
		requestIDs <- request.ID
		if attempts.Add(1) == 1 {
			return
		}
		respondAvailability(t, validator, message, request, entityAvailabilityResponse{
			Status: statusAccepted, ReportedAt: nowString(), Count: len(request.Data.Entities),
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := core.Flush(); err != nil {
		t.Fatal(err)
	}
	session := connectSession(t, server.ClientURL())
	report := EntityAvailabilityReport{
		EntityID: testEntityID, Status: AvailabilityUnavailable, SourceObservedAt: time.Now().UTC(),
		ReasonCode: "hearth.entity_unavailable",
	}
	if err := session.ReportEntityAvailability(testContext(t), []EntityAvailabilityReport{report}); err != nil {
		t.Fatal(err)
	}
	firstID := <-requestIDs
	secondID := <-requestIDs
	if firstID != secondID {
		t.Fatalf("availability retry IDs = %q and %q", firstID, secondID)
	}
}

func TestReportEntityAvailabilityRejectsInvalidReportsLocally(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	session := connectSession(t, server.ClientURL())
	now := time.Now().UTC()
	valid := EntityAvailabilityReport{
		EntityID: testEntityID, Status: AvailabilityAvailable, SourceObservedAt: now,
	}
	for _, reports := range [][]EntityAvailabilityReport{
		nil,
		{valid, valid},
		{{EntityID: testEntityID, Status: AvailabilityAvailable, SourceObservedAt: now,
			ReasonCode: "hearth.entity_unavailable"}},
		{{EntityID: testEntityID, Status: AvailabilityUnavailable, SourceObservedAt: now}},
		{{EntityID: "invalid", Status: AvailabilityAvailable, SourceObservedAt: now}},
	} {
		err := session.ReportEntityAvailability(testContext(t), reports)
		if _, ok := errors.AsType[*ValidationError](err); !ok {
			t.Fatalf("invalid reports %#v error = %v", reports, err)
		}
	}
}

func TestAcknowledgedAvailabilityIsNotReplayedByHeartbeat(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	validator := compileValidator(t)
	startClaimAndReleaseResponders(t, core, validator)
	if _, err := core.Subscribe(natswire.AdapterHeartbeatWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[adapterHeartbeatRequest](
			validator, contractsv1.AdapterHeartbeatRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode heartbeat: %v", decodeErr)
			return
		}
		respondTestLifecycle(t, validator, message, request.ID, request.CorrelationID,
			contractsv1.AdapterHeartbeatResponseSchemaID, adapterHeartbeatResponse{
				Status:         statusAccepted,
				LeaseExpiresAt: time.Now().UTC().Add(15 * time.Second).Format(time.RFC3339Nano),
			})
	}); err != nil {
		t.Fatal(err)
	}
	availabilityCounts := make(chan int, 4)
	if _, err := core.Subscribe(natswire.EntityAvailabilityWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[entityAvailabilityRequest](
			validator, contractsv1.EntityAvailabilityRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode availability request: %v", decodeErr)
			return
		}
		availabilityCounts <- len(request.Data.Entities)
		respondAvailability(t, validator, message, request, entityAvailabilityResponse{
			Status: statusAccepted, ReportedAt: nowString(), Count: len(request.Data.Entities),
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := core.Flush(); err != nil {
		t.Fatal(err)
	}
	session, connectErr := Connect(testContext(t), testConfig(server.ClientURL()))
	if connectErr != nil {
		t.Fatal(connectErr)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.SetHealth(testContext(t), HealthReport{
		Status: HealthHealthy, SourceObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	if err := session.ReportEntityAvailability(testContext(t), []EntityAvailabilityReport{{
		EntityID: testEntityID, Status: AvailabilityAvailable, SourceObservedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}
	if got := <-availabilityCounts; got != 1 {
		t.Fatalf("direct availability batch = %d", got)
	}

	if err := session.SetHealth(testContext(t), HealthReport{
		Status: HealthHealthy, SourceObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case count := <-availabilityCounts:
		t.Fatalf("heartbeat caused an additional availability report of %d Entities", count)
	case <-time.After(250 * time.Millisecond):
	}
}

func TestEntityAvailabilityFencingTerminatesSession(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	validator := compileValidator(t)
	if _, err := core.Subscribe(natswire.EntityAvailabilityWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[entityAvailabilityRequest](
			validator, contractsv1.EntityAvailabilityRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode availability request: %v", decodeErr)
			return
		}
		respondAvailability(t, validator, message, request, entityAvailabilityResponse{
			Status: statusRejected,
			Error:  &entityAvailabilityError{Code: "runtime_fenced", Message: "runtime replaced"},
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := core.Flush(); err != nil {
		t.Fatal(err)
	}
	session := connectSession(t, server.ClientURL())
	err := session.ReportEntityAvailability(testContext(t), []EntityAvailabilityReport{{
		EntityID: testEntityID, Status: AvailabilityAvailable, SourceObservedAt: time.Now().UTC(),
	}})
	if !errors.Is(err, ErrRuntimeFenced) {
		t.Fatalf("availability fencing error = %v", err)
	}
	if futureErr := session.ReportEntityAvailability(context.Background(), []EntityAvailabilityReport{{
		EntityID: testEntityID, Status: AvailabilityAvailable, SourceObservedAt: time.Now().UTC(),
	}}); !errors.Is(futureErr, ErrRuntimeFenced) {
		t.Fatalf("future availability error = %v", futureErr)
	}
}

func respondAvailability(
	t *testing.T,
	validator *contractsv1.Validator,
	message *natsgo.Msg,
	request natswire.Envelope[entityAvailabilityRequest],
	response entityAvailabilityResponse,
) {
	t.Helper()
	respondTestLifecycle(
		t, validator, message, request.ID, request.CorrelationID,
		contractsv1.EntityAvailabilityResponseSchemaID, response,
	)
}
