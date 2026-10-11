package notifications

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Repository owns atomic runtime checks, route snapshots and immutable evidence.
type Repository interface {
	Register(context.Context, Registration) (Destination, error)
	GetDestination(context.Context, DestinationID) (Destination, error)
	ListDestinations(context.Context, int, string) (DestinationPage, error)
	Admit(context.Context, SendInput, time.Time, time.Time) (Attempt, bool, error)
	Complete(context.Context, AttemptID, Evidence, time.Time) (Attempt, error)
	GetAttempt(context.Context, AttemptID) (Attempt, error)
	ListAttempts(context.Context, AttemptFilter) (AttemptPage, error)
	Recover(context.Context, time.Time) error
	DeleteHistoryBefore(context.Context, time.Time, int) (int64, error)
}

// Sender makes one bounded request; failures after possible publication are uncertain.
type Sender interface {
	Send(context.Context, Attempt) Evidence
}
type Service struct {
	repo                         Repository
	sender                       Sender
	mutex                        sync.Mutex
	publicClosed, closed, failed bool
	active                       int
	slots                        map[string]int
	done                         chan struct{}
}

// NewService terminalizes interrupted evidence before allowing admission.
func NewService(ctx context.Context, repo Repository, sender Sender) (*Service, error) {
	if repo == nil || sender == nil {
		return nil, ErrInvalidInput
	}
	if err := repo.Recover(ctx, time.Now().UTC()); err != nil {
		return nil, errors.Join(ErrPersistence, err)
	}
	return &Service{repo: repo, sender: sender, slots: make(map[string]int), done: make(chan struct{})}, nil
}
func (s *Service) Register(ctx context.Context, r Registration) (Destination, error) {
	r, err := PrepareRegistration(r)
	if err != nil {
		return Destination{}, err
	}
	return s.repo.Register(ctx, r)
}
func (s *Service) GetDestination(ctx context.Context, id DestinationID) (Destination, error) {
	if err := ValidateID(string(id), "ndst_"); err != nil {
		return Destination{}, err
	}
	return s.repo.GetDestination(ctx, id)
}
func (s *Service) ListDestinations(ctx context.Context, limit int, cursor string) (DestinationPage, error) {
	if _, err := PageLimit(limit); err != nil {
		return DestinationPage{}, err
	}
	return s.repo.ListDestinations(ctx, limit, cursor)
}
func (s *Service) GetAttempt(ctx context.Context, id AttemptID) (Attempt, error) {
	if err := ValidateID(string(id), "ntf_"); err != nil {
		return Attempt{}, err
	}
	return s.repo.GetAttempt(ctx, id)
}
func (s *Service) ListAttempts(ctx context.Context, f AttemptFilter) (AttemptPage, error) {
	if _, err := PageLimit(f.Limit); err != nil {
		return AttemptPage{}, err
	}
	if f.DestinationID != "" {
		if err := ValidateID(string(f.DestinationID), "ndst_"); err != nil {
			return AttemptPage{}, err
		}
	}
	return s.repo.ListAttempts(ctx, f)
}
func (s *Service) ClosePublicAdmission() { s.mutex.Lock(); s.publicClosed = true; s.mutex.Unlock() }
func (s *Service) Ready() bool           { s.mutex.Lock(); defer s.mutex.Unlock(); return !s.failed }
func (s *Service) SendPublic(ctx context.Context, in SendInput) (Attempt, error) {
	return s.send(ctx, in, true)
}
func (s *Service) Send(ctx context.Context, in SendInput) (Attempt, error) {
	return s.send(ctx, in, false)
}

// Drain closes internal admission and joins all attempts without canceling them.
func (s *Service) Drain(ctx context.Context) error {
	s.mutex.Lock()
	if !s.closed {
		s.closed = true
		if s.active == 0 {
			close(s.done)
		}
	}
	s.mutex.Unlock()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) latch() { s.mutex.Lock(); s.failed = true; s.mutex.Unlock() }
func (s *Service) send(ctx context.Context, in SendInput, public bool) (Attempt, error) {
	if err := ValidateSend(in); err != nil {
		return Attempt{}, err
	}
	if err := ctx.Err(); err != nil {
		return Attempt{}, err
	}
	s.mutex.Lock()
	if s.closed || s.failed || (public && s.publicClosed) {
		s.mutex.Unlock()
		return Attempt{}, ErrAdmissionClosed
	}
	s.active++
	s.mutex.Unlock()
	defer func() {
		s.mutex.Lock()
		defer s.mutex.Unlock()
		s.active--
		if s.closed && s.active == 0 {
			close(s.done)
		}
	}()
	start := time.Now().UTC()
	deadline := start.Add(SendTimeout)
	owned, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancel()
	attempt, available, err := s.repo.Admit(owned, in, start, deadline)
	if err != nil {
		if !errors.Is(err, ErrDestinationNotFound) && !errors.Is(err, ErrInvalidInput) {
			s.latch()
			err = errors.Join(ErrPersistence, err)
		}
		return Attempt{}, err
	}
	busy := false
	if available {
		s.mutex.Lock()
		busy = s.slots[attempt.Route.AdapterID] >= AdapterSlots
		if !busy {
			s.slots[attempt.Route.AdapterID]++
		}
		s.mutex.Unlock()
		if !busy {
			defer func() { s.mutex.Lock(); s.slots[attempt.Route.AdapterID]--; s.mutex.Unlock() }()
		}
	}
	evidence := s.dispatch(owned, attempt, available, busy)
	persist, stop := context.WithTimeout(context.WithoutCancel(ctx), PersistenceTimeout)
	defer stop()
	terminal, err := s.repo.Complete(persist, attempt.ID, evidence, time.Now().UTC())
	if err != nil {
		s.latch()
		return Attempt{}, errors.Join(ErrPersistence, err)
	}
	return terminal, nil
}

func (s *Service) dispatch(ctx context.Context, attempt Attempt, available, busy bool) Evidence {
	if !available {
		return Evidence{Outcome: NotSent, ReasonCode: "destination_unavailable"}
	}
	if busy {
		return Evidence{Outcome: NotSent, ReasonCode: "adapter_busy"}
	}
	if !time.Now().Before(attempt.Deadline) {
		return Evidence{Outcome: NotSent, ReasonCode: "deadline_expired"}
	}
	e := s.sender.Send(ctx, attempt)
	valid := ValidateEvidence(e) == nil
	// Sender evidence of nonsubmission remains valid after expiry. In particular,
	// the deadline can pass between dispatch's guard and the sender's own guard.
	// The transport rejects late wire replies before returning any such evidence.
	if valid && e.Outcome == NotSent {
		return e
	}
	if !time.Now().Before(attempt.Deadline) {
		return Evidence{Outcome: Unknown, ReasonCode: "outcome_timeout"}
	}
	if !valid {
		return Evidence{Outcome: Unknown, ReasonCode: "invalid_response"}
	}
	return e
}
