package api //nolint:testpackage // HTTP tests verify local validation before any service side effects.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// A10/A11: malformed JSON is route-local 422; required revisions stay Huma-owned.
//
//nolint:gocognit // Matrix assertions also verify failed validation leaves no writes or sends.
func TestAutomationHTTPValidationStatuses(t *testing.T) {
	t.Parallel()
	h := newAutomationHTTPHarness(t)
	raw := readAutomationFixture(t, "valid-power")
	record := createHTTPAutomation(t, h, raw)
	path := "/v1/automations/" + string(record.ID)
	for _, revision := range []string{"", "=", "=0", "=-1", "=1.2", "=secret", "=9223372036854775808"} {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			response := automationRequest(h.router, method, path+"?expected_revision"+revision, raw, "")
			requireAutomationStatus(t, response, http.StatusUnprocessableEntity)
		}
	}
	for _, key := range []string{"", "white space", "\t", "é", strings.Repeat("x", 129), "control\x7f"} {
		response := automationRequest(h.router, http.MethodPost, path+"/runs", "", key)
		requireAutomationStatus(t, response, http.StatusBadRequest)
		if strings.Contains(response.Body.String(), "white space") {
			t.Fatal("key leaked")
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		target := "/v1/automations"
		if method == http.MethodPut {
			target = path + "?expected_revision=1"
		}
		cases := []struct {
			raw    string
			status int
		}{
			{"", 400}, {" ", 422}, {`{"name":"secret",`, 422}, {raw + " {}", 422}, {"null", 422}, {"[]", 422},
			{strings.Replace(raw, `"enabled": false`, `"revision": 1`, 1), 422},
			{strings.Replace(raw, `"name": "Evening lights",`, "", 1), 422},
			{strings.Replace(raw, `"weekdays"`, `"Uppercase"`, 1), 422},
			{strings.Replace(raw, `"cron"`, `"event"`, 1), 422},
			{strings.Replace(raw, `"entity_id":`, `"command_id":"secret", "entity_id":`, 1), 422},
			{strings.Replace(raw, `"entity_id":`, `"correlation_id":"secret", "entity_id":`, 1), 422},
			{strings.Replace(raw, `{"value": true}`, `null`, 1), 422},
			{strings.Replace(raw, `{"value": true}`, `[]`, 1), 422},
			{strings.Replace(raw, `"enabled": false`, `"enabled": "false"`, 1), 422},
			{strings.Replace(raw, `"Evening lights"`, `"   "`, 1), 400},
			{strings.Replace(raw, `"weekends"`, `"weekdays"`, 1), 400},
			{strings.Replace(raw, `"0 19 * * 1-5"`, `"@hourly"`, 1), 400},
			{strings.Replace(raw, `"0 19 * * 1-5"`, `"   "`, 1), 400},
			{strings.Replace(raw, `"0 19 * * 1-5"`, `""`, 1), 422},
			{strings.Replace(raw, string(fixtureEntityID), "secret", 1), 400},
			{strings.Replace(raw, string(fixtureEntityID), "ent_01900000-0000-7000-8000-000000000099", 1), 400},
			{strings.Replace(raw, `"operation_name": "set"`, `"operation_name": "toggle"`, 1), 400},
			{strings.Replace(raw, `{"value": true}`, `{"value":"secret"}`, 1), 400},
			{strings.Replace(raw, `{"value": true}`, `{"value":true,"secret":1}`, 1), 400},
			{strings.Replace(raw, `{"value": true}`, `{"secret":"`+strings.Repeat("x", 65536)+`"}`, 1), 422},
		}
		for i, test := range cases {
			response := automationRequest(h.router, method, target, test.raw, "")
			if response.Code != test.status {
				t.Fatalf("%s case %d: %d want %d: %s", method, i, response.Code, test.status, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "secret") {
				t.Fatalf("%s case %d leaked input: %s", method, i, response.Body.String())
			}
		}
	}
	for _, body := range []string{`{}`, `{"command_id":"secret","correlation_id":"secret"}`} {
		requireAutomationStatus(
			t,
			automationRequest(h.router, http.MethodPost, path+"/runs", body, "key"),
			http.StatusBadRequest,
		)
	}
	for _, target := range []string{"/v1/automations/invalid", "/v1/automation-runs/run_01900000-0000-7000-8000-000000000001", "/v1/automation-runs?automation_id=invalid"} {
		requireAutomationStatus(t, automationRequest(h.router, http.MethodGet, target, "", ""), http.StatusBadRequest)
	}
	requireAutomationStatus(
		t,
		automationRequest(
			h.router,
			http.MethodGet,
			"/v1/automation-runs/arn_01900000-0000-7000-8000-000000000099",
			"",
			"",
		),
		http.StatusNotFound,
	)
	page, err := h.service.ListAutomations(t.Context(), automations.AutomationListParams{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Revision != 1 || h.sends.Load() != 0 {
		t.Fatalf("invalid request changed data: %#v %v", page, err)
	}
}

// A11/A12: structural bounds do not accidentally become uniqueness-by-expression.
func TestAutomationHTTPStructuralBoundsAndDisabledReplacement(t *testing.T) {
	t.Parallel()
	h := newAutomationHTTPHarness(t)
	if _, err := h.devices.SetEntityEnabled(t.Context(), fixtureEntityID, false); err != nil {
		t.Fatal(err)
	}
	base := `{"name":"bounds","triggers":[%s],"steps":[%s]}`
	step := `{"entity_id":"` + string(fixtureEntityID) + `","operation_name":"set","parameters":{"value":true}}`
	for _, count := range []int{0, 1, 32, 33} {
		triggers := make([]string, count)
		for i := range count {
			triggers[i] = fmt.Sprintf(`{"id":"trigger-%d","kind":"cron","expression":"0 19 * * *"}`, i)
		}
		response := automationRequest(
			h.router,
			http.MethodPost,
			"/v1/automations",
			fmt.Sprintf(base, strings.Join(triggers, ","), step),
			"",
		)
		expected := http.StatusCreated
		if count == 0 || count == 33 {
			expected = http.StatusUnprocessableEntity
		}
		requireAutomationStatus(t, response, expected)
	}
	trigger := `{"id":"daily","kind":"cron","expression":"0 19 * * *"}`
	for _, count := range []int{0, 1, 100, 101} {
		steps := make([]string, count)
		for i := range count {
			steps[i] = step
		}
		response := automationRequest(
			h.router,
			http.MethodPost,
			"/v1/automations",
			fmt.Sprintf(base, trigger, strings.Join(steps, ",")),
			"",
		)
		expected := http.StatusCreated
		if count == 0 || count == 101 {
			expected = http.StatusUnprocessableEntity
		}
		requireAutomationStatus(t, response, expected)
	}
	// Existing enabled=true must not survive omission on replacement.
	raw := readAutomationFixture(t, "valid-power")
	record := createHTTPAutomation(t, h, strings.Replace(raw, `"enabled": false`, `"enabled": true`, 1))
	if !record.Enabled {
		t.Fatal("explicit enabled lost")
	}
	response := automationRequest(
		h.router,
		http.MethodPut,
		"/v1/automations/"+string(record.ID)+"?expected_revision=1",
		strings.Replace(raw, `"enabled": false,`, "", 1),
		"",
	)
	requireAutomationStatus(t, response, http.StatusOK)
	if body := decodeAutomationResponse[AutomationBody](t, response); body.Enabled || body.Revision != 2 {
		t.Fatalf("replacement = %#v", body)
	}
}

func TestAutomationHTTPConcurrentSameKey(t *testing.T) {
	t.Parallel()
	h := newAutomationHTTPHarness(t)
	record := createHTTPAutomation(t, h, readAutomationFixture(t, "valid-power"))
	const clients = 8
	var wait sync.WaitGroup
	gate := make(chan struct{})
	ids := make(chan automations.AutomationRunID, clients)
	var created atomic.Int64
	for range clients {
		wait.Go(func() {
			<-gate
			response := automationRequest(
				h.router,
				http.MethodPost,
				"/v1/automations/"+string(record.ID)+"/runs",
				"",
				"shared-key",
			)
			if response.Code == http.StatusAccepted {
				created.Add(1)
			} else if response.Code != http.StatusOK {
				t.Errorf("admission: %d %s", response.Code, response.Body.String())
				return
			}
			// Do not Fatal in a worker goroutine.
			var run AutomationRunBody
			if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
				t.Error(err)
				return
			}
			ids <- run.ID
		})
	}
	close(gate)
	wait.Wait()
	close(ids)
	var first automations.AutomationRunID
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("same key admitted distinct runs")
		}
	}
	if created.Load() != 1 {
		t.Fatal("same-key concurrency did not admit exactly one new Run")
	}
	h.unblock()
	waitHTTPAutomationWorkers(t, h)
	if h.sends.Load() != 1 {
		t.Fatal("duplicate dispatch")
	}
}

// A11: transport must not coerce arbitrary catalog-owned parameter numbers.
type precisionAutomationService struct {
	Automations

	definition automations.AutomationDefinition
}

func (service *precisionAutomationService) CreateAutomation(
	_ context.Context,
	definition automations.AutomationDefinition,
) (automations.AutomationRecord, error) {
	service.definition = definition
	return automations.AutomationRecord{Definition: definition, Revision: 1}, nil
}

// HouseholdTimezone satisfies the definition-response seam without storage.
func (service *precisionAutomationService) HouseholdTimezone() *time.Location { return time.UTC }
func TestAutomationHTTPNumberPrecision(t *testing.T) {
	t.Parallel()
	codec, err := automations.NewAutomationDefinitionCodec()
	if err != nil {
		t.Fatal(err)
	}
	service := &precisionAutomationService{}
	response := automationRequest(
		automationTestRouter(service, codec),
		http.MethodPost,
		"/v1/automations",
		readAutomationFixture(t, "valid"),
		"",
	)
	requireAutomationStatus(t, response, http.StatusCreated)
	for _, number := range []string{"9007199254740993", "0.1234567890123456789"} {
		if !strings.Contains(string(service.definition.Steps[0].Parameters), number) ||
			!strings.Contains(response.Body.String(), number) {
			t.Fatalf("number rounded: %s", response.Body.String())
		}
	}
}
