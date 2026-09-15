package sqlite //nolint:testpackage // Tests exercise package-private SQLite persistence behavior.

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// snapshotObservedAt is the fixed broker-assigned Observation time the snapshot
// fixtures use. It is a function rather than a global so no test can mutate the
// shared clock.
func snapshotObservedAt() time.Time {
	return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
}

// registerSnapshotEntities registers one power Entity and one brightness Entity
// so a snapshot test can observe one, leave the other never-observed, and ask
// about a third Entity that was never registered.
func registerSnapshotEntities(
	t *testing.T,
	database *sql.DB,
) (*devices.Service, devices.EntityID, devices.EntityID) {
	t.Helper()
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	service := newTestService(
		repository,
		nil,
		catalog,
		devices.Dependencies{Now: snapshotObservedAt},
	)
	binding, err := service.Register(
		context.Background(), "simulator", testRuntimeID, multiEntityRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(binding.Entities) != 2 {
		t.Fatalf("registered entities = %d, want 2", len(binding.Entities))
	}
	return service, binding.Entities[0].EntityID, binding.Entities[1].EntityID
}

func observeSnapshotEntity(t *testing.T, service *devices.Service, entityID devices.EntityID, value string) {
	t.Helper()
	observation := newObservation(t, entityID, value, snapshotObservedAt())
	result, err := service.ProjectObservation(
		context.Background(), "simulator", testRuntimeID, observation, snapshotObservedAt(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != devices.DispositionApplied {
		t.Fatalf("observation disposition = %q, want applied", result.Disposition)
	}
}

func TestGetEntityStateSnapshotDistinguishesPresentNeverObservedAndMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service, present, neverObserved := registerSnapshotEntities(t, database)
	observeSnapshotEntity(t, service, present, `true`)
	missing := newTestEntityID(t)

	snapshot, err := service.GetEntityStateSnapshot(
		ctx, []devices.EntityID{neverObserved, present, missing, present},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries) != 3 {
		t.Fatalf("deduplicated snapshot entries = %d, want 3", len(snapshot.Entries))
	}
	presentEntry := snapshot.Entries[present]
	if !presentEntry.Exists || presentEntry.State == nil || string(presentEntry.State.Value) != `true` ||
		presentEntry.State.EntityID != present || presentEntry.State.ObservationID == "" {
		t.Fatalf("present entry = %#v", presentEntry)
	}
	if entry := snapshot.Entries[neverObserved]; !entry.Exists || entry.State != nil {
		t.Fatalf("never-observed entry = %#v", entry)
	}
	if entry := snapshot.Entries[missing]; entry.Exists || entry.State != nil || entry.EntityID != missing {
		t.Fatalf("missing entry = %#v", entry)
	}
}

func TestGetEntityStateSnapshotEmptyRequestDoesNotTouchSQLite(t *testing.T) {
	t.Parallel()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service, present, _ := registerSnapshotEntities(t, database)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := service.GetEntityStateSnapshot(context.Background(), nil)
	if err != nil {
		t.Fatalf("empty request failed after the database closed: %v", err)
	}
	if snapshot.Entries == nil || len(snapshot.Entries) != 0 {
		t.Fatalf("empty snapshot = %#v", snapshot)
	}
	if _, snapshotErr := service.GetEntityStateSnapshot(
		context.Background(), []devices.EntityID{present},
	); snapshotErr == nil {
		t.Fatal("non-empty request succeeded after the database closed, want SQL failure")
	}
}

func TestGetEntityStateSnapshotReturnsOwnedStateBytes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service, present, _ := registerSnapshotEntities(t, database)
	observeSnapshotEntity(t, service, present, `true`)

	first, err := service.GetEntityStateSnapshot(ctx, []devices.EntityID{present})
	if err != nil {
		t.Fatal(err)
	}
	first.Entries[present].State.Value[0] = 'X'

	second, err := service.GetEntityStateSnapshot(ctx, []devices.EntityID{present})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(second.Entries[present].State.Value); got != `true` {
		t.Fatalf("second snapshot value = %s, want owned bytes", got)
	}
}

func TestGetEntityStateSnapshotReportsCorruptStoredState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service, present, _ := registerSnapshotEntities(t, database)
	observeSnapshotEntity(t, service, present, `true`)

	if _, err := database.ExecContext(
		ctx,
		`UPDATE entity_states SET observed_at = 'not-a-timestamp' WHERE entity_id = ?`,
		string(present),
	); err != nil {
		t.Fatal(err)
	}
	_, err := service.GetEntityStateSnapshot(ctx, []devices.EntityID{present})
	if !errors.Is(err, devices.ErrEntityStateSnapshotCorrupt) {
		t.Fatalf("corrupt State error = %v, want ErrEntityStateSnapshotCorrupt", err)
	}
	if errors.Is(err, devices.ErrInvalidEntityEvent) {
		t.Fatalf("corrupt State error classified as an Entity Event failure: %v", err)
	}
}

func TestGetEntityStateSnapshotReportsInvalidStoredStateJSON(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service, present, _ := registerSnapshotEntities(t, database)
	observeSnapshotEntity(t, service, present, `true`)

	// The stored-State CHECK normally forbids invalid JSON, so one test turns it
	// off on the single pooled connection, corrupts the row, and restores it. A
	// real database can only hold this through repair drift, which is exactly the
	// evidence the snapshot read must refuse.
	if _, err := database.ExecContext(ctx, "PRAGMA ignore_check_constraints = ON"); err != nil {
		t.Skipf("installed SQLite cannot ignore check constraints: %v", err)
	}
	if _, err := database.ExecContext(
		ctx,
		`UPDATE entity_states SET value_json = 'not json' WHERE entity_id = ?`,
		string(present),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "PRAGMA ignore_check_constraints = OFF"); err != nil {
		t.Fatal(err)
	}

	_, err := service.GetEntityStateSnapshot(ctx, []devices.EntityID{present})
	if !errors.Is(err, devices.ErrEntityStateSnapshotCorrupt) {
		t.Fatalf("invalid stored State JSON error = %v, want ErrEntityStateSnapshotCorrupt", err)
	}
}

// A stored State whose Observation identity is prefixed but not a canonical
// Observation ID is unusable evidence: the scanner must classify it as corrupt
// before any evaluator sees it. The Observation foreign key only enforces
// existence, so this test injects the drift with enforcement off on the single
// pooled connection.
func TestGetEntityStateSnapshotReportsNonCanonicalObservationID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service, present, _ := registerSnapshotEntities(t, database)
	observeSnapshotEntity(t, service, present, `true`)

	if _, err := database.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(
		ctx,
		`UPDATE entity_states SET observation_id = 'obs_not-canonical' WHERE entity_id = ?`,
		string(present),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}

	_, err := service.GetEntityStateSnapshot(ctx, []devices.EntityID{present})
	if !errors.Is(err, devices.ErrEntityStateSnapshotCorrupt) {
		t.Fatalf("non-canonical Observation ID error = %v, want ErrEntityStateSnapshotCorrupt", err)
	}
	if errors.Is(err, devices.ErrInvalidEntityEvent) {
		t.Fatalf("non-canonical Observation ID classified as an Entity Event failure: %v", err)
	}
}

// snapshotObservationRow is the stored identity and receive order of one
// Observations row a corruption test repoints a materialized State at.
type snapshotObservationRow struct {
	observationID devices.ObservationID
	receiveOrder  int64
}

// snapshotObservations reads the stored Observation rows for one Entity in
// receive order, so a corruption test can name the identity and receive order a
// State must agree with instead of guessing them.
func snapshotObservations(
	t *testing.T,
	database *sql.DB,
	entityID devices.EntityID,
) []snapshotObservationRow {
	t.Helper()
	rows, err := database.QueryContext(
		context.Background(),
		`SELECT observation_id, receive_order FROM observations WHERE entity_id = ? ORDER BY receive_order`,
		string(entityID),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var observations []snapshotObservationRow
	for rows.Next() {
		var observation snapshotObservationRow
		if scanErr := rows.Scan(&observation.observationID, &observation.receiveOrder); scanErr != nil {
			t.Fatal(scanErr)
		}
		observations = append(observations, observation)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatal(rowsErr)
	}
	if len(observations) == 0 {
		t.Fatalf("entity %q has no Observation rows to corrupt", entityID)
	}
	return observations
}

// insertSnapshotObservation writes one Observations row directly, so a
// corruption test can point a materialized State at evidence that is not its own
// accepted Observation without projecting through the normal write path. It
// returns the stored identity and receive order.
func insertSnapshotObservation(
	ctx context.Context,
	t *testing.T,
	database *sql.DB,
	entityID devices.EntityID,
	disposition devices.ObservationDisposition,
	value string,
	observedAt time.Time,
) snapshotObservationRow {
	t.Helper()
	observation, observationErr := devices.NewObservationID()
	if observationErr != nil {
		t.Fatal(observationErr)
	}
	var stateValue, rejectionCode any
	if disposition == devices.DispositionRejected {
		rejectionCode = string(devices.RejectionInvalidValue)
	} else {
		stateValue = value
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO observations (
			observation_id, adapter_id, entity_id, disposition, rejection_code,
			state_value_json, adapter_received_at, observed_at
		) VALUES (?, 'simulator', ?, ?, ?, ?, ?, ?)`,
		string(observation), string(entityID), string(disposition), rejectionCode, stateValue,
		formatTime(observedAt), formatSortableTime(observedAt),
	); err != nil {
		t.Fatal(err)
	}
	var receiveOrder int64
	if err := database.QueryRowContext(
		ctx, `SELECT receive_order FROM observations WHERE observation_id = ?`, string(observation),
	).Scan(&receiveOrder); err != nil {
		t.Fatal(err)
	}
	return snapshotObservationRow{observationID: observation, receiveOrder: receiveOrder}
}

// pointStateAtObservation repoints one materialized State at the Observation
// identity and receive order the caller supplies. entity_states stores those as
// two independent foreign keys, so the update satisfies both constraints while
// still being able to name a State that is not the entity's own accepted
// Observation.
func pointStateAtObservation(
	ctx context.Context,
	t *testing.T,
	database *sql.DB,
	entityID devices.EntityID,
	observation snapshotObservationRow,
) {
	t.Helper()
	if _, err := database.ExecContext(ctx,
		`UPDATE entity_states SET observation_id = ?, receive_order = ? WHERE entity_id = ?`,
		string(observation.observationID), observation.receiveOrder, string(entityID),
	); err != nil {
		t.Fatal(err)
	}
}

// TestGetEntityStateSnapshotRejectsStateWithoutTrustworthyBackingObservation
// proves the snapshot read refuses a materialized State whose own claimed
// evidence does not back it. entity_states.observation_id and
// entity_states.receive_order are separate foreign keys, so each corruption
// below leaves both keys valid while making the State unusable: an Observation
// of another Entity, an identity and receive order that name different rows, a
// rejected Observation, and a retained value or timestamp the Observation does
// not repeat. Every case must fail the whole read, so the corrupt State can
// never reach the Condition evaluator as evidence.
func TestGetEntityStateSnapshotRejectsStateWithoutTrustworthyBackingObservation(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name    string
		corrupt func(t *testing.T, ctx context.Context, database *sql.DB, observed, other devices.EntityID)
	}{
		{
			name: "backing Observation belongs to another Entity",
			corrupt: func(t *testing.T, ctx context.Context, database *sql.DB, observed, other devices.EntityID) {
				t.Helper()
				// A fresh Observation for the other Entity is unreferenced, so the
				// State can name it without colliding with that Entity's own State.
				trespassing := insertSnapshotObservation(
					ctx, t, database, other, devices.DispositionApplied, `50`, snapshotObservedAt(),
				)
				pointStateAtObservation(ctx, t, database, observed, trespassing)
			},
		},
		{
			name: "identity and receive order name different Observations",
			corrupt: func(t *testing.T, ctx context.Context, database *sql.DB, observed, _ devices.EntityID) {
				t.Helper()
				observations := snapshotObservations(t, database, observed)
				if len(observations) < 2 {
					t.Fatalf("entity %q needs two Observations for this corruption", observed)
				}
				// The State keeps the newer Observation identity but takes the older
				// Observation's receive order, so each foreign key stays valid while
				// no single row satisfies both.
				if _, err := database.ExecContext(ctx,
					`UPDATE entity_states SET receive_order = ? WHERE entity_id = ?`,
					observations[0].receiveOrder, string(observed),
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "backing Observation was rejected",
			corrupt: func(t *testing.T, ctx context.Context, database *sql.DB, observed, _ devices.EntityID) {
				t.Helper()
				rejected := insertSnapshotObservation(
					ctx, t, database, observed, devices.DispositionRejected, ``, snapshotObservedAt(),
				)
				pointStateAtObservation(ctx, t, database, observed, rejected)
			},
		},
		{
			name: "retained value disagrees with the backing Observation",
			corrupt: func(t *testing.T, ctx context.Context, database *sql.DB, observed, _ devices.EntityID) {
				t.Helper()
				if _, err := database.ExecContext(ctx,
					`UPDATE entity_states SET value_json = 'true' WHERE entity_id = ?`,
					string(observed),
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "observed_at disagrees with the backing Observation",
			corrupt: func(t *testing.T, ctx context.Context, database *sql.DB, observed, _ devices.EntityID) {
				t.Helper()
				if _, err := database.ExecContext(ctx,
					`UPDATE entity_states SET observed_at = ? WHERE entity_id = ?`,
					formatTime(snapshotObservedAt().Add(time.Hour)), string(observed),
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "adapter_received_at disagrees with the backing Observation",
			corrupt: func(t *testing.T, ctx context.Context, database *sql.DB, observed, _ devices.EntityID) {
				t.Helper()
				if _, err := database.ExecContext(ctx,
					`UPDATE entity_states SET adapter_received_at = ? WHERE entity_id = ?`,
					formatTime(snapshotObservedAt().Add(2*time.Hour)), string(observed),
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "source_updated_at disagrees with the backing Observation",
			corrupt: func(t *testing.T, ctx context.Context, database *sql.DB, observed, _ devices.EntityID) {
				t.Helper()
				if _, err := database.ExecContext(ctx,
					`UPDATE entity_states SET source_updated_at = ? WHERE entity_id = ?`,
					formatTime(snapshotObservedAt()), string(observed),
				); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
			service, observed, other := registerSnapshotEntities(t, database)
			// Two Observations for the observed Entity leave the State pointing at
			// the newer one while the older one stays available as drift evidence.
			observeSnapshotEntity(t, service, observed, `true`)
			observeSnapshotEntity(t, service, observed, `false`)
			observeSnapshotEntity(t, service, other, `50`)

			test.corrupt(t, ctx, database, observed, other)

			if _, err := service.GetEntityStateSnapshot(
				ctx, []devices.EntityID{observed, other},
			); !errors.Is(err, devices.ErrEntityStateSnapshotCorrupt) {
				t.Fatalf("corrupt backing evidence error = %v, want ErrEntityStateSnapshotCorrupt", err)
			}
		})
	}
}

// TestGetEntityStateSnapshotReportsMissingBackingObservation covers repair
// drift rather than a constraint the schema can express: the State still names
// an Observation row that no longer exists. Foreign keys are turned off on the
// single pooled connection to create that state, and the snapshot read must
// still refuse to serve a State with no evidence identity.
func TestGetEntityStateSnapshotReportsMissingBackingObservation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service, observed, _ := registerSnapshotEntities(t, database)
	observeSnapshotEntity(t, service, observed, `true`)

	observations := snapshotObservations(t, database, observed)
	if _, err := database.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(
		ctx, `DELETE FROM observations WHERE observation_id = ?`, string(observations[0].observationID),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}

	_, err := service.GetEntityStateSnapshot(ctx, []devices.EntityID{observed})
	if !errors.Is(err, devices.ErrEntityStateSnapshotCorrupt) {
		t.Fatalf("missing backing Observation error = %v, want ErrEntityStateSnapshotCorrupt", err)
	}
}

// TestGetEntityStateSnapshotReadsWholeCommittedPair proves one snapshot
// statement cannot observe a half-applied batch: an independent writer replaces
// two Entity States, each together with its backing Observation, in one
// transaction, and every read sees either the complete old pair or the complete
// new pair, never a mix.
func TestGetEntityStateSnapshotReadsWholeCommittedPair(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hearth.db")
	database := openRegistrationDatabase(t, path)
	service, first, second := registerSnapshotEntities(t, database)
	observeSnapshotEntity(t, service, first, `true`)
	observeSnapshotEntity(t, service, second, `50`)

	writer, err := platformdb.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()

	previousFirst, previousSecond := `true`, `50`
	for round := range 20 {
		nextFirst := strconv.FormatBool(round%2 == 0)
		nextSecond := strconv.Itoa(60 + round)

		transaction, beginErr := writer.BeginTx(ctx, nil)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		for _, update := range []struct {
			value string
			id    devices.EntityID
		}{{nextFirst, first}, {nextSecond, second}} {
			// The State and the Observation backing it are one committed unit, so
			// the writer replaces both in the same transaction.
			if _, updateErr := transaction.ExecContext(
				ctx, `UPDATE entity_states SET value_json = ? WHERE entity_id = ?`,
				update.value, string(update.id),
			); updateErr != nil {
				t.Fatal(updateErr)
			}
			if _, updateErr := transaction.ExecContext(
				ctx,
				`UPDATE observations SET state_value_json = ?
				 WHERE observation_id = (
				     SELECT observation_id FROM entity_states WHERE entity_id = ?
				 )`,
				update.value, string(update.id),
			); updateErr != nil {
				t.Fatal(updateErr)
			}
		}
		assertSnapshotPair(t, service, first, second, previousFirst, previousSecond)
		if commitErr := transaction.Commit(); commitErr != nil {
			t.Fatal(commitErr)
		}
		assertSnapshotPair(t, service, first, second, nextFirst, nextSecond)
		previousFirst, previousSecond = nextFirst, nextSecond
	}
}

func assertSnapshotPair(
	t *testing.T,
	service *devices.Service,
	first devices.EntityID,
	second devices.EntityID,
	wantFirst string,
	wantSecond string,
) {
	t.Helper()
	snapshot, err := service.GetEntityStateSnapshot(
		context.Background(), []devices.EntityID{first, second},
	)
	if err != nil {
		t.Fatal(err)
	}
	gotFirst := string(snapshot.Entries[first].State.Value)
	gotSecond := string(snapshot.Entries[second].State.Value)
	if gotFirst != wantFirst || gotSecond != wantSecond {
		t.Fatalf(
			"snapshot pair = (%s, %s), want the whole committed pair (%s, %s)",
			gotFirst, gotSecond, wantFirst, wantSecond,
		)
	}
}
