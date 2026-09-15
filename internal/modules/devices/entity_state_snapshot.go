package devices

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// EntityStateSnapshotEntry is the evidence for one explicitly requested Entity
// ID. It keeps negative evidence distinct: Exists=false means the requested
// Entity does not currently exist, while Exists=true with State=nil means it
// exists but has no accepted State. A copy owns its State JSON bytes.
type EntityStateSnapshotEntry struct {
	EntityID EntityID
	Exists   bool
	State    *State
}

// EntityStateSnapshot covers every explicitly requested Entity ID, including
// negative evidence. A missing map key means the ID was never requested; a
// present key always carries that Entity's own evidence, so a caller can tell
// "not covered" from "covered but missing".
type EntityStateSnapshot struct {
	Entries map[EntityID]EntityStateSnapshotEntry
}

// ErrEntityStateSnapshotCorrupt diagnoses unusable stored State evidence, not a
// malformed Device Fact. It is permanent for a given stored row: redelivery
// cannot repair bytes Core cannot interpret. It never matches
// [ErrInvalidDeviceFact] and never enters the Device Fact termination path, so a
// corrupt State read is a retryable admission failure the automations consumer
// negatively acknowledges.
var ErrEntityStateSnapshotCorrupt = errors.New("entity state snapshot contains corrupt stored evidence")

// GetEntityStateSnapshot reads one coherent State snapshot for exactly the
// requested Entity IDs. It validates, canonicalizes, deduplicates, and sorts
// the request first, returns an empty snapshot without touching persistence for
// an empty request, and returns every requested ID exactly once or an error for
// the whole call.
//
// It never filters by enablement, availability, owner health, or current Entity
// type support: retained State stays readable, and interpreting old State
// against current support is an evaluation decision, not a read decision. The
// returned snapshot covers the requested IDs only; a definition that needs more
// evidence must supply them, because a missing key is not negative evidence.
func (service *Service) GetEntityStateSnapshot(
	ctx context.Context,
	ids []EntityID,
) (EntityStateSnapshot, error) {
	requested, err := canonicalizeEntityStateSnapshotRequest(ids)
	if err != nil {
		return EntityStateSnapshot{}, err
	}
	if len(requested) == 0 {
		return EntityStateSnapshot{Entries: map[EntityID]EntityStateSnapshotEntry{}}, nil
	}
	snapshot, err := service.stores.Reads.GetEntityStateSnapshot(ctx, requested)
	if err != nil {
		return EntityStateSnapshot{}, err
	}
	return validateEntityStateSnapshot(requested, snapshot)
}

// canonicalizeEntityStateSnapshotRequest checks every requested ID against the
// canonical Entity ID grammar, removes duplicates, and sorts the remainder.
// Sorting makes the requested relation deterministic, so one extra read is
// never triggered by ordering alone, and deduplication keeps one snapshot entry
// per Entity.
func canonicalizeEntityStateSnapshotRequest(ids []EntityID) ([]EntityID, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	canonical := make([]EntityID, 0, len(ids))
	seen := make(map[EntityID]struct{}, len(ids))
	for _, id := range ids {
		if _, err := ParseEntityID(string(id)); err != nil {
			return nil, fmt.Errorf("parse entity state snapshot entity ID: %w", err)
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		canonical = append(canonical, id)
	}
	slices.Sort(canonical)
	return canonical, nil
}

// validateEntityStateSnapshot turns persistence rows into owned domain evidence
// and rejects a snapshot that cannot be trusted as a whole: a coverage mismatch,
// an entry that disagrees with its key, an absent Entity that still carries
// State, or a State whose identity disagrees with the key. Every rejection is
// [ErrEntityStateSnapshotCorrupt], because a partial or self-contradictory
// snapshot must never reach the evaluator as if it were evidence.
func validateEntityStateSnapshot(
	requested []EntityID,
	snapshot EntityStateSnapshot,
) (EntityStateSnapshot, error) {
	if len(snapshot.Entries) != len(requested) {
		return EntityStateSnapshot{}, fmt.Errorf(
			"%w: snapshot covers %d of %d requested entities",
			ErrEntityStateSnapshotCorrupt, len(snapshot.Entries), len(requested),
		)
	}
	entries := make(map[EntityID]EntityStateSnapshotEntry, len(requested))
	for _, id := range requested {
		entry, ok := snapshot.Entries[id]
		if !ok {
			return EntityStateSnapshot{}, fmt.Errorf(
				"%w: snapshot omits requested entity %q",
				ErrEntityStateSnapshotCorrupt, id,
			)
		}
		if entry.EntityID != id {
			return EntityStateSnapshot{}, fmt.Errorf(
				"%w: entry %q disagrees with requested entity %q",
				ErrEntityStateSnapshotCorrupt, entry.EntityID, id,
			)
		}
		if !entry.Exists && entry.State != nil {
			return EntityStateSnapshot{}, fmt.Errorf(
				"%w: absent entity %q still carries State",
				ErrEntityStateSnapshotCorrupt, id,
			)
		}
		if entry.State != nil {
			if entry.State.EntityID != id {
				return EntityStateSnapshot{}, fmt.Errorf(
					"%w: State for entity %q identifies as %q",
					ErrEntityStateSnapshotCorrupt, id, entry.State.EntityID,
				)
			}
			if _, err := ParseObservationID(string(entry.State.ObservationID)); err != nil {
				return EntityStateSnapshot{}, fmt.Errorf(
					"%w: State for entity %q has invalid Observation identity",
					ErrEntityStateSnapshotCorrupt, id,
				)
			}
		}
		entries[id] = copyEntityStateSnapshotEntry(entry)
	}
	return EntityStateSnapshot{Entries: entries}, nil
}

func copyEntityStateSnapshotEntry(entry EntityStateSnapshotEntry) EntityStateSnapshotEntry {
	cloned := EntityStateSnapshotEntry{EntityID: entry.EntityID, Exists: entry.Exists}
	if entry.State != nil {
		state := copyState(*entry.State)
		cloned.State = &state
	}
	return cloned
}
