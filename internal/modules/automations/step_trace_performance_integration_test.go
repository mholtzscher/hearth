//go:build integration

package automations_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	moderncsqlite "modernc.org/sqlite"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	notificationssqlite "github.com/mholtzscher/hearth/internal/modules/notifications/sqlite"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// This opt-in integration harness is intentionally portable to the pre-change
// baseline: it uses only the stable service entry points and reads retained
// evidence through schema-detected SQL, rather than the changed Run arrays.
//
// Run with:
//
//	AUTOMATION_PERF=1 AUTOMATION_PERF_ITERATIONS=10 GO_PACKAGES='./internal/modules/automations' \
//	  mise run --skip-deps test -- -tags=integration -run '^TestAutomationStepTracePerformance$' -count=1 -v
//
// No performance result is produced unless explicitly enabled. Keep each
// comparison side in a fresh checkout/process and use the same iteration count.
// Competing entity_states reads are enabled by default; set AUTOMATION_PERF_COMPETE=0
// on both sides only when a no-competition comparison is also useful.
//
//nolint:paralleltest // Samples share process allocation counters and must run serially.
func TestAutomationStepTracePerformance(
	t *testing.T,
) {
	if os.Getenv("AUTOMATION_PERF") != "1" {
		t.Skip("set AUTOMATION_PERF=1 to run the opt-in comparison harness")
	}
	iterations := 10
	if value := os.Getenv("AUTOMATION_PERF_ITERATIONS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			t.Fatalf("AUTOMATION_PERF_ITERATIONS must be a positive integer, got %q", value)
		}
		iterations = parsed
	}
	//nolint:paralleltest // Samples share process allocation counters and must run serially.
	for _, scenario := range []string{"sparse", "dense"} {
		t.Run(scenario, func(t *testing.T) {
			measureStepTraceScenario(t, scenario, iterations)
		})
	}
}

type stepTraceWorkload struct {
	definition   automations.Definition
	selected     map[string][]string
	commandOrder []string
	commands     []string
	unselected   []string
	want         stepTraceEffects
}

type stepTraceEffects struct {
	Commands      int `json:"commands"`
	Delays        int `json:"delays"`
	Notifications int `json:"notifications"`
	Functions     int `json:"functions"`
	Branches      int `json:"branches"`
}

type stepTraceSample struct {
	Scenario             string                      `json:"scenario"`
	Iterations           int                         `json:"iterations"`
	WarmupRuns           int                         `json:"warmup_runs"`
	AdmissionTotalNS     int64                       `json:"admission_total_ns"`
	WorkerWaitTotalNS    int64                       `json:"worker_wait_total_ns"`
	HistoryReadTotalNS   int64                       `json:"history_read_total_ns"`
	ElapsedTotalNS       int64                       `json:"elapsed_total_ns"`
	AllocationBytes      uint64                      `json:"allocation_bytes"`
	AllocationCount      uint64                      `json:"allocation_count"`
	RetainedEvidenceRows int                         `json:"retained_evidence_rows_per_run"`
	RetainedLogicalBytes int64                       `json:"retained_logical_bytes_per_run"`
	ErrorCount           int                         `json:"error_count"`
	EffectsPerRun        stepTraceEffects            `json:"effects_per_run"`
	Database             stepTraceDBMetrics          `json:"database_metrics"`
	CompetitionEnabled   bool                        `json:"competing_module_reads_enabled"`
	Competition          stepTraceCompetitionMetrics `json:"competing_module_reads"`
	Outcome              string                      `json:"outcome"`
	TimingScope          string                      `json:"timing_scope"`
	InstrumentationGaps  string                      `json:"instrumentation_gaps"`
}

type stepTraceCompetitionMetrics struct {
	CompletedOperations int64 `json:"completed_operations"`
	TotalWaitNS         int64 `json:"total_wait_ns"`
	MaxWaitNS           int64 `json:"max_wait_ns"`
	Errors              int64 `json:"errors"`
}

type stepTraceEvidenceRow struct {
	table  string
	values map[string]string
	bytes  int64
}

type stepTraceCallWindow struct {
	commandStart      int
	commandEnd        int
	functionStart     int
	functionEnd       int
	notificationStart int
	notificationEnd   int
}

// stepTraceDBMetrics describes driver-level operations, not database/sql Scan
// decoding or SQLite's physical page/index storage.
type stepTraceDBMetrics struct {
	QueryCount                int64 `json:"query_count"`
	ExecCount                 int64 `json:"exec_count"`
	QueryErrorCount           int64 `json:"query_error_count"`
	ExecErrorCount            int64 `json:"exec_error_count"`
	CancelledQueryCount       int64 `json:"cancelled_query_count"`
	RowsReturned              int64 `json:"rows_returned"`
	StringBytesReturned       int64 `json:"string_bytes_returned"`
	QueryToRowsClosedTotalNS  int64 `json:"query_to_rows_closed_total_ns"`
	QueryToRowsClosedMaxNS    int64 `json:"query_to_rows_closed_max_ns"`
	TransactionCount          int64 `json:"transaction_count"`
	TransactionTotalNS        int64 `json:"transaction_total_ns"`
	TransactionMaxNS          int64 `json:"transaction_max_ns"`
	TransactionErrorCount     int64 `json:"transaction_error_count"`
	EvidenceSelectCount       int64 `json:"execution_evidence_select_count"`
	RunScopedEvidenceSelects  int64 `json:"run_scoped_evidence_selects"`
	StepScopedEvidenceSelects int64 `json:"step_scoped_evidence_selects"`
	OtherSelectCount          int64 `json:"other_select_count"`
	EvidenceWriteCount        int64 `json:"execution_evidence_write_count"`
	CompetingReadQueryCount   int64 `json:"competing_entity_states_query_count"`
	CompetingReadRows         int64 `json:"competing_entity_states_rows"`
}

type stepTraceDBStats struct {
	mu      sync.Mutex
	metrics stepTraceDBMetrics
}

func (stats *stepTraceDBStats) reset() {
	stats.mu.Lock()
	stats.metrics = stepTraceDBMetrics{}
	stats.mu.Unlock()
}

func (stats *stepTraceDBStats) snapshot() stepTraceDBMetrics {
	stats.mu.Lock()
	defer stats.mu.Unlock()
	return stats.metrics
}

func (stats *stepTraceDBStats) queryStarted(query string) bool {
	lower := normalizedStepTraceSQL(query)
	isCompetingRead := strings.Contains(lower, "from entity_states")
	hasEvidence := hasStepTraceEvidenceTable(lower)
	stats.mu.Lock()
	defer stats.mu.Unlock()
	stats.metrics.QueryCount++
	if isCompetingRead {
		stats.metrics.CompetingReadQueryCount++
	}
	isSelect := strings.HasPrefix(lower, "select")
	if isSelect && !hasEvidence {
		stats.metrics.OtherSelectCount++
	}
	if hasEvidence && isSelect {
		stats.metrics.EvidenceSelectCount++
		where := ""
		if _, after, found := strings.Cut(lower, " where "); found {
			where = after
		}
		if strings.Contains(where, "step_id =") {
			stats.metrics.StepScopedEvidenceSelects++
		} else if strings.Contains(where, "run_id =") {
			stats.metrics.RunScopedEvidenceSelects++
		}
	}
	return isCompetingRead
}

func normalizedStepTraceSQL(query string) string {
	query = strings.TrimSpace(query)
	for {
		switch {
		case strings.HasPrefix(query, "--"):
			if newline := strings.IndexByte(query, '\n'); newline >= 0 {
				query = strings.TrimSpace(query[newline+1:])
			} else {
				return ""
			}
		case strings.HasPrefix(query, "/*"):
			if end := strings.Index(query[2:], "*/"); end >= 0 {
				query = strings.TrimSpace(query[end+4:])
			} else {
				return ""
			}
		default:
			return strings.Join(strings.Fields(strings.ToLower(query)), " ")
		}
	}
}

func (stats *stepTraceDBStats) queryFailed(cancelled bool) {
	stats.mu.Lock()
	if cancelled {
		stats.metrics.CancelledQueryCount++
	} else {
		stats.metrics.QueryErrorCount++
	}
	stats.mu.Unlock()
}

func (stats *stepTraceDBStats) execStarted(query string) {
	lower := strings.ToLower(query)
	stats.mu.Lock()
	stats.metrics.ExecCount++
	if hasStepTraceEvidenceTable(lower) {
		stats.metrics.EvidenceWriteCount++
	}
	stats.mu.Unlock()
}

func (stats *stepTraceDBStats) execFailed() {
	stats.mu.Lock()
	stats.metrics.ExecErrorCount++
	stats.mu.Unlock()
}

func (stats *stepTraceDBStats) rowsRead(values []driver.Value, competing bool) {
	var stringBytes int64
	for _, value := range values {
		switch value := value.(type) {
		case string:
			stringBytes += int64(len(value))
		case []byte:
			stringBytes += int64(len(value))
		}
	}
	stats.mu.Lock()
	stats.metrics.RowsReturned++
	stats.metrics.StringBytesReturned += stringBytes
	if competing {
		stats.metrics.CompetingReadRows++
	}
	stats.mu.Unlock()
}

func (stats *stepTraceDBStats) queryFinished(elapsed time.Duration) {
	ns := elapsed.Nanoseconds()
	stats.mu.Lock()
	stats.metrics.QueryToRowsClosedTotalNS += ns
	if ns > stats.metrics.QueryToRowsClosedMaxNS {
		stats.metrics.QueryToRowsClosedMaxNS = ns
	}
	stats.mu.Unlock()
}

func (stats *stepTraceDBStats) transactionFinished(elapsed time.Duration, err error) {
	ns := elapsed.Nanoseconds()
	stats.mu.Lock()
	stats.metrics.TransactionCount++
	stats.metrics.TransactionTotalNS += ns
	if err != nil {
		stats.metrics.TransactionErrorCount++
	}
	if ns > stats.metrics.TransactionMaxNS {
		stats.metrics.TransactionMaxNS = ns
	}
	stats.mu.Unlock()
}

func hasStepTraceEvidenceTable(query string) bool {
	for _, table := range []string{
		"automation_run_steps", "automation_run_delays", "automation_run_notifications",
		"automation_run_functions", "automation_run_branch_decisions", "automation_step_executions",
	} {
		if strings.Contains(query, table) {
			return true
		}
	}
	return false
}

// stepTraceConnector wraps this test's connector only; unlike [sql.Register] it
// has no process-global name or registration state.
type stepTraceConnector struct {
	driver.Connector

	stats *stepTraceDBStats
}

func (connector stepTraceConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := connector.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &stepTraceConn{Conn: conn, stats: connector.stats}, nil
}

type stepTraceConn struct {
	driver.Conn

	stats *stepTraceDBStats
}

func (conn *stepTraceConn) Prepare(query string) (driver.Stmt, error) {
	return conn.prepare(context.Background(), query)
}

func (conn *stepTraceConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return conn.prepare(ctx, query)
}

func (conn *stepTraceConn) prepare(ctx context.Context, query string) (driver.Stmt, error) {
	var stmt driver.Stmt
	var err error
	if preparer, ok := conn.Conn.(driver.ConnPrepareContext); ok {
		stmt, err = preparer.PrepareContext(ctx, query)
	} else {
		stmt, err = conn.Conn.Prepare(query)
	}
	if err != nil {
		return nil, err
	}
	return &stepTraceStmt{Stmt: stmt, query: query, stats: conn.stats}, nil
}

func (conn *stepTraceConn) Begin() (driver.Tx, error) {
	return conn.begin(context.Background(), driver.TxOptions{})
}

func (conn *stepTraceConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	return conn.begin(ctx, options)
}

func (conn *stepTraceConn) begin(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	started := time.Now()
	var tx driver.Tx
	var err error
	if beginner, ok := conn.Conn.(driver.ConnBeginTx); ok {
		tx, err = beginner.BeginTx(ctx, options)
	} else if options.Isolation != driver.IsolationLevel(0) || options.ReadOnly {
		return nil, errors.New("underlying SQLite connection does not support transaction options")
	} else {
		tx, err = conn.Conn.Begin() //nolint:staticcheck // Fallback for drivers without ConnBeginTx.
	}
	if err != nil {
		return nil, err
	}
	return &stepTraceTx{Tx: tx, stats: conn.stats, started: started}, nil
}

func (conn *stepTraceConn) ExecContext(
	ctx context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Result, error) {
	execer, ok := conn.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	conn.stats.execStarted(query)
	result, err := execer.ExecContext(ctx, query, args)
	if err != nil {
		conn.stats.execFailed()
	}
	return result, err
}

func (conn *stepTraceConn) QueryContext(
	ctx context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Rows, error) {
	queryer, ok := conn.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	competing := conn.stats.queryStarted(query)
	started := time.Now()
	rows, err := queryer.QueryContext(ctx, query, args)
	if err != nil {
		conn.stats.queryFailed(ctx.Err() != nil)
		conn.stats.queryFinished(time.Since(started))
		return nil, err
	}
	return &stepTraceRows{Rows: rows, stats: conn.stats, started: started, competing: competing}, nil
}

func (conn *stepTraceConn) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := conn.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

func (conn *stepTraceConn) Ping(ctx context.Context) error {
	if pinger, ok := conn.Conn.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

func (conn *stepTraceConn) ResetSession(ctx context.Context) error {
	if resetter, ok := conn.Conn.(driver.SessionResetter); ok {
		return resetter.ResetSession(ctx)
	}
	return nil
}

func (conn *stepTraceConn) IsValid() bool {
	if validator, ok := conn.Conn.(driver.Validator); ok {
		return validator.IsValid()
	}
	return true
}

type stepTraceStmt struct {
	driver.Stmt

	query string
	stats *stepTraceDBStats
}

func (stmt *stepTraceStmt) Exec(args []driver.Value) (driver.Result, error) {
	return stmt.ExecContext(context.Background(), namedValuesFromValues(args))
}

func (stmt *stepTraceStmt) Query(args []driver.Value) (driver.Rows, error) {
	return stmt.QueryContext(context.Background(), namedValuesFromValues(args))
}

func namedValuesFromValues(args []driver.Value) []driver.NamedValue {
	values := make([]driver.NamedValue, len(args))
	for index, value := range args {
		values[index] = driver.NamedValue{Ordinal: index + 1, Value: value}
	}
	return values
}

func (stmt *stepTraceStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	stmt.stats.execStarted(stmt.query)
	var result driver.Result
	var err error
	if execer, ok := stmt.Stmt.(driver.StmtExecContext); ok {
		result, err = execer.ExecContext(ctx, args)
	} else {
		return nil, driver.ErrSkip
	}
	if err != nil {
		stmt.stats.execFailed()
	}
	return result, err
}

func (stmt *stepTraceStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	competing := stmt.stats.queryStarted(stmt.query)
	started := time.Now()
	var rows driver.Rows
	var err error
	if queryer, ok := stmt.Stmt.(driver.StmtQueryContext); ok {
		rows, err = queryer.QueryContext(ctx, args)
	} else {
		return nil, driver.ErrSkip
	}
	if err != nil {
		stmt.stats.queryFailed(ctx.Err() != nil)
		stmt.stats.queryFinished(time.Since(started))
		return nil, err
	}
	return &stepTraceRows{Rows: rows, stats: stmt.stats, started: started, competing: competing}, nil
}

type stepTraceTx struct {
	driver.Tx

	stats   *stepTraceDBStats
	started time.Time
	once    sync.Once
}

func (tx *stepTraceTx) Commit() error {
	err := tx.Tx.Commit()
	tx.finish(err)
	return err
}

func (tx *stepTraceTx) Rollback() error {
	err := tx.Tx.Rollback()
	tx.finish(err)
	return err
}

func (tx *stepTraceTx) finish(err error) {
	tx.once.Do(func() { tx.stats.transactionFinished(time.Since(tx.started), err) })
}

type stepTraceRows struct {
	driver.Rows

	stats     *stepTraceDBStats
	started   time.Time
	competing bool
	once      sync.Once
}

func (rows *stepTraceRows) Next(destination []driver.Value) error {
	err := rows.Rows.Next(destination)
	switch {
	case err == nil:
		rows.stats.rowsRead(destination, rows.competing)
	case errors.Is(err, io.EOF):
		rows.finish()
	default:
		rows.stats.queryFailed(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
		rows.finish()
	}
	return err
}

func (rows *stepTraceRows) Close() error {
	err := rows.Rows.Close()
	rows.finish()
	return err
}

func (rows *stepTraceRows) finish() {
	rows.once.Do(func() { rows.stats.queryFinished(time.Since(rows.started)) })
}

func openStepTraceDatabase(t *testing.T) (*sql.DB, *stepTraceDBStats) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hearth.db")
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	base, err := moderncsqlite.NewConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	stats := &stepTraceDBStats{}
	database := sql.OpenDB(stepTraceConnector{Connector: base, stats: stats})
	database.SetMaxOpenConns(1)
	if pingErr := database.PingContext(t.Context()); pingErr != nil {
		database.Close()
		t.Fatalf("ping instrumented SQLite: %v", pingErr)
	}
	if migrateErr := platformdb.Migrate(t.Context(), database); migrateErr != nil {
		database.Close()
		t.Fatalf("migrate instrumented SQLite: %v", migrateErr)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database, stats
}

func newStepTraceNotificationFixture(
	t *testing.T,
	database *sql.DB,
	sender notificationSenderFunc,
) notificationFixture {
	t.Helper()
	runtimes := adapterssqlite.NewAdapterRepository(database)
	runtimeID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	registration := notifications.Registration{
		AdapterID: "alerts",
		RuntimeID: "run_" + runtimeID.String(),
		Key:       "home",
		Name:      "Home",
	}
	claimNotificationRuntime(t, runtimes, registration)
	repository := notificationssqlite.NewRepository(database)
	service, err := notifications.NewService(t.Context(), repository, sender)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := service.Register(t.Context(), registration)
	if err != nil {
		t.Fatal(err)
	}
	return notificationFixture{database, repository, service, runtimes, registration, destination}
}

type stepTraceCompetingReader struct {
	start   chan struct{}
	done    chan struct{}
	cancel  context.CancelFunc
	mu      sync.Mutex
	metrics stepTraceCompetitionMetrics
}

func startStepTraceCompetingReader(database *sql.DB) *stepTraceCompetingReader {
	ctx, cancel := context.WithCancel(context.Background())
	reader := &stepTraceCompetingReader{start: make(chan struct{}), done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(reader.done)
		<-reader.start
		for ctx.Err() == nil {
			started := time.Now()
			var count int64
			err := database.QueryRowContext(ctx, "SELECT count(*) FROM entity_states").Scan(&count)
			elapsed := time.Since(started)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				reader.mu.Lock()
				reader.metrics.Errors++
				reader.mu.Unlock()
				return
			}
			_ = count // The query's returned row is observed by the driver wrapper.
			reader.mu.Lock()
			reader.metrics.CompletedOperations++
			reader.metrics.TotalWaitNS += elapsed.Nanoseconds()
			if elapsed.Nanoseconds() > reader.metrics.MaxWaitNS {
				reader.metrics.MaxWaitNS = elapsed.Nanoseconds()
			}
			reader.mu.Unlock()
		}
	}()
	return reader
}

func (reader *stepTraceCompetingReader) begin() {
	close(reader.start)
}

func (reader *stepTraceCompetingReader) stop() stepTraceCompetitionMetrics {
	reader.cancel()
	<-reader.done
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.metrics
}

func stepTraceCompetitionEnabled(t *testing.T) bool {
	t.Helper()
	switch strings.ToLower(os.Getenv("AUTOMATION_PERF_COMPETE")) {
	case "", "1", "true":
		return true
	case "0", "false":
		return false
	default:
		t.Fatalf("AUTOMATION_PERF_COMPETE must be 0, 1, false, or true; got %q", os.Getenv("AUTOMATION_PERF_COMPETE"))
		return false
	}
}

func measureStepTraceScenario(t *testing.T, scenario string, iterations int) {
	t.Helper()
	var notificationAttempts []notifications.Attempt
	database, databaseStats := openStepTraceDatabase(t)
	fixture := newStepTraceNotificationFixture(t, database, notificationSenderFunc(
		func(_ context.Context, attempt notifications.Attempt) notifications.Evidence {
			notificationAttempts = append(notificationAttempts, attempt)
			return notifications.Evidence{Outcome: notifications.Accepted, Receipt: "perf-accepted"}
		},
	))
	functions := &scriptedFunctions{outcome: adapters.Succeeded{Result: json.RawMessage(`{"ok":true}`)}}
	deps := runtimeTestDependencies()
	deps.Notifications = fixture.service
	deps.Functions = functions
	scripted := newScriptedDevices()
	service := automations.NewService(
		automationssqlite.NewAutomationRepository(fixture.database, deps), scripted, deps,
	)
	workload := buildStepTraceWorkload(t, scenario, fixture.destination.ID)
	notificationAttempts = make([]notifications.Attempt, 0, (iterations+1)*workload.want.Notifications)
	record := createRuntimeAutomation(t, service, workload.definition)

	// Explicitly warm the database, service, and exercised execution paths. The
	// warmup is deliberately outside all reported timing and allocation deltas.
	warmupWindow := stepTraceCallCounts(scripted, functions, notificationAttempts)
	warmupID := runStepTraceCycle(t, service, record.ID, workload, scripted, functions, &notificationAttempts)
	warmupWindow.finish(scripted, functions, notificationAttempts)
	warmupRows := readStepTraceEvidence(t, fixture.database, warmupID)
	assertStepTraceRows(t, warmupID, warmupRows, workload)
	if err := verifyStepTraceExternalCalls(
		workload,
		warmupRows,
		scripted.executions[warmupWindow.commandStart:warmupWindow.commandEnd],
		functions.inputs[warmupWindow.functionStart:warmupWindow.functionEnd],
		notificationAttempts[warmupWindow.notificationStart:warmupWindow.notificationEnd],
	); err != nil {
		t.Fatalf("warmup external-call evidence: %v", err)
	}

	var sample stepTraceSample
	sample.Scenario = scenario
	sample.Iterations = iterations
	sample.WarmupRuns = 1
	sample.EffectsPerRun = workload.want
	sample.ErrorCount = 0 // Any admission, worker, status, or effect mismatch fails the test.
	sample.Outcome = "all measured Runs succeeded; selected external calls matched retained Step evidence"
	sample.TimingScope = "admission, asynchronous worker wait (including delay sleeps), and GetHistoryEntry; elapsed includes bounded competing-reader operation and join"
	sample.InstrumentationGaps = "query-to-rows-closed includes caller row consumption and is not pure SQL time; raw string/byte payload and row counts are not decode CPU; SQL-shape classes are heuristics; logical retained bytes exclude SQLite page/index overhead; allocations include the wrapper and competitor"

	compete := stepTraceCompetitionEnabled(t)
	sample.CompetitionEnabled = compete
	databaseStats.reset()
	var competitor *stepTraceCompetingReader
	if compete {
		competitor = startStepTraceCompetingReader(database)
	}
	runIDs := make([]string, iterations)
	callWindows := make([]stepTraceCallWindow, iterations)
	var before, after runtime.MemStats
	runtimeReadMemStats(&before)
	started := time.Now()
	if competitor != nil {
		competitor.begin()
	}
	for index := range iterations {
		callWindows[index] = stepTraceCallCounts(scripted, functions, notificationAttempts)
		id, admission, worker, detail := timeStepTraceCycle(
			t,
			service,
			record.ID,
			workload,
			scripted,
			functions,
			&notificationAttempts,
		)
		runIDs[index] = id
		callWindows[index].finish(scripted, functions, notificationAttempts)
		sample.AdmissionTotalNS += admission.Nanoseconds()
		sample.WorkerWaitTotalNS += worker.Nanoseconds()
		sample.HistoryReadTotalNS += detail.Nanoseconds()
	}
	if competitor != nil {
		sample.Competition = competitor.stop()
	}
	sample.ElapsedTotalNS = time.Since(started).Nanoseconds()
	runtimeReadMemStats(&after)
	sample.AllocationBytes = after.TotalAlloc - before.TotalAlloc
	sample.AllocationCount = after.Mallocs - before.Mallocs
	sample.Database = databaseStats.snapshot()
	sample.ErrorCount = int(
		sample.Database.QueryErrorCount + sample.Database.ExecErrorCount + sample.Database.TransactionErrorCount,
	)
	if sample.ErrorCount != 0 {
		t.Errorf("instrumented database errors = %d (query=%d exec=%d transaction=%d)",
			sample.ErrorCount, sample.Database.QueryErrorCount, sample.Database.ExecErrorCount,
			sample.Database.TransactionErrorCount)
	}
	if sample.Competition.Errors != 0 {
		t.Errorf("competing entity_states read errors = %d", sample.Competition.Errors)
	}
	if compete && sample.Competition.CompletedOperations == 0 {
		t.Error("competing reader completed no entity_states reads during the measured window")
	}
	for index, runID := range runIDs {
		if runID == "" {
			t.Fatalf("measured Run %d has no ID", index)
		}
		rows := readStepTraceEvidence(t, fixture.database, runID)
		assertStepTraceRows(t, runID, rows, workload)
		if err := verifyStepTraceExternalCalls(
			workload,
			rows,
			scripted.executions[callWindows[index].commandStart:callWindows[index].commandEnd],
			functions.inputs[callWindows[index].functionStart:callWindows[index].functionEnd],
			notificationAttempts[callWindows[index].notificationStart:callWindows[index].notificationEnd],
		); err != nil {
			t.Errorf("measured Run %s external-call evidence: %v", runID, err)
		}
		if index == len(runIDs)-1 {
			sample.RetainedEvidenceRows = len(rows)
			for _, row := range rows {
				sample.RetainedLogicalBytes += row.bytes
			}
		}
	}
	encoded, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("AUTOMATION_PERF_SAMPLE %s", encoded)
}

// runtimeReadMemStats is kept separate to make the allocation window explicit;
// benchmark-like allocation totals include the service calls, not setup/warmup.
func runtimeReadMemStats(stats *runtime.MemStats) {
	runtime.ReadMemStats(stats)
}

func runStepTraceCycle(
	t *testing.T,
	service *automations.Service,
	automationID automations.AutomationID,
	workload stepTraceWorkload,
	scripted *scriptedDevices,
	functions *scriptedFunctions,
	notificationAttempts *[]notifications.Attempt,
) string {
	t.Helper()
	id, _, _, _ := timeStepTraceCycle(t, service, automationID, workload, scripted, functions, notificationAttempts)
	return id
}

func stepTraceCallCounts(
	scripted *scriptedDevices,
	functions *scriptedFunctions,
	notifications []notifications.Attempt,
) stepTraceCallWindow {
	return stepTraceCallWindow{
		commandStart:      len(scripted.executions),
		functionStart:     len(functions.inputs),
		notificationStart: len(notifications),
	}
}

func (window *stepTraceCallWindow) finish(
	scripted *scriptedDevices,
	functions *scriptedFunctions,
	notifications []notifications.Attempt,
) {
	window.commandEnd = len(scripted.executions)
	window.functionEnd = len(functions.inputs)
	window.notificationEnd = len(notifications)
}

func timeStepTraceCycle(
	t *testing.T,
	service *automations.Service,
	automationID automations.AutomationID,
	workload stepTraceWorkload,
	scripted *scriptedDevices,
	functions *scriptedFunctions,
	notificationAttempts *[]notifications.Attempt,
) (string, time.Duration, time.Duration, time.Duration) {
	t.Helper()
	commandBefore, functionBefore, notificationBefore := scripted.executionCount(), len(
		functions.inputs,
	), len(*notificationAttempts)
	ctx := context.Background()
	started := time.Now()
	startedRun, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: automationID})
	if err != nil {
		t.Fatalf("StartManualRun: %v", err)
	}
	admission := time.Since(started)
	started = time.Now()
	waitForRuns(t, service)
	worker := time.Since(started)
	started = time.Now()
	entry, err := service.GetHistoryEntry(ctx, automationID, string(startedRun.ID))
	if err != nil {
		t.Fatalf("GetHistoryEntry: %v", err)
	}
	detail := time.Since(started)
	run, ok := entry.(automations.Run)
	if !ok || automations.RunStateStatus(run.State) != automations.RunSucceeded {
		t.Fatalf("retained Run outcome = %#v, want succeeded", entry)
	}
	if got := scripted.executionCount() - commandBefore; got != workload.want.Commands {
		t.Fatalf("Command effects = %d, want %d", got, workload.want.Commands)
	}
	if got := len(functions.inputs) - functionBefore; got != workload.want.Functions {
		t.Fatalf("Function effects = %d, want %d", got, workload.want.Functions)
	}
	if got := len(*notificationAttempts) - notificationBefore; got != workload.want.Notifications {
		t.Fatalf("Notification effects = %d, want %d", got, workload.want.Notifications)
	}
	return string(startedRun.ID), admission, worker, detail
}

func buildStepTraceWorkload(t *testing.T, scenario string, destination notifications.DestinationID) stepTraceWorkload {
	t.Helper()
	command := func(id string) automations.Step {
		step := runtimeDefinition(t, 1).Steps[0]
		step.ID = automations.StepID(id)
		return step
	}
	delay := func(id string) automations.Step { return delayNode(id, 1) }
	notify := func(id string) automations.Step {
		return automations.Step{ID: automations.StepID(id), Body: automations.NotificationStep{
			DestinationID: destination, Title: "Performance trace", Message: "fast accepted notification",
		}}
	}
	function := func(id string) automations.Step { return functionNode(t, id) }

	chooseSelected := []automations.Step{command("chosen_command_sparse")}
	chooseUnselected := []automations.Step{command("unselected_choose_command"), delay("unselected_choose_delay"),
		notify("unselected_choose_notification"), function("unselected_choose_function")}
	if scenario == "dense" {
		chooseSelected = []automations.Step{command("chosen_command_a"), delay("chosen_delay"),
			notify("chosen_notification"), function("chosen_function"), command("chosen_command_b")}
		chooseUnselected = []automations.Step{function("unselected_choose_function_dense")}
	} else if scenario != "sparse" {
		t.Fatalf("unknown performance scenario %q", scenario)
	}
	choose := automations.Step{ID: "choose_route", Body: automations.ChooseStep{Branches: []automations.ChooseBranch{
		{ID: "selected_arm", Conditions: branchTriggerCondition(true), Steps: chooseSelected},
		{ID: "unselected_arm", Conditions: branchTriggerCondition(false), Steps: chooseUnselected},
	}}}
	selectedThen := []automations.Step{delay("selected_delay"), command("selected_command"),
		notify("selected_notification"), function("selected_function"), choose}
	unselectedElse := []automations.Step{command("unselected_if_command"), delay("unselected_if_delay"),
		notify("unselected_if_notification"), function("unselected_if_function")}
	steps := []automations.Step{
		command("prefix_command"),
		branchIf("if_route", branchTriggerCondition(true), selectedThen, unselectedElse),
		command("tail_command"),
	}
	definition := runtimeDefinition(t, 0)
	definition.Steps = steps

	selected := map[string][]string{
		"command":      {"prefix_command", "selected_command", "tail_command"},
		"delay":        {"selected_delay"},
		"notification": {"selected_notification"},
		"function":     {"selected_function"},
		"branch":       {"if_route", "choose_route"},
	}
	if scenario == "sparse" {
		selected["command"] = append(selected["command"], "chosen_command_sparse")
	} else {
		selected["command"] = append(selected["command"], "chosen_command_a", "chosen_command_b")
		selected["delay"] = append(selected["delay"], "chosen_delay")
		selected["notification"] = append(selected["notification"], "chosen_notification")
		selected["function"] = append(selected["function"], "chosen_function")
	}
	allCommands := []string{"prefix_command", "selected_command", "tail_command", "unselected_if_command"}
	commandOrder := []string{"prefix_command", "selected_command"}
	unselected := []string{
		"unselected_if_command",
		"unselected_if_delay",
		"unselected_if_notification",
		"unselected_if_function",
	}
	if scenario == "dense" {
		allCommands = append(allCommands, "chosen_command_a", "chosen_command_b")
		commandOrder = append(commandOrder, "chosen_command_a", "chosen_command_b")
		unselected = append(unselected, "unselected_choose_function_dense")
	} else {
		allCommands = append(allCommands, "unselected_choose_command")
		unselected = append(unselected, "unselected_choose_command", "unselected_choose_delay",
			"unselected_choose_notification", "unselected_choose_function")
		allCommands = append(allCommands, "chosen_command_sparse")
		commandOrder = append(commandOrder, "chosen_command_sparse")
	}
	commandOrder = append(commandOrder, "tail_command")
	return stepTraceWorkload{
		definition:   definition,
		selected:     selected,
		commandOrder: commandOrder,
		commands:     allCommands,
		unselected:   unselected,
		want: stepTraceEffects{
			Commands: len(selected["command"]),
			Delays:   len(selected["delay"]),
			Notifications: len(
				selected["notification"],
			),
			Functions: len(selected["function"]),
			Branches:  len(selected["branch"]),
		},
	}
}

func assertStepTraceRows(
	t *testing.T,
	runID string,
	rows []stepTraceEvidenceRow,
	workload stepTraceWorkload,
) {
	t.Helper()
	if len(rows) == 0 {
		t.Fatalf("Run %s has no retained evidence rows", runID)
	}
	unified := hasUnifiedStepTraceEvidence(rows)
	seen := make(map[string]map[string]string, len(workload.selected))
	for kind := range workload.selected {
		seen[kind] = make(map[string]string)
	}
	for _, row := range rows {
		addStepTraceEvidenceRow(t, runID, seen, row, unified)
	}
	for kind, expectedIDs := range workload.selected {
		wantIDs := expectedIDs
		if !unified && kind == "command" {
			wantIDs = workload.commands
		}
		assertStepTraceKindRows(t, runID, kind, expectedIDs, wantIDs, seen[kind], unified)
	}
	if !unified {
		assertBaselineCommandReservations(t, workload, seen["command"])
	}
	assertUnselectedStepTraceRows(t, workload, seen, unified)
}

func hasUnifiedStepTraceEvidence(rows []stepTraceEvidenceRow) bool {
	for _, row := range rows {
		if row.table == "automation_step_executions" {
			return true
		}
	}
	return false
}

func addStepTraceEvidenceRow(
	t *testing.T,
	runID string,
	seen map[string]map[string]string,
	row stepTraceEvidenceRow,
	unified bool,
) {
	t.Helper()
	stepID := row.values["step_id"]
	if stepID == "" {
		return
	}
	kind := stepTraceEvidenceKind(row, unified)
	if kind == "" {
		t.Fatalf("Run %s has evidence row with unknown kind/table: %s %#v", runID, row.table, row.values)
	}
	status := row.values["phase"]
	if status == "" {
		status = row.values["status"]
	}
	if !unified && kind == "branch" {
		status = "decided"
	}
	seen[kind][stepID] = status
}

func stepTraceEvidenceKind(row stepTraceEvidenceRow, unified bool) string {
	if unified {
		kind := row.values["kind"]
		if kind == "if" || kind == "choose" {
			return "branch"
		}
		return kind
	}
	switch row.table {
	case "automation_run_steps":
		return "command"
	case "automation_run_delays":
		return "delay"
	case "automation_run_notifications":
		return "notification"
	case "automation_run_functions":
		return "function"
	case "automation_run_branch_decisions":
		return "branch"
	default:
		return ""
	}
}

func assertStepTraceKindRows(
	t *testing.T,
	runID string,
	kind string,
	expectedIDs []string,
	wantIDs []string,
	seen map[string]string,
	unified bool,
) {
	t.Helper()
	if len(seen) != len(wantIDs) {
		t.Errorf(
			"Run %s retained %d %s details, want %d (expected IDs %v)",
			runID,
			len(seen),
			kind,
			len(wantIDs),
			wantIDs,
		)
	}
	for gotID := range seen {
		if !containsString(wantIDs, gotID) {
			t.Errorf("Run %s retained unexpected %s Step %q", runID, kind, gotID)
		}
	}
	wantStatus := stepTraceExpectedStatus(kind, unified)
	for _, stepID := range expectedIDs {
		status, exists := seen[stepID]
		if !exists {
			t.Errorf("Run %s missing retained %s detail for selected Step %q", runID, kind, stepID)
			continue
		}
		if status != wantStatus {
			t.Errorf("Run %s retained %s Step %q status = %q, want %q", runID, kind, stepID, status, wantStatus)
		}
	}
}

func stepTraceExpectedStatus(kind string, unified bool) string {
	if unified {
		if kind == "branch" {
			return "decided"
		}
		return "succeeded"
	}
	switch kind {
	case "command":
		return "satisfied"
	case "delay":
		return "completed"
	case "notification":
		return "accepted"
	case "function":
		return "succeeded"
	case "branch":
		return "decided"
	default:
		return ""
	}
}

func assertBaselineCommandReservations(t *testing.T, workload stepTraceWorkload, seen map[string]string) {
	t.Helper()
	for _, stepID := range workload.commands {
		status, exists := seen[stepID]
		if !exists {
			t.Errorf("baseline Command reservation row missing for definition leaf %q", stepID)
			continue
		}
		if !containsString(workload.selected["command"], stepID) && status != "not_attempted" {
			t.Errorf("unselected Command %q status = %q, want not_attempted", stepID, status)
		}
	}
}

func assertUnselectedStepTraceRows(
	t *testing.T,
	workload stepTraceWorkload,
	seen map[string]map[string]string,
	unified bool,
) {
	t.Helper()
	for _, stepID := range workload.unselected {
		for kind, ids := range workload.selected {
			if containsString(ids, stepID) {
				t.Fatalf("invalid workload oracle: Step %q is selected as %s and unselected", stepID, kind)
			}
		}
		for kind, records := range seen {
			if status, exists := records[stepID]; exists &&
				(unified || kind != "command" || status != "not_attempted") {
				t.Errorf("unselected %s Step %q has retained effect status %q", kind, stepID, status)
			}
		}
	}
}

func verifyStepTraceExternalCalls(
	workload stepTraceWorkload,
	rows []stepTraceEvidenceRow,
	commands []devices.CommandInput,
	functions []adapters.ReservedInvokeInput,
	attempts []notifications.Attempt,
) error {
	steps, err := stepTraceDefinitionSteps(workload.definition.Steps)
	if err != nil {
		return err
	}
	evidence, err := stepTraceRowsByKind(rows)
	if err != nil {
		return err
	}
	if commandErr := verifyStepTraceCommands(workload, steps, evidence, commands); commandErr != nil {
		return commandErr
	}
	if notificationErr := verifyStepTraceNotifications(workload, steps, evidence, attempts); notificationErr != nil {
		return notificationErr
	}
	return verifyStepTraceFunctions(workload, steps, evidence, functions)
}

func stepTraceDefinitionSteps(steps []automations.Step) (map[string]automations.Step, error) {
	result := make(map[string]automations.Step)
	if err := addStepTraceDefinitionSteps(result, steps); err != nil {
		return nil, err
	}
	return result, nil
}

func addStepTraceDefinitionSteps(result map[string]automations.Step, steps []automations.Step) error {
	for _, step := range steps {
		id := string(step.ID)
		if _, exists := result[id]; exists {
			return fmt.Errorf("duplicate definition Step %q", id)
		}
		result[id] = step
		for _, nested := range stepTraceNestedSequences(step) {
			if err := addStepTraceDefinitionSteps(result, nested); err != nil {
				return err
			}
		}
	}
	return nil
}

func stepTraceNestedSequences(step automations.Step) [][]automations.Step {
	switch body := step.Body.(type) {
	case automations.IfStep:
		return [][]automations.Step{body.Then, body.Else}
	case automations.ChooseStep:
		sequences := make([][]automations.Step, 0, len(body.Branches)+1)
		for _, branch := range body.Branches {
			sequences = append(sequences, branch.Steps)
		}
		return append(sequences, body.Default)
	case automations.CommandStep, automations.DelayStep, automations.FunctionStep, automations.NotificationStep:
		return nil
	}
	panic("unreachable: exhaustive StepBody switch")
}

func stepTraceRowsByKind(rows []stepTraceEvidenceRow) (map[string]map[string]stepTraceEvidenceRow, error) {
	result := map[string]map[string]stepTraceEvidenceRow{
		"command": {}, "notification": {}, "function": {},
	}
	unified := hasUnifiedStepTraceEvidence(rows)
	for _, row := range rows {
		kind := stepTraceEvidenceKind(row, unified)
		if _, relevant := result[kind]; !relevant {
			continue
		}
		stepID := row.values["step_id"]
		if _, exists := result[kind][stepID]; exists {
			return nil, fmt.Errorf("duplicate retained %s evidence for Step %q", kind, stepID)
		}
		result[kind][stepID] = row
	}
	return result, nil
}

func verifyStepTraceCommands(
	workload stepTraceWorkload,
	steps map[string]automations.Step,
	evidence map[string]map[string]stepTraceEvidenceRow,
	calls []devices.CommandInput,
) error {
	if len(calls) != len(workload.commandOrder) {
		return fmt.Errorf("observed %d Command calls, want %d", len(calls), len(workload.commandOrder))
	}
	seenIDs := make(map[devices.CommandID]struct{}, len(calls))
	for index, stepID := range workload.commandOrder {
		actual := calls[index]
		if _, duplicate := seenIDs[actual.ID]; duplicate {
			return fmt.Errorf("observed duplicate Command call identity %q", actual.ID)
		}
		seenIDs[actual.ID] = struct{}{}
		if err := verifyStepTraceCommandAt(stepID, steps, evidence, actual); err != nil {
			return err
		}
	}
	return nil
}

func verifyStepTraceCommandAt(
	stepID string,
	steps map[string]automations.Step,
	evidence map[string]map[string]stepTraceEvidenceRow,
	actual devices.CommandInput,
) error {
	step, exists := steps[stepID]
	if !exists {
		return fmt.Errorf("selected Command Step %q is absent from definition", stepID)
	}
	expected, ok := step.Body.(automations.CommandStep)
	if !ok {
		return fmt.Errorf("selected Step %q is %T, want CommandStep", stepID, step.Body)
	}
	row, exists := evidence["command"][stepID]
	if !exists {
		return fmt.Errorf("retained Command evidence missing for Step %q", stepID)
	}
	commandID, err := stepTraceEvidenceValue(row, "command_id", "reserved_command_id")
	if err != nil {
		return fmt.Errorf("Step %q Command ID: %w", stepID, err)
	}
	verifiedID, err := stepTraceEvidenceValue(row, "verified_command_id")
	if err != nil {
		return fmt.Errorf("Step %q verified Command ID: %w", stepID, err)
	}
	correlationID, err := stepTraceEvidenceValue(row, "correlation_id", "reserved_correlation_id")
	if err != nil {
		return fmt.Errorf("Step %q correlation ID: %w", stepID, err)
	}
	if string(actual.ID) != commandID || verifiedID != string(actual.ID) {
		return fmt.Errorf("Step %q retained Command identity does not match observed call %q", stepID, actual.ID)
	}
	if correlationID != string(actual.CorrelationID) {
		return fmt.Errorf("Step %q retained correlation %q, observed %q", stepID, correlationID, actual.CorrelationID)
	}
	if actual.EntityID != expected.EntityID || actual.OperationName != expected.OperationName {
		return fmt.Errorf("Step %q Command entity/operation does not match definition", stepID)
	}
	matches, err := stepTraceJSONEqual([]byte(expected.Parameters), []byte(actual.Parameters))
	if err != nil {
		return fmt.Errorf("Step %q Command parameters are invalid JSON: %w", stepID, err)
	}
	if !matches {
		return fmt.Errorf("Step %q Command parameters do not match definition", stepID)
	}
	return nil
}

func verifyStepTraceNotifications(
	workload stepTraceWorkload,
	steps map[string]automations.Step,
	evidence map[string]map[string]stepTraceEvidenceRow,
	calls []notifications.Attempt,
) error {
	want := workload.selected["notification"]
	if len(calls) != len(want) {
		return fmt.Errorf("observed %d Notification calls, want %d", len(calls), len(want))
	}
	byID := make(map[notifications.AttemptID]notifications.Attempt, len(calls))
	for _, call := range calls {
		if _, exists := byID[call.ID]; exists {
			return fmt.Errorf("observed duplicate Notification Attempt ID %q", call.ID)
		}
		byID[call.ID] = call
	}
	used := make(map[notifications.AttemptID]struct{}, len(want))
	for _, stepID := range want {
		step, exists := steps[stepID]
		if !exists {
			return fmt.Errorf("selected Notification Step %q is absent from definition", stepID)
		}
		expected, ok := step.Body.(automations.NotificationStep)
		if !ok {
			return fmt.Errorf("selected Step %q is %T, want NotificationStep", stepID, step.Body)
		}
		row, exists := evidence["notification"][stepID]
		if !exists {
			return fmt.Errorf("retained Notification evidence missing for Step %q", stepID)
		}
		attemptID, err := stepTraceEvidenceValue(row, "attempt_id")
		if err != nil {
			return fmt.Errorf("Step %q Attempt ID: %w", stepID, err)
		}
		call, exists := byID[notifications.AttemptID(attemptID)]
		if !exists {
			return fmt.Errorf("Step %q retained Attempt ID %q has no observed Notification call", stepID, attemptID)
		}
		if call.DestinationID != expected.DestinationID || call.Message.Title != expected.Title ||
			call.Message.Text != expected.Message {
			return fmt.Errorf("Step %q Notification destination/message does not match definition", stepID)
		}
		used[call.ID] = struct{}{}
	}
	if len(used) != len(calls) {
		return fmt.Errorf("%d observed Notification calls were not linked to selected Steps", len(calls)-len(used))
	}
	return nil
}

func verifyStepTraceFunctions(
	workload stepTraceWorkload,
	steps map[string]automations.Step,
	evidence map[string]map[string]stepTraceEvidenceRow,
	calls []adapters.ReservedInvokeInput,
) error {
	want := workload.selected["function"]
	if len(calls) != len(want) {
		return fmt.Errorf("observed %d Function calls, want %d", len(calls), len(want))
	}
	byID := make(map[adapters.InvocationID]adapters.ReservedInvokeInput, len(calls))
	for _, call := range calls {
		if _, exists := byID[call.InvocationID]; exists {
			return fmt.Errorf("observed duplicate Function Invocation ID %q", call.InvocationID)
		}
		byID[call.InvocationID] = call
	}
	used := make(map[adapters.InvocationID]struct{}, len(want))
	for _, stepID := range want {
		invocationID, err := verifyStepTraceFunctionAt(stepID, steps, evidence, byID)
		if err != nil {
			return err
		}
		used[invocationID] = struct{}{}
	}
	if len(used) != len(calls) {
		return fmt.Errorf("%d observed Function calls were not linked to selected Steps", len(calls)-len(used))
	}
	return nil
}

func verifyStepTraceFunctionAt(
	stepID string,
	steps map[string]automations.Step,
	evidence map[string]map[string]stepTraceEvidenceRow,
	calls map[adapters.InvocationID]adapters.ReservedInvokeInput,
) (adapters.InvocationID, error) {
	step, exists := steps[stepID]
	if !exists {
		return "", fmt.Errorf("selected Function Step %q is absent from definition", stepID)
	}
	expected, ok := step.Body.(automations.FunctionStep)
	if !ok {
		return "", fmt.Errorf("selected Step %q is %T, want FunctionStep", stepID, step.Body)
	}
	row, exists := evidence["function"][stepID]
	if !exists {
		return "", fmt.Errorf("retained Function evidence missing for Step %q", stepID)
	}
	textID, err := stepTraceEvidenceValue(row, "invocation_id")
	if err != nil {
		return "", fmt.Errorf("Step %q Invocation ID: %w", stepID, err)
	}
	id := adapters.InvocationID(textID)
	call, exists := calls[id]
	if !exists {
		return "", fmt.Errorf("Step %q retained Invocation ID %q has no observed Function call", stepID, textID)
	}
	contract := call.ContractInput
	if contract.AdapterID != expected.AdapterID || contract.FunctionKey != expected.FunctionKey ||
		contract.ContractFingerprint != expected.ContractFingerprint {
		return "", fmt.Errorf("Step %q Function route/fingerprint does not match definition", stepID)
	}
	matches, err := stepTraceJSONEqual(expected.Arguments, contract.Arguments)
	if err != nil {
		return "", fmt.Errorf("Step %q Function arguments are invalid JSON: %w", stepID, err)
	}
	if !matches {
		return "", fmt.Errorf("Step %q Function arguments do not match definition", stepID)
	}
	return id, nil
}

func stepTraceEvidenceValue(row stepTraceEvidenceRow, field string, legacyFields ...string) (string, error) {
	for _, name := range append([]string{field}, legacyFields...) {
		if value := row.values[name]; value != "" {
			return value, nil
		}
	}
	encoded := row.values["evidence_json"]
	if encoded == "" {
		return "", fmt.Errorf("field %q is missing", field)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &values); err != nil {
		return "", fmt.Errorf("decode evidence JSON: %w", err)
	}
	value, exists := values[field]
	if !exists {
		return "", fmt.Errorf("field %q is missing", field)
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", fmt.Errorf("decode field %q: %w", field, err)
	}
	return text, nil
}

func stepTraceJSONEqual(left, right []byte) (bool, error) {
	decode := func(raw []byte) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return value, nil
	}
	leftValue, err := decode(left)
	if err != nil {
		return false, err
	}
	rightValue, err := decode(right)
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(leftValue, rightValue), nil
}

func TestStepTraceExternalCallOracleRejectsMismatches(t *testing.T) {
	t.Parallel()
	wantCommand := automations.CommandStep{
		EntityID: "entity", OperationName: "set", Parameters: []byte(`{"value":true}`),
	}
	workload := stepTraceWorkload{
		definition:   automations.Definition{Steps: []automations.Step{{ID: "selected", Body: wantCommand}}},
		selected:     map[string][]string{"command": {"selected"}},
		commandOrder: []string{"selected"},
	}
	command := devices.CommandInput{
		ID:            "cmd_observed",
		CorrelationID: "cor_observed",
		EntityID:      wantCommand.EntityID,
		OperationName: wantCommand.OperationName,
		Parameters:    append(devices.CommandParameters(nil), wantCommand.Parameters...),
	}
	row := stepTraceEvidenceRow{
		table: "automation_step_executions",
		values: map[string]string{
			"step_id":       "selected",
			"kind":          "command",
			"evidence_json": `{"command_id":"cmd_observed","correlation_id":"cor_observed","verified_command_id":"cmd_observed"}`,
		},
	}
	if err := verifyStepTraceExternalCalls(
		workload,
		[]stepTraceEvidenceRow{row},
		[]devices.CommandInput{command},
		nil,
		nil,
	); err != nil {
		t.Fatalf("valid command evidence rejected: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*devices.CommandInput, *stepTraceEvidenceRow)
	}{
		{
			name: "wrong entity",
			mutate: func(input *devices.CommandInput, _ *stepTraceEvidenceRow) {
				input.EntityID = "foreign-entity"
			},
		},
		{
			name: "wrong command arguments",
			mutate: func(input *devices.CommandInput, _ *stepTraceEvidenceRow) {
				input.Parameters = []byte(`{"value":false}`)
			},
		},
		{
			name: "foreign retained command link",
			mutate: func(_ *devices.CommandInput, retained *stepTraceEvidenceRow) {
				retained.values["evidence_json"] = `{"command_id":"cmd_foreign","correlation_id":"cor_observed","verified_command_id":"cmd_observed"}`
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			badCommand := command
			badRow := stepTraceEvidenceRow{table: row.table, values: maps.Clone(row.values)}
			test.mutate(&badCommand, &badRow)
			if err := verifyStepTraceExternalCalls(
				workload, []stepTraceEvidenceRow{badRow}, []devices.CommandInput{badCommand}, nil, nil,
			); err == nil {
				t.Fatal("oracle accepted mismatched external-call evidence")
			}
		})
	}
}

func readStepTraceEvidence(
	t *testing.T,
	database *sql.DB,
	runID string,
) []stepTraceEvidenceRow {
	t.Helper()
	names := readStepTraceTableNames(t, database)
	var result []stepTraceEvidenceRow
	for _, table := range names {
		result = append(result, readStepTraceTableEvidence(t, database, table, runID)...)
	}
	return result
}

func readStepTraceTableNames(t *testing.T, database *sql.DB) []string {
	t.Helper()
	tables, queryErr := database.QueryContext(t.Context(), `SELECT name FROM sqlite_schema
		WHERE type = 'table' AND (name LIKE 'automation_run_%' OR name = 'automation_step_executions')
		ORDER BY name`)
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	defer func() {
		if closeErr := tables.Close(); closeErr != nil {
			t.Errorf("close evidence table list: %v", closeErr)
		}
	}()
	var names []string
	for tables.Next() {
		var name string
		if scanErr := tables.Scan(&name); scanErr != nil {
			t.Fatal(scanErr)
		}
		names = append(names, name)
	}
	if iterationErr := tables.Err(); iterationErr != nil {
		t.Fatal(iterationErr)
	}
	return names
}

func readStepTraceTableEvidence(
	t *testing.T,
	database *sql.DB,
	table string,
	runID string,
) []stepTraceEvidenceRow {
	t.Helper()
	// Every schema column contributes to logical-byte totals; the schemas vary by baseline.
	//nolint:unqueryvet // Read every column so logical-byte totals remain comparable across historical schemas.
	query := `SELECT * FROM "` + strings.ReplaceAll(
		table,
		`"`,
		`""`,
	) + `" WHERE run_id = ?`
	rows, queryErr := database.QueryContext(t.Context(), query, runID)
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close evidence rows for %s: %v", table, closeErr)
		}
	}()
	columns, columnsErr := rows.Columns()
	if columnsErr != nil {
		t.Fatal(columnsErr)
	}
	var result []stepTraceEvidenceRow
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if scanErr := rows.Scan(pointers...); scanErr != nil {
			t.Fatal(scanErr)
		}
		row := stepTraceEvidenceRow{table: table, values: make(map[string]string, len(columns))}
		for index, value := range values {
			if value == nil {
				continue
			}
			text := fmt.Sprint(value)
			row.values[columns[index]] = text
			row.bytes += int64(len(text))
		}
		result = append(result, row)
	}
	if iterationErr := rows.Err(); iterationErr != nil {
		t.Fatal(iterationErr)
	}
	return result
}

func containsString(values []string, target string) bool {
	return slices.Contains(values, target)
}
