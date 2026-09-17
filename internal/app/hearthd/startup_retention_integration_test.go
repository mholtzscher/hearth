package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// TestCoreStartupPrunesRetainedHistory proves the real startup path runs one
// retention pass while readiness stays independent: the current-State
// Observation anchor survives while the expired non-current Observation and
// every expired Entity Event are deleted, expired terminal Automation history is
// deleted through the injected retention, and fresh runs plus matched-Fact
// receipts stay.
func TestCoreStartupPrunesRetainedHistory(t *testing.T) {
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
	// A non-pooling client avoids leaving an idle keep-alive connection that the
	// HTTP server would wait on until its read-header timeout at shutdown.
	client := newNonPoolingHTTPClient(t)
	runContext, stopCore := context.WithCancel(ctx)
	defer stopCore()
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{HouseholdTimezone: "UTC",
			HTTPAddr: httpAddress, NATSURL: server.ClientURL(), SQLitePath: databasePath,
		}, slog.New(slog.DiscardHandler))
	}()
	waitForCoreHTTPStatus(ctx, t, client, httpAddress, "/healthz", runErrors)
	// Readiness must not wait for the startup sweep: the HTTP readiness surface
	// serves before, during, and after the pass.
	waitForCoreHTTPStatus(ctx, t, client, httpAddress, "/readyz", runErrors)

	observer, observerErr := platformdb.Open(ctx, databasePath)
	if observerErr != nil {
		t.Fatal(observerErr)
	}
	defer func() { _ = observer.Close() }()
	// The startup pass runs inside the retention worker, so wait for its effect
	// instead of assuming it finished before serving.
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		select {
		case runErr := <-runErrors:
			if runErr != nil {
				return false, runErr
			}
			return false, context.Canceled
		default:
		}
		return countRetentionRows(ctx, observer, "observations") == 1 &&
			countRetentionRows(ctx, observer, "entity_events") == 1 &&
			countRetentionRows(ctx, observer, "automation_history") == 2 &&
			countRetentionRows(ctx, observer, "automation_fact_receipts") == 1, nil
	})
	// The surviving Observation is the current-State anchor, the surviving
	// Entity Event is the fresh one, and the older terminal Automation Run was
	// deleted while the fresh Run and the interrupted startup Run stayed.
	assertRetentionIDs(ctx, t, observer, "observations", "observation_id",
		[]string{"obs_01890f47-7a6b-7c4d-8e9f-0123456789a2"})
	assertRetentionIDs(ctx, t, observer, "entity_events", "event_id",
		[]string{"evt_01890f47-7a6b-7c4d-8e9f-0123456789b3"})
	assertRetentionIDs(ctx, t, observer, "automation_history", "id", []string{
		"arn_01890f47-7a6b-7c4d-8e9f-0123456789a2",
		"arn_01890f47-7a6b-7c4d-8e9f-0123456789a3",
	})

	stopCore()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("hearthd did not stop")
	}
}

// TestCoreStartupPruneFailureKeepsReadiness proves a real startup retention
// failure is isolated: SQLite rejects deleting the seeded expired Observation,
// so the devices pass fails through the real database path and logs one safe
// failure record for the devices module without raw error text, while the
// automations pass still deletes its expired history and /readyz stays OK.
func TestCoreStartupPruneFailureKeepsReadiness(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	seedStartupRetentionDatabase(ctx, t, databasePath)
	blockExpiredObservationPrune(ctx, t, databasePath, startupPruneBlockerSecret)

	server := startLifecycleNATSServer(t)
	httpAddress := freeLoopbackAddr(t)
	// A non-pooling client avoids leaving an idle keep-alive connection that the
	// HTTP server would wait on until its read-header timeout at shutdown.
	client := newNonPoolingHTTPClient(t)
	logger, recorder := withRecording(slog.LevelDebug)
	runContext, stopCore := context.WithCancel(ctx)
	defer stopCore()
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{HouseholdTimezone: "UTC",
			HTTPAddr: httpAddress, NATSURL: server.ClientURL(), SQLitePath: databasePath,
		}, logger)
	}()
	waitForCoreHTTPStatus(ctx, t, client, httpAddress, "/healthz", runErrors)
	// The failed startup sweep must not make Core unready: /readyz serves OK
	// before, during, and after the pass.
	waitForCoreHTTPStatus(ctx, t, client, httpAddress, "/readyz", runErrors)

	failure := waitForRecord(t, recorder, "core.devices_history_prune_failed", 15*time.Second)
	if failure.Level != slog.LevelError {
		t.Fatalf("devices prune failure level = %v, want Error", failure.Level)
	}
	requireRecordAttr(t, failure, "error_code", "devices_history_prune_failed")
	requireRecordAttr(t, failure, "module", devicesHistoryPruneModule)
	for _, record := range recorder.snapshot() {
		requireNoRawHistoryPruneError(t, record, startupPruneBlockerSecret)
	}
	if got := len(recordsWithEvent(recorder.snapshot(), "core.automation_history_prune_failed")); got != 0 {
		t.Fatalf("automation failure records = %d, want 0", got)
	}

	observer, observerErr := platformdb.Open(ctx, databasePath)
	if observerErr != nil {
		t.Fatal(observerErr)
	}
	defer func() { _ = observer.Close() }()
	// The devices pass still deleted expired Entity Events even though the
	// Observation deletion failed, and the automations pass still deleted its
	// expired terminal Run while retaining the fresh run and the matched-Fact
	// receipt. The blocked expired Observation stays because SQLite aborted it.
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		select {
		case runErr := <-runErrors:
			if runErr != nil {
				return false, runErr
			}
			return false, context.Canceled
		default:
		}
		return countRetentionRows(ctx, observer, "observations") == 2 &&
			countRetentionRows(ctx, observer, "entity_events") == 1 &&
			countRetentionRows(ctx, observer, "automation_history") == 2 &&
			countRetentionRows(ctx, observer, "automation_fact_receipts") == 1, nil
	})
	assertRetentionIDs(ctx, t, observer, "observations", "observation_id", []string{
		"obs_01890f47-7a6b-7c4d-8e9f-0123456789a1",
		"obs_01890f47-7a6b-7c4d-8e9f-0123456789a2",
	})
	assertRetentionIDs(ctx, t, observer, "entity_events", "event_id",
		[]string{"evt_01890f47-7a6b-7c4d-8e9f-0123456789b3"})
	assertRetentionIDs(ctx, t, observer, "automation_history", "id", []string{
		"arn_01890f47-7a6b-7c4d-8e9f-0123456789a2",
		"arn_01890f47-7a6b-7c4d-8e9f-0123456789a3",
	})

	stopCore()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("hearthd did not stop")
	}
}

// startupPruneBlockerSecret is the raw SQLite error text the blocked prune
// carries; no log record may contain it.
const startupPruneBlockerSecret = "s3cr3t-prune-detail"

// blockExpiredObservationPrune installs a SQLite BEFORE DELETE trigger that
// aborts deleting the seeded expired non-current Observation. It makes the
// devices retention pass fail through the real database path, with no
// production hook, so readiness and cross-module isolation are tested against
// the actual Run wiring.
func blockExpiredObservationPrune(
	ctx context.Context,
	t *testing.T,
	databasePath string,
	secret string,
) {
	t.Helper()
	database, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	trigger := fmt.Sprintf(`
		CREATE TRIGGER block_expired_observation_prune
		BEFORE DELETE ON observations
		WHEN OLD.observation_id = 'obs_01890f47-7a6b-7c4d-8e9f-0123456789a1'
		BEGIN
			SELECT RAISE(ABORT, %q);
		END`, secret)
	if _, execErr := database.ExecContext(ctx, trigger); execErr != nil {
		t.Fatal(execErr)
	}
}

func seedStartupRetentionDatabase(ctx context.Context, t *testing.T, databasePath string) {
	t.Helper()
	database := dbtest.OpenMigrated(t, databasePath)
	defer func() { _ = database.Close() }()
	seededAt := time.Now().UTC()
	seedTimestamp := seededAt.Format(time.RFC3339Nano)
	// Production stores observed_at and recorded_at fixed-width so the retention
	// cutoff compares lexicographically; RFC3339Nano would drop trailing zeros.
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
	// Entity Events have no current-State anchor. The two expired rows must be
	// deleted by the startup pass and only the fresh row may survive.
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
	// Automation history uses the injected `automation_history_retention` window:
	// the 40-day-old terminal Run expires, while the fresh terminal Run and the
	// running Run (interrupted by startup recovery) stay. A manual Run needs a
	// definition snapshot and no Fact columns; only manual Runs are used so no
	// Fact summary is required by the history table's family constraint.
	const snapshot = `{"name":"Startup retention","enabled":true,"triggers":[],"steps":[]}`
	if _, execErr := database.ExecContext(ctx, `
		INSERT INTO automation_history (
			id, automation_id, automation_name, kind, revision, recorded_at,
			run_snapshot_json, run_source, run_status, run_started_at, run_completed_at,
			run_matched_trigger_ids_json, condition_decision_json
		) VALUES
			('arn_01890f47-7a6b-7c4d-8e9f-0123456789a1',
				'aut_01890f47-7a6b-7c4d-8e9f-0123456789b0', 'Startup retention',
				'run', 1, ?, ?, 'manual', 'succeeded', ?, ?, '[]',
				'{"mode":"not_configured","bypass_requested":false}'),
			('arn_01890f47-7a6b-7c4d-8e9f-0123456789a2',
				'aut_01890f47-7a6b-7c4d-8e9f-0123456789b0', 'Startup retention',
				'run', 1, ?, ?, 'manual', 'succeeded', ?, ?, '[]',
				'{"mode":"not_configured","bypass_requested":false}'),
			('arn_01890f47-7a6b-7c4d-8e9f-0123456789a3',
				'aut_01890f47-7a6b-7c4d-8e9f-0123456789b0', 'Startup retention',
				'run', 1, ?, ?, 'manual', 'running', ?, NULL, '[]',
				'{"mode":"not_configured","bypass_requested":false}')`,
		sortableTimestamp(seededAt.Add(-40*24*time.Hour)), snapshot,
		sortableTimestamp(seededAt.Add(-40*24*time.Hour)),
		sortableTimestamp(seededAt.Add(-40*24*time.Hour)),
		seedTimestamp, snapshot, seedTimestamp, seedTimestamp,
		seedTimestamp, snapshot, seedTimestamp,
	); execErr != nil {
		t.Fatal(execErr)
	}
	// A matched-Fact receipt for the expired Run must survive its deletion.
	if _, execErr := database.ExecContext(ctx, `
		INSERT INTO automation_fact_receipts (fact_id, automation_id, outcome_kind, history_id)
		VALUES ('fct_01890f47-7a6b-7c4d-8e9f-0123456789c0',
			'aut_01890f47-7a6b-7c4d-8e9f-0123456789b0', 'run',
			'arn_01890f47-7a6b-7c4d-8e9f-0123456789a1')`); execErr != nil {
		t.Fatal(execErr)
	}
}

// waitForCoreHealthz waits until /healthz serves, which happens only after
// startup completes, so a 200 means serving began independently of the startup
// retention pass running in its worker.
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
