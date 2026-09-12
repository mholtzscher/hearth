package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	// DeviceFactRelayBatchSize bounds one outbox read. The relay publishes a
	// full batch in enqueue order before reading the next, so the outbox never
	// grows a backlog larger than one batch plus the rows committed while that
	// batch is published.
	DeviceFactRelayBatchSize = 64
	// DeviceFactRelayPollInterval is the authoritative rediscovery cadence. The
	// wake hint only shortens latency; a lost hint, a process restart or work
	// committed while the relay was busy is always found by this poll.
	DeviceFactRelayPollInterval = 5 * time.Second
	// DeviceFactRelayRetryBackoff is the fixed delay between retries after a
	// transient outbox or broker failure. It never grows, never mints a new
	// identity and never reorders the outbox.
	DeviceFactRelayRetryBackoff = time.Second
	// DeviceFactPublishTimeout bounds one JetStream publish and its PubAck wait,
	// so a stalled broker cannot hold the single worker forever.
	DeviceFactPublishTimeout = 5 * time.Second
)

// Device Fact relay diagnostic stages and fixed error codes. Each retry
// diagnostic names the stage that failed and one fixed code, so a search for the
// code finds this file and no diagnostic depends on raw upstream error text.
const (
	deviceFactStageList    = "list"
	deviceFactStagePublish = "publish"
	deviceFactStageAck     = "ack"
	deviceFactStageDelete  = "delete"
)

const (
	deviceFactCodeListFailed       = "list_failed"
	deviceFactCodePublishFailed    = "publish_failed"
	deviceFactCodeAckMissing       = "ack_missing"
	deviceFactCodeUnexpectedStream = "unexpected_stream"
	deviceFactCodeDeleteFailed     = "delete_failed"
)

// Relay log events. device_fact.retry reports a transient failure whose pending
// row is kept and retried; device_fact.poison reports the deterministic failure
// that stops the relay and leaves the offending row for an operator.
const (
	deviceFactEventRetry  = "device_fact.retry"
	deviceFactEventPoison = "device_fact.poison"
)

// errDeviceFactRetry marks a transient outbox or broker failure. The failure is
// already logged with its stage and fixed code, so the worker only has to wait
// out the bounded backoff and try again.
var errDeviceFactRetry = errors.New("device fact relay retry")

// deviceFactPublisher performs one JetStream publish and waits for its PubAck.
// It is a seam so tests can observe, stall or fail one publication without a
// socket, while production always calls exactly one PublishMsg per pending fact.
type deviceFactPublisher func(ctx context.Context, message *natsgo.Msg) (*jetstream.PubAck, error)

// deviceFactRelayOptions carries the bounded relay timing. Production uses the
// defaults above; focused tests shorten them so a retry, a wake and a drain can
// be observed without sleeping as a correctness oracle.
type deviceFactRelayOptions struct {
	batchSize      int
	pollInterval   time.Duration
	retryBackoff   time.Duration
	publishTimeout time.Duration
}

func defaultDeviceFactRelayOptions() deviceFactRelayOptions {
	return deviceFactRelayOptions{
		batchSize:      DeviceFactRelayBatchSize,
		pollInterval:   DeviceFactRelayPollInterval,
		retryBackoff:   DeviceFactRelayRetryBackoff,
		publishTimeout: DeviceFactPublishTimeout,
	}
}

// DeviceFactRelay is the single durable publisher for the Device Fact outbox. It
// implements devices.DeviceFactNotifier: a commit whose transaction queued a
// pending fact wakes it, and it publishes pending facts oldest-first over
// JetStream, deleting each row only after the broker has acknowledged it.
//
// Durability lives in the outbox, not in the relay. A row survives a relay
// restart, a broker outage and a Process exit, and the relay always re-reads the
// oldest pending rows on startup. Publication is therefore at-least-once from
// the outbox, with bounded broker-side deduplication at best: Nats-Msg-Id is the
// stable fact ID, and the stream's duplicate window collapses a retried publish
// only while that window lasts. The relay's retry span is unbounded -- a long
// outage or a restart can exceed the window -- so a retry after the window has
// elapsed is stored again, and every consumer must stay idempotent on the stable
// fact identity instead of assuming one stored fact per row.
//
// The relay delivers or faults, it never discards. A broker, timeout or delete
// failure keeps the row and retries with a fixed bounded backoff. A
// deterministic poison row -- one Core cannot map, or one Core cannot even
// decode from the outbox -- keeps the row, stops the relay and makes
// [DeviceFactRelay.Active] false, because deleting a fact Core cannot represent
// would silently lose durable evidence.
type DeviceFactRelay struct {
	outbox    devices.DeviceFactOutbox
	validator *contractsv1.Validator
	logger    *slog.Logger
	publish   deviceFactPublisher

	batchSize      int
	pollInterval   time.Duration
	retryBackoff   time.Duration
	publishTimeout time.Duration

	wake    chan struct{}
	stop    chan struct{}
	stopped chan struct{}

	// workerCtx bounds the worker's publication pass. Drain cancels it, so a busy
	// outbox or an in-flight publish cannot delay shutdown.
	workerCtx    context.Context
	cancelWorker context.CancelFunc

	stopOnce   sync.Once
	active     atomic.Bool
	faultMutex sync.Mutex
	faultErr   error
}

// StartDeviceFactRelay validates its dependencies and starts the single relay
// worker over one JetStream context. The stream must already be provisioned and
// validated; the relay publishes into it and never creates or configures it.
func StartDeviceFactRelay(
	js jetstream.JetStream,
	outbox devices.DeviceFactOutbox,
	validator *contractsv1.Validator,
	logger *slog.Logger,
) (*DeviceFactRelay, error) {
	if js == nil {
		return nil, errors.New("device fact relay requires a JetStream context")
	}
	return startDeviceFactRelay(
		outbox,
		validator,
		logger,
		func(ctx context.Context, message *natsgo.Msg) (*jetstream.PubAck, error) {
			return js.PublishMsg(ctx, message)
		},
		defaultDeviceFactRelayOptions(),
	)
}

// startDeviceFactRelay is the single relay constructor. The publish seam and the
// bounded timings are injected so in-package tests can stall, fail or observe one
// publication and can shorten the poll, backoff and publish bounds.
func startDeviceFactRelay(
	outbox devices.DeviceFactOutbox,
	validator *contractsv1.Validator,
	logger *slog.Logger,
	publish deviceFactPublisher,
	options deviceFactRelayOptions,
) (*DeviceFactRelay, error) {
	if outbox == nil {
		return nil, errors.New("device fact relay requires an outbox")
	}
	if validator == nil {
		return nil, errors.New("device fact relay requires a compiled schema validator")
	}
	if publish == nil {
		return nil, errors.New("device fact relay requires a publication function")
	}
	if options.batchSize < 1 {
		return nil, errors.New("device fact relay requires a positive batch size")
	}
	if options.pollInterval <= 0 || options.retryBackoff <= 0 || options.publishTimeout <= 0 {
		return nil, errors.New("device fact relay requires positive poll, backoff and publish bounds")
	}
	relay := &DeviceFactRelay{
		outbox:         outbox,
		validator:      validator,
		logger:         defaultLogger(logger),
		publish:        publish,
		batchSize:      options.batchSize,
		pollInterval:   options.pollInterval,
		retryBackoff:   options.retryBackoff,
		publishTimeout: options.publishTimeout,
		wake:           make(chan struct{}, 1),
		stop:           make(chan struct{}),
		stopped:        make(chan struct{}),
	}
	relay.workerCtx, relay.cancelWorker = context.WithCancel(context.Background())
	relay.active.Store(true)
	go relay.run()
	return relay, nil
}

// Active reports whether the relay still publishes pending facts. It is false
// before construction finishes, after Drain stops readiness, and after a poison
// fault stops the relay, so readiness fails instead of reporting a publisher
// that can no longer make progress. It never proves the broker accepted a fact.
func (relay *DeviceFactRelay) Active() bool {
	return relay != nil && relay.active.Load()
}

// NotifyPendingDeviceFacts offers the relay one nonblocking wake hint after a
// transaction committed a pending fact. It never performs I/O, never blocks and
// never returns an error; a lost hint only defers publication until the next
// poll, because the outbox is authoritative and the poll re-reads it.
func (relay *DeviceFactRelay) NotifyPendingDeviceFacts() {
	if relay == nil {
		return
	}
	select {
	case relay.wake <- struct{}{}:
	default:
	}
}

// Closed reports worker termination. It closes after the single publisher
// returns, so a caller that joined Drain knows no publication goroutine remains.
func (relay *DeviceFactRelay) Closed() <-chan struct{} {
	if relay == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return relay.stopped
}

// Drain stops readiness, publishes every pending fact until the outbox is empty
// or ctx expires, and joins the worker before returning. It is the only
// shutdown path: it stops admission, waits for the worker to leave the
// publication path, and then publishes the remainder itself, so exactly one
// publisher ever owns the outbox order.
//
// Unpublished rows are never discarded. When ctx expires mid-drain the
// remaining rows stay in the outbox and the next startup re-reads them; when a
// poison row is reached, Drain returns that fault and leaves the row. Drain is
// idempotent and always returns.
func (relay *DeviceFactRelay) Drain(ctx context.Context) error {
	if relay == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	relay.active.Store(false)
	relay.stopOnce.Do(func() {
		close(relay.stop)
		// Cancelling the worker context aborts an in-flight publication pass (and
		// any publish waiting on the broker) instead of waiting out its timeout.
		relay.cancelWorker()
	})
	// The worker is the only other publisher. Joining it first means the drain
	// below is the single publisher and still preserves outbox order. A worker
	// that cannot leave the publication path inside the context budget is
	// reported as an expired drain rather than joined past it, because publishing
	// from two goroutines at once would break outbox order.
	select {
	case <-relay.stopped:
	case <-ctx.Done():
		return fmt.Errorf("drain device fact relay: %w", ctx.Err())
	}
	if fault := relay.fault(); fault != nil {
		return fault
	}
	return relay.drainPending(ctx)
}

// run is the single publication worker. It reads the outbox oldest-first,
// publishes and deletes in order, and exits only on shutdown or a poison fault.
func (relay *DeviceFactRelay) run() {
	defer close(relay.stopped)
	poll := time.NewTicker(relay.pollInterval)
	defer poll.Stop()
	for {
		err := relay.publishPending(relay.workerCtx)
		if err == nil {
			select {
			case <-relay.stop:
				return
			case <-relay.wake:
			case <-poll.C:
			}
			continue
		}
		if poisonErr := asDeviceFactPoisonError(err); poisonErr != nil {
			relay.recordFault(poisonErr)
			relay.logPoison(context.Background(), poisonErr)
			return
		}
		// A cancelled worker context is shutdown, not a retry.
		if relay.shutdown() {
			return
		}
		// A transient failure already logged its stage and code. Wait out the
		// fixed backoff, but never delay shutdown.
		timer := time.NewTimer(relay.retryBackoff)
		select {
		case <-relay.stop:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// shutdown reports whether Drain has begun. A cancelled worker context is a
// shutdown signal, never a retryable failure.
func (relay *DeviceFactRelay) shutdown() bool {
	select {
	case <-relay.stop:
		return true
	default:
		return false
	}
}

// drainPending retries the pending set until it is empty, a poison row faults the
// relay, or ctx expires. It is the synchronous drain owned by Drain.
func (relay *DeviceFactRelay) drainPending(ctx context.Context) error {
	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("drain device fact relay: %w", ctxErr)
		}
		err := relay.publishPending(ctx)
		if err == nil {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("drain device fact relay: %w", ctxErr)
		}
		if poisonErr := asDeviceFactPoisonError(err); poisonErr != nil {
			relay.recordFault(poisonErr)
			relay.logPoison(context.Background(), poisonErr)
			return poisonErr
		}
		timer := time.NewTimer(relay.retryBackoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("drain device fact relay: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

// publishPending publishes pending facts oldest-first until the outbox is empty.
// It returns nil when a read finds nothing pending, the permanent poison error
// for a row Core cannot decode or map, and errDeviceFactRetry for a transient
// failure after logging it. A transient read failure is always retryable; only
// the outbox's own permanent row class faults the relay. It deliberately returns
// on the first transient failure so no row is skipped and the retry starts again
// from the oldest pending row.
func (relay *DeviceFactRelay) publishPending(ctx context.Context) error {
	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		pending, listErr := relay.outbox.ListPendingDeviceFacts(ctx, relay.batchSize)
		if listErr != nil {
			// A row whose stored bytes cannot be decoded is deterministic, so the
			// relay faults and preserves it instead of retrying a decode that can
			// never succeed. Every other read failure is transient.
			if poisonErr := asInvalidOutboxRowPoison(listErr); poisonErr != nil {
				return poisonErr
			}
			relay.logRetry(ctx, deviceFactStageList, deviceFactCodeListFailed, "", "")
			return errDeviceFactRetry
		}
		if len(pending) == 0 {
			return nil
		}
		for _, item := range pending {
			if err := relay.publishPendingFact(ctx, item); err != nil {
				return err
			}
		}
	}
}

// publishPendingFact maps one pending fact, publishes it and deletes its row only
// after the broker acknowledged it into the expected stream. A duplicate
// acknowledgement is a success: the fact is already stored under the same
// Nats-Msg-Id. The row is deleted only after the acknowledgement, so a failed
// publish or a failed delete leaves the durable fact pending and the next
// attempt reuses the same identity and bytes.
func (relay *DeviceFactRelay) publishPendingFact(
	ctx context.Context,
	pending devices.PendingDeviceFact,
) error {
	message, mappingErr := mapPendingDeviceFact(relay.validator, pending)
	if mappingErr != nil {
		return mappingErr
	}
	publishCtx, cancelPublish := context.WithTimeout(ctx, relay.publishTimeout)
	ack, publishErr := relay.publish(publishCtx, message.natsMessage())
	cancelPublish()
	if publishErr != nil {
		relay.logPublishRetry(ctx, message, publishErr)
		return errDeviceFactRetry
	}
	if ack == nil {
		relay.logRetry(ctx, deviceFactStageAck, deviceFactCodeAckMissing, message.family, message.sourceID)
		return errDeviceFactRetry
	}
	// The expected-stream header makes the broker reject a publication into any
	// other stream, so an acknowledgement naming another stream means the fact is
	// not where Core must delete it: keep the row and retry rather than lose it.
	if ack.Stream != DeviceFactStreamName {
		relay.logRetry(ctx, deviceFactStageAck, deviceFactCodeUnexpectedStream, message.family, message.sourceID)
		return errDeviceFactRetry
	}
	if deleteErr := relay.outbox.DeleteDeviceFact(ctx, message.factID); deleteErr != nil {
		relay.logRetry(ctx, deviceFactStageDelete, deviceFactCodeDeleteFailed, message.family, message.sourceID)
		return errDeviceFactRetry
	}
	return nil
}

// logPublishRetry records a failed publication with its fixed code. A cancelled
// attempt is shutdown rather than a broker fault, so it is not logged; a publish
// bound expiry is logged, because a stalled broker is exactly the diagnostic an
// operator needs.
func (relay *DeviceFactRelay) logPublishRetry(
	ctx context.Context,
	message deviceFactMessage,
	publishErr error,
) {
	if errors.Is(publishErr, context.Canceled) {
		return
	}
	relay.logRetry(ctx, deviceFactStagePublish, deviceFactCodePublishFailed, message.family, message.sourceID)
}

// recordFault stops readiness and remembers the first permanent failure, so
// every later Active and Drain call observes the same fault.
func (relay *DeviceFactRelay) recordFault(err error) {
	relay.active.Store(false)
	relay.faultMutex.Lock()
	defer relay.faultMutex.Unlock()
	if relay.faultErr == nil {
		relay.faultErr = err
	}
}

// fault returns the recorded permanent failure, if any.
func (relay *DeviceFactRelay) fault() error {
	relay.faultMutex.Lock()
	defer relay.faultMutex.Unlock()
	return relay.faultErr
}

// logRetry records one bounded retry diagnostic with its stage, fixed code and,
// when known, the fact family and its safe source identity. Payloads, full
// subjects and raw upstream error text are never recorded.
func (relay *DeviceFactRelay) logRetry(
	ctx context.Context,
	stage string,
	errorCode string,
	family devices.DeviceFactFamily,
	sourceID string,
) {
	attributes := []slog.Attr{
		slog.String(transportEventKey, deviceFactEventRetry),
		slog.String("stage", stage),
		slog.String(transportErrorCodeKey, errorCode),
	}
	if family != "" {
		attributes = append(attributes,
			slog.String("family", string(family)),
			deviceFactSourceIDAttr(family, sourceID),
		)
	}
	relay.logger.LogAttrs(ctx, slog.LevelWarn, "device fact relay retry", attributes...)
}

// logPoison records the deterministic failure that stopped the relay. The
// offending fact identity, family, safe source identity and fixed code are
// retained so an operator can find the preserved row; the raw cause is never
// recorded.
func (relay *DeviceFactRelay) logPoison(ctx context.Context, poisonErr *deviceFactPoisonError) {
	attributes := []slog.Attr{
		slog.String(transportEventKey, deviceFactEventPoison),
		slog.String("stage", poisonErr.stage),
		slog.String(transportErrorCodeKey, poisonErr.code),
	}
	if poisonErr.factID != "" {
		attributes = append(attributes, slog.String("fact_id", poisonErr.factID))
	}
	if poisonErr.family != "" {
		attributes = append(attributes,
			slog.String("family", string(poisonErr.family)),
			deviceFactSourceIDAttr(poisonErr.family, poisonErr.sourceID),
		)
	}
	relay.logger.LogAttrs(ctx, slog.LevelError, "device fact relay faulted", attributes...)
}

// deviceFactSourceIDAttr returns the source identity attribute for one family so
// a diagnostic carries a searchable, safe identity and never a full subject.
func deviceFactSourceIDAttr(family devices.DeviceFactFamily, sourceID string) slog.Attr {
	switch family {
	case devices.DeviceFactFamilyObservation:
		return slog.String("observation_id", sourceID)
	case devices.DeviceFactFamilyEntityEvent:
		return slog.String("event_id", sourceID)
	}
	return slog.String("source_id", sourceID)
}
