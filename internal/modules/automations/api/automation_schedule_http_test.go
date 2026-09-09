package api //nolint:testpackage // Contract tests drive scheduler diagnostics over real SQLite and HTTP.

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// scheduleHTTPClock is a mutex-guarded fake shared by the repository
// scheduler clock and the scheduler loop clock. The fake date stays strictly
// after real creation time so schedule_not_before eligibility holds while
// ticks stay fully deterministic.
type scheduleHTTPClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *scheduleHTTPClock) get() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *scheduleHTTPClock) set(now time.Time) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = now
}

type automationScheduleHTTPHarness struct {
	router      http.Handler
	codec       *automations.AutomationDefinitionCodec
	service     *automations.Service
	devices     *devices.Service
	database    *sql.DB
	clock       *scheduleHTTPClock
	wakeup      chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

func newAutomationScheduleHTTPHarness(t *testing.T) *automationScheduleHTTPHarness {
	t.Helper()
	base := time.Now().UTC().Truncate(time.Minute).Add(3 * time.Minute)
	h := &automationScheduleHTTPHarness{
		clock:   &scheduleHTTPClock{now: base.Add(30 * time.Second)},
		wakeup:  make(chan struct{}, 16),
		release: make(chan struct{}),
	}
	var err error
	h.database, err = platformdb.Open(t.Context(), filepath.Join(t.TempDir(), "schedule-http.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := h.database.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if err = platformdb.Migrate(t.Context(), h.database); err != nil {
		t.Fatal(err)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	records := devices.NewSQLiteRepository(h.database, catalog)
	logger := slog.New(slog.DiscardHandler)
	h.devices = devices.NewService(
		devices.SQLiteStores(records),
		automationHTTPSender(
			func(
				ctx context.Context,
				adapter string,
				runtime devices.RuntimeID,
				request devices.CommandRequest,
			) (devices.CommandAcceptance, error) {
				select {
				case <-h.release:
				case <-ctx.Done():
					return devices.CommandAcceptance{}, ctx.Err()
				}
				id, idErr := devices.NewObservationID()
				if idErr != nil {
					return devices.CommandAcceptance{}, idErr
				}
				_, projectErr := h.devices.ProjectObservation(ctx, adapter, runtime, devices.Observation{
					ID:                id,
					EntityID:          request.EntityID,
					Value:             devices.Value(`true`),
					AdapterReceivedAt: time.Now().UTC(),
					RefreshForCommand: &request.ID,
				}, time.Now().UTC())
				return devices.CommandAcceptance{Accepted: true}, projectErr
			},
		),
		catalog,
		devices.Dependencies{
			Logger:      logger,
			NewEntityID: func() (devices.EntityID, error) { return fixtureEntityID, nil },
		},
	)
	runtime := devices.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if err = h.devices.ClaimAdapterRuntime(
		t.Context(),
		devices.ClaimAdapterRuntimeParams{
			AdapterID:       "test",
			RuntimeID:       runtime,
			SoftwareName:    "http-test",
			SoftwareVersion: "1",
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err = h.devices.Register(t.Context(), "test", runtime, devices.Registration{
		BindingKey: "light",
		Device:     devices.DeviceDescriptor{Name: "Light", Kind: devices.DeviceKindLight},
		Entities: []devices.EntityDescriptor{
			{
				Key:        "power",
				ExternalID: "power",
				Name:       "Power",
				TypeID:     devices.EntityTypePowerV1,
				Support:    devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	h.codec, err = automations.NewAutomationDefinitionCodec()
	if err != nil {
		t.Fatal(err)
	}
	repository := automations.NewSQLiteRepository(
		h.database,
		automations.WithAutomationSchedulerClock(h.clock.get),
	)
	h.service = automations.NewService(
		repository,
		h.devices,
		records,
		h.codec,
		time.UTC,
		logger,
		automations.WithSchedulerClock(h.clock.get),
		automations.WithSchedulerWakeup(h.wakeup),
	)
	t.Cleanup(func() {
		h.unblock()
		h.service.StopAutomationScheduler()
		h.service.StopAutomationExecutionAdmission()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if waitErr := h.service.WaitAutomationRuns(ctx); waitErr != nil {
			t.Error(waitErr)
		}
	})
	h.router = automationTestRouter(h.service, h.codec)
	return h
}

func (h *automationScheduleHTTPHarness) unblock() { h.releaseOnce.Do(func() { close(h.release) }) }

func (h *automationScheduleHTTPHarness) tick(at time.Time) {
	h.clock.set(at)
	h.wakeup <- struct{}{}
}

func createScheduleHTTPAutomation(t *testing.T, h *automationScheduleHTTPHarness, body string) AutomationBody {
	t.Helper()
	response := automationRequest(h.router, http.MethodPost, "/v1/automations", body, "")
	requireAutomationStatus(t, response, http.StatusCreated)
	record := decodeAutomationResponse[AutomationBody](t, response)
	if response.Header().Get("Location") != "/v1/automations/"+string(record.ID) || record.Revision != 1 {
		t.Fatalf("creation metadata = %s", response.Body.String())
	}
	return record
}

func scheduleHTTPDefinitions(
	t *testing.T,
	h *automationScheduleHTTPHarness,
	path string,
) automationHTTPPage[AutomationBody] {
	t.Helper()
	response := automationRequest(h.router, http.MethodGet, path, "", "")
	requireAutomationStatus(t, response, http.StatusOK)
	page := decodeAutomationResponse[automationHTTPPage[AutomationBody]](t, response)
	if page.Items == nil {
		t.Fatal("empty definition collection must be [] not null")
	}
	return page
}

func waitScheduleHTTPWorkers(t *testing.T, h *automationScheduleHTTPHarness) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := h.service.WaitAutomationRuns(ctx); err != nil {
		t.Fatal(err)
	}
}

func scheduleHTTPOccurrences(
	t *testing.T,
	h *automationScheduleHTTPHarness,
	path string,
) automationHTTPPage[AutomationOccurrenceBody] {
	t.Helper()
	response := automationRequest(h.router, http.MethodGet, path, "", "")
	requireAutomationStatus(t, response, http.StatusOK)
	page := decodeAutomationResponse[automationHTTPPage[AutomationOccurrenceBody]](t, response)
	if page.Items == nil {
		t.Fatal("empty occurrence collection must be [] not null")
	}
	return page
}

func scheduleHTTPGaps(
	t *testing.T,
	h *automationScheduleHTTPHarness,
	path string,
) automationHTTPPage[AutomationScheduleGapBody] {
	t.Helper()
	response := automationRequest(h.router, http.MethodGet, path, "", "")
	requireAutomationStatus(t, response, http.StatusOK)
	page := decodeAutomationResponse[automationHTTPPage[AutomationScheduleGapBody]](t, response)
	if page.Items == nil {
		t.Fatal("empty gap collection must be [] not null")
	}
	return page
}

func waitScheduleHTTPOccurrences(t *testing.T, h *automationScheduleHTTPHarness, count int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if page := scheduleHTTPOccurrences(t, h, "/v1/automation-occurrences"); len(page.Items) == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("waiting for %d occurrences", count)
}

const scheduleHTTPDefinition = `{
  "name": "Every minute",
  "enabled": true,
  "triggers": [
    {"id": "first", "kind": "cron", "expression": "* * * * *"},
    {"id": "second", "kind": "cron", "expression": "* * * * *"}
  ],
  "steps": [
    {"entity_id": "ent_01900000-0000-7000-8000-000000000001", "operation_name": "set", "parameters": {"value": true}}
  ]
}`

const scheduleHTTPEditedDefinition = `{
  "name": "Edited schedule",
  "enabled": true,
  "triggers": [
    {"id": "first", "kind": "cron", "expression": "0 0 * * *"},
    {"id": "second", "kind": "cron", "expression": "0 0 * * *"}
  ],
  "steps": [
    {"entity_id": "ent_01900000-0000-7000-8000-000000000001", "operation_name": "set", "parameters": {"value": true}}
  ]
}`

func requireScheduleSnapshot(
	t *testing.T,
	occurrence AutomationOccurrenceBody,
	status string,
	scheduledAt time.Time,
) {
	t.Helper()
	if string(occurrence.Status) != status || !occurrence.ScheduledAt.Equal(scheduledAt) ||
		occurrence.Revision != 1 || occurrence.Name != "Every minute" || occurrence.Timezone != "UTC" {
		t.Fatalf("occurrence identity = %+v", occurrence)
	}
	want := []AutomationTriggerBody{
		{ID: "first", Kind: "cron", Expression: "* * * * *"},
		{ID: "second", Kind: "cron", Expression: "* * * * *"},
	}
	if len(occurrence.MatchedTriggers) != 2 || occurrence.MatchedTriggers[0] != want[0] ||
		occurrence.MatchedTriggers[1] != want[1] {
		t.Fatalf("matched snapshots = %+v", occurrence.MatchedTriggers)
	}
	if status == "started" && (occurrence.RunID == nil || occurrence.SkipReason != nil) {
		t.Fatalf("started linkage = %+v", occurrence)
	}
	if status == "skipped" && (occurrence.RunID != nil || occurrence.SkipReason == nil ||
		*occurrence.SkipReason != "automation_run_active") {
		t.Fatalf("skipped linkage = %+v", occurrence)
	}
}

// C3: started and skipped matches expose complete Trigger snapshots over HTTP,
// retain them across edits and deletion, and page with spec-1 cursor parity.
//
//nolint:cyclop,gocognit,gocyclo // One linear schedule lifecycle keeps tick, snapshot, cursor, and restart evidence in order.
func TestAutomationHTTPScheduleDiagnostics(t *testing.T) {
	t.Parallel()
	h := newAutomationScheduleHTTPHarness(t)
	if page := scheduleHTTPOccurrences(t, h, "/v1/automation-occurrences"); len(page.Items) != 0 ||
		page.NextCursor != "" {
		t.Fatalf("nonempty initial occurrences = %+v", page)
	}
	if page := scheduleHTTPGaps(t, h, "/v1/automation-schedule-gaps"); len(page.Items) != 0 ||
		page.NextCursor != "" {
		t.Fatalf("nonempty initial gaps = %+v", page)
	}
	record := createScheduleHTTPAutomation(t, h, scheduleHTTPDefinition)
	if record.HouseholdTimezone != "UTC" {
		t.Fatalf("creation household_timezone = %q", record.HouseholdTimezone)
	}
	if err := h.service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	minuteOne := h.clock.get().UTC().Truncate(time.Minute).Add(time.Minute)
	h.tick(minuteOne.Add(5 * time.Second))
	waitScheduleHTTPOccurrences(t, h, 1)
	// The blocked command holds the first Run active, so the next minute is
	// one skipped Occurrence with the same complete snapshots, not a queue.
	minuteTwo := minuteOne.Add(time.Minute)
	h.tick(minuteTwo.Add(5 * time.Second))
	waitScheduleHTTPOccurrences(t, h, 2)
	page := scheduleHTTPOccurrences(t, h, "/v1/automation-occurrences")
	requireScheduleSnapshot(t, page.Items[0], "skipped", minuteTwo)
	requireScheduleSnapshot(t, page.Items[1], "started", minuteOne)
	if page.NextCursor != "" {
		t.Fatal("unpaged history must not mint a cursor")
	}
	h.unblock()
	waitScheduleHTTPWorkers(t, h)
	runDetail := automationRequest(
		h.router,
		http.MethodGet,
		"/v1/automation-runs/"+string(*page.Items[1].RunID),
		"",
		"",
	)
	requireAutomationStatus(t, runDetail, http.StatusOK)
	scheduled := decodeAutomationResponse[AutomationRunBody](t, runDetail)
	if scheduled.Source != "scheduled" || scheduled.ScheduledAt == nil ||
		!scheduled.ScheduledAt.Equal(minuteOne) || len(scheduled.MatchedTriggerIDs) != 2 ||
		scheduled.MatchedTriggerIDs[0] != "first" || scheduled.MatchedTriggerIDs[1] != "second" {
		t.Fatalf("scheduled run = %+v", scheduled)
	}
	// Edits never rewrite retained snapshots; definition reads keep the
	// read-only household zone.
	path := "/v1/automations/" + string(record.ID)
	updated := automationRequest(
		h.router,
		http.MethodPut,
		path+"?expected_revision=1",
		scheduleHTTPEditedDefinition,
		"",
	)
	requireAutomationStatus(t, updated, http.StatusOK)
	edited := decodeAutomationResponse[AutomationBody](t, updated)
	if edited.Revision != 2 || edited.Name != "Edited schedule" || edited.HouseholdTimezone != "UTC" {
		t.Fatalf("edited definition = %+v", edited)
	}
	detail := automationRequest(h.router, http.MethodGet, path, "", "")
	requireAutomationStatus(t, detail, http.StatusOK)
	if body := decodeAutomationResponse[AutomationBody](t, detail); body.HouseholdTimezone != "UTC" {
		t.Fatalf("definition read household_timezone = %q", body.HouseholdTimezone)
	}
	listed := scheduleHTTPDefinitions(t, h, "/v1/automations")
	if len(listed.Items) != 1 || listed.Items[0].HouseholdTimezone != "UTC" {
		t.Fatalf("definition list household_timezone = %+v", listed)
	}
	after := scheduleHTTPOccurrences(t, h, "/v1/automation-occurrences")
	requireScheduleSnapshot(t, after.Items[0], "skipped", minuteTwo)
	requireScheduleSnapshot(t, after.Items[1], "started", minuteOne)
	// Newest-first paging with a stable filter-bound cursor.
	filter := "/v1/automation-occurrences?automation_id=" + string(record.ID)
	first := scheduleHTTPOccurrences(t, h, filter+"&limit=1")
	if len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("occurrence first page = %+v", first)
	}
	requireScheduleSnapshot(t, first.Items[0], "skipped", minuteTwo)
	second := scheduleHTTPOccurrences(t, h, filter+"&limit=1&cursor="+first.NextCursor)
	if len(second.Items) != 1 || second.NextCursor != "" {
		t.Fatalf("occurrence continuation = %+v", second)
	}
	requireScheduleSnapshot(t, second.Items[0], "started", minuteOne)
	// Cursor misuse across resources, filters, and canonical time fails closed.
	automationsCursor, err := encodeAutomationCursor(
		automationCursor{Version: 1, Resource: "automations", ID: string(record.ID)},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		"/v1/automation-occurrences?cursor=" + automationsCursor,
		"/v1/automation-occurrences?cursor=" + first.NextCursor,
		"/v1/automations?cursor=" + first.NextCursor,
		"/v1/automation-runs?cursor=" + first.NextCursor,
		"/v1/automation-occurrences?cursor=!",
		"/v1/automation-schedule-gaps?cursor=" + first.NextCursor,
		"/v1/automation-occurrences?automation_id=" + string(record.ID) + "&cursor=" +
			scheduleHTTPOccurrences(t, h, "/v1/automation-occurrences?limit=1").NextCursor,
	} {
		requireAutomationStatus(t, automationRequest(h.router, http.MethodGet, target, "", ""), http.StatusBadRequest)
	}
	noncanonical, err := encodeAutomationCursor(automationOccurrenceCursor{
		Version:      1,
		Resource:     "automation_occurrences",
		AutomationID: string(record.ID),
		ScheduledAt:  minuteTwo.Format("2006-01-02T15:04:05.999999999-07:00"),
		ID:           string(record.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireAutomationStatus(
		t,
		automationRequest(
			h.router,
			http.MethodGet,
			filter+"&cursor="+noncanonical,
			"",
			"",
		),
		http.StatusBadRequest,
	)
	for _, collection := range []string{"/v1/automation-occurrences", "/v1/automation-schedule-gaps"} {
		for _, limit := range []string{"0", "201"} {
			requireAutomationStatus(
				t,
				automationRequest(h.router, http.MethodGet, collection+"?limit="+limit, "", ""),
				http.StatusUnprocessableEntity,
			)
		}
	}
	requireAutomationStatus(
		t,
		automationRequest(h.router, http.MethodGet, "/v1/automation-occurrences?automation_id=nope", "", ""),
		http.StatusBadRequest,
	)
	// Deleted-definition history remains readable with complete snapshots.
	requireAutomationStatus(
		t,
		automationRequest(h.router, http.MethodDelete, path+"?expected_revision=2", "", ""),
		http.StatusNoContent,
	)
	deleted := scheduleHTTPOccurrences(t, h, filter)
	if len(deleted.Items) != 2 {
		t.Fatalf("deleted history = %+v", deleted)
	}
	requireScheduleSnapshot(t, deleted.Items[0], "skipped", minuteTwo)
	requireScheduleSnapshot(t, deleted.Items[1], "started", minuteOne)
	// A restart records the unevaluated interval without inventing matches.
	h.service.StopAutomationScheduler()
	restartAt := minuteTwo.Add(5 * time.Minute)
	h.clock.set(restartAt)
	if err = h.service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	restartAt = restartAt.UTC().Truncate(time.Minute)
	gaps := scheduleHTTPGaps(t, h, "/v1/automation-schedule-gaps?limit=1")
	if len(gaps.Items) != 1 || gaps.NextCursor != "" {
		t.Fatalf("restart gaps = %+v", gaps)
	}
	gap := gaps.Items[0]
	if !gap.FromExclusive.Equal(minuteTwo) || !gap.ThroughInclusive.Equal(restartAt) ||
		!gap.RecordedAt.Equal(restartAt) || gap.Reason != "core_restart" ||
		!strings.HasPrefix(gap.ID, "asg_") {
		t.Fatalf("restart gap = %+v", gap)
	}
	h.service.StopAutomationScheduler()
	secondRestart := restartAt.Add(3 * time.Minute)
	h.clock.set(secondRestart)
	if err = h.service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	paged := scheduleHTTPGaps(t, h, "/v1/automation-schedule-gaps?limit=1")
	if len(paged.Items) != 1 || paged.NextCursor == "" {
		t.Fatalf("gap first page = %+v", paged)
	}
	if !paged.Items[0].FromExclusive.Equal(restartAt) ||
		!paged.Items[0].ThroughInclusive.Equal(secondRestart) {
		t.Fatalf("second gap = %+v", paged.Items[0])
	}
	rest := scheduleHTTPGaps(t, h, "/v1/automation-schedule-gaps?limit=1&cursor="+paged.NextCursor)
	if len(rest.Items) != 1 || rest.NextCursor != "" || rest.Items[0].ID != gap.ID {
		t.Fatalf("gap continuation = %+v", rest)
	}
	requireAutomationStatus(
		t,
		automationRequest(h.router, http.MethodGet, "/v1/automation-schedule-gaps?cursor="+first.NextCursor, "", ""),
		http.StatusBadRequest,
	)
	if final := scheduleHTTPOccurrences(t, h, "/v1/automation-occurrences"); len(final.Items) != 2 {
		t.Fatalf("restart invented matches: %+v", final)
	}
}

// C3: runtime OpenAPI publishes the diagnostic routes, snake_case models, the
// read-only household zone, and impossible-date cron documentation.
//
//nolint:gocognit // Route, schema, and documentation assertions stay together as one publication contract.
func TestAutomationHTTPScheduleOpenAPI(t *testing.T) {
	t.Parallel()
	h := newAutomationScheduleHTTPHarness(t)
	response := automationRequest(h.router, http.MethodGet, "/openapi.json", "", "")
	requireAutomationStatus(t, response, http.StatusOK)
	var document struct {
		Paths      map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/automation-occurrences", "/v1/automation-schedule-gaps"} {
		raw, ok := document.Paths[path]
		if !ok {
			t.Fatalf("OpenAPI missing %s", path)
		}
		var operations map[string]json.RawMessage
		if err := json.Unmarshal(raw, &operations); err != nil {
			t.Fatal(err)
		}
		if _, found := operations["get"]; !found {
			t.Fatalf("OpenAPI %s missing GET", path)
		}
	}
	schemas, err := json.Marshal(document.Components.Schemas)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(schemas), "AutomationOccurrenceBody") ||
		!strings.Contains(string(schemas), "AutomationScheduleGapBody") {
		t.Fatal("OpenAPI missing diagnostic schemas")
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, token := range []string{
		"list-automation-occurrences", "list-automation-schedule-gaps",
		"household_timezone", "matched_triggers", "from_exclusive", "through_inclusive",
		"0 0 31 2 *",
	} {
		if !strings.Contains(text, token) {
			t.Fatalf("OpenAPI missing %q", token)
		}
	}
	occurrence, found := document.Components.Schemas["AutomationOccurrenceBody"]
	if !found {
		t.Fatal("OpenAPI missing AutomationOccurrenceBody")
	}
	var occurrenceSchema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err = json.Unmarshal(occurrence, &occurrenceSchema); err != nil {
		t.Fatal(err)
	}
	for _, property := range []string{
		"automation_id", "revision", "name", "matched_triggers", "timezone",
		"scheduled_at", "evaluated_at", "status", "run_id", "skip_reason",
	} {
		if _, present := occurrenceSchema.Properties[property]; !present {
			t.Fatalf("occurrence schema missing %q", property)
		}
	}
	triggerSchema, found := document.Components.Schemas["AutomationTriggerBody"]
	if !found {
		t.Fatal("OpenAPI missing AutomationTriggerBody")
	}
	var triggerDocument struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err = json.Unmarshal(triggerSchema, &triggerDocument); err != nil {
		t.Fatal(err)
	}
	expression, found := triggerDocument.Properties["expression"]
	if !found || !strings.Contains(expression.Description, "0 0 31 2 *") {
		t.Fatalf("trigger expression lost the impossible-date documentation: %+v", triggerDocument.Properties)
	}
	definition, found := document.Components.Schemas["AutomationBody"]
	if !found {
		t.Fatal("OpenAPI missing AutomationBody")
	}
	var definitionSchema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err = json.Unmarshal(definition, &definitionSchema); err != nil {
		t.Fatal(err)
	}
	if _, present := definitionSchema.Properties["household_timezone"]; !present {
		t.Fatal("definition schema missing household_timezone")
	}
}
