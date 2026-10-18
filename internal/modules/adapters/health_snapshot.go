package adapters

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

type HealthRevision int64

const snapshotRuntimeKey = "runtime_id"

type HealthSnapshot struct {
	AdapterID        string
	RuntimeID        *RuntimeID
	Revision         HealthRevision
	Status           AdapterHealthStatus
	Source           string
	Reason           *HealthReason
	SourceObservedAt *time.Time
	ChangedAt        time.Time
	EvidenceAt       time.Time
}

// NormalizeHealthSnapshot validates freely constructed snapshot evidence and
// returns a detached copy with UTC times. Nonzero UTC-offset times are rejected.
func NormalizeHealthSnapshot(snapshot HealthSnapshot) (HealthSnapshot, error) {
	if !registrationSlugPattern.MatchString(snapshot.AdapterID) {
		return HealthSnapshot{}, errors.New("invalid snapshot Adapter ID")
	}
	if snapshot.Revision < 1 {
		return HealthSnapshot{}, errors.New("health revision must be positive")
	}
	if snapshot.RuntimeID != nil {
		runtime, err := ParseRuntimeID(string(*snapshot.RuntimeID))
		if err != nil {
			return HealthSnapshot{}, err
		}
		snapshot.RuntimeID = &runtime
	}
	if snapshot.Source != "core" && snapshot.Source != "adapter" {
		return HealthSnapshot{}, errors.New("invalid health source")
	}
	if err := validateSnapshotStatus(snapshot); err != nil {
		return HealthSnapshot{}, err
	}
	if err := ValidateUTCTime(snapshot.ChangedAt); err != nil {
		return HealthSnapshot{}, fmt.Errorf("changed at: %w", err)
	}
	if err := ValidateUTCTime(snapshot.EvidenceAt); err != nil {
		return HealthSnapshot{}, fmt.Errorf("evidence at: %w", err)
	}
	snapshot.ChangedAt = snapshot.ChangedAt.UTC()
	snapshot.EvidenceAt = snapshot.EvidenceAt.UTC()
	if snapshot.SourceObservedAt != nil {
		if err := ValidateUTCTime(*snapshot.SourceObservedAt); err != nil {
			return HealthSnapshot{}, err
		}
		observed := snapshot.SourceObservedAt.UTC()
		snapshot.SourceObservedAt = &observed
	}
	snapshot.Reason = CopyHealthReason(snapshot.Reason)
	return snapshot, nil
}

func validateSnapshotStatus(snapshot HealthSnapshot) error {
	switch snapshot.Status {
	case AdapterHealthHealthy:
		if snapshot.RuntimeID == nil || snapshot.Reason != nil {
			return errors.New("healthy snapshot requires runtime and no reason")
		}
	case AdapterHealthUnknown, AdapterHealthUnhealthy:
		if snapshot.Reason == nil {
			return errors.New("non-healthy snapshot requires reason")
		}
		if err := validateHealthReason(snapshot.Reason); err != nil {
			return err
		}
		if !strings.HasPrefix(snapshot.Reason.Code, "hearth.") && !strings.HasPrefix(snapshot.Reason.Code, "adapter.") {
			return errors.New("health reason must use hearth or adapter namespace")
		}
	default:
		return errors.New("invalid snapshot status")
	}
	return nil
}

// ValidateUTCTime enforces the timestamp boundary shared by runtime persistence
// and publication ports. Equivalent zero-offset RFC3339 spellings are accepted.
func ValidateUTCTime(value time.Time) error {
	_, offset := value.Zone()
	if value.IsZero() || offset != 0 || value.Year() < 1 || value.Year() > 9999 {
		return errors.New("nonzero UTC timestamp required")
	}
	return nil
}

// EncodeHealthSnapshot validates and encodes wire-named fields with lexically
// sorted keys, exact int64 revision, UTC RFC3339Nano times, and no whitespace.
func EncodeHealthSnapshot(snapshot HealthSnapshot) ([]byte, error) {
	normalized, err := NormalizeHealthSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	return encodeNormalizedHealthSnapshot(normalized)
}

func encodeNormalizedHealthSnapshot(snapshot HealthSnapshot) ([]byte, error) {
	fields := map[string]any{
		"adapter_id":       snapshot.AdapterID,
		snapshotRuntimeKey: snapshot.RuntimeID,
		"revision":         int64(snapshot.Revision),
		"status":           snapshot.Status,
		"source":           snapshot.Source,
		"changed_at": snapshot.ChangedAt.Format(
			time.RFC3339Nano,
		),
		"evidence_at": snapshot.EvidenceAt.Format(time.RFC3339Nano),
	}
	if snapshot.Reason != nil {
		fields["reason_code"] = snapshot.Reason.Code
	}
	if snapshot.SourceObservedAt != nil {
		fields["source_observed_at"] = snapshot.SourceObservedAt.Format(time.RFC3339Nano)
	}
	return json.Marshal(fields)
}

// DecodeHealthSnapshot accepts a strict wire-shaped JSON object (at most 16 KiB)
// and returns normalized evidence. Unknown/duplicate fields, explicit optional
// nulls, absent runtime_id, noninteger revisions, and trailing input reject.
func DecodeHealthSnapshot(body []byte) (HealthSnapshot, error) {
	fields, err := decodeSnapshotObject(body)
	if err != nil {
		return HealthSnapshot{}, err
	}
	snapshot, err := decodeSnapshotFields(fields)
	if err != nil {
		return HealthSnapshot{}, err
	}
	return NormalizeHealthSnapshot(snapshot)
}

// CanonicalizeHealthSnapshot decodes retained or incoming JSON and returns both
// detached normalized evidence and its canonical comparison bytes, preparing
// each exactly once within the receiving repository operation.
func CanonicalizeHealthSnapshot(body []byte) (HealthSnapshot, []byte, error) {
	snapshot, err := DecodeHealthSnapshot(body)
	if err != nil {
		return HealthSnapshot{}, nil, err
	}
	canonical, err := encodeNormalizedHealthSnapshot(snapshot)
	if err != nil {
		return HealthSnapshot{}, nil, err
	}
	return snapshot, canonical, nil
}

func decodeSnapshotObject(body []byte) (map[string]json.RawMessage, error) {
	if len(body) > 16*1024 || !utf8.Valid(body) {
		return nil, errors.New("invalid snapshot encoding")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("snapshot must be an object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("invalid snapshot key")
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, errors.New("duplicate snapshot key")
		}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	if _, err = decoder.Token(); err != nil {
		return nil, err
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing snapshot input")
	}
	return fields, nil
}

func decodeSnapshotFields(fields map[string]json.RawMessage) (HealthSnapshot, error) {
	var snapshot HealthSnapshot
	required := map[string]any{
		"adapter_id":       &snapshot.AdapterID,
		snapshotRuntimeKey: &snapshot.RuntimeID,
		"revision":         &snapshot.Revision,
		"status":           &snapshot.Status,
		"source":           &snapshot.Source,
		"changed_at":       &snapshot.ChangedAt,
		"evidence_at":      &snapshot.EvidenceAt,
	}
	for key, target := range required {
		raw, present := fields[key]
		if !present || (key != snapshotRuntimeKey && bytes.Equal(raw, []byte("null"))) {
			return HealthSnapshot{}, fmt.Errorf("missing snapshot %s", key)
		}
		if err := json.Unmarshal(raw, target); err != nil {
			return HealthSnapshot{}, fmt.Errorf("decode snapshot %s: %w", key, err)
		}
		delete(fields, key)
	}
	for key, raw := range fields {
		if bytes.Equal(raw, []byte("null")) {
			return HealthSnapshot{}, errors.New("optional snapshot fields cannot be null")
		}
		switch key {
		case "reason_code":
			var code string
			if err := json.Unmarshal(raw, &code); err != nil {
				return HealthSnapshot{}, err
			}
			snapshot.Reason = &HealthReason{Code: code}
		case "source_observed_at":
			var observed time.Time
			if err := json.Unmarshal(raw, &observed); err != nil {
				return HealthSnapshot{}, err
			}
			snapshot.SourceObservedAt = &observed
		default:
			return HealthSnapshot{}, fmt.Errorf("unknown snapshot field %s", key)
		}
	}
	return snapshot, nil
}
