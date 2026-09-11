package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// TestCoreStartupPreservesRetainedHistory proves startup performs no retention
// prune: even long-expired Entity Event and non-current Observation rows
// survive a restart and wait for the next hourly pass.
func TestCoreStartupPreservesRetainedHistory(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	seedStartupRetentionDatabase(ctx, t, databasePath)

	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})

	httpAddress := unusedLoopbackAddress(t)
	runContext, stopCore := context.WithCancel(ctx)
	defer stopCore()
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{HouseholdTimezone: "UTC",
			HTTPAddr: httpAddress, NATSURL: server.ClientURL(), SQLitePath: databasePath,
		}, slog.New(slog.DiscardHandler))
	}()
	waitForCoreHealthz(ctx, t, httpAddress, runErrors)

	observer, observerErr := platformdb.Open(ctx, databasePath)
	if observerErr != nil {
		t.Fatal(observerErr)
	}
	defer func() { _ = observer.Close() }()
	if retained := countRetainedRows(ctx, t, databasePath, "observations"); retained != 2 {
		t.Fatalf("observations after startup = %d, want 2", retained)
	}
	if retained := countRetainedRows(ctx, t, databasePath, "entity_events"); retained != 3 {
		t.Fatalf("entity events after startup = %d, want 3", retained)
	}

	stopCore()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hearthd did not stop")
	}
}

func seedStartupRetentionDatabase(ctx context.Context, t *testing.T, databasePath string) {
	t.Helper()
	database, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	if migrateErr := platformdb.Migrate(ctx, database); migrateErr != nil {
		t.Fatal(migrateErr)
	}
	seededAt := time.Now().UTC()
	seedTimestamp := seededAt.Format(time.RFC3339Nano)
	// Production stores observed_at fixed-width so the retention cutoff compares
	// lexicographically; RFC3339Nano would drop trailing zeros.
	sortableTimestamp := func(value time.Time) string {
		return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
	}
	if _, execErr := database.ExecContext(ctx, `
		INSERT INTO devices (id, kind, name, created_at, updated_at)
		VALUES ('dev_01890f47-7a6b-7c4d-8e9f-0123456789a1', 'light', 'Startup light', ?, ?)`,
		seedTimestamp, seedTimestamp,
	); execErr != nil {
		t.Fatal(execErr)
	}
	if _, execErr := database.ExecContext(ctx, `
		INSERT INTO entities (id, device_id, name, type_id, support_json, created_at, updated_at)
		VALUES ('ent_01890f47-7a6b-7c4d-8e9f-0123456789a1', 'dev_01890f47-7a6b-7c4d-8e9f-0123456789a1',
			'Power', 'test/v1', '{}', ?, ?)`,
		seedTimestamp, seedTimestamp,
	); execErr != nil {
		t.Fatal(execErr)
	}
	if _, execErr := database.ExecContext(ctx, `
		INSERT INTO observations (
			observation_id, adapter_id, entity_id, disposition,
			state_value_json, adapter_received_at, observed_at
		) VALUES
			('obs_01890f47-7a6b-7c4d-8e9f-0123456789a1', 'simulator', 'ent_01890f47-7a6b-7c4d-8e9f-0123456789a1',
				'applied', 'false', ?, ?),
			('obs_01890f47-7a6b-7c4d-8e9f-0123456789a2', 'simulator', 'ent_01890f47-7a6b-7c4d-8e9f-0123456789a1',
				'applied', 'true', ?, ?)`,
		seedTimestamp,
		sortableTimestamp(seededAt.Add(-60*24*time.Hour)),
		seedTimestamp,
		sortableTimestamp(seededAt.Add(-59*24*time.Hour)),
	); execErr != nil {
		t.Fatal(execErr)
	}
	if _, execErr := database.ExecContext(ctx, `
		INSERT INTO entity_states (
			entity_id, observation_id, value_json, adapter_received_at, observed_at, receive_order
		) SELECT 'ent_01890f47-7a6b-7c4d-8e9f-0123456789a1',
			'obs_01890f47-7a6b-7c4d-8e9f-0123456789a2', 'true', adapter_received_at,
			observed_at, receive_order
		  FROM observations WHERE observation_id = 'obs_01890f47-7a6b-7c4d-8e9f-0123456789a2'`,
	); execErr != nil {
		t.Fatal(execErr)
	}
	// Entity Events have no current-State anchor, so every expired row is
	// eligible for the next pass; startup must still leave all of them alone.
	if _, execErr := database.ExecContext(ctx, `
		INSERT INTO entity_events (
			event_id, adapter_id, runtime_id, entity_id, correlation_id, name,
			fingerprint, disposition, rejection_code, emitted_at, received_at, recorded_at
		) VALUES
			('evt_01890f47-7a6b-7c4d-8e9f-0123456789b1', 'simulator',
				'run_01890f47-7a6b-7c4d-8e9f-0123456789a1',
				'ent_01890f47-7a6b-7c4d-8e9f-0123456789a1',
				'cor_01890f47-7a6b-7c4d-8e9f-0123456789c1', 'single_press',
				zeroblob(32), 'accepted', NULL, ?, ?, ?),
			('evt_01890f47-7a6b-7c4d-8e9f-0123456789b2', 'simulator',
				'run_01890f47-7a6b-7c4d-8e9f-0123456789a1',
				'ent_01890f47-7a6b-7c4d-8e9f-0123456789a1',
				'cor_01890f47-7a6b-7c4d-8e9f-0123456789c2', 'double_press',
				zeroblob(32), 'rejected', 'unsupported_event', ?, ?, ?),
			('evt_01890f47-7a6b-7c4d-8e9f-0123456789b3', 'simulator',
				'run_01890f47-7a6b-7c4d-8e9f-0123456789a1',
				'ent_01890f47-7a6b-7c4d-8e9f-0123456789a1',
				'cor_01890f47-7a6b-7c4d-8e9f-0123456789c3', 'single_press',
				zeroblob(32), 'accepted', NULL, ?, ?, ?)`,
		sortableTimestamp(seededAt.Add(-60*24*time.Hour)), seedTimestamp,
		sortableTimestamp(seededAt.Add(-60*24*time.Hour)),
		sortableTimestamp(seededAt.Add(-90*24*time.Hour)), seedTimestamp,
		sortableTimestamp(seededAt.Add(-90*24*time.Hour)),
		seedTimestamp, seedTimestamp, seedTimestamp,
	); execErr != nil {
		t.Fatal(execErr)
	}
}

// waitForCoreHealthz waits until /healthz serves, which happens only after
// startup completes, so a 200 means the removed startup prune did not run
// before serving.
func waitForCoreHealthz(
	ctx context.Context,
	t *testing.T,
	httpAddress string,
	runErrors <-chan error,
) {
	t.Helper()
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		select {
		case runErr := <-runErrors:
			if runErr != nil {
				return false, runErr
			}
			return false, context.Canceled
		default:
		}
		request, requestErr := http.NewRequestWithContext(
			ctx, http.MethodGet, "http://"+httpAddress+"/healthz", nil,
		)
		if requestErr != nil {
			return false, requestErr
		}
		response, responseErr := http.DefaultClient.Do(request)
		if responseErr != nil {
			return false, nil
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK, nil
	})
}

func countRetainedRows(ctx context.Context, t *testing.T, databasePath, table string) int {
	t.Helper()
	database, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	var retained int
	if queryErr := database.QueryRowContext(
		ctx, "SELECT count(*) FROM "+table,
	).Scan(&retained); queryErr != nil {
		t.Fatal(queryErr)
	}
	return retained
}
