package nats //nolint:testpackage // Exercises relay with actual JetStream publication.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

// Only persistence outcomes are simulated; delivery and PubAck use JetStream.
type healthTestStore struct {
	mu             sync.Mutex
	rows           []adapters.PendingHealthUpdate
	deleteFailures int
	deletions      chan adapters.HealthUpdateID
	empty          chan struct{}
	emptyOnce      sync.Once
	deletePermit   chan struct{}
	deleteEntered  chan struct{}
}

func newHealthTestStore() *healthTestStore {
	return &healthTestStore{
		deletions: make(chan adapters.HealthUpdateID, 16),
		empty:     make(chan struct{}),
	}
}

func (s *healthTestStore) enqueue(row adapters.PendingHealthUpdate) {
	s.mu.Lock()
	s.rows = append(s.rows, row)
	s.mu.Unlock()
}

func (s *healthTestStore) ListPendingHealthUpdates(
	_ context.Context,
	limit int,
) ([]adapters.PendingHealthUpdate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.rows) == 0 {
		s.emptyOnce.Do(func() { close(s.empty) })
		return nil, nil
	}
	return append([]adapters.PendingHealthUpdate(nil), s.rows[:min(limit, len(s.rows))]...), nil
}

func (s *healthTestStore) DeleteHealthUpdate(_ context.Context, id adapters.HealthUpdateID) error {
	if s.deletePermit != nil {
		s.deleteEntered <- struct{}{}
		<-s.deletePermit // Deliberately prove that cancellation is not a join.
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteFailures > 0 {
		s.deleteFailures--
		return errors.New("delete failed")
	}
	if len(s.rows) == 0 || s.rows[0].ID != id {
		return errors.New("out of order delete")
	}
	s.rows = s.rows[1:]
	s.deletions <- id
	return nil
}

func healthTestRow() adapters.PendingHealthUpdate {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	cause := "hbt_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	return adapters.PendingHealthUpdate{
		ID: "ahu_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Snapshot: adapters.HealthSnapshot{
			AdapterID:  "zigbee-main",
			Revision:   12,
			Status:     adapters.AdapterHealthUnknown,
			Source:     "core",
			Reason:     &adapters.HealthReason{Code: "hearth.starting"},
			ChangedAt:  now,
			EvidenceAt: now,
		},
		EnqueuedAt:    now,
		CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		CausationID:   &cause,
		TraceParent:   "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		TraceState:    "hearth=core",
	}
}

func startTestHealthRelay(t *testing.T, js jetstream.JetStream, store *healthTestStore) *HealthRelay {
	t.Helper()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	relay, err := StartHealthRelay(js, store, validator, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = relay.Stop(ctx)
	})
	return relay
}
