package nats //nolint:testpackage // Tests exercise package-private relay, mapping and stream behavior.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

const (
	testFactTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	testFactTracestate  = "hearth=core,device=b0"
	// testFactLiveness bounds every relay wait. It is long enough that a healthy
	// relay always wins the race and short enough to fail a hung one.
	testFactLiveness = 5 * time.Second
)

// deviceFactChange is a broadcast latch shared by a test's outbox and publisher.
// Every observed state change closes the current channel and installs a fresh
// one, so a waiter that captures the channel before re-checking its condition can
// never miss a change and never has to sleep to make progress.
type deviceFactChange struct {
	mutex   sync.Mutex
	current chan struct{}
}

func newDeviceFactChange() *deviceFactChange {
	return &deviceFactChange{current: make(chan struct{})}
}

func (change *deviceFactChange) signal() {
	change.mutex.Lock()
	defer change.mutex.Unlock()
	close(change.current)
	change.current = make(chan struct{})
}

func (change *deviceFactChange) wait() <-chan struct{} {
	change.mutex.Lock()
	defer change.mutex.Unlock()
	return change.current
}

// waitForDeviceFactCondition blocks until condition returns true or the liveness
// budget expires. It re-checks after every change signal, so it is a bounded
// liveness wait rather than a correctness oracle backed by a sleep.
func waitForDeviceFactCondition(t *testing.T, change *deviceFactChange, condition func() bool) {
	t.Helper()
	timer := time.NewTimer(testFactLiveness)
	defer timer.Stop()
	for {
		observed := change.wait()
		if condition() {
			return
		}
		select {
		case <-observed:
		case <-timer.C:
			t.Fatal("timed out waiting for device fact relay state")
		}
	}
}

// fakeDeviceFactOutbox is a deterministic in-memory outbox. A test seeds pending
// facts, observes the exact deletions the relay performed and can fail reads or
// deletes for a bounded number of calls.
type fakeDeviceFactOutbox struct {
	change       *deviceFactChange
	mutex        sync.Mutex
	pending      []devices.PendingDeviceFact
	listFailures int
	deleteFails  int
	lists        int
	deletions    []devices.DeviceFactID
}

func newFakeDeviceFactOutbox(change *deviceFactChange) *fakeDeviceFactOutbox {
	return &fakeDeviceFactOutbox{change: change}
}

func (outbox *fakeDeviceFactOutbox) enqueue(facts ...devices.PendingDeviceFact) {
	outbox.mutex.Lock()
	outbox.pending = append(outbox.pending, facts...)
	outbox.mutex.Unlock()
	outbox.change.signal()
}

func (outbox *fakeDeviceFactOutbox) ListPendingDeviceFacts(
	_ context.Context,
	limit int,
) ([]devices.PendingDeviceFact, error) {
	outbox.mutex.Lock()
	outbox.lists++
	if outbox.listFailures > 0 {
		outbox.listFailures--
		outbox.mutex.Unlock()
		return nil, errors.New("device fact outbox read failed")
	}
	if limit > len(outbox.pending) {
		limit = len(outbox.pending)
	}
	listed := make([]devices.PendingDeviceFact, limit)
	copy(listed, outbox.pending[:limit])
	outbox.mutex.Unlock()
	outbox.change.signal()
	return listed, nil
}

func (outbox *fakeDeviceFactOutbox) DeleteDeviceFact(_ context.Context, factID devices.DeviceFactID) error {
	outbox.mutex.Lock()
	if outbox.deleteFails > 0 {
		outbox.deleteFails--
		outbox.mutex.Unlock()
		return errors.New("device fact outbox delete failed")
	}
	for index, item := range outbox.pending {
		if pendingFactID(item.Fact) != factID {
			continue
		}
		outbox.pending = append(outbox.pending[:index], outbox.pending[index+1:]...)
		outbox.deletions = append(outbox.deletions, factID)
		outbox.mutex.Unlock()
		outbox.change.signal()
		return nil
	}
	outbox.mutex.Unlock()
	return nil
}

func (outbox *fakeDeviceFactOutbox) failLists(count int) {
	outbox.mutex.Lock()
	defer outbox.mutex.Unlock()
	outbox.listFailures = count
}

func (outbox *fakeDeviceFactOutbox) failDeletes(count int) {
	outbox.mutex.Lock()
	defer outbox.mutex.Unlock()
	outbox.deleteFails = count
}

func (outbox *fakeDeviceFactOutbox) listCount() int {
	outbox.mutex.Lock()
	defer outbox.mutex.Unlock()
	return outbox.lists
}

func (outbox *fakeDeviceFactOutbox) deletedIDs() []devices.DeviceFactID {
	outbox.mutex.Lock()
	defer outbox.mutex.Unlock()
	return append([]devices.DeviceFactID(nil), outbox.deletions...)
}

func (outbox *fakeDeviceFactOutbox) pendingIDs() []devices.DeviceFactID {
	outbox.mutex.Lock()
	defer outbox.mutex.Unlock()
	identities := make([]devices.DeviceFactID, 0, len(outbox.pending))
	for _, item := range outbox.pending {
		identities = append(identities, pendingFactID(item.Fact))
	}
	return identities
}

// recordingDeviceFactPublisher captures every publication and can fail, mark a
// duplicate, or block one publication until the test releases it.
type recordingDeviceFactPublisher struct {
	change    *deviceFactChange
	mutex     sync.Mutex
	attempts  int
	records   []recordedDeviceFactMessage
	failures  int
	failAll   bool
	duplicate bool
	ackStream string
	block     chan struct{}
	blockFor  int
}

type recordedDeviceFactMessage struct {
	subject        string
	messageID      string
	expectedStream string
	traceparent    string
	tracestate     string
	payload        []byte
}

func newRecordingDeviceFactPublisher(change *deviceFactChange) *recordingDeviceFactPublisher {
	return &recordingDeviceFactPublisher{change: change}
}

func (publisher *recordingDeviceFactPublisher) publish(
	ctx context.Context,
	message *natsgo.Msg,
) (*jetstream.PubAck, error) {
	publisher.mutex.Lock()
	publisher.attempts++
	publisher.records = append(publisher.records, recordedDeviceFactMessage{
		subject:        message.Subject,
		messageID:      message.Header.Get(natsgo.MsgIdHdr),
		expectedStream: message.Header.Get(natsgo.ExpectedStreamHdr),
		traceparent:    message.Header.Get(traceparentHeaderKey),
		tracestate:     message.Header.Get(tracestateHeaderKey),
		payload:        append([]byte(nil), message.Data...),
	})
	shouldFail := publisher.failAll || publisher.failures > 0
	if publisher.failures > 0 {
		publisher.failures--
	}
	block := publisher.block
	shouldBlock := publisher.blockFor != 0
	if publisher.blockFor > 0 {
		publisher.blockFor--
	}
	duplicate := publisher.duplicate
	stream := publisher.ackStream
	publisher.mutex.Unlock()
	publisher.change.signal()

	if shouldBlock {
		if block == nil {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if shouldFail {
		return nil, errors.New("device fact broker unavailable")
	}
	if stream == "" {
		stream = DeviceFactStreamName
	}
	return &jetstream.PubAck{Stream: stream, Duplicate: duplicate}, nil
}

func (publisher *recordingDeviceFactPublisher) failNext(count int) {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	publisher.failures = count
}

func (publisher *recordingDeviceFactPublisher) persistFailure(failing bool) {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	publisher.failAll = failing
}

func (publisher *recordingDeviceFactPublisher) reportDuplicates() {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	publisher.duplicate = true
}

func (publisher *recordingDeviceFactPublisher) acknowledgeInStream(name string) {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	publisher.ackStream = name
}

// holdAllPublications makes every publication wait on release until it is closed
// or its context ends. A nil release holds each publication until its context
// ends.
func (publisher *recordingDeviceFactPublisher) holdAllPublications(release chan struct{}) {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	publisher.block = release
	publisher.blockFor = -1
}

// holdNextPublications makes the next count publications wait on release until it
// is closed or their context ends. A nil release holds each of them until its
// context ends, so a test can stall exactly the first publication and let a later
// drain phase succeed.
func (publisher *recordingDeviceFactPublisher) holdNextPublications(count int, release chan struct{}) {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	publisher.block = release
	publisher.blockFor = count
}

func (publisher *recordingDeviceFactPublisher) attemptCount() int {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	return publisher.attempts
}

func (publisher *recordingDeviceFactPublisher) recorded() []recordedDeviceFactMessage {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	return append([]recordedDeviceFactMessage(nil), publisher.records...)
}

// observingDeviceFactOutbox decorates a real durable outbox so a test can wait
// for the relay to reach a durable state instead of polling the database.
type observingDeviceFactOutbox struct {
	devices.DeviceFactOutbox

	change *deviceFactChange
}

func (outbox *observingDeviceFactOutbox) ListPendingDeviceFacts(
	ctx context.Context,
	limit int,
) ([]devices.PendingDeviceFact, error) {
	facts, err := outbox.DeviceFactOutbox.ListPendingDeviceFacts(ctx, limit)
	outbox.change.signal()
	return facts, err
}

func (outbox *observingDeviceFactOutbox) DeleteDeviceFact(
	ctx context.Context,
	factID devices.DeviceFactID,
) error {
	err := outbox.DeviceFactOutbox.DeleteDeviceFact(ctx, factID)
	outbox.change.signal()
	return err
}

// openDeviceFactOutbox opens one migrated SQLite database and exposes its real
// Device Fact outbox, so a test can prove durability across relay restarts.
func openDeviceFactOutbox(
	t *testing.T,
	change *deviceFactChange,
) (*sql.DB, devices.DeviceFactOutbox) {
	t.Helper()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := devicessqlite.NewDeviceRepository(database, nil)
	return database, &observingDeviceFactOutbox{DeviceFactOutbox: repository, change: change}
}

func insertPendingObservationFact(t *testing.T, database *sql.DB, fact devices.ObservationFact) {
	t.Helper()
	if err := insertObservationDeviceFact(context.Background(), database, fact); err != nil {
		t.Fatalf("insert pending observation fact: %v", err)
	}
}

// insertObservationDeviceFact writes one pending Observation row directly, so a
// test can prove the durable outbox stays writable while a publication is in
// flight. It returns the raw error so a non-test goroutine can report it instead
// of failing from the wrong goroutine.
func insertObservationDeviceFact(
	ctx context.Context,
	database *sql.DB,
	fact devices.ObservationFact,
) error {
	var sourceUpdatedAt any
	var previousValue any
	if fact.SourceUpdatedAt != nil {
		sourceUpdatedAt = fact.SourceUpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	if fact.PreviousValue != nil {
		previousValue = string(fact.PreviousValue)
	}
	_, err := database.ExecContext(
		ctx,
		`INSERT INTO device_facts_outbox (
			fact_id, family, entity_id, variant, source_id, correlation_id, created_at,
			traceparent, tracestate, value_json, previous_value_json, adapter_received_at, source_updated_at, observed_at
		) VALUES (?, 'observation', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(fact.ID),
		string(fact.EntityID),
		string(fact.Disposition),
		string(fact.ObservationID),
		string(fact.CorrelationID),
		fact.CreatedAt.UTC().Format(time.RFC3339Nano),
		fact.Trace.Traceparent,
		fact.Trace.Tracestate,
		string(fact.Value),
		previousValue,
		fact.AdapterReceivedAt.UTC().Format(time.RFC3339Nano),
		sourceUpdatedAt,
		fact.ObservedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert observation device fact: %w", err)
	}
	return nil
}

func insertPendingEntityEventFact(t *testing.T, database *sql.DB, fact devices.EntityEventFact) {
	t.Helper()
	if _, err := database.ExecContext(
		context.Background(),
		`INSERT INTO device_facts_outbox (
			fact_id, family, entity_id, variant, source_id, correlation_id, created_at,
			traceparent, tracestate, reported_at, received_at, recorded_at
		) VALUES (?, 'entity-event', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(fact.ID),
		string(fact.EntityID),
		string(fact.Name),
		string(fact.EventID),
		string(fact.CorrelationID),
		fact.CreatedAt.UTC().Format(time.RFC3339Nano),
		fact.Trace.Traceparent,
		fact.Trace.Tracestate,
		fact.ReportedAt.UTC().Format(time.RFC3339Nano),
		fact.ReceivedAt.UTC().Format(time.RFC3339Nano),
		fact.RecordedAt.UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert pending entity event fact: %v", err)
	}
}

// corruptOutboxRowTimestamp makes one pending row permanently undecodable by
// replacing its commit time with a value no canonical timestamp parser accepts.
// Only a direct store edit can do this: the schema constrains every other column,
// and a corrupt timestamp is exactly the decode drift the relay must fault on
// instead of retrying forever.
func corruptOutboxRowTimestamp(t *testing.T, database *sql.DB, factID devices.DeviceFactID) {
	t.Helper()
	if _, err := database.ExecContext(
		context.Background(),
		`UPDATE device_facts_outbox SET created_at = 'not-a-timestamp' WHERE fact_id = ?`,
		string(factID),
	); err != nil {
		t.Fatalf("corrupt pending device fact timestamp: %v", err)
	}
}

func countPendingDeviceFacts(t *testing.T, database *sql.DB) int {
	t.Helper()
	var count int
	if err := database.QueryRowContext(
		context.Background(), `SELECT COUNT(*) FROM device_facts_outbox`,
	).Scan(&count); err != nil {
		t.Fatalf("count pending device facts: %v", err)
	}
	return count
}

// pendingFactIDs reads the durable outbox identities in enqueue order, so a test
// can prove exactly which rows a fault left blocked.
func pendingFactIDs(t *testing.T, database *sql.DB) []string {
	t.Helper()
	rows, err := database.QueryContext(
		context.Background(), `SELECT fact_id FROM device_facts_outbox ORDER BY enqueue_order`,
	)
	if err != nil {
		t.Fatalf("read pending device facts: %v", err)
	}
	defer rows.Close()
	identities := make([]string, 0, 1)
	for rows.Next() {
		var factID string
		if scanErr := rows.Scan(&factID); scanErr != nil {
			t.Fatal(scanErr)
		}
		identities = append(identities, factID)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatal(rowsErr)
	}
	return identities
}

func testDeviceFactValidator(t *testing.T) *contractsv1.Validator {
	t.Helper()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return validator
}

// discardLogger returns a logger that drops every record, for tests that observe
// relay state instead of diagnostics.
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func testDeviceFactRelayOptions() deviceFactRelayOptions {
	return deviceFactRelayOptions{
		batchSize:      DeviceFactRelayBatchSize,
		pollInterval:   time.Hour,
		retryBackoff:   10 * time.Millisecond,
		publishTimeout: 2 * time.Second,
	}
}

// startTestDeviceFactRelay starts one relay with production mapping and the
// injected publisher and timing, and always drains it before the test ends.
func startTestDeviceFactRelay(
	t *testing.T,
	outbox devices.DeviceFactOutbox,
	publish deviceFactPublisher,
	options deviceFactRelayOptions,
) *DeviceFactRelay {
	t.Helper()
	relay, err := startDeviceFactRelay(
		outbox, testDeviceFactValidator(t), discardLogger(), publish, options,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		drainContext, cancelDrain := context.WithTimeout(context.Background(), testFactLiveness)
		defer cancelDrain()
		_ = relay.Drain(drainContext)
	})
	return relay
}

func jsDeviceFactPublisher(js jetstream.JetStream) deviceFactPublisher {
	return func(ctx context.Context, message *natsgo.Msg) (*jetstream.PubAck, error) {
		return js.PublishMsg(ctx, message)
	}
}

func mustDeviceFactID(t *testing.T) devices.DeviceFactID {
	t.Helper()
	factID, err := devices.NewDeviceFactID()
	if err != nil {
		t.Fatal(err)
	}
	return factID
}

func mustEntityID(t *testing.T) devices.EntityID {
	t.Helper()
	entityID, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	return entityID
}

func mustObservationID(t *testing.T) devices.ObservationID {
	t.Helper()
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	return observationID
}

func mustEntityEventID(t *testing.T) devices.EntityEventID {
	t.Helper()
	eventID, err := devices.NewEntityEventID()
	if err != nil {
		t.Fatal(err)
	}
	return eventID
}

func mustCorrelationID(t *testing.T) devices.CorrelationID {
	t.Helper()
	correlationID, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	return correlationID
}

// testObservationFact builds one canonical pending Observation fact with stable
// committed timestamps and a carry-over trace context, so a test can assert the
// exact wire bytes the relay must reproduce on every retry.
func testObservationFact(
	t *testing.T,
	entityID devices.EntityID,
	observedAt time.Time,
	value string,
	disposition devices.ObservationDisposition,
) devices.ObservationFact {
	t.Helper()
	sourceUpdatedAt := observedAt.Add(-3 * time.Second)
	return devices.ObservationFact{
		ID:                mustDeviceFactID(t),
		ObservationID:     mustObservationID(t),
		EntityID:          entityID,
		Disposition:       disposition,
		Value:             devices.Value(value),
		PreviousValue:     devices.Value(`null`),
		CorrelationID:     mustCorrelationID(t),
		AdapterReceivedAt: observedAt.Add(-2 * time.Second),
		SourceUpdatedAt:   &sourceUpdatedAt,
		ObservedAt:        observedAt,
		CreatedAt:         observedAt.Add(2 * time.Second),
		Trace: devices.DeviceFactTraceContext{
			Traceparent: testFactTraceparent,
			Tracestate:  testFactTracestate,
		},
	}
}

// testEntityEventFact builds one canonical pending Entity Event fact.
func testEntityEventFact(
	t *testing.T,
	entityID devices.EntityID,
	name devices.EntityEventName,
	reportedAt time.Time,
) devices.EntityEventFact {
	t.Helper()
	return devices.EntityEventFact{
		ID:            mustDeviceFactID(t),
		EventID:       mustEntityEventID(t),
		EntityID:      entityID,
		Name:          name,
		CorrelationID: mustCorrelationID(t),
		ReportedAt:    reportedAt,
		ReceivedAt:    reportedAt.Add(time.Second),
		RecordedAt:    reportedAt.Add(2 * time.Second),
		CreatedAt:     reportedAt.Add(2 * time.Second),
		Trace: devices.DeviceFactTraceContext{
			Traceparent: testFactTraceparent,
			Tracestate:  testFactTracestate,
		},
	}
}

func pendingFact(sequence int64, fact devices.DeviceFact) devices.PendingDeviceFact {
	return devices.PendingDeviceFact{Sequence: sequence, Fact: fact}
}

// pendingFactID reads the stable identity of one typed pending fact. Production
// mapping reads the same field from the concrete family.
func pendingFactID(fact devices.DeviceFact) devices.DeviceFactID {
	switch typed := fact.(type) {
	case devices.ObservationFact:
		return typed.ID
	case devices.EntityEventFact:
		return typed.ID
	}
	return ""
}

// decodeTestFactEnvelope validates one published payload against its family
// schema and returns the strict envelope with the family data left raw, so an
// assertion proves both the envelope and the family payload are schema-valid.
func decodeTestFactEnvelope(
	t *testing.T,
	validator *contractsv1.Validator,
	schemaID string,
	payload []byte,
) natswire.Envelope[json.RawMessage] {
	t.Helper()
	envelope, err := natswire.Decode[json.RawMessage](validator, schemaID, payload)
	if err != nil {
		t.Fatalf("decode device fact envelope: %v", err)
	}
	return envelope
}
