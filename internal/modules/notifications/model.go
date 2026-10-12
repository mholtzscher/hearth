// Package notifications owns registered destinations and durable immediate send evidence.
package notifications

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type DestinationID string
type AttemptID string
type Destination struct {
	ID        DestinationID
	Name      string
	Available bool
}
type Message struct{ Title, Text string }
type SendInput struct {
	AttemptID     AttemptID
	DestinationID DestinationID
	Message       Message
}
type Outcome string

const (
	Accepted           Outcome = "accepted"
	Rejected           Outcome = "rejected"
	NotSent            Outcome = "not_sent"
	Unknown            Outcome = "unknown"
	SendTimeout                = 10 * time.Second
	PersistenceTimeout         = 5 * time.Second
	Retention                  = 30 * 24 * time.Hour
	DefaultPageLimit           = 50
)

type Registration struct{ AdapterID, RuntimeID, Key, Name string }
type RouteSnapshot struct{ AdapterID, LocalKey, RuntimeID string }
type Attempt struct {
	ID                  AttemptID
	DestinationID       DestinationID
	DestinationName     string
	Route               RouteSnapshot
	Message             Message
	StartedAt, Deadline time.Time
	CompletedAt         *time.Time
	Outcome             Outcome
	ReasonCode, Receipt string
}
type AttemptFilter struct {
	DestinationID DestinationID
	Limit         int
	Cursor        string
}
type AttemptPage struct {
	Items      []Attempt
	NextCursor string
}
type DestinationPage struct {
	Items      []Destination
	NextCursor string
}
type Evidence struct {
	Outcome             Outcome
	ReasonCode, Receipt string
}

var (
	ErrInvalidInput        = errors.New("invalid notification input")
	ErrDestinationNotFound = errors.New("notification destination not found")
	ErrAttemptNotFound     = errors.New("notification attempt not found")
	// ErrAttemptIDConflict distinguishes a reserved identity collision from an ambiguous outcome write.
	ErrAttemptIDConflict = errors.New("notification attempt identity conflict")
	ErrStaleRuntime      = errors.New("stale adapter runtime")
	ErrAdmissionClosed   = errors.New("notification admission closed")
	ErrPersistence       = errors.New("notification persistence failed")
)

func NewDestinationID() (DestinationID, error) {
	id, err := uuid.NewV7()
	return DestinationID("ndst_" + id.String()), err
}
func NewAttemptID() (AttemptID, error) {
	id, err := uuid.NewV7()
	return AttemptID("ntf_" + id.String()), err
}
func ValidateID(value, prefix string) error {
	id, err := uuid.Parse(strings.TrimPrefix(value, prefix))
	if !strings.HasPrefix(value, prefix) || err != nil || id.Version() != 7 || id.Variant() != uuid.RFC4122 ||
		value != prefix+id.String() {
		return fmt.Errorf("%w: invalid %s identity", ErrInvalidInput, prefix)
	}
	return nil
}
func PrepareRegistration(r Registration) (Registration, error) {
	name, err := PrepareDestinationDescriptor(r.Key, r.Name)
	if err != nil || !validSlug(r.AdapterID) || len(r.AdapterID) > 63 ||
		ValidateID(r.RuntimeID, "run_") != nil {
		return Registration{}, fmt.Errorf("%w: invalid registration", ErrInvalidInput)
	}
	r.Name = name
	return r, nil
}

// PrepareDestinationDescriptor validates static destination metadata without a
// connected Adapter runtime. Registration and scripted config share these bounds.
func PrepareDestinationDescriptor(key, name string) (string, error) {
	name = strings.TrimSpace(name)
	if !validSlug(key) || len(key) > 63 || !utf8.ValidString(name) ||
		utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 200 {
		return "", fmt.Errorf("%w: invalid destination descriptor", ErrInvalidInput)
	}
	return name, nil
}
func ValidateSend(in SendInput) error {
	if ValidateID(string(in.AttemptID), "ntf_") != nil || ValidateID(string(in.DestinationID), "ndst_") != nil {
		return fmt.Errorf("%w: invalid send", ErrInvalidInput)
	}
	return ValidateMessage(in.Message)
}

// ValidateMessage enforces the shared static message contract without requiring an attempt identity.
func ValidateMessage(message Message) error {
	if !utf8.ValidString(message.Title) || !utf8.ValidString(message.Text) ||
		utf8.RuneCountInString(message.Title) > 256 || utf8.RuneCountInString(message.Text) > 2000 ||
		strings.TrimSpace(message.Text) == "" {
		return fmt.Errorf("%w: invalid message", ErrInvalidInput)
	}
	return nil
}
func ValidateEvidence(e Evidence) error {
	if e.Outcome == Accepted {
		if e.ReasonCode != "" || !utf8.ValidString(e.Receipt) || utf8.RuneCountInString(e.Receipt) > 256 {
			return ErrInvalidInput
		}
		return nil
	}
	if (e.Outcome != Rejected && e.Outcome != NotSent && e.Outcome != Unknown) || !validSlug(e.ReasonCode) ||
		len(e.ReasonCode) > 64 ||
		e.Receipt != "" {
		return ErrInvalidInput
	}
	var allowed bool
	switch e.Outcome {
	case Accepted:
		return ErrInvalidInput // Accepted evidence was handled above.
	case Rejected:
		allowed = e.ReasonCode == "destination_unknown" || e.ReasonCode == "invalid_message" ||
			e.ReasonCode == "provider_forbidden" ||
			e.ReasonCode == "provider_rate_limited" ||
			e.ReasonCode == "provider_rejected"
	case NotSent:
		allowed = e.ReasonCode == "destination_unavailable" || e.ReasonCode == "adapter_busy" ||
			e.ReasonCode == "deadline_expired"
	case Unknown:
		allowed = e.ReasonCode == "outcome_timeout" || e.ReasonCode == "invalid_response" ||
			e.ReasonCode == "transport_error" ||
			e.ReasonCode == "core_restarted" ||
			e.ReasonCode == "provider_error"
	}
	if !allowed {
		return ErrInvalidInput
	}
	return nil
}
func PageLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultPageLimit, nil
	}
	if limit < 1 || limit > 200 {
		return 0, ErrInvalidInput
	}
	return limit, nil
}
func validSlug(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for i, c := range value {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '_' || c == '-') {
			continue
		}
		return false
	}
	return true
}
