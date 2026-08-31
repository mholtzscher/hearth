package adapter //nolint:testpackage // Tests exercise package-private availability cache behavior.

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

func TestReportEntityAvailabilityRetriesOneEnvelopeAndCachesAcknowledgedReport(t *testing.T) {
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
		ReasonCode: "hearth.entity_unavailable", Detail: "upstream resource missing",
	}
	if err := session.ReportEntityAvailability(testContext(t), []EntityAvailabilityReport{report}); err != nil {
		t.Fatal(err)
	}
	firstID := <-requestIDs
	secondID := <-requestIDs
	if firstID != secondID {
		t.Fatalf("availability retry IDs = %q and %q", firstID, secondID)
	}
	session.stateMutex.Lock()
	cached, ok := session.availabilityCache[testEntityID]
	session.stateMutex.Unlock()
	if !ok || cached.Status != AvailabilityUnavailable || cached.ReasonCode != report.ReasonCode ||
		cached.SourceObservedAt.Location() != time.UTC {
		t.Fatalf("cached availability = %#v, present %t", cached, ok)
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

//nolint:gocognit // The lifecycle sequence verifies cache population, bounded replay, and invalidation.
func TestHeartbeatRefreshReplaysCacheInBoundedBatchesAndUnhealthyClearsIt(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	validator := compileValidator(t)
	startClaimAndReleaseResponders(t, core, validator)
	var heartbeatCount atomic.Int32
	if _, err := core.Subscribe(natswire.AdapterHeartbeatWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[adapterHeartbeatRequest](
			validator, contractsv1.AdapterHeartbeatRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode heartbeat: %v", decodeErr)
			return
		}
		refresh := heartbeatCount.Add(1) == 2
		respondTestLifecycle(t, validator, message, request.ID, request.CorrelationID,
			contractsv1.AdapterHeartbeatResponseSchemaID, adapterHeartbeatResponse{
				Status:                    statusAccepted,
				LeaseExpiresAt:            time.Now().UTC().Add(15 * time.Second).Format(time.RFC3339Nano),
				RefreshEntityAvailability: &refresh,
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

	reports := make([]EntityAvailabilityReport, maximumAvailabilityBatchSize+1)
	for index := range reports {
		reports[index] = EntityAvailabilityReport{
			EntityID: mustID(t, "ent"), Status: AvailabilityAvailable, SourceObservedAt: time.Now().UTC(),
		}
	}
	if err := session.ReportEntityAvailability(
		testContext(t), reports[:maximumAvailabilityBatchSize],
	); err != nil {
		t.Fatal(err)
	}
	if got := <-availabilityCounts; got != maximumAvailabilityBatchSize {
		t.Fatalf("first direct availability batch = %d", got)
	}
	if err := session.ReportEntityAvailability(
		testContext(t), reports[maximumAvailabilityBatchSize:],
	); err != nil {
		t.Fatal(err)
	}
	if got := <-availabilityCounts; got != 1 {
		t.Fatalf("second direct availability batch = %d", got)
	}

	if err := session.SetHealth(testContext(t), HealthReport{
		Status: HealthHealthy, SourceObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	for index, want := range []int{maximumAvailabilityBatchSize, 1} {
		select {
		case got := <-availabilityCounts:
			if got != want {
				t.Fatalf("replay batch %d = %d, want %d", index, got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("replay batch %d did not arrive", index)
		}
	}

	if err := session.SetHealth(testContext(t), HealthReport{
		Status: HealthUnhealthy, SourceObservedAt: time.Now().UTC(),
		ReasonCode: "hearth.external_system_unavailable",
	}); err != nil {
		t.Fatal(err)
	}
	session.stateMutex.Lock()
	cacheSize := len(session.availabilityCache)
	session.stateMutex.Unlock()
	if cacheSize != 0 {
		t.Fatalf("availability cache size after unhealthy = %d", cacheSize)
	}
}

func TestStalledAvailabilityReplayDoesNotBlockNextHeartbeat(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	validator := compileValidator(t)
	startClaimAndReleaseResponders(t, core, validator)

	continuedHeartbeats := make(chan struct{}, 1)
	var refreshSent atomic.Bool
	if _, err := core.Subscribe(natswire.AdapterHeartbeatWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[adapterHeartbeatRequest](
			validator, contractsv1.AdapterHeartbeatRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode heartbeat: %v", decodeErr)
			return
		}
		refresh := false
		if request.Data.ExternalSystem.Status == string(HealthHealthy) &&
			refreshSent.CompareAndSwap(false, true) {
			refresh = true
		} else if refreshSent.Load() {
			select {
			case continuedHeartbeats <- struct{}{}:
			default:
			}
		}
		respondTestLifecycle(t, validator, message, request.ID, request.CorrelationID,
			contractsv1.AdapterHeartbeatResponseSchemaID, adapterHeartbeatResponse{
				Status:                    statusAccepted,
				LeaseExpiresAt:            time.Now().UTC().Add(15 * time.Second).Format(time.RFC3339Nano),
				RefreshEntityAvailability: &refresh,
			})
	}); err != nil {
		t.Fatal(err)
	}
	availabilityStarted := make(chan struct{}, 1)
	if _, err := core.Subscribe(natswire.EntityAvailabilityWildcard(), func(*natsgo.Msg) {
		select {
		case availabilityStarted <- struct{}{}:
		default:
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := core.Flush(); err != nil {
		t.Fatal(err)
	}

	session, err := Connect(testContext(t), testConfig(server.ClientURL()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	session.stateMutex.Lock()
	session.availabilityCache[testEntityID] = EntityAvailabilityReport{
		EntityID: testEntityID, Status: AvailabilityAvailable, SourceObservedAt: time.Now().UTC(),
	}
	session.stateMutex.Unlock()
	if healthErr := session.SetHealth(testContext(t), HealthReport{
		Status: HealthHealthy, SourceObservedAt: time.Now().UTC(),
	}); healthErr != nil {
		t.Fatal(healthErr)
	}
	select {
	case <-availabilityStarted:
	case <-time.After(time.Second):
		t.Fatal("availability replay did not start")
	}
	session.wakeHeartbeat()
	select {
	case <-continuedHeartbeats:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("heartbeat was blocked by availability replay")
	}
}

func TestEntityAvailabilityFencingTerminatesSessionAndClearsCache(t *testing.T) {
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
	session.stateMutex.Lock()
	session.availabilityCache[testEntityID] = EntityAvailabilityReport{EntityID: testEntityID}
	session.stateMutex.Unlock()
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
	session.stateMutex.Lock()
	cacheSize := len(session.availabilityCache)
	session.stateMutex.Unlock()
	if cacheSize != 0 {
		t.Fatalf("availability cache size after fencing = %d", cacheSize)
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
