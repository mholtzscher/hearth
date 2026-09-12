package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// DeviceFactDispatcherName is the client name of the dedicated Device Fact
// publication connection, so broker-side connection listings distinguish it
// from the shared Core ingest/request connection.
const DeviceFactDispatcherName = "hearthd-device-facts"

// DeviceFactReconnectWait is the delay between reconnection attempts on the
// dedicated fact connection. It matches the shared Core connection's wait so
// both recover on the same cadence.
const DeviceFactReconnectWait = 250 * time.Millisecond

const (
	// CoreNATSWriteTimeout bounds one socket write on both Core NATS connections:
	// the shared ingest/request connection and the dedicated Device Fact
	// publication connection. Pinned nats.go v1.53.1 holds a connection's mutex
	// across a socket write (natsWriter.flush is called from Conn.publish, from
	// the flusher goroutine and from the ping timer, all under Conn.mu) and its
	// default FlusherTimeout is one minute. An unread peer would therefore hold
	// Conn.mu for up to a minute, and every synchronous Conn call and every
	// Conn.Close that needs that mutex would block behind it. That includes the
	// dispatcher worker, whose per-fact freshness check reads IsConnected and
	// Stats on the shared connection: a stalled shared connection would keep
	// Drain past its five-second budget even after the dedicated connection is
	// closed, because closing one connection cannot release the other's mutex.
	// A bounded write turns a stalled socket into a failed write instead: the
	// dedicated connection drops the fact, which is already its contract, while
	// the shared connection keeps nats.go's reconnect buffering, reconnect policy
	// and retry cadence — only the write deadline differs.
	CoreNATSWriteTimeout = time.Second
	// DeviceFactMaxMessageBytes bounds one encoded Device Fact. A larger fact
	// is an internal defect and is dropped instead of published.
	DeviceFactMaxMessageBytes = 64 * 1024
	// DeviceFactPendingMessages bounds the dispatcher queue by message count.
	DeviceFactPendingMessages = 256
	// DeviceFactPendingBytes bounds the dispatcher queue by admitted bytes,
	// including the one message the worker may be publishing.
	DeviceFactPendingBytes = 4 * 1024 * 1024
)

// Device Fact suppression reasons reported at Debug by device_fact.suppressed.
const (
	deviceFactReasonNotLive          = "not_live"
	deviceFactReasonGenerationChange = "generation_changed"
	deviceFactReasonBeforeEpoch      = "before_epoch"
)

// Device Fact publication failure stages reported by device_fact.not_published.
const (
	deviceFactStageIdentify = "identify"
	deviceFactStageClock    = "clock"
	deviceFactStageMap      = "map"
	deviceFactStageEncode   = "encode"
	deviceFactStageSize     = "size"
	deviceFactStageEnqueue  = "enqueue"
	deviceFactStagePublish  = "publish"
)

// Fixed publication error codes. Each distinct failure mode has one code so a
// search for it finds both this file and its operator documentation, and so no
// diagnostic ever depends on raw upstream error text.
const (
	deviceFactCodeIDFailed       = "fact_id_failed"
	deviceFactCodeClockZero      = "clock_zero"
	deviceFactCodeInvalid        = "fact_invalid"
	deviceFactCodeEncodeFailed   = "fact_encode_failed"
	deviceFactCodeTooLarge       = "fact_too_large"
	deviceFactCodeQueueFull      = "fact_queue_full"
	deviceFactCodeAdmissionShut  = "fact_admission_closed"
	deviceFactCodeUnavailable    = "fact_connection_unavailable"
	deviceFactCodeReconnectLimit = "fact_reconnect_buffer_exceeded"
	deviceFactCodeClosed         = "fact_connection_closed"
	deviceFactCodePublishFailed  = "fact_publish_failed"
)

// DeviceFactConnectionOptions returns the dedicated Device Fact publication
// connection options: a distinct client name, unlimited reconnects,
// ReconnectBufSize(-1), so a publication attempted while the connection is
// reconnecting fails immediately instead of being delivered after reconnect,
// and FlusherTimeout(CoreNATSWriteTimeout), so no socket write can hold the
// connection's mutex long enough to stall Conn.Close. The shared Core
// connection keeps nats.go's default buffering and applies the same write
// bound; callers append their own diagnostics options to this base.
func DeviceFactConnectionOptions() []natsgo.Option {
	return []natsgo.Option{
		natsgo.Name(DeviceFactDispatcherName),
		natsgo.MaxReconnects(-1),
		natsgo.ReconnectWait(DeviceFactReconnectWait),
		natsgo.ReconnectBufSize(-1),
		natsgo.FlusherTimeout(CoreNATSWriteTimeout),
	}
}

// observationFactData is the strict external Observation fact payload. It
// carries canonical committed data only: no Adapter or runtime identity, and no
// rejected or duplicate input.
type observationFactData struct {
	ObservationID     string          `json:"observation_id"`
	EntityID          string          `json:"entity_id"`
	Disposition       string          `json:"disposition"`
	Value             json.RawMessage `json:"value"`
	AdapterReceivedAt string          `json:"adapter_received_at"`
	SourceUpdatedAt   *string         `json:"source_updated_at,omitempty"`
	ObservedAt        string          `json:"observed_at"`
}

// entityEventFactData is the strict external Entity Event fact payload.
type entityEventFactData struct {
	EventID    string `json:"event_id"`
	EntityID   string `json:"entity_id"`
	Name       string `json:"name"`
	ReportedAt string `json:"reported_at"`
	ReceivedAt string `json:"received_at"`
	RecordedAt string `json:"recorded_at"`
}

// commandFactData is the strict external Command transition payload. It mirrors
// the durable commands table invariants and omits Adapter and runtime identity.
type commandFactData struct {
	CommandID            string          `json:"command_id"`
	EntityID             string          `json:"entity_id"`
	Operation            string          `json:"operation"`
	Parameters           json.RawMessage `json:"parameters"`
	Status               string          `json:"status"`
	RequestedAt          string          `json:"requested_at"`
	DeadlineAt           string          `json:"deadline_at"`
	AcceptedAt           *string         `json:"accepted_at,omitempty"`
	CompletedAt          *string         `json:"completed_at,omitempty"`
	FailureCode          *string         `json:"failure_code,omitempty"`
	OutcomeObservationID *string         `json:"outcome_observation_id,omitempty"`
}

// queuedDeviceFact is one mapped, encoded, validated Device Fact waiting for
// the single publication worker. It captures the generations observed at
// enqueue time so the worker can detect a connection generation change and
// drop instead of publishing stale work.
type queuedDeviceFact struct {
	family            natswire.DeviceFactFamily
	sourceID          string
	subject           string
	payload           []byte
	headers           natsgo.Header
	ingestGeneration  NATSConnectionGeneration
	publishGeneration NATSConnectionGeneration
	receiveAt         time.Time
}

// DeviceFactDispatcher is the bounded, best-effort Core NATS Device Fact
// publisher. It implements devices.DeviceFactSink: each sink method performs
// bounded CPU work on the caller, never calls a nats.Conn method, never blocks
// on network I/O and never returns an error to devices. One worker publishes
// eligible queued facts FIFO at most once each.
//
// The queue isolates committed device work from socket write latency; it is not
// a recovery buffer. Nothing queued survives a dispatcher restart, a Core
// restart or either connection generation change, and publication never
// retries or flushes.
type DeviceFactDispatcher struct {
	connection *natsgo.Conn
	validator  *contractsv1.Validator
	epochs     *DeviceFactEpochs
	logger     *slog.Logger
	now        func() time.Time
	newFactID  func() (devices.DeviceFactID, error)
	publish    func(subject string, headers natsgo.Header, payload []byte) error

	queue      chan queuedDeviceFact
	progress   chan struct{}
	stopWorker chan struct{}
	stopped    chan struct{}
	stopOnce   sync.Once

	admissionOpen atomic.Bool

	mutex        sync.Mutex
	pendingBytes int
	pending      int
}

// StartDeviceFactDispatcher validates its dependencies and starts the single
// publication worker. A nil connection, validator or epoch fence fails before
// any goroutine starts, so a partially assembled dispatcher is never returned.
func StartDeviceFactDispatcher(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	epochs *DeviceFactEpochs,
	logger *slog.Logger,
) (*DeviceFactDispatcher, error) {
	if connection == nil {
		return nil, errors.New("device fact dispatcher requires a publication connection")
	}
	return startDeviceFactDispatcher(connection, validator, epochs, logger, func(
		subject string,
		headers natsgo.Header,
		payload []byte,
	) error {
		return connection.PublishMsg(&natsgo.Msg{Subject: subject, Header: headers, Data: payload})
	})
}

// startDeviceFactDispatcher is the single dispatcher constructor. The publish
// seam is injected so in-package tests can observe or stall one publication
// without a real socket, and production always supplies exactly one plain
// PublishMsg call per queued fact.
func startDeviceFactDispatcher(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	epochs *DeviceFactEpochs,
	logger *slog.Logger,
	publish func(subject string, headers natsgo.Header, payload []byte) error,
) (*DeviceFactDispatcher, error) {
	if connection == nil {
		return nil, errors.New("device fact dispatcher requires a publication connection")
	}
	if validator == nil {
		return nil, errors.New("device fact dispatcher requires a compiled schema validator")
	}
	if epochs == nil {
		return nil, errors.New("device fact dispatcher requires connection epochs")
	}
	if publish == nil {
		return nil, errors.New("device fact dispatcher requires a publication function")
	}
	dispatcher := &DeviceFactDispatcher{
		connection: connection,
		validator:  validator,
		epochs:     epochs,
		logger:     defaultLogger(logger),
		now:        time.Now,
		newFactID:  devices.NewDeviceFactID,
		publish:    publish,
		queue:      make(chan queuedDeviceFact, DeviceFactPendingMessages),
		progress:   make(chan struct{}, 1),
		stopWorker: make(chan struct{}),
		stopped:    make(chan struct{}),
	}
	dispatcher.admissionOpen.Store(true)
	go dispatcher.run()
	return dispatcher, nil
}

// Active reports whether the dispatcher still accepts and publishes facts. It
// clears as soon as admission stops, so readiness fails at the start of
// shutdown instead of after it finishes. It never proves a subscriber exists.
func (dispatcher *DeviceFactDispatcher) Active() bool {
	return dispatcher != nil && dispatcher.admissionOpen.Load()
}

// StopAdmission rejects new work. Already queued work stays eligible until
// Drain finishes, so facts committed before shutdown still reach live
// subscribers while durable consumers drain.
func (dispatcher *DeviceFactDispatcher) StopAdmission() {
	if dispatcher == nil {
		return
	}
	dispatcher.admissionOpen.Store(false)
}

// Drain stops admission and publishes currently eligible queued work until the
// queue is empty or ctx expires. On expiry it closes the dedicated publication
// connection to unblock a stalled write, discards the remaining queue and joins
// the worker before returning ctx's error. Drain is idempotent and always
// returns, and its abort is bounded by CoreNATSWriteTimeout rather than by
// nats.go's one-minute default: the write deadline releases the connection
// mutex that a stalled publication holds, so the drain cannot exceed its ctx
// deadline by more than the couple of bounded writes that were already in
// flight. Closing the dedicated connection alone cannot release the shared
// connection's mutex, which the worker's freshness check also waits on, so the
// shared connection carries the same bounded write deadline.
func (dispatcher *DeviceFactDispatcher) Drain(ctx context.Context) error {
	if dispatcher == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dispatcher.StopAdmission()
	for {
		if dispatcher.outstanding() == 0 {
			dispatcher.join()
			return nil
		}
		select {
		case <-ctx.Done():
			dispatcher.abort()
			return fmt.Errorf("drain device fact dispatcher: %w", ctx.Err())
		case <-dispatcher.progress:
		case <-dispatcher.stopped:
			return nil
		}
	}
}

// Closed reports worker termination. It closes after the worker returns, so a
// caller that joined Drain knows no publication goroutine remains.
func (dispatcher *DeviceFactDispatcher) Closed() <-chan struct{} {
	if dispatcher == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return dispatcher.stopped
}

// ObservationAccepted enqueues one accepted Observation fact. Only applied and
// unchanged Observations are facts; the mapper itself rejects any other
// disposition.
func (dispatcher *DeviceFactDispatcher) ObservationAccepted(ctx context.Context, fact devices.ObservationFact) {
	const family = natswire.DeviceFactFamilyObservation
	sourceID := string(fact.ObservationID)
	snapshot := dispatcher.epochs.Snapshot()
	if !dispatcher.cachedEligible(ctx, family, sourceID, snapshot, fact.ObservedAt) {
		return
	}
	if fact.ObservationID == "" || fact.EntityID == "" ||
		fact.AdapterReceivedAt.IsZero() || fact.ObservedAt.IsZero() {
		dispatcher.logNotPublished(
			ctx, family, sourceID, deviceFactStageMap, deviceFactCodeInvalid, slog.LevelError,
		)
		return
	}
	factID, emittedAt, ok := dispatcher.identify(ctx, family, sourceID)
	if !ok {
		return
	}
	subject, subjectErr := natswire.ObservationFactSubject(string(fact.EntityID), string(fact.Disposition))
	if subjectErr != nil {
		dispatcher.logNotPublished(
			ctx, family, sourceID, deviceFactStageMap, deviceFactCodeInvalid, slog.LevelError,
		)
		return
	}
	data := observationFactData{
		ObservationID:     string(fact.ObservationID),
		EntityID:          string(fact.EntityID),
		Disposition:       string(fact.Disposition),
		Value:             json.RawMessage(fact.Value),
		AdapterReceivedAt: formatFactTime(fact.AdapterReceivedAt),
		ObservedAt:        formatFactTime(fact.ObservedAt),
	}
	if fact.SourceUpdatedAt != nil {
		formatted := formatFactTime(*fact.SourceUpdatedAt)
		data.SourceUpdatedAt = &formatted
	}
	payload, encodeErr := natswire.Encode(
		dispatcher.validator,
		contractsv1.ObservationFactSchemaID,
		natswire.Envelope[observationFactData]{
			ID:            factID,
			Schema:        contractsv1.ObservationFactSchemaID,
			EmittedAt:     formatFactTime(emittedAt),
			CorrelationID: string(fact.CorrelationID),
			CausationID:   new(string(fact.ObservationID)),
			Data:          data,
		},
	)
	if encodeErr != nil {
		dispatcher.logNotPublished(
			ctx, family, sourceID, deviceFactStageEncode, deviceFactCodeEncodeFailed, slog.LevelError,
		)
		return
	}
	dispatcher.offer(ctx, family, sourceID, snapshot, fact.ObservedAt, subject, payload)
}

// EntityEventAccepted enqueues one first-seen accepted Entity Event fact.
func (dispatcher *DeviceFactDispatcher) EntityEventAccepted(ctx context.Context, fact devices.EntityEventFact) {
	const family = natswire.DeviceFactFamilyEntityEvent
	sourceID := string(fact.EventID)
	snapshot := dispatcher.epochs.Snapshot()
	if !dispatcher.cachedEligible(ctx, family, sourceID, snapshot, fact.ReceivedAt) {
		return
	}
	if fact.EventID == "" || fact.EntityID == "" || fact.ReportedAt.IsZero() ||
		fact.ReceivedAt.IsZero() || fact.RecordedAt.IsZero() {
		dispatcher.logNotPublished(
			ctx, family, sourceID, deviceFactStageMap, deviceFactCodeInvalid, slog.LevelError,
		)
		return
	}
	factID, emittedAt, ok := dispatcher.identify(ctx, family, sourceID)
	if !ok {
		return
	}
	subject, subjectErr := natswire.EntityEventFactSubject(string(fact.EntityID), string(fact.Name))
	if subjectErr != nil {
		dispatcher.logNotPublished(
			ctx, family, sourceID, deviceFactStageMap, deviceFactCodeInvalid, slog.LevelError,
		)
		return
	}
	payload, encodeErr := natswire.Encode(
		dispatcher.validator,
		contractsv1.EntityEventFactSchemaID,
		natswire.Envelope[entityEventFactData]{
			ID:            factID,
			Schema:        contractsv1.EntityEventFactSchemaID,
			EmittedAt:     formatFactTime(emittedAt),
			CorrelationID: string(fact.CorrelationID),
			CausationID:   new(string(fact.EventID)),
			Data: entityEventFactData{
				EventID:    string(fact.EventID),
				EntityID:   string(fact.EntityID),
				Name:       string(fact.Name),
				ReportedAt: formatFactTime(fact.ReportedAt),
				ReceivedAt: formatFactTime(fact.ReceivedAt),
				RecordedAt: formatFactTime(fact.RecordedAt),
			},
		},
	)
	if encodeErr != nil {
		dispatcher.logNotPublished(
			ctx, family, sourceID, deviceFactStageEncode, deviceFactCodeEncodeFailed, slog.LevelError,
		)
		return
	}
	dispatcher.offer(ctx, family, sourceID, snapshot, fact.ReceivedAt, subject, payload)
}

// CommandTransitioned enqueues one durable Command status transition fact.
// Command transitions originate inside Core, so they carry no JetStream receive
// time and are gated only by the combined connection window.
func (dispatcher *DeviceFactDispatcher) CommandTransitioned(ctx context.Context, fact devices.CommandFact) {
	const family = natswire.DeviceFactFamilyCommand
	record := fact.Record
	sourceID := string(record.ID)
	snapshot := dispatcher.epochs.Snapshot()
	if !dispatcher.cachedEligible(ctx, family, sourceID, snapshot, time.Time{}) {
		return
	}
	if record.ID == "" || record.EntityID == "" || record.RequestedAt.IsZero() || record.DeadlineAt.IsZero() {
		dispatcher.logNotPublished(
			ctx, family, sourceID, deviceFactStageMap, deviceFactCodeInvalid, slog.LevelError,
		)
		return
	}
	factID, emittedAt, ok := dispatcher.identify(ctx, family, sourceID)
	if !ok {
		return
	}
	subject, subjectErr := natswire.CommandFactSubject(string(record.EntityID), string(record.Status))
	if subjectErr != nil {
		dispatcher.logNotPublished(
			ctx, family, sourceID, deviceFactStageMap, deviceFactCodeInvalid, slog.LevelError,
		)
		return
	}
	payload, encodeErr := natswire.Encode(
		dispatcher.validator,
		contractsv1.CommandFactSchemaID,
		natswire.Envelope[commandFactData]{
			ID:            factID,
			Schema:        contractsv1.CommandFactSchemaID,
			EmittedAt:     formatFactTime(emittedAt),
			CorrelationID: string(record.CorrelationID),
			CausationID:   new(string(record.ID)),
			Data: commandFactData{
				CommandID:            string(record.ID),
				EntityID:             string(record.EntityID),
				Operation:            string(record.OperationName),
				Parameters:           json.RawMessage(record.Parameters),
				Status:               string(record.Status),
				RequestedAt:          formatFactTime(record.RequestedAt),
				DeadlineAt:           formatFactTime(record.DeadlineAt),
				AcceptedAt:           formatOptionalFactTime(record.AcceptedAt),
				CompletedAt:          formatOptionalFactTime(record.CompletedAt),
				FailureCode:          formatOptionalFailureCode(record.FailureCode),
				OutcomeObservationID: formatOptionalObservationID(record.OutcomeObservationID),
			},
		},
	)
	if encodeErr != nil {
		dispatcher.logNotPublished(
			ctx, family, sourceID, deviceFactStageEncode, deviceFactCodeEncodeFailed, slog.LevelError,
		)
		return
	}
	dispatcher.offer(ctx, family, sourceID, snapshot, time.Time{}, subject, payload)
}

// identify mints the publication identity and Core publication time. Failure
// leaves the fact unpublished and produces one fixed diagnostic.
func (dispatcher *DeviceFactDispatcher) identify(
	ctx context.Context,
	family natswire.DeviceFactFamily,
	sourceID string,
) (string, time.Time, bool) {
	factID, idErr := dispatcher.newFactID()
	if idErr != nil {
		dispatcher.logNotPublished(
			ctx, family, sourceID, deviceFactStageIdentify, deviceFactCodeIDFailed, slog.LevelError,
		)
		return "", time.Time{}, false
	}
	emittedAt := dispatcher.now().UTC()
	if emittedAt.IsZero() {
		dispatcher.logNotPublished(
			ctx, family, sourceID, deviceFactStageClock, deviceFactCodeClockZero, slog.LevelError,
		)
		return "", time.Time{}, false
	}
	return string(factID), emittedAt, true
}

// offer size-checks, injects the originating W3C trace and enqueues one encoded
// fact. It never calls the publication connection.
func (dispatcher *DeviceFactDispatcher) offer(
	ctx context.Context,
	family natswire.DeviceFactFamily,
	sourceID string,
	snapshot DeviceFactEpochSnapshot,
	receiveAt time.Time,
	subject string,
	payload []byte,
) {
	if len(payload) > DeviceFactMaxMessageBytes {
		dispatcher.logNotPublished(
			ctx, family, sourceID, deviceFactStageSize, deviceFactCodeTooLarge, slog.LevelError,
		)
		return
	}
	headers := make(natsgo.Header)
	natswire.InjectTrace(ctx, headers)
	dispatcher.enqueue(ctx, queuedDeviceFact{
		family:            family,
		sourceID:          sourceID,
		subject:           subject,
		payload:           payload,
		headers:           headers,
		ingestGeneration:  snapshot.IngestGeneration,
		publishGeneration: snapshot.PublishGeneration,
		receiveAt:         receiveAt,
	})
}

// cachedEligible applies the enqueue-stage fast filter from the cached epoch
// snapshot alone. A report stored before the cached live window is suppressed
// here; the worker stage revalidates against the actual connections.
func (dispatcher *DeviceFactDispatcher) cachedEligible(
	ctx context.Context,
	family natswire.DeviceFactFamily,
	sourceID string,
	snapshot DeviceFactEpochSnapshot,
	receiveAt time.Time,
) bool {
	if !snapshot.Live {
		dispatcher.logSuppressed(ctx, family, sourceID, deviceFactReasonNotLive)
		return false
	}
	if !receiveAt.IsZero() && receiveAt.Before(snapshot.LiveSince) {
		dispatcher.logSuppressed(ctx, family, sourceID, deviceFactReasonBeforeEpoch,
			slog.Time("received_at", receiveAt),
			slog.Time("live_since", snapshot.LiveSince),
		)
		return false
	}
	return true
}

// enqueue admits one fact without waiting. It drops and diagnoses when
// admission is closed, when the queue is full, or when either bound would be
// exceeded. It holds the accounting mutex only for in-memory bookkeeping.
func (dispatcher *DeviceFactDispatcher) enqueue(ctx context.Context, item queuedDeviceFact) {
	if !dispatcher.admissionOpen.Load() {
		dispatcher.logNotPublishedWith(
			ctx, item.family, item.sourceID, deviceFactStageEnqueue, deviceFactCodeAdmissionShut, slog.LevelWarn,
			nil,
		)
		return
	}
	size := len(item.payload)
	dispatcher.mutex.Lock()
	if dispatcher.pendingBytes+size > DeviceFactPendingBytes {
		pendingBytes := dispatcher.pendingBytes
		pendingMessages := len(dispatcher.queue)
		dispatcher.mutex.Unlock()
		dispatcher.logQueueFull(ctx, item.family, item.sourceID, pendingMessages, pendingBytes)
		return
	}
	select {
	case dispatcher.queue <- item:
		dispatcher.pendingBytes += size
		dispatcher.pending++
		dispatcher.mutex.Unlock()
	default:
		pendingBytes := dispatcher.pendingBytes
		pendingMessages := len(dispatcher.queue)
		dispatcher.mutex.Unlock()
		dispatcher.logQueueFull(ctx, item.family, item.sourceID, pendingMessages, pendingBytes)
	}
}

// run is the single publication worker. It preserves enqueue order for the
// facts it publishes and exits only when it is stopped.
func (dispatcher *DeviceFactDispatcher) run() {
	defer close(dispatcher.stopped)
	for {
		select {
		case item := <-dispatcher.queue:
			dispatcher.publishQueued(item)
		case <-dispatcher.stopWorker:
			return
		}
	}
}

// publishQueued revalidates one queued fact against the live connections and
// publishes it at most once. It holds no epoch mutex across the publication and
// never retries.
//
// The eligibility check is the last work before the single publish call, as the
// contract requires. A reconnect that completes inside the remaining interval
// between that check and Conn.PublishMsg cannot be eliminated: pinned nats.go
// v1.53.1 exposes no check-and-publish primitive, and the reconnect transition
// and Conn.publish serialize only on Conn.mu, which this package cannot take.
// The residual window is bounded by that same mutex handoff: a publication that
// arrives while the connection is still reconnecting fails with
// ErrReconnectBufExceeded (ReconnectBufSize(-1)) and is dropped, and only a
// publication that arrives after the reconnect has already completed can reach
// the new generation.
func (dispatcher *DeviceFactDispatcher) publishQueued(item queuedDeviceFact) {
	defer dispatcher.releaseItem(len(item.payload))
	// Continue the originating trace in worker diagnostics; the queued item
	// carries only injected headers, never the caller's context.
	ctx := natswire.ExtractTrace(context.Background(), item.headers)
	liveSince, live := dispatcher.epochs.LiveSince(item.ingestGeneration, item.publishGeneration)
	if !live {
		reason := deviceFactReasonGenerationChange
		if !dispatcher.epochs.Snapshot().Live {
			reason = deviceFactReasonNotLive
		}
		dispatcher.logSuppressed(ctx, item.family, item.sourceID, reason)
		return
	}
	if !item.receiveAt.IsZero() && item.receiveAt.Before(liveSince) {
		dispatcher.logSuppressed(ctx, item.family, item.sourceID, deviceFactReasonBeforeEpoch,
			slog.Time("received_at", item.receiveAt),
			slog.Time("live_since", liveSince),
		)
		return
	}
	if publishErr := dispatcher.publish(item.subject, item.headers, item.payload); publishErr != nil {
		dispatcher.logPublishFailure(ctx, item, publishErr)
	}
}

// releaseItem returns one finished fact's accounting and wakes Drain. It is
// called for dropped and published facts alike.
func (dispatcher *DeviceFactDispatcher) releaseItem(size int) {
	dispatcher.mutex.Lock()
	dispatcher.subtractLocked(size)
	dispatcher.mutex.Unlock()
	select {
	case dispatcher.progress <- struct{}{}:
	default:
	}
}

// outstanding reports how many admitted facts are queued or in flight.
func (dispatcher *DeviceFactDispatcher) outstanding() int {
	dispatcher.mutex.Lock()
	defer dispatcher.mutex.Unlock()
	return dispatcher.pending
}

// join stops the worker and waits for it to exit.
func (dispatcher *DeviceFactDispatcher) join() {
	dispatcher.stopOnce.Do(func() { close(dispatcher.stopWorker) })
	<-dispatcher.stopped
}

// abort abandons a stalled drain: it stops the worker, discards the remaining
// queue, closes the dedicated publication connection to release a publication
// blocked on the connection's own lock, and joins the worker. The worker is
// stopped and the queue discarded before the close, because a worker still
// holding items can repeatedly re-enter a blocked socket write and starve the
// close of the connection mutex. Afterwards the close and the worker's own
// reads of the shared connection wait only for the one or two writes that were
// already in flight, each bounded by CoreNATSWriteTimeout, so this join cannot
// outlast a couple of bounded writes instead of nats.go's one-minute default.
func (dispatcher *DeviceFactDispatcher) abort() {
	dispatcher.stopOnce.Do(func() { close(dispatcher.stopWorker) })
	dispatcher.discardQueued()
	dispatcher.connection.Close()
	<-dispatcher.stopped
}

// discardQueued drops every still-queued fact, recording no fact. A fact that
// never reaches the connection is lost permanently by contract.
func (dispatcher *DeviceFactDispatcher) discardQueued() {
	for {
		select {
		case item := <-dispatcher.queue:
			dispatcher.releaseItemDiscarded(len(item.payload))
		default:
			return
		}
	}
}

// releaseItemDiscarded removes one discarded fact's accounting without waking
// Drain, which has already returned.
func (dispatcher *DeviceFactDispatcher) releaseItemDiscarded(size int) {
	dispatcher.mutex.Lock()
	defer dispatcher.mutex.Unlock()
	dispatcher.subtractLocked(size)
}

// subtractLocked removes one finished or discarded fact from the bounded
// accounting, clamped so no path can underflow the counters.
func (dispatcher *DeviceFactDispatcher) subtractLocked(size int) {
	if dispatcher.pendingBytes >= size {
		dispatcher.pendingBytes -= size
	} else {
		dispatcher.pendingBytes = 0
	}
	if dispatcher.pending > 0 {
		dispatcher.pending--
	}
}

// logSuppressed records one Debug freshness suppression with the family, a safe
// source ID and the reason. A receive time before the drop is the clock-skew
// diagnostic, so it is retained as an attribute.
func (dispatcher *DeviceFactDispatcher) logSuppressed(
	ctx context.Context,
	family natswire.DeviceFactFamily,
	sourceID string,
	reason string,
	extra ...slog.Attr,
) {
	attributes := []slog.Attr{
		slog.String(transportEventKey, "device_fact.suppressed"),
		slog.String("family", string(family)),
		deviceFactSourceIDAttr(family, sourceID),
		slog.String("reason", reason),
	}
	attributes = append(attributes, extra...)
	dispatcher.logger.LogAttrs(ctx, slog.LevelDebug, "device fact suppressed", attributes...)
}

// logNotPublished records one publication failure with its stage and fixed
// error code. Payloads, subjects, State values and Command parameters are never
// recorded.
func (dispatcher *DeviceFactDispatcher) logNotPublished(
	ctx context.Context,
	family natswire.DeviceFactFamily,
	sourceID string,
	stage string,
	errorCode string,
	level slog.Level,
) {
	dispatcher.logNotPublishedWith(ctx, family, sourceID, stage, errorCode, level, nil)
}

// logNotPublishedWith is logNotPublished with additional safe attributes.
func (dispatcher *DeviceFactDispatcher) logNotPublishedWith(
	ctx context.Context,
	family natswire.DeviceFactFamily,
	sourceID string,
	stage string,
	errorCode string,
	level slog.Level,
	extra []slog.Attr,
) {
	attributes := []slog.Attr{
		slog.String(transportEventKey, "device_fact.not_published"),
		slog.String("family", string(family)),
		deviceFactSourceIDAttr(family, sourceID),
		slog.String("stage", stage),
		slog.String(transportErrorCodeKey, errorCode),
	}
	attributes = append(attributes, extra...)
	dispatcher.logger.LogAttrs(ctx, level, "device fact not published", attributes...)
}

// logQueueFull records a capacity drop with the current bounded counts.
func (dispatcher *DeviceFactDispatcher) logQueueFull(
	ctx context.Context,
	family natswire.DeviceFactFamily,
	sourceID string,
	pendingMessages int,
	pendingBytes int,
) {
	dispatcher.logNotPublishedWith(
		ctx, family, sourceID, deviceFactStageEnqueue, deviceFactCodeQueueFull, slog.LevelWarn,
		[]slog.Attr{
			slog.Int("pending_messages", pendingMessages),
			slog.Int("pending_bytes", pendingBytes),
		},
	)
}

// logPublishFailure records the one safe diagnostic for a refused publication.
// A reconnecting or closed connection is an expected loss, not an internal
// defect.
func (dispatcher *DeviceFactDispatcher) logPublishFailure(
	ctx context.Context,
	item queuedDeviceFact,
	publishErr error,
) {
	errorCode := deviceFactCodePublishFailed
	level := slog.LevelError
	switch {
	case errors.Is(publishErr, natsgo.ErrReconnectBufExceeded):
		errorCode = deviceFactCodeReconnectLimit
		level = slog.LevelWarn
	case errors.Is(publishErr, natsgo.ErrConnectionClosed),
		errors.Is(publishErr, natsgo.ErrConnectionDraining):
		errorCode = deviceFactCodeClosed
		level = slog.LevelWarn
	}
	dispatcher.logNotPublished(ctx, item.family, item.sourceID, deviceFactStagePublish, errorCode, level)
}

// deviceFactSourceIDAttr returns the source identity attribute for one family
// so a diagnostic carries a searchable, safe identity and never a full subject.
func deviceFactSourceIDAttr(family natswire.DeviceFactFamily, sourceID string) slog.Attr {
	switch family {
	case natswire.DeviceFactFamilyObservation:
		return slog.String("observation_id", sourceID)
	case natswire.DeviceFactFamilyEntityEvent:
		return slog.String("event_id", sourceID)
	case natswire.DeviceFactFamilyCommand:
		return slog.String("command_id", sourceID)
	}
	return slog.String("source_id", sourceID)
}

// formatFactTime renders one required Core or committed timestamp as the
// canonical UTC RFC 3339 form the strict schemas require.
func formatFactTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func formatOptionalFactTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := formatFactTime(*value)
	return &formatted
}

func formatOptionalFailureCode(value *devices.CommandFailureCode) *string {
	if value == nil {
		return nil
	}
	formatted := string(*value)
	return &formatted
}

func formatOptionalObservationID(value *devices.ObservationID) *string {
	if value == nil {
		return nil
	}
	formatted := string(*value)
	return &formatted
}
