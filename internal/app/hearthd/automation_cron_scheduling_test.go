package hearthd //nolint:testpackage // C4 exercises the actual process with embedded NATS and socket-backed HTTP.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"

	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/api"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

// cronFakeClock is a mutex-guarded fake time source shared by the Run
// scheduler loop clock and the repository scheduler final-check clock,
// mirroring how Run aligns WithSchedulerClock with
// WithAutomationSchedulerClock from the same source.
type cronFakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *cronFakeClock) get() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *cronFakeClock) set(now time.Time) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = now
}

// startCronCore launches the actual process with a deterministic scheduler
// clock and an injected wakeup channel that fully replaces the one-second
// production timer. The caller drives exact minutes by setting the clock
// before signaling the wakeup; no wall-clock sleep times scheduler delivery.
// Every tick goes through signalCronTick, never a bare channel send, so the
// clock is advanced only after the previous evaluation provably completed.
func startCronCore(
	ctx context.Context,
	t *testing.T,
	databasePath, timezone, httpAddress, natsURL string,
	clock *cronFakeClock,
	wakeup chan struct{},
) (chan error, context.CancelFunc) {
	t.Helper()
	runContext, stop := context.WithCancel(ctx)
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{
			HouseholdTimezone: timezone,
			HTTPAddr:          httpAddress,
			NATSURL:           natsURL,
			SQLitePath:        databasePath,
		}, slog.New(slog.DiscardHandler),
			WithSchedulerClock(clock.get),
			WithSchedulerWakeup(wakeup))
	}()
	t.Cleanup(stop)
	return runErrors, stop
}

// signalCronTick delivers one scheduler wakeup for the instant already
// installed on the fake clock, then delivers a second wakeup (ack) at the
// SAME instant. A bare send on the unbuffered wakeup channel only proves the
// loop received the value: the loop reads the fake clock after receiving, so
// changing the clock right after a bare receipt lets the tick under test
// read the NEXT instant instead of the intended one, silently skipping the
// duplicate/startup/backward path the test claims to exercise. The scheduler
// loop is serial (one goroutine, one receive site, synchronous evaluation in
// runAutomationScheduler), so it can receive the ack only after the first
// evaluation fully returned. The ack receipt therefore proves the tick under
// test completed while the clock was still fixed; only after signalCronTick
// returns may the caller install the next instant. The ack itself is one more
// evaluation at the same instant — a harmless duplicate no-op for minutes
// that admit nothing — which is why no-op ticks (duplicate, startup,
// backward) whose high-water never moves can still be provably completed.
// Forward ticks additionally wait on waitCronHighWater, which proves the
// commit; the ack proves evaluation ordering. This barrier needs no
// production change: it uses only the injected test wakeup channel and the
// documented serial shape of the loop. It proves evaluation completed, not
// asynchronous Run execution, which the occurrence/run/dispatch waits below
// cover separately.
func signalCronTick(wakeup chan struct{}) {
	wakeup <- struct{}{}
	wakeup <- struct{}{}
}

// awaitCronSubscriptionClosed drains the subscription status channel until
// the NATS library closes it after reporting SubscriptionClosed. The wait is
// event-driven with the test context as the failure bound: no sleep, no
// absence polling. Subscription.Drain keeps invoking async callbacks until
// all pending messages are processed and only then closes the subscription,
// so returning here proves every callback for messages routed before the
// drain completed.
func awaitCronSubscriptionClosed(ctx context.Context, t *testing.T, statuses <-chan natsgo.SubStatus) {
	t.Helper()
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for status := range statuses {
			_ = status
		}
	}()
	select {
	case <-drained:
	case <-ctx.Done():
		t.Fatal("observer subscription did not finish draining")
	}
}

// registerCronPowerEntity registers one simulator power entity through the
// embedded SDK and serves Commands that accept and immediately observe,
// completing scheduled Steps through the shared device Command path.
func registerCronPowerEntity(
	ctx context.Context,
	t *testing.T,
	natsURL, bindingKey string,
	dispatches *atomic.Int64,
	blockSecond *atomic.Bool,
	entered, release chan struct{},
) string {
	t.Helper()
	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: "simulator", SoftwareName: "cron-scheduler", SoftwareVersion: "1", NATSURL: natsURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	support := contractpowerv1.Support{State: contractpowerv1.StateSupport{},
		Operations: contractpowerv1.OperationSupport{Set: contractpowerv1.SetSupport{}}}
	descriptor, err := sdkpowerv1.NewEntityDescriptor(adapter.EntityMetadata{
		Key: "power", ExternalID: bindingKey + ".power", Name: "Power",
	}, support)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := session.Register(ctx, adapter.Registration{
		BindingKey: bindingKey,
		Device:     adapter.DeviceDescriptor{Name: bindingKey, Kind: "light"},
		Entities:   []adapter.EntityDescriptor{descriptor},
	})
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	handler, err := sdkpowerv1.NewCommandHandler(entityID, support, sdkpowerv1.Handlers{
		Set: func(commandContext context.Context, command sdkpowerv1.SetCommand, responder adapter.Responder) error {
			count := dispatches.Add(1)
			evidence, acceptErr := responder.Accept()
			if acceptErr != nil {
				return acceptErr
			}
			if blockSecond.Load() && count == 1 {
				close(entered)
				select {
				case <-release:
				case <-commandContext.Done():
					return commandContext.Err()
				}
			}
			observation, observationErr := sdkpowerv1.NewObservation(sdkpowerv1.ObservationInput{
				EntityID: entityID, Support: support, State: contractpowerv1.State(command.Parameters.Value),
				AdapterReceivedAt: time.Now().UTC(),
			})
			if observationErr != nil {
				return observationErr
			}
			_, publishErr := evidence.PublishObservation(commandContext, observation)
			return publishErr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	serveContext, stopServing := context.WithCancel(ctx)
	t.Cleanup(stopServing)
	go func() { _ = session.ServeCommands(serveContext, handler) }()
	return entityID
}

// cronTimestampLayout mirrors the private automations.automationTimestampLayout
// (2006-01-02T15:04:05.000000000Z). The test file cannot import the private
// constant, so this duplicate formats expected high-water minutes for direct
// SQLite polling. Keep in sync with internal/modules/automations/sqlite_repository.go.
const cronTimestampLayout = "2006-01-02T15:04:05.000000000Z"

// queryCronHighWater opens a separate SQLite connection and returns the raw
// persisted high_water_minute. A separate connection is required because the
// running core holds its own connection; WAL plus busy_timeout lets this
// short-lived reader observe committed scheduler progress without disturbing
// the process.
func queryCronHighWater(ctx context.Context, t *testing.T, databasePath string) (string, error) {
	t.Helper()
	database, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = database.Close() }()
	var highWater string
	if queryErr := database.QueryRowContext(
		ctx,
		`SELECT high_water_minute FROM automation_scheduler_state WHERE scheduler_key = 1`,
	).Scan(&highWater); queryErr != nil {
		return "", queryErr
	}
	return highWater, nil
}

// waitCronHighWater polls persisted scheduler progress until it reaches the
// expected UTC minute. Forward scheduler ticks advance high_water_minute in
// the same SQLite transaction that commits Occurrences/Runs, so reaching the
// expected minute proves that tick's evaluation fully completed — unlike
// polling history length, which returns immediately when nothing was admitted
// yet and yields false-positive no-dispatch oracles.
func waitCronHighWater(ctx context.Context, t *testing.T, databasePath string, expected time.Time) {
	t.Helper()
	want := expected.UTC().Format(cronTimestampLayout)
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		got, err := queryCronHighWater(ctx, t, databasePath)
		if err != nil {
			// The scheduler state row does not exist until the first
			// successful initialization; keep polling through startup.
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, nil
		}
		return got == want, nil
	})
}

// cronCountOccurrencesAt counts history rows for one exact UTC minute, so
// no-admission assertions can prove a specific minute stayed empty after its
// tick provably completed, rather than asserting a global length that may not
// yet include the tick under test.
func cronCountOccurrencesAt(occurrences []automationsapi.AutomationOccurrenceBody, minute time.Time) int {
	count := 0
	for _, occurrence := range occurrences {
		if occurrence.ScheduledAt.Equal(minute) {
			count++
		}
	}
	return count
}

// cronOverrideScheduleNotBefore makes fixed-instant fixture eligibility
// independent of the real wall-clock date. HTTP creation stamps
// schedule_not_before from the repository wall clock (first minute after the
// write), so a hardcoded November 2026 evaluation minute is only eligible
// while tests run before that date. Overwriting the bound to a fixed past
// instant keeps the November fold oracle stable no matter when the suite runs.
// It must be called after HTTP creation and before any tick that evaluates the
// fixture minute.
func cronOverrideScheduleNotBefore(
	ctx context.Context, t *testing.T, databasePath, automationID string, bound time.Time,
) {
	t.Helper()
	database, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	if _, execErr := database.ExecContext(
		ctx,
		`UPDATE automations SET schedule_not_before = ? WHERE id = ?`,
		bound.UTC().Format(cronTimestampLayout), automationID,
	); execErr != nil {
		t.Fatal(execErr)
	}
}

// insertCronCommandEvidence seeds one owned device Command row for a begun
// automation Step without dispatching through NATS, mirroring
// insertAutomationCommandEvidence in the automations package tests (which this
// package cannot import). The crash-after-first-Command window (spec B8)
// requires real owned evidence: BeginAutomationStep alone only reserves
// intent, while a matching commands row proves the first Command existed
// before the crash, so recovery must preserve its evidence and never resume
// the remaining Steps.
func insertCronCommandEvidence(
	ctx context.Context,
	t *testing.T,
	database *sql.DB,
	commandID devices.CommandID,
	correlationID devices.CorrelationID,
	status string,
) {
	t.Helper()
	timestamp := time.Date(2026, time.June, 1, 19, 0, 0, 0, time.UTC).Format(cronTimestampLayout)
	statements := []string{
		`INSERT OR IGNORE INTO devices(id,kind,name,created_at,updated_at) VALUES ('dev_01900000-0000-7000-8000-000000000001','light','Light','` + timestamp + `','` + timestamp + `')`,
		`INSERT OR IGNORE INTO entities(id,device_id,name,type_id,support_json,created_at,updated_at) VALUES ('ent_01900000-0000-7000-8000-000000000001','dev_01900000-0000-7000-8000-000000000001','Power','hearth.power/v1','{}','` + timestamp + `','` + timestamp + `')`,
	}
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	var completed, observation any
	if status != "requested" {
		completed = timestamp
	}
	if status == "satisfied" {
		observation = "obs_" + string(commandID)[4:]
	}
	if _, err := database.ExecContext(
		ctx,
		`INSERT INTO commands(id,entity_id,adapter_id,operation,parameters_json,correlation_id,status,requested_at,deadline_at,completed_at,outcome_observation_id) VALUES (?,'ent_01900000-0000-7000-8000-000000000001','test','set','{}',?,?,?,?,?,?)`,
		string(commandID),
		string(correlationID),
		status,
		timestamp,
		timestamp,
		completed,
		observation,
	); err != nil {
		t.Fatal(err)
	}
}

func waitCronCommandSubscription(t *testing.T, server *natsserver.Server) {
	t.Helper()
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		subscriptions, err := server.Subsz(&natsserver.SubszOptions{Subscriptions: true})
		if err != nil {
			return false, err
		}
		for _, subscription := range subscriptions.Subs {
			if strings.Contains(subscription.Subject, ".command.") {
				return true, nil
			}
		}
		return false, nil
	})
}

// cronHTTPClient builds the C4-only process HTTP client. It disables keep
// alive connections so every probe dials a fresh connection and closes it
// after the response. The shared [http.DefaultClient] pools idle keep alive
// connections, and the polling helpers built on it leave freshly accepted
// server connections in StateNew around core shutdown.
// [http.Server.Shutdown] cannot reach quiescence while a StateNew connection
// is present (the five-second StateNew grace equals the five-second
// production shutdown timeout in run.go), so a lingering keep alive
// connection fails the shutdown with "shutdown HTTP: context deadline
// exceeded". A fresh client per probe keeps core shutdown independent of
// client transport timing. It never touches the shared transport.
func cronHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
	}
}

// cronDrainAndClose fully consumes and closes a process HTTP response body so
// no half-read connection state survives a probe.
func cronDrainAndClose(response *http.Response) {
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
}

// cronProcessRequest performs one C4 process HTTP request and decodes the
// expected status body as JSON, failing the test on transport errors and
// unexpected statuses. It mirrors automationProcessRequest without sharing
// the global keep alive transport: see cronHTTPClient.
func cronProcessRequest[T any](ctx context.Context, t *testing.T, method, url, body, key string, status int) T {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Close = true
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := cronHTTPClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer cronDrainAndClose(response)
	if response.StatusCode != status {
		t.Fatalf("%s %s status=%d want=%d", method, url, response.StatusCode, status)
	}
	var result T
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

// cronWaitForCoreHealthz waits until /healthz serves, which happens only after
// startup completes. It mirrors waitForCoreHealthz without sharing the global
// keep alive transport: see cronHTTPClient.
func cronWaitForCoreHealthz(
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
		request.Close = true
		response, responseErr := cronHTTPClient().Do(request)
		if responseErr != nil {
			return false, nil
		}
		defer cronDrainAndClose(response)
		return response.StatusCode == http.StatusOK, nil
	})
}

// cronPollReadyz polls /readyz until it reports ready. It mirrors pollReadyz
// without sharing the global keep alive transport: see cronHTTPClient.
func cronPollReadyz(ctx context.Context, t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Close = true
		response, err := cronHTTPClient().Do(request)
		if err == nil {
			ready := response.StatusCode == http.StatusOK
			cronDrainAndClose(response)
			if ready {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for readyz: %v", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("readyz at %s never became ready", url)
}

// cronPostRaw performs one process HTTP request and returns the status with
// the raw body, so grammar rejections can assert exact statuses and diagnostics.
func cronPostRaw(
	ctx context.Context,
	t *testing.T,
	method, url, body, key string,
) (int, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Close = true
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := cronHTTPClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(raw)
}

func cronDefinitionJSON(name, entityID string, triggers ...string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, `{"name":%q,"enabled":true,"triggers":[`, name)
	for i, trigger := range triggers {
		if i > 0 {
			builder.WriteString(",")
		}
		fmt.Fprintf(&builder, `{"id":"trigger-%d","kind":"cron","expression":%q}`, i+1, trigger)
	}
	fmt.Fprintf(&builder, `],"steps":[{"entity_id":%q,"operation_name":"set","parameters":{"value":true}}]}`, entityID)
	return builder.String()
}

// cronOccurrencePage mirrors the flat Huma list encoding: the response body
// carries items directly rather than the handler Output wrapper.
type cronOccurrencePage struct {
	Items []automationsapi.AutomationOccurrenceBody `json:"items"`
}

type cronGapPage struct {
	Items []automationsapi.AutomationScheduleGapBody `json:"items"`
}

type cronRunPage struct {
	Items []automationsapi.AutomationRunSummaryBody `json:"items"`
}

func cronListOccurrences(
	ctx context.Context,
	t *testing.T,
	baseURL string,
) []automationsapi.AutomationOccurrenceBody {
	t.Helper()
	output := cronProcessRequest[cronOccurrencePage](
		ctx, t, http.MethodGet, baseURL+"/v1/automation-occurrences", "", "", http.StatusOK)
	return output.Items
}

func cronListGaps(
	ctx context.Context,
	t *testing.T,
	baseURL string,
) []automationsapi.AutomationScheduleGapBody {
	t.Helper()
	output := cronProcessRequest[cronGapPage](
		ctx, t, http.MethodGet, baseURL+"/v1/automation-schedule-gaps", "", "", http.StatusOK)
	return output.Items
}

func cronListRuns(
	ctx context.Context,
	t *testing.T,
	baseURL string,
) []automationsapi.AutomationRunSummaryBody {
	t.Helper()
	output := cronProcessRequest[cronRunPage](
		ctx, t, http.MethodGet, baseURL+"/v1/automation-runs", "", "", http.StatusOK)
	return output.Items
}

func waitCronOccurrences(
	ctx context.Context,
	t *testing.T,
	baseURL string,
	count int,
) []automationsapi.AutomationOccurrenceBody {
	t.Helper()
	var items []automationsapi.AutomationOccurrenceBody
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		items = cronListOccurrences(ctx, t, baseURL)
		return len(items) == count, nil
	})
	return items
}

func waitCronRunSucceeded(
	ctx context.Context,
	t *testing.T,
	baseURL, runID string,
) automationsapi.AutomationRunBody {
	t.Helper()
	var run automationsapi.AutomationRunBody
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		run = cronProcessRequest[automationsapi.AutomationRunBody](
			ctx, t, http.MethodGet, baseURL+"/v1/automation-runs/"+runID, "", "", http.StatusOK)
		return run.Status == automations.AutomationRunStatusSucceeded, nil
	})
	return run
}

func stopCronCore(ctx context.Context, t *testing.T, stop context.CancelFunc, runErrors <-chan error) {
	t.Helper()
	stop()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-ctx.Done():
		t.Fatal("process did not shut down")
	}
}

// C4/B1+B4+B6+B10: the actual process admits one coalesced scheduled Run for
// same-minute matches and executes it through the shared simulator device
// Command path. An invalid cron expression is rejected over HTTP and never
// dispatches; definition responses expose the household zone.
func TestCoreSchedulerEndToEndDispatchCoalesced(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	httpAddress := unusedLoopbackAddress(t)
	baseURL := "http://" + httpAddress
	wallMinute := time.Now().UTC().Truncate(time.Minute)
	fakeStart := wallMinute.Add(5 * time.Minute).Add(20 * time.Second)
	clock := &cronFakeClock{now: fakeStart}
	// Unbuffered wakeup gives a receipt handshake: each send blocks until the
	// scheduler loop receives it. Receipt alone never proves evaluation
	// completed (the loop reads the fake clock after receiving), so every
	// tick below goes through signalCronTick: the ack receipt proves the
	// evaluation completed while the clock was still fixed, and only then is
	// the clock advanced. Forward ticks additionally wait on high-water,
	// which advances in the same transaction that commits Occurrences/Runs.
	wakeup := make(chan struct{})
	runErrors, stop := startCronCore(ctx, t, databasePath, "UTC", httpAddress, server.ClientURL(), clock, wakeup)
	cronWaitForCoreHealthz(ctx, t, httpAddress, runErrors)
	cronPollReadyz(ctx, t, baseURL+"/readyz")

	var dispatches atomic.Int64
	var blockSecond atomic.Bool
	entityID := registerCronPowerEntity(ctx, t, server.ClientURL(), "cron-coalesce",
		&dispatches, &blockSecond, make(chan struct{}), make(chan struct{}))
	waitCronCommandSubscription(t, server)

	// B1: parser-supported but product-rejected expressions never dispatch.
	if status, raw := cronPostRaw(ctx, t, http.MethodPost, baseURL+"/v1/automations",
		strings.Replace(
			cronDefinitionJSON("Bad", entityID, "* * * * *"),
			`"expression":"* * * * *"`,
			`"expression":"TZ=UTC * * * * *"`,
			1,
		), ""); status != http.StatusBadRequest {
		t.Fatalf("rejected grammar status=%d body=%s", status, raw)
	}

	created := cronProcessRequest[automationsapi.AutomationBody](ctx, t, http.MethodPost,
		baseURL+"/v1/automations", cronDefinitionJSON("Coalesce", entityID, "* * * * *", "* * * * *"), "",
		http.StatusCreated)
	if created.HouseholdTimezone != "UTC" {
		t.Fatalf("definition household_timezone = %q, want UTC", created.HouseholdTimezone)
	}

	// B5 current-minute startup: the startup minute itself never executes.
	// The ack inside signalCronTick proves the B5 evaluation completed while
	// the clock still read the startup instant, so the per-minute count below
	// asserts an evaluated minute rather than an unevaluated one (polling
	// len==0 right after a bare wakeup would return before evaluation).
	startupMinute := fakeStart.UTC().Truncate(time.Minute)
	clock.set(fakeStart.Add(10 * time.Second))
	signalCronTick(wakeup)

	// B4: the next minute admits exactly one coalesced Run for both matches.
	minute := startupMinute.Add(time.Minute)
	clock.set(minute.Add(20 * time.Second))
	signalCronTick(wakeup)
	// Forward-tick completion barrier: high-water advances in the same
	// transaction that commits the Occurrence/Run.
	waitCronHighWater(ctx, t, databasePath, minute)
	occurrences := waitCronOccurrences(ctx, t, baseURL, 1)
	occurrence := occurrences[0]
	if occurrence.Status != automations.AutomationOccurrenceStarted || occurrence.RunID == nil ||
		!occurrence.ScheduledAt.Equal(minute) || occurrence.Timezone != "UTC" ||
		len(occurrence.MatchedTriggers) != 2 ||
		occurrence.MatchedTriggers[0].ID != "trigger-1" ||
		occurrence.MatchedTriggers[1].ID != "trigger-2" {
		t.Fatalf("coalesced occurrence = %+v", occurrence)
	}
	run := waitCronRunSucceeded(ctx, t, baseURL, string(*occurrence.RunID))
	if run.Source != automations.AutomationRunSourceScheduled || run.ScheduledAt == nil ||
		!run.ScheduledAt.Equal(minute) || len(run.MatchedTriggerIDs) != 2 ||
		run.MatchedTriggerIDs[0] != "trigger-1" || run.MatchedTriggerIDs[1] != "trigger-2" {
		t.Fatalf("coalesced run = %+v", run)
	}
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		return dispatches.Load() == 1, nil
	})
	// The B5 startup minute stayed empty across both completed evaluations.
	if got := cronCountOccurrencesAt(cronListOccurrences(ctx, t, baseURL), startupMinute); got != 0 {
		t.Fatalf("startup minute executed %d occurrences", got)
	}

	// B4 repeat tick: the same minute never duplicates.
	// The ack inside signalCronTick proves the duplicate evaluation completed
	// while the clock still read the same minute, so this tick genuinely
	// exercised the duplicate path instead of accidentally reading the probe
	// instant. The probe minute admittedly executes (one more
	// Occurrence/Run/dispatch), so the no-duplicate oracle counts per-minute
	// rows instead of asserting a global length that the probe changes.
	clock.set(minute.Add(50 * time.Second))
	signalCronTick(wakeup)
	probeMinute := minute.Add(time.Minute)
	clock.set(probeMinute.Add(5 * time.Second))
	signalCronTick(wakeup)
	waitCronHighWater(ctx, t, databasePath, probeMinute)
	occurrences = waitCronOccurrences(ctx, t, baseURL, 2)
	if got := cronCountOccurrencesAt(occurrences, minute); got != 1 {
		t.Fatalf("repeat tick duplicated minute: %d occurrences at %v", got, minute)
	}
	if got := cronCountOccurrencesAt(occurrences, probeMinute); got != 1 {
		t.Fatalf("probe minute missing: %+v", occurrences)
	}
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		return dispatches.Load() == 2, nil
	})
	stopCronCore(ctx, t, stop, runErrors)
}

// C4/B6: with a blocked manual Run, the actual process commits exactly one
// skipped Occurrence containing every matching Trigger snapshot for the same
// minute, while an independent Automation still executes.
func TestCoreSchedulerEndToEndOverlapSkip(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	httpAddress := unusedLoopbackAddress(t)
	baseURL := "http://" + httpAddress
	wallMinute := time.Now().UTC().Truncate(time.Minute)
	fakeStart := wallMinute.Add(5 * time.Minute).Add(20 * time.Second)
	clock := &cronFakeClock{now: fakeStart}
	wakeup := make(chan struct{})
	runErrors, stop := startCronCore(ctx, t, databasePath, "UTC", httpAddress, server.ClientURL(), clock, wakeup)
	defer func() { stopCronCore(ctx, t, stop, runErrors) }()
	cronWaitForCoreHealthz(ctx, t, httpAddress, runErrors)
	cronPollReadyz(ctx, t, baseURL+"/readyz")

	var dispatches atomic.Int64
	var blockSecond atomic.Bool
	blockSecond.Store(true)
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	entityID := registerCronPowerEntity(ctx, t, server.ClientURL(), "cron-overlap",
		&dispatches, &blockSecond, entered, release)
	waitCronCommandSubscription(t, server)

	blocked := cronProcessRequest[automationsapi.AutomationBody](ctx, t, http.MethodPost,
		baseURL+"/v1/automations", cronDefinitionJSON("Blocked", entityID, "* * * * *", "* * * * *"), "",
		http.StatusCreated)
	independent := cronProcessRequest[automationsapi.AutomationBody](ctx, t, http.MethodPost,
		baseURL+"/v1/automations", cronDefinitionJSON("Independent", entityID, "* * * * *"), "",
		http.StatusCreated)

	manual := cronProcessRequest[automationsapi.AutomationRunBody](ctx, t, http.MethodPost,
		baseURL+"/v1/automations/"+string(blocked.ID)+"/runs", "", "overlap-block", http.StatusAccepted)
	if len(manual.MatchedTriggerIDs) != 0 {
		t.Fatalf("manual run matched triggers = %+v", manual.MatchedTriggerIDs)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("manual Command never reached the adapter")
	}

	minute := fakeStart.UTC().Truncate(time.Minute).Add(time.Minute)
	clock.set(minute.Add(20 * time.Second))
	signalCronTick(wakeup)
	occurrences := waitCronOccurrences(ctx, t, baseURL, 2)
	var skipped, started *automationsapi.AutomationOccurrenceBody
	for i := range occurrences {
		switch occurrences[i].AutomationID {
		case blocked.ID:
			skipped = &occurrences[i]
		case independent.ID:
			started = &occurrences[i]
		}
	}
	if skipped == nil || started == nil {
		t.Fatalf("overlap occurrences = %+v", occurrences)
	}
	if skipped.Status != automations.AutomationOccurrenceSkipped || skipped.RunID != nil ||
		skipped.SkipReason == nil || *skipped.SkipReason != string(automations.AutomationOccurrenceSkipActive) ||
		len(skipped.MatchedTriggers) != 2 || skipped.MatchedTriggers[0].ID != "trigger-1" ||
		skipped.MatchedTriggers[1].ID != "trigger-2" || !skipped.ScheduledAt.Equal(minute) {
		t.Fatalf("skipped occurrence = %+v", skipped)
	}
	if started.Status != automations.AutomationOccurrenceStarted || started.RunID == nil ||
		!started.ScheduledAt.Equal(minute) {
		t.Fatalf("independent occurrence = %+v", started)
	}
	runs := cronListRuns(ctx, t, baseURL)
	scheduled := 0
	for _, summary := range runs {
		if summary.Source == automations.AutomationRunSourceScheduled {
			scheduled++
		}
	}
	if scheduled != 1 {
		t.Fatalf("scheduled runs during overlap = %d, want 1", scheduled)
	}

	release <- struct{}{}
	waitCronRunSucceeded(ctx, t, baseURL, string(manual.ID))
	if started.RunID != nil {
		waitCronRunSucceeded(ctx, t, baseURL, string(*started.RunID))
	}
}

// C4/B5+B9: restart skips missed minutes and the in-progress restart minute,
// records one bounded core_restart gap, and admits the next future minute.
func TestCoreSchedulerRestartNoCatchUpEndToEnd(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	httpAddress := unusedLoopbackAddress(t)
	baseURL := "http://" + httpAddress
	wallMinute := time.Now().UTC().Truncate(time.Minute)
	first := wallMinute.Add(5 * time.Minute).Add(20 * time.Second)
	firstMinute := first.UTC().Truncate(time.Minute)
	clock := &cronFakeClock{now: first}
	wakeup := make(chan struct{})
	runErrors, stop := startCronCore(ctx, t, databasePath, "UTC", httpAddress, server.ClientURL(), clock, wakeup)
	cronWaitForCoreHealthz(ctx, t, httpAddress, runErrors)
	cronPollReadyz(ctx, t, baseURL+"/readyz")

	var dispatches atomic.Int64
	var blockSecond atomic.Bool
	entityID := registerCronPowerEntity(ctx, t, server.ClientURL(), "cron-restart",
		&dispatches, &blockSecond, make(chan struct{}), make(chan struct{}))
	waitCronCommandSubscription(t, server)
	cronProcessRequest[automationsapi.AutomationBody](ctx, t, http.MethodPost,
		baseURL+"/v1/automations", cronDefinitionJSON("Restart", entityID, "* * * * *"), "",
		http.StatusCreated)

	next := firstMinute.Add(time.Minute)
	clock.set(next.Add(20 * time.Second))
	signalCronTick(wakeup)
	waitCronHighWater(ctx, t, databasePath, next)
	occurrences := waitCronOccurrences(ctx, t, baseURL, 1)
	if !occurrences[0].ScheduledAt.Equal(next) {
		t.Fatalf("first occurrence = %+v", occurrences[0])
	}
	waitCronRunSucceeded(ctx, t, baseURL, string(*occurrences[0].RunID))
	stopCronCore(ctx, t, stop, runErrors)

	// Restart three minutes later: the two missed minutes and the restart
	// minute itself must not execute.
	restart := next.Add(3 * time.Minute)
	clock.set(restart)
	wakeupRestart := make(chan struct{})
	runErrors, stop = startCronCore(ctx, t, databasePath, "UTC", httpAddress, server.ClientURL(), clock, wakeupRestart)
	defer func() { stopCronCore(ctx, t, stop, runErrors) }()
	cronWaitForCoreHealthz(ctx, t, httpAddress, runErrors)
	cronPollReadyz(ctx, t, baseURL+"/readyz")
	// The restart-minute tick is a no-op (high-water never moves), so no poll
	// can prove it completed. The ack inside signalCronTick proves it was
	// evaluated at the restart instant before the clock advanced; all
	// restart-minute assertions additionally happen after the future
	// high-water barrier below.
	restartMinute := restart.UTC().Truncate(time.Minute)
	clock.set(restartMinute.Add(5 * time.Second))
	signalCronTick(wakeupRestart)
	future := restartMinute.Add(time.Minute)
	clock.set(future.Add(5 * time.Second))
	signalCronTick(wakeupRestart)
	waitCronHighWater(ctx, t, databasePath, future)
	occurrences = waitCronOccurrences(ctx, t, baseURL, 2)
	if got := cronCountOccurrencesAt(occurrences, restartMinute); got != 0 {
		t.Fatalf("restart minute executed %d occurrences", got)
	}
	if !occurrences[0].ScheduledAt.Equal(future) || !occurrences[1].ScheduledAt.Equal(next) {
		t.Fatalf("post-restart occurrences = %+v", occurrences)
	}
	gaps := cronListGaps(ctx, t, baseURL)
	if len(gaps) != 1 || gaps[0].Reason != "core_restart" ||
		!gaps[0].FromExclusive.Equal(next) || !gaps[0].ThroughInclusive.Equal(restartMinute) {
		t.Fatalf("restart gaps = %+v", gaps)
	}
	// The future minute above admitted normally with no additional gap: the
	// single gap is still exactly the restart interval.
	if gaps = cronListGaps(ctx, t, baseURL); len(gaps) != 1 {
		t.Fatalf("contiguous minute recorded a gap: %+v", gaps)
	}
}

// C4/B2+B3: household-local matching follows the configured zone rather than
// fixed UTC, and the repeated New York 01:30 admits only its first fold.
// Hand-specified instants are the oracle: 2026-11-01T05:30:00Z matches,
// 2026-11-01T06:30:00Z does not.
func TestCoreSchedulerNewYorkFoldEndToEnd(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	httpAddress := unusedLoopbackAddress(t)
	baseURL := "http://" + httpAddress
	start := time.Date(2026, time.November, 1, 5, 29, 20, 0, time.UTC)
	clock := &cronFakeClock{now: start}
	// Every tick goes through signalCronTick (ack receipt proves evaluation
	// completed at the installed instant); forward ticks additionally wait on
	// high-water, which advances in the same transaction that commits.
	wakeup := make(chan struct{})
	runErrors, stop := startCronCore(
		ctx, t, databasePath, "America/New_York", httpAddress, server.ClientURL(), clock, wakeup)
	// The B3 restart phase replaces this cleanup stop: the deferred stop runs
	// after the restarted core stops, so reassigning stop/runErrors is safe.
	// Keep the first core's stop until the explicit restart below.
	cronWaitForCoreHealthz(ctx, t, httpAddress, runErrors)
	cronPollReadyz(ctx, t, baseURL+"/readyz")

	var dispatches atomic.Int64
	var blockSecond atomic.Bool
	entityID := registerCronPowerEntity(ctx, t, server.ClientURL(), "cron-fold",
		&dispatches, &blockSecond, make(chan struct{}), make(chan struct{}))
	waitCronCommandSubscription(t, server)
	createdBody := fmt.Sprintf(
		`{"name":"Fold","enabled":true,"triggers":[{"id":"early","kind":"cron","expression":"30 1 * * *"}]`+
			`,"steps":[{"entity_id":%q,"operation_name":"set","parameters":{"value":true}}]}`,
		entityID,
	)
	created := cronProcessRequest[automationsapi.AutomationBody](ctx, t, http.MethodPost,
		baseURL+"/v1/automations", createdBody, "", http.StatusCreated)
	if created.HouseholdTimezone != "America/New_York" {
		t.Fatalf("definition household_timezone = %q", created.HouseholdTimezone)
	}
	// Fixed-instant eligibility independent of the real date: HTTP creation
	// stamps schedule_not_before from the wall clock, which would filter the
	// November 2026 minutes once the suite runs after that date. Pin the bound
	// to a fixed past instant so the fold oracle holds at any real date.
	cronOverrideScheduleNotBefore(ctx, t, databasePath, string(created.ID),
		time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC))

	// B2: 05:30Z is 01:30 EDT, so the local expression matches.
	first := time.Date(2026, time.November, 1, 5, 30, 0, 0, time.UTC)
	clock.set(first.Add(20 * time.Second))
	signalCronTick(wakeup)
	waitCronHighWater(ctx, t, databasePath, first)
	occurrences := waitCronOccurrences(ctx, t, baseURL, 1)
	if !occurrences[0].ScheduledAt.Equal(first) ||
		occurrences[0].Status != automations.AutomationOccurrenceStarted ||
		occurrences[0].Timezone != "America/New_York" {
		t.Fatalf("first-fold occurrence = %+v", occurrences[0])
	}
	waitCronRunSucceeded(ctx, t, baseURL, string(*occurrences[0].RunID))
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		return dispatches.Load() == 1, nil
	})

	// B3: restart into the fold, then the second 01:30 at 06:30Z must not admit.
	// Stopping after the first fold clears all in-memory "last fired" state, so
	// rejecting the second fold after a real restart proves the bounded
	// backward civil-minute comparison decides — not process memory. Startup at
	// 06:20Z (second 01:20 EST) records the missed interval, then the 06:30Z
	// tick is a forward evaluation (high-water must reach 06:30) that admits
	// nothing and dispatches nothing.
	stopCronCore(ctx, t, stop, runErrors)
	restartEntry := time.Date(2026, time.November, 1, 6, 20, 0, 0, time.UTC)
	clock.set(restartEntry)
	wakeupRestart := make(chan struct{})
	runErrors, stop = startCronCore(
		ctx, t, databasePath, "America/New_York", httpAddress, server.ClientURL(), clock, wakeupRestart)
	defer func() { stopCronCore(ctx, t, stop, runErrors) }()
	cronWaitForCoreHealthz(ctx, t, httpAddress, runErrors)
	cronPollReadyz(ctx, t, baseURL+"/readyz")
	second := time.Date(2026, time.November, 1, 6, 30, 0, 0, time.UTC)
	clock.set(second.Add(20 * time.Second))
	signalCronTick(wakeupRestart)
	waitCronHighWater(ctx, t, databasePath, second)
	occurrences = cronListOccurrences(ctx, t, baseURL)
	if len(occurrences) != 1 || !occurrences[0].ScheduledAt.Equal(first) {
		t.Fatalf("second fold admitted: %+v", occurrences)
	}
	if got := dispatches.Load(); got != 1 {
		t.Fatalf("second fold dispatched %d commands", got)
	}
}

// seedCronCrashFixtures persists the two crash windows a real Run startup
// must recover: one scheduled Run admitted but never given to a worker, and
// one with its first Step begun AND its first owned Command durably recorded.
// BeginAutomationStep alone only reserves intent; the inserted commands row
// (same shape as insertAutomationCommandEvidence in the automations package
// tests) proves the first Command existed before the crash, matching spec B8
// "crash after first Command never resumes remaining Steps". It returns the
// admitted UTC minute for the startup assertions.
func seedCronCrashFixtures(
	ctx context.Context,
	t *testing.T,
	databasePath string,
	clock *cronFakeClock,
) time.Time {
	t.Helper()
	database, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = platformdb.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	wallMinute := time.Now().UTC().Truncate(time.Minute)
	seedMinute := wallMinute.Add(5 * time.Minute)
	clock.set(seedMinute.Add(time.Minute).Add(20 * time.Second))
	repo := automations.NewSQLiteRepository(database, automations.WithAutomationSchedulerClock(clock.get))
	if _, err = repo.InitializeAutomationScheduler(ctx, seedMinute, time.UTC); err != nil {
		t.Fatal(err)
	}
	steps := func() []automations.AutomationStep {
		return []automations.AutomationStep{
			{
				EntityID:      "ent_01900000-0000-7000-8000-000000000001",
				OperationName: "set",
				Parameters:    devices.CommandParameters(`{"value":true}`),
			},
			{
				EntityID:      "ent_01900000-0000-7000-8000-000000000001",
				OperationName: "set",
				Parameters:    devices.CommandParameters(`{"value":false}`),
			},
		}
	}
	_, err = repo.CreateAutomation(ctx, automations.AutomationDefinition{
		Name: "CrashBeforeWorker", Enabled: true,
		Triggers: []automations.AutomationTrigger{
			{ID: "nightly", Kind: automations.AutomationTriggerKindCron, Expression: "* * * * *"},
		},
		Steps: steps(),
	})
	if err != nil {
		t.Fatal(err)
	}
	afterFirst, err := repo.CreateAutomation(ctx, automations.AutomationDefinition{
		Name: "CrashAfterFirst", Enabled: true,
		Triggers: []automations.AutomationTrigger{
			{ID: "nightly", Kind: automations.AutomationTriggerKindCron, Expression: "* * * * *"},
		},
		Steps: steps(),
	})
	if err != nil {
		t.Fatal(err)
	}
	admitMinute := seedMinute.Add(time.Minute)
	batch, err := repo.EvaluateAutomationMinute(ctx, admitMinute.Add(20*time.Second), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Runs) != 2 || len(batch.Occurrences) != 2 {
		t.Fatalf("seed batch = %+v", batch)
	}
	var afterFirstRunID automations.AutomationRunID
	for _, run := range batch.Runs {
		if run.Snapshot.AutomationID == afterFirst.ID {
			afterFirstRunID = run.ID
		}
	}
	commandID, err := devices.NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	correlationID, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.BeginAutomationStep(ctx, automations.AutomationStepStart{
		RunID: afterFirstRunID, Index: 0, CommandID: commandID, CorrelationID: correlationID,
	}); err != nil {
		t.Fatal(err)
	}
	// Owned first-Command evidence: the Step reserved the identities above, and
	// this matching commands row makes the Command owned (dispatched but not
	// yet observed), so recovery must preserve its evidence while still
	// interrupting without resuming Step 1.
	insertCronCommandEvidence(ctx, t, database, commandID, correlationID, "dispatched")
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	return admitMinute
}

// verifyCronCrashRecovery proves both persisted crash windows interrupted
// with core_restarted and left their remaining Steps unattempted, so nothing
// can ever replay them. CrashBeforeWorker never began, so both Steps stay
// without Command evidence; CrashAfterFirst preserves its owned first-Command
// evidence (CommandID equals the reserved identity) while Step 1 stays
// unattempted.
func verifyCronCrashRecovery(ctx context.Context, t *testing.T, baseURL string) {
	t.Helper()
	runs := cronListRuns(ctx, t, baseURL)
	byName := make(map[string]automationsapi.AutomationRunSummaryBody, len(runs))
	for _, summary := range runs {
		byName[summary.Name] = summary
	}
	for _, name := range []string{"CrashBeforeWorker", "CrashAfterFirst"} {
		summary, ok := byName[name]
		if !ok || summary.Status != automations.AutomationRunStatusInterrupted ||
			summary.Source != automations.AutomationRunSourceScheduled {
			t.Fatalf("recovered runs = %+v", runs)
		}
		detail := cronProcessRequest[automationsapi.AutomationRunBody](
			ctx, t, http.MethodGet, baseURL+"/v1/automation-runs/"+string(summary.ID), "", "", http.StatusOK)
		if detail.FailureCode == nil || *detail.FailureCode != string(automations.AutomationFailureCoreRestarted) ||
			len(detail.Steps) != 2 || detail.Steps[1].Status != automations.AutomationStepStatusNotAttempted {
			t.Fatalf("recovered run never replays remaining Steps: %+v", detail)
		}
		if name == "CrashAfterFirst" {
			if detail.Steps[0].Status != automations.AutomationStepStatusInterrupted ||
				detail.Steps[0].CommandID == nil || detail.Steps[0].ReservedCommandID == nil ||
				*detail.Steps[0].CommandID != *detail.Steps[0].ReservedCommandID {
				t.Fatalf("crash-after-Command lost owned evidence: %+v", detail)
			}
		} else {
			if detail.Steps[0].CommandID != nil {
				t.Fatalf("crash-before-worker invented Command evidence: %+v", detail)
			}
		}
	}
}

// C4/B8: crash-admitted scheduled Runs interrupt on startup with zero replay.
// An abrupt crash cannot be safely simulated in-process, so this test persists
// the exact crash windows (admitted-before-worker and begun-first-Step) with a
// real repository, then starts the actual process and proves startup recovery
// interrupts both Runs, leaves remaining Steps unattempted, and delivers zero
// Commands to the shared NATS transport.
func TestCoreSchedulerCrashRecoveryZeroReplay(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	clock := &cronFakeClock{}
	admitMinute := seedCronCrashFixtures(ctx, t, databasePath, clock)

	observer, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	var delivered atomic.Int64
	subscription, err := observer.Subscribe("hearth.v1.>", func(*natsgo.Msg) { delivered.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = subscription.Drain() }()
	if err = observer.Flush(); err != nil {
		t.Fatal(err)
	}

	httpAddress := unusedLoopbackAddress(t)
	baseURL := "http://" + httpAddress
	clock.set(admitMinute.Add(2 * time.Minute))
	wakeup := make(chan struct{})
	runErrors, stop := startCronCore(ctx, t, databasePath, "UTC", httpAddress, server.ClientURL(), clock, wakeup)
	defer func() { stopCronCore(ctx, t, stop, runErrors) }()
	cronWaitForCoreHealthz(ctx, t, httpAddress, runErrors)
	cronPollReadyz(ctx, t, baseURL+"/readyz")
	// Startup recovery (interrupt) and scheduler init both run synchronously
	// inside Run before healthz/readyz serve, so any replay publish
	// necessarily precedes readyz on the publisher side. Readyz orders nothing
	// on the observer side — the async subscription callback may not have run
	// yet when an atomic is read — so no zero-delivery assertion happens here.
	verifyCronCrashRecovery(ctx, t, baseURL)
	// The startup minute itself admits nothing new: initialization advanced
	// high-water to the startup minute without admission, so the history must
	// contain exactly the two seeded occurrences and no row at the startup
	// minute. No extra same-minute tick is sent — it would be a no-op whose
	// len==2 poll returns before evaluation (false-positive oracle). The
	// persisted high-water reaching the startup minute (set at init, observed
	// here on a separate connection) plus readyz proves the startup minute was
	// fully processed.
	waitCronHighWater(ctx, t, databasePath, admitMinute.Add(2*time.Minute).Truncate(time.Minute))
	occurrences := cronListOccurrences(ctx, t, baseURL)
	if len(occurrences) != 2 {
		t.Fatalf("startup admitted new occurrences: %+v", occurrences)
	}
	if got := cronCountOccurrencesAt(occurrences, admitMinute.Add(2*time.Minute).Truncate(time.Minute)); got != 0 {
		t.Fatalf("startup minute executed %d occurrences", got)
	}
	// Zero-delivery barrier: Flush round-trips the observer connection, so
	// every message the server routed before this point is already
	// client-side (NATS serves PONG after already-queued MSGs on the same
	// connection); Drain removes interest while the library keeps invoking
	// callbacks until all pending messages are processed, then reports
	// SubscriptionClosed. Awaiting that event (not the atomic, not readyz)
	// proves callback completion, so the final Load is exact. Proof boundary:
	// this covers the whole window from before core start (the wildcard
	// hearth.v1.> subscription was flushed pre-startup, so the server cannot
	// have routed a replay past it) through recovery, readyz, and the HTTP
	// assertions above; combined with the interrupted/unattempted run evidence
	// in verifyCronCrashRecovery it proves zero replay. It cannot observe
	// publishes after Drain, but none can occur: recovery registered no
	// workers for interrupted runs, no ticks were sent, and no adapters are
	// registered in this test.
	if flushErr := observer.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}
	closed := subscription.StatusChanged(natsgo.SubscriptionClosed)
	if drainErr := subscription.Drain(); drainErr != nil {
		t.Fatal(drainErr)
	}
	awaitCronSubscriptionClosed(ctx, t, closed)
	if got := delivered.Load(); got != 0 {
		t.Fatalf("startup redelivered %d commands", got)
	}
}

// C4/B7+B9+B10+B4: definition reorder never rewrites history, historical
// snapshots keep their admission timezone after a restart with another zone,
// the new zone applies to future minutes only, and a backward clock after
// restart replays nothing.
func TestCoreSchedulerTimezoneRestartPreservesHistory(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	httpAddress := unusedLoopbackAddress(t)
	baseURL := "http://" + httpAddress
	wallMinute := time.Now().UTC().Truncate(time.Minute)
	first := wallMinute.Add(5 * time.Minute).Add(20 * time.Second)
	firstMinute := first.UTC().Truncate(time.Minute)
	clock := &cronFakeClock{now: first}
	wakeup := make(chan struct{})
	runErrors, stop := startCronCore(ctx, t, databasePath, "UTC", httpAddress, server.ClientURL(), clock, wakeup)
	cronWaitForCoreHealthz(ctx, t, httpAddress, runErrors)
	cronPollReadyz(ctx, t, baseURL+"/readyz")

	var dispatches atomic.Int64
	var blockSecond atomic.Bool
	entityID := registerCronPowerEntity(ctx, t, server.ClientURL(), "cron-history",
		&dispatches, &blockSecond, make(chan struct{}), make(chan struct{}))
	waitCronCommandSubscription(t, server)
	created := cronProcessRequest[automationsapi.AutomationBody](ctx, t, http.MethodPost,
		baseURL+"/v1/automations", fmt.Sprintf(
			`{"name":"History","enabled":true,"triggers":[{"id":"first","kind":"cron","expression":"* * * * *"},`+
				`{"id":"second","kind":"cron","expression":"* * * * *"}]`+
				`,"steps":[{"entity_id":%q,"operation_name":"set","parameters":{"value":true}}]}`,
			entityID,
		), "", http.StatusCreated)

	minute := firstMinute.Add(time.Minute)
	clock.set(minute.Add(20 * time.Second))
	signalCronTick(wakeup)
	waitCronHighWater(ctx, t, databasePath, minute)
	occurrences := waitCronOccurrences(ctx, t, baseURL, 1)
	if occurrences[0].Revision != 1 || len(occurrences[0].MatchedTriggers) != 2 ||
		occurrences[0].MatchedTriggers[0].ID != "first" ||
		occurrences[0].MatchedTriggers[1].ID != "second" ||
		occurrences[0].Timezone != "UTC" {
		t.Fatalf("admission snapshot = %+v", occurrences[0])
	}
	waitCronRunSucceeded(ctx, t, baseURL, string(*occurrences[0].RunID))

	// B7: reordering Triggers preserves IDs while history keeps array order.
	updated := cronProcessRequest[automationsapi.AutomationBody](ctx, t, http.MethodPut,
		baseURL+"/v1/automations/"+string(created.ID)+"?expected_revision=1", fmt.Sprintf(
			`{"name":"Renamed","enabled":true,"triggers":[{"id":"second","kind":"cron","expression":"* * * * *"},`+
				`{"id":"first","kind":"cron","expression":"* * * * *"}]`+
				`,"steps":[{"entity_id":%q,"operation_name":"set","parameters":{"value":true}}]}`,
			entityID,
		), "", http.StatusOK)
	if updated.Revision != 2 {
		t.Fatalf("updated revision = %+v", updated)
	}
	occurrences = cronListOccurrences(ctx, t, baseURL)
	if len(occurrences) != 1 || occurrences[0].Revision != 1 || occurrences[0].Name != "History" ||
		occurrences[0].MatchedTriggers[0].ID != "first" {
		t.Fatalf("history rewritten by reorder: %+v", occurrences)
	}
	stopCronCore(ctx, t, stop, runErrors)

	// Restart backward with another zone: history keeps the UTC snapshot, the
	// definition exposes the new zone, and the backward minute replays nothing.
	// The backward tick is a no-op (high-water never moves), so no poll can
	// prove it completed. The ack inside signalCronTick proves it was
	// evaluated at the backward instant before the clock advanced; gap and
	// replay assertions additionally happen after the future high-water
	// barrier below.
	clock.set(firstMinute)
	wakeupRestart := make(chan struct{})
	runErrors, stop = startCronCore(
		ctx, t, databasePath, "America/New_York", httpAddress, server.ClientURL(), clock, wakeupRestart)
	defer func() { stopCronCore(ctx, t, stop, runErrors) }()
	cronWaitForCoreHealthz(ctx, t, httpAddress, runErrors)
	cronPollReadyz(ctx, t, baseURL+"/readyz")

	definition := cronProcessRequest[automationsapi.AutomationBody](
		ctx, t, http.MethodGet, baseURL+"/v1/automations/"+string(created.ID), "", "", http.StatusOK)
	if definition.HouseholdTimezone != "America/New_York" {
		t.Fatalf("restarted household_timezone = %q", definition.HouseholdTimezone)
	}
	occurrences = cronListOccurrences(ctx, t, baseURL)
	if len(occurrences) != 1 || occurrences[0].Timezone != "UTC" || occurrences[0].Revision != 1 ||
		occurrences[0].MatchedTriggers[0].ID != "first" ||
		occurrences[0].MatchedTriggers[1].ID != "second" {
		t.Fatalf("historical snapshot changed across restart: %+v", occurrences)
	}
	clock.set(firstMinute.Add(30 * time.Second))
	signalCronTick(wakeupRestart)
	// Future minutes use the new zone for new snapshots only.
	future := minute.Add(time.Minute)
	clock.set(future.Add(5 * time.Second))
	signalCronTick(wakeupRestart)
	waitCronHighWater(ctx, t, databasePath, future)
	occurrences = waitCronOccurrences(ctx, t, baseURL, 2)
	if got := cronCountOccurrencesAt(occurrences, firstMinute); got != 0 {
		t.Fatalf("backward restart replayed %d occurrences", got)
	}
	if gaps := cronListGaps(ctx, t, baseURL); len(gaps) != 0 {
		t.Fatalf("backward restart recorded a gap: %+v", gaps)
	}
	if occurrences[0].Timezone != "America/New_York" || !occurrences[0].ScheduledAt.Equal(future) ||
		occurrences[1].Timezone != "UTC" {
		t.Fatalf("zone change did not apply to future minutes only: %+v", occurrences)
	}
}
