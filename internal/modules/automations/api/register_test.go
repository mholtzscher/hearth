package api //nolint:testpackage // Contract tests inspect canonical publication and cursor boundaries.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

const fixtureEntityID = devices.EntityID("ent_01900000-0000-7000-8000-000000000001")

type automationHTTPSender func(context.Context, string, devices.RuntimeID, devices.CommandRequest) (devices.CommandAcceptance, error)

func (send automationHTTPSender) Send(
	ctx context.Context,
	adapter string,
	runtime devices.RuntimeID,
	request devices.CommandRequest,
) (devices.CommandAcceptance, error) {
	return send(ctx, adapter, runtime, request)
}

type automationHTTPHarness struct {
	router      http.Handler
	codec       *automations.AutomationDefinitionCodec
	service     *automations.Service
	devices     *devices.Service
	database    *sql.DB
	sends       atomic.Int64
	started     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

func newAutomationHTTPHarness(t *testing.T) *automationHTTPHarness {
	t.Helper()
	h := &automationHTTPHarness{started: make(chan struct{}, 100), release: make(chan struct{})}
	var err error
	h.database, err = platformdb.Open(t.Context(), filepath.Join(t.TempDir(), "http.db"))
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
			func(ctx context.Context, adapter string, runtime devices.RuntimeID, request devices.CommandRequest) (devices.CommandAcceptance, error) {
				h.sends.Add(1)
				h.started <- struct{}{}
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
	h.service = automations.NewService(
		automations.NewSQLiteRepository(h.database),
		h.devices,
		records,
		h.codec,
		time.UTC,
		logger,
	)
	t.Cleanup(func() {
		h.unblock()
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
func (h *automationHTTPHarness) unblock() { h.releaseOnce.Do(func() { close(h.release) }) }
func automationTestRouter(service Automations, codec *automations.AutomationDefinitionCodec) http.Handler {
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Automation contract", "1"))
	Register(huma.NewGroup(api, "/v1"), service, codec)
	return router
}
func automationRequest(handler http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
func requireAutomationStatus(t *testing.T, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d: %s", response.Code, status, response.Body.String())
	}
}
func decodeAutomationResponse[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var body T
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}
func readAutomationFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("../testdata/automation-definitions/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func createHTTPAutomation(t *testing.T, h *automationHTTPHarness, body string) AutomationBody {
	t.Helper()
	response := automationRequest(h.router, http.MethodPost, "/v1/automations", body, "")
	requireAutomationStatus(t, response, http.StatusCreated)
	record := decodeAutomationResponse[AutomationBody](t, response)
	if response.Header().Get("Location") != "/v1/automations/"+string(record.ID) || record.Revision != 1 {
		t.Fatalf("creation metadata = %s", response.Body.String())
	}
	return record
}
func waitHTTPAutomationWorkers(t *testing.T, h *automationHTTPHarness) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := h.service.WaitAutomationRuns(ctx); err != nil {
		t.Fatal(err)
	}
}

// A3/A4/A9/A10/A12: a blocked real Command makes async admission and request
// cancellation observable; edits and deletion never rewrite retained history.
func TestAutomationHTTPManualLifecycle(t *testing.T) {
	t.Parallel()
	h := newAutomationHTTPHarness(t)
	definition := strings.Replace(readAutomationFixture(t, "valid-power"), `"enabled": false,`, "", 1)
	record := createHTTPAutomation(t, h, definition)
	if record.Enabled {
		t.Fatal("POST omission must disable triggering")
	}
	path := "/v1/automations/" + string(record.ID)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, path+"/runs", nil)
	request.Header.Set("Idempotency-Key", "private-key")
	response := httptest.NewRecorder()
	h.router.ServeHTTP(response, request)
	requireAutomationStatus(t, response, http.StatusAccepted)
	cancel()
	select {
	case <-h.started:
	case <-time.After(5 * time.Second):
		t.Fatal("command never started")
	}
	run := decodeAutomationResponse[AutomationRunBody](t, response)
	if run.Status != "running" || len(run.MatchedTriggerIDs) != 0 || run.MatchedTriggerIDs == nil ||
		run.Snapshot.Timezone != "UTC" {
		t.Fatalf("admission = %#v", run)
	}
	location := "/v1/automation-runs/" + string(run.ID)
	if response.Header().Get("Location") != location {
		t.Fatal("missing Run Location")
	}
	active := automationRequest(h.router, http.MethodPost, path+"/runs", "", "different-key")
	requireAutomationStatus(t, active, http.StatusConflict)
	if !strings.Contains(active.Body.String(), `"code":"automation_run_active"`) {
		t.Fatal(active.Body.String())
	}
	requireAutomationStatus(
		t,
		automationRequest(h.router, http.MethodDelete, path+"?expected_revision=1", "", ""),
		http.StatusConflict,
	)
	edited := strings.Replace(definition, "Evening lights", "Edited", 1)
	updated := automationRequest(h.router, http.MethodPut, path+"?expected_revision=1", edited, "")
	requireAutomationStatus(t, updated, http.StatusOK)
	if body := decodeAutomationResponse[AutomationBody](
		t,
		updated,
	); body.Enabled || body.Revision != 2 ||
		body.Name != "Edited" {
		t.Fatalf("PUT = %#v", body)
	}
	stale := automationRequest(h.router, http.MethodPut, path+"?expected_revision=1", definition, "")
	requireAutomationStatus(t, stale, http.StatusConflict)
	if !strings.Contains(stale.Body.String(), `"code":"automation_revision_conflict"`) {
		t.Fatal(stale.Body.String())
	}
	requireAutomationStatus(
		t,
		automationRequest(h.router, http.MethodDelete, path+"?expected_revision=1", "", ""),
		http.StatusConflict,
	)
	h.unblock()
	waitHTTPAutomationWorkers(t, h)
	requireAutomationStatus(
		t,
		automationRequest(h.router, http.MethodDelete, path+"?expected_revision=2", "", ""),
		http.StatusNoContent,
	)
	requireAutomationStatus(t, automationRequest(h.router, http.MethodGet, path, "", ""), http.StatusNotFound)
	reused := automationRequest(h.router, http.MethodPost, path+"/runs", "", "private-key")
	requireAutomationStatus(t, reused, http.StatusOK)
	final := decodeAutomationResponse[AutomationRunBody](t, reused)
	requireOriginalAutomationRun(t, final, run.ID)
	detail := automationRequest(h.router, http.MethodGet, location, "", "")
	requireAutomationStatus(t, detail, http.StatusOK)
	rawDetail := decodeAutomationResponse[struct {
		Snapshot struct {
			Definition json.RawMessage `json:"definition"`
		} `json:"snapshot"`
	}](t, detail)
	snapshotDocument, schemaErr := jsonschema.UnmarshalJSON(bytes.NewReader(rawDetail.Snapshot.Definition))
	if schemaErr != nil {
		t.Fatal(schemaErr)
	}
	if schemaErr = automationContractSchema(t, h.codec).Validate(snapshotDocument); schemaErr != nil {
		t.Fatal(schemaErr)
	}
	for _, secret := range []string{"private-key", "cor_", "correlation", "idempotency"} {
		if strings.Contains(detail.Body.String(), secret) || strings.Contains(reused.Body.String(), secret) {
			t.Fatalf("Run leaked %q", secret)
		}
	}
	summary := automationRequest(
		h.router,
		http.MethodGet,
		"/v1/automation-runs?automation_id="+string(record.ID),
		"",
		"",
	)
	requireAutomationStatus(t, summary, http.StatusOK)
	for _, secret := range []string{"parameters", "steps", "private-key", "cor_"} {
		if strings.Contains(summary.Body.String(), secret) {
			t.Fatalf("summary leaked %q", secret)
		}
	}
	requireAutomationStatus(
		t,
		automationRequest(h.router, http.MethodPost, path+"/runs", "", "new-key"),
		http.StatusNotFound,
	)
	if h.sends.Load() != 1 {
		t.Fatalf("retry dispatched %d times", h.sends.Load())
	}
	h.service.StopAutomationExecutionAdmission()
	unavailable := automationRequest(h.router, http.MethodPost, path+"/runs", "", "private-key")
	requireAutomationStatus(t, unavailable, http.StatusServiceUnavailable)
	if !strings.Contains(unavailable.Body.String(), "automation_unavailable") {
		t.Fatal(unavailable.Body.String())
	}
}

// A10/A11: the same hand-authored examples cross schema, codec and real HTTP.
func TestAutomationHTTPFixtureSchemaContract(t *testing.T) {
	t.Parallel()
	h := newAutomationHTTPHarness(t)
	schema := automationContractSchema(t, h.codec)
	for _, name := range []string{"valid-power", "unknown-field", "singular-trigger", "nonobject-parameters"} {
		raw := readAutomationFixture(t, name)
		document, parseErr := jsonschema.UnmarshalJSON(strings.NewReader(raw))
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		valid := name == "valid-power"
		if schemaErr := schema.Validate(document); (schemaErr == nil) != valid {
			t.Fatalf("schema %s: %v", name, schemaErr)
		}
		if _, codecErr := h.codec.DecodeAutomationDefinition([]byte(raw)); (codecErr == nil) != valid {
			t.Fatalf("codec %s: %v", name, codecErr)
		}
		response := automationRequest(h.router, http.MethodPost, "/v1/automations", raw, "")
		if !valid {
			requireAutomationStatus(t, response, http.StatusUnprocessableEntity)
			continue
		}
		requireAutomationStatus(t, response, http.StatusCreated)
		body := decodeAutomationResponse[map[string]json.RawMessage](t, response)
		for _, metadata := range []string{"$schema", "id", "revision", "created_at", "updated_at", "household_timezone"} {
			delete(body, metadata)
		}
		encoded, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		decoded, decodeErr := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if schemaErr := schema.Validate(decoded); schemaErr != nil {
			t.Fatalf("response definition differs from schema: %v", schemaErr)
		}
	}
	if h.sends.Load() != 0 {
		t.Fatal("management dispatched commands")
	}
}

type automationHTTPErrorService struct {
	Automations

	err error
}

func (service automationHTTPErrorService) CreateAutomation(
	context.Context,
	automations.AutomationDefinition,
) (automations.AutomationRecord, error) {
	return automations.AutomationRecord{}, service.err
}
func TestAutomationHTTPSanitizedErrors(t *testing.T) {
	t.Parallel()
	codec, err := automations.NewAutomationDefinitionCodec()
	if err != nil {
		t.Fatal(err)
	}
	router := automationTestRouter(
		automationHTTPErrorService{err: errors.New("SQL password=private-secret adapter payload")},
		codec,
	)
	raw := readAutomationFixture(t, "valid-power")
	cases := []struct {
		body   string
		status int
	}{
		{raw, http.StatusInternalServerError}, {`{"name":"private-secret",`, http.StatusUnprocessableEntity},
		{strings.Replace(raw, `"enabled": false`, `"enabled": "private-secret"`, 1), http.StatusUnprocessableEntity},
		{"", http.StatusBadRequest},
	}
	for _, test := range cases {
		response := automationRequest(router, http.MethodPost, "/v1/automations", test.body, "")
		requireAutomationStatus(t, response, test.status)
		if strings.Contains(response.Body.String(), "private-secret") ||
			strings.Contains(response.Body.String(), "password") ||
			strings.Contains(response.Body.String(), "SQL") {
			t.Fatalf("error leaked payload: %s", response.Body.String())
		}
		if !strings.HasPrefix(response.Header().Get("Content-Type"), "application/problem+json") {
			t.Fatal("error is not RFC 9457")
		}
	}
}

func requireOriginalAutomationRun(t *testing.T, final AutomationRunBody, originalID automations.AutomationRunID) {
	t.Helper()
	if final.ID != originalID || final.Status != "succeeded" || final.Snapshot.Revision != 1 ||
		final.Snapshot.Definition.Name != "Evening lights" ||
		len(final.Snapshot.Definition.Triggers) != 2 ||
		final.Snapshot.Definition.Triggers[0].ID != "weekdays" ||
		final.Snapshot.Definition.Triggers[1].ID != "weekends" {
		t.Fatalf("retained snapshot = %#v", final)
	}
	if final.Steps[0].CommandID == nil || final.Steps[0].ReservedCommandID == nil ||
		*final.Steps[0].CommandID != *final.Steps[0].ReservedCommandID ||
		final.Steps[0].Outcome == nil ||
		*final.Steps[0].Outcome != "observed" {
		t.Fatalf("owned evidence = %#v", final.Steps)
	}
}

func automationContractSchema(t *testing.T, codec *automations.AutomationDefinitionCodec) *jsonschema.Schema {
	t.Helper()
	schemaDocument, err := jsonschema.UnmarshalJSON(bytes.NewReader(codec.AutomationDefinitionSchema()))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaID = "urn:hearth:schema:automation-definition:v1"
	if err = compiler.AddResource(schemaID, schemaDocument); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(schemaID)
	if err != nil {
		t.Fatal(err)
	}

	return schema
}

func TestAutomationHTTPFailuresRemainRunResources(t *testing.T) {
	t.Parallel()
	h := newAutomationHTTPHarness(t)
	if _, err := h.devices.SetEntityEnabled(t.Context(), fixtureEntityID, false); err != nil {
		t.Fatal(err)
	}
	record := createHTTPAutomation(t, h, readAutomationFixture(t, "valid-power"))
	admitted := startHTTPAutomation(t, h, record.ID, "disabled-target")
	response := automationRequest(h.router, http.MethodGet, "/v1/automation-runs/"+string(admitted.ID), "", "")
	requireAutomationStatus(t, response, http.StatusOK)
	run := decodeAutomationResponse[AutomationRunBody](t, response)
	if run.Status != "failed" || run.FailureCode == nil || *run.FailureCode != "entity_disabled" ||
		run.CompletedAt == nil {
		t.Fatalf("failure = %#v", run)
	}
	if len(run.Steps) != 1 || run.Steps[0].Status != "failed" || run.Steps[0].CommandID == nil ||
		run.Steps[0].CommandStatus == nil ||
		*run.Steps[0].CommandStatus != "entity_disabled" ||
		run.Steps[0].Outcome != nil {
		t.Fatalf("failure evidence = %#v", run.Steps)
	}
	if strings.Contains(response.Body.String(), "scheduled_at") ||
		strings.Contains(response.Body.String(), "correlation") {
		t.Fatal("absent or private fields leaked")
	}
	if h.sends.Load() != 0 {
		t.Fatal("disabled target dispatched")
	}
}
