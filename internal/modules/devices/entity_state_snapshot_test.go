package devices //nolint:testpackage // Tests exercise package-private domain seams and repository fixtures.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

const (
	snapshotEntityID      = EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789a1")
	snapshotObservationID = ObservationID("obs_01890f47-7a6b-7c4d-8e9f-0123456789a1")
)

func snapshotEntry(id EntityID, exists bool, value string) EntityStateSnapshotEntry {
	entry := EntityStateSnapshotEntry{EntityID: id, Exists: exists}
	if value != "" {
		entry.State = &State{
			EntityID: id, Value: Value(value), ObservationID: snapshotObservationID,
			ObservedAt: time.Unix(0, 0).UTC(),
		}
	}
	return entry
}

func TestGetEntityStateSnapshotReturnsEmptyWithoutRepositoryRead(t *testing.T) {
	t.Parallel()
	repository := newReadRepository()
	service := newTestService(repository, nil, nil, Dependencies{})

	snapshot, err := service.GetEntityStateSnapshot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Entries == nil || len(snapshot.Entries) != 0 {
		t.Fatalf("empty snapshot = %#v", snapshot)
	}
	if repository.snapshotCalls != 0 {
		t.Fatalf("empty request read the repository %d times", repository.snapshotCalls)
	}
}

func TestGetEntityStateSnapshotCanonicalizesDeduplicatesAndSortsRequest(t *testing.T) {
	t.Parallel()
	repository := newReadRepository()
	service := newTestService(repository, nil, nil, Dependencies{})
	low := EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789a1")
	high := EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789a2")
	repository.snapshot = EntityStateSnapshot{
		Entries: map[EntityID]EntityStateSnapshotEntry{ //nolint:exhaustive // Only the IDs under test need entries.
			low:  snapshotEntry(low, false, ""),
			high: snapshotEntry(high, true, `true`),
		},
	}

	snapshot, err := service.GetEntityStateSnapshot(
		context.Background(), []EntityID{high, low, high, low},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries) != 2 {
		t.Fatalf("snapshot entries = %#v", snapshot.Entries)
	}
	if want := []EntityID{low, high}; !slices.Equal(repository.snapshotIDs, want) {
		t.Fatalf("repository request = %#v, want %#v", repository.snapshotIDs, want)
	}
}

func TestGetEntityStateSnapshotKeepsPresentNeverObservedAndMissingDistinct(t *testing.T) {
	t.Parallel()
	repository := newReadRepository()
	service := newTestService(repository, nil, nil, Dependencies{})
	present := EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789a1")
	neverObserved := EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789a2")
	missing := EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789a3")
	repository.snapshot = EntityStateSnapshot{
		Entries: map[EntityID]EntityStateSnapshotEntry{ //nolint:exhaustive // Only the IDs under test need entries.
			present:       snapshotEntry(present, true, `true`),
			neverObserved: snapshotEntry(neverObserved, true, ""),
			missing:       snapshotEntry(missing, false, ""),
		},
	}

	snapshot, err := service.GetEntityStateSnapshot(
		context.Background(), []EntityID{present, neverObserved, missing},
	)
	if err != nil {
		t.Fatal(err)
	}
	if entry := snapshot.Entries[present]; !entry.Exists || entry.State == nil ||
		string(entry.State.Value) != `true` {
		t.Fatalf("present entry = %#v", entry)
	}
	if entry := snapshot.Entries[neverObserved]; !entry.Exists || entry.State != nil {
		t.Fatalf("never-observed entry = %#v", entry)
	}
	if entry := snapshot.Entries[missing]; entry.Exists || entry.State != nil {
		t.Fatalf("missing entry = %#v", entry)
	}
}

func TestGetEntityStateSnapshotOwnsStateBytes(t *testing.T) {
	t.Parallel()
	repository := newReadRepository()
	service := newTestService(repository, nil, nil, Dependencies{})
	repository.snapshot = EntityStateSnapshot{
		Entries: map[EntityID]EntityStateSnapshotEntry{ //nolint:exhaustive // Only the IDs under test need entries.
			snapshotEntityID: snapshotEntry(snapshotEntityID, true, `{"on":true}`),
		},
	}

	first, err := service.GetEntityStateSnapshot(context.Background(), []EntityID{snapshotEntityID})
	if err != nil {
		t.Fatal(err)
	}
	first.Entries[snapshotEntityID].State.Value[0] = 'X'
	if stored := repository.snapshot.Entries[snapshotEntityID].State.Value; string(stored) != `{"on":true}` {
		t.Fatalf("caller mutation reached stored bytes: %s", stored)
	}
	second, err := service.GetEntityStateSnapshot(context.Background(), []EntityID{snapshotEntityID})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(second.Entries[snapshotEntityID].State.Value); got != `{"on":true}` {
		t.Fatalf("second snapshot value = %s", got)
	}
}

func TestGetEntityStateSnapshotRejectsInconsistentEvidence(t *testing.T) {
	t.Parallel()
	other := EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789a2")
	absentWithState := snapshotEntry(snapshotEntityID, false, "")
	absentWithState.State = &State{EntityID: snapshotEntityID, Value: Value(`true`)}
	misidentified := EntityStateSnapshotEntry{
		EntityID: other, Exists: true,
		State: &State{EntityID: other, Value: Value(`true`)},
	}

	for _, test := range []struct {
		name    string
		entries map[EntityID]EntityStateSnapshotEntry
	}{
		{name: "omitted requested entity", entries: map[EntityID]EntityStateSnapshotEntry{}},
		{name: "absent entity with state", entries: map[EntityID]EntityStateSnapshotEntry{ //nolint:exhaustive // Only the IDs under test need entries.
			snapshotEntityID: absentWithState,
		}},
		{name: "entry key disagrees with evidence", entries: map[EntityID]EntityStateSnapshotEntry{ //nolint:exhaustive // Only the IDs under test need entries.
			snapshotEntityID: misidentified,
		}},
		{name: "state identity disagrees with key", entries: map[EntityID]EntityStateSnapshotEntry{ //nolint:exhaustive // Only the IDs under test need entries.
			snapshotEntityID: {
				EntityID: snapshotEntityID, Exists: true,
				State: &State{EntityID: other, Value: Value(`true`)},
			},
		}},
		{name: "state has noncanonical Observation identity", entries: map[EntityID]EntityStateSnapshotEntry{ //nolint:exhaustive // Only the IDs under test need entries.
			snapshotEntityID: {
				EntityID: snapshotEntityID, Exists: true,
				State: &State{
					EntityID: snapshotEntityID, Value: Value(`true`),
					ObservationID: ObservationID("obs_not-canonical"),
				},
			},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := newReadRepository()
			repository.snapshot = EntityStateSnapshot{Entries: test.entries}
			service := newTestService(repository, nil, nil, Dependencies{})
			_, err := service.GetEntityStateSnapshot(context.Background(), []EntityID{snapshotEntityID})
			if !errors.Is(err, ErrEntityStateSnapshotCorrupt) {
				t.Fatalf("error = %v, want ErrEntityStateSnapshotCorrupt", err)
			}
		})
	}
}

func TestGetEntityStateSnapshotRejectsMalformedRequestBeforeRead(t *testing.T) {
	t.Parallel()
	repository := newReadRepository()
	service := newTestService(repository, nil, nil, Dependencies{})

	_, err := service.GetEntityStateSnapshot(
		context.Background(), []EntityID{"not-an-entity", snapshotEntityID},
	)
	if err == nil {
		t.Fatal("malformed request accepted")
	}
	if repository.snapshotCalls != 0 {
		t.Fatalf("malformed request read the repository %d times", repository.snapshotCalls)
	}
}
