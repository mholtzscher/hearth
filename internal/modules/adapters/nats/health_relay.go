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
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

const (
	healthPollInterval      = 5 * time.Second
	healthOperationTimeout  = 5 * time.Second
	healthMinBackoff        = 100 * time.Millisecond
	healthMaxBackoff        = 5 * time.Second
	healthBackoffMultiplier = 2
)

// healthSnapshot is a transport DTO, not a serialization of domain structs.
type healthSnapshot struct {
	AdapterID        string  `json:"adapter_id"`
	RuntimeID        *string `json:"runtime_id"`
	Revision         int64   `json:"revision"`
	Status           string  `json:"status"`
	Source           string  `json:"source"`
	ReasonCode       *string `json:"reason_code,omitempty"`
	SourceObservedAt *string `json:"source_observed_at,omitempty"`
	ChangedAt        string  `json:"changed_at"`
	EvidenceAt       string  `json:"evidence_at"`
}

// HealthRelay owns exactly one publisher, including during drain. Its hints
// carry no durable work; the repository and periodic poll are authoritative.
type HealthRelay struct {
	js         jetstream.JetStream
	store      adapters.HealthPublicationStore
	validator  *contractsv1.Validator
	logger     *slog.Logger
	worker     *lifecycle.WorkerHandle
	wake       chan struct{}
	syncWake   chan struct{}
	drain      chan struct{}
	drainOnce  sync.Once
	syncMu     sync.Mutex
	syncClosed bool
	active     atomic.Bool
	faultMu    sync.Mutex
	fault      error
}

// StartHealthRelay starts the sole publisher after stream provisioning and
// startup QueueCurrentHealthSnapshots have succeeded. Backlog is not a readiness
// gate: assembly checks Active, Fault, and live resource compatibility instead.
func StartHealthRelay(
	js jetstream.JetStream,
	store adapters.HealthPublicationStore,
	validator *contractsv1.Validator,
	logger *slog.Logger,
) (*HealthRelay, error) {
	if js == nil || store == nil || validator == nil {
		return nil, errors.New("health relay dependencies required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	r := &HealthRelay{
		js:        js,
		store:     store,
		validator: validator,
		logger:    logger,
		wake:      make(chan struct{}, 1),
		syncWake:  make(chan struct{}, 1),
		drain:     make(chan struct{}),
	}
	r.active.Store(true)
	r.worker = lifecycle.StartWorker(context.Background(), r.run)
	return r, nil
}

func (r *HealthRelay) NotifyPendingHealthUpdates() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// RequestSnapshotSync is safe in a NATS reconnect callback: no SQLite or broker I/O.
func (r *HealthRelay) RequestSnapshotSync() {
	r.syncMu.Lock()
	defer r.syncMu.Unlock()
	if r.syncClosed {
		return
	}
	select {
	case r.syncWake <- struct{}{}:
	default:
	}
}

func (r *HealthRelay) StopSnapshotSync()       { r.syncMu.Lock(); r.syncClosed = true; r.syncMu.Unlock() }
func (r *HealthRelay) Active() bool            { return r != nil && r.active.Load() }
func (r *HealthRelay) Fault() error            { r.faultMu.Lock(); defer r.faultMu.Unlock(); return r.fault }
func (r *HealthRelay) Closed() <-chan struct{} { return r.worker.Closed() }
func (r *HealthRelay) Stop(ctx context.Context) error {
	r.StopSnapshotSync()
	return r.worker.Stop(ctx)
}

// Drain keeps publication in the original worker, never starts a second owner.
// A deadline cancels that worker; callers must still observe Closed before
// closing its dependencies if their join deadline expires.
func (r *HealthRelay) Drain(ctx context.Context) error {
	r.StopSnapshotSync()
	r.active.Store(false)
	r.drainOnce.Do(func() { close(r.drain); r.NotifyPendingHealthUpdates() })
	if err := r.worker.Wait(ctx); err != nil {
		return r.worker.Stop(ctx)
	}
	return nil
}

func (r *HealthRelay) setFault(err error) { r.faultMu.Lock(); r.fault = err; r.faultMu.Unlock() }

func (r *HealthRelay) run(ctx context.Context) error {
	defer r.active.Store(false)
	backoff := healthMinBackoff
	syncPending := false
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-r.syncWake:
			syncPending = true
		default:
		}
		var found bool
		var err error
		if syncPending {
			err = r.synchronize(ctx)
		} else {
			found, err = r.publishOldest(ctx)
		}
		if errors.Is(err, adapters.ErrCorruptHealthEvidence) {
			return r.poison()
		}
		if err != nil {
			r.setFault(errors.New("health delivery dependency failure"))
			r.logger.WarnContext(
				ctx,
				"health publication retry",
				"event",
				"adapter_health.retry",
				"error_code",
				"delivery_failed",
			)
			r.wait(ctx, backoff, false)
			backoff = min(backoff*healthBackoffMultiplier, healthMaxBackoff)
			continue
		}
		backoff = healthMinBackoff
		if syncPending {
			syncPending = false
			continue
		}
		r.setFault(nil)
		if found {
			continue
		}
		select {
		case <-r.drain:
			return nil
		default:
		}
		r.wait(ctx, healthPollInterval, true)
	}
}

func (r *HealthRelay) synchronize(ctx context.Context) error {
	op, cancel := context.WithTimeout(ctx, healthOperationTimeout)
	defer cancel()
	return r.store.QueueCurrentHealthSnapshots(op, time.Now().UTC())
}

func (r *HealthRelay) publishOldest(ctx context.Context) (bool, error) {
	op, cancel := context.WithTimeout(ctx, healthOperationTimeout)
	row, found, err := r.store.OldestPendingHealthUpdate(op)
	cancel()
	if err != nil || !found {
		return found, err
	}
	message, err := r.prepare(row)
	if err != nil {
		return true, fmt.Errorf("%w: invalid publication", adapters.ErrCorruptHealthEvidence)
	}
	op, cancel = context.WithTimeout(ctx, healthOperationTimeout)
	ack, err := r.js.PublishMsg(op, message)
	cancel()
	if err != nil {
		return true, err
	}
	if ack == nil || ack.Stream != HealthStreamName {
		return true, errors.New("unexpected health PubAck")
	}
	op, cancel = context.WithTimeout(ctx, healthOperationTimeout)
	defer cancel()
	return true, r.store.DeleteHealthUpdate(op, row.ID)
}

func (r *HealthRelay) poison() error {
	r.setFault(errors.New("health outbox poison"))
	r.logger.Error("health publication poison", "event", "adapter_health.poison", "error_code", "invalid_outbox")
	return r.Fault()
}

func (r *HealthRelay) wait(ctx context.Context, duration time.Duration, hints bool) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	if !hints {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			return
		}
	}
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	case <-r.wake:
	case <-r.syncWake:
		r.RequestSnapshotSync()
	}
}

func (r *HealthRelay) prepare(row adapters.PendingHealthUpdate) (*natsgo.Msg, error) {
	if _, err := adapters.ParseHealthUpdateID(string(row.ID)); err != nil {
		return nil, err
	}
	snapshot, err := adapters.NormalizeHealthSnapshot(row.Snapshot)
	if err != nil {
		return nil, err
	}
	if row.EnqueuedAt.IsZero() || row.EnqueuedAt.Location() != time.UTC {
		return nil, errors.New("invalid enqueue time")
	}
	dto := healthSnapshot{
		AdapterID:  snapshot.AdapterID,
		Revision:   int64(snapshot.Revision),
		Status:     string(snapshot.Status),
		Source:     snapshot.Source,
		ChangedAt:  snapshot.ChangedAt.Format(time.RFC3339Nano),
		EvidenceAt: snapshot.EvidenceAt.Format(time.RFC3339Nano),
	}
	if snapshot.RuntimeID != nil {
		value := string(*snapshot.RuntimeID)
		dto.RuntimeID = &value
	}
	if snapshot.Reason != nil {
		value := snapshot.Reason.Code
		dto.ReasonCode = &value
	}
	if snapshot.SourceObservedAt != nil {
		value := snapshot.SourceObservedAt.Format(time.RFC3339Nano)
		dto.SourceObservedAt = &value
	}
	payload, err := natswire.Encode(
		r.validator,
		contractsv1.AdapterHealthUpdateSchemaID,
		natswire.Envelope[healthSnapshot]{
			ID:            string(row.ID),
			Schema:        contractsv1.AdapterHealthUpdateSchemaID,
			EmittedAt:     row.EnqueuedAt.Format(time.RFC3339Nano),
			CorrelationID: row.CorrelationID,
			CausationID:   row.CausationID,
			Data:          dto,
		},
	)
	if err != nil {
		return nil, err
	}
	subject, err := natswire.AdapterHealthSubject(snapshot.AdapterID)
	if err != nil {
		return nil, err
	}
	message := natsgo.NewMsg(subject)
	message.Data = payload
	message.Header.Set(natsgo.MsgIdHdr, string(row.ID))
	if row.TraceParent != "" {
		message.Header.Set("traceparent", row.TraceParent)
	}
	if row.TraceState != "" {
		message.Header.Set("tracestate", row.TraceState)
	}
	if headerErr := natswire.ValidateAdapterHealthHeaders(message.Header, string(row.ID)); headerErr != nil {
		return nil, headerErr
	}
	if message.Size() > HealthMaxMessageSize {
		return nil, errors.New("health message too large")
	}
	return message, nil
}
