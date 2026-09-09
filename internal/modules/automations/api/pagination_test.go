package api //nolint:testpackage // Cursors are transport-owned, canonical continuation positions.

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

type automationHTTPPage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
}

func automationPageResponse[T any](t *testing.T, h *automationHTTPHarness, path string) automationHTTPPage[T] {
	t.Helper()
	response := automationRequest(h.router, http.MethodGet, path, "", "")
	requireAutomationStatus(t, response, http.StatusOK)
	page := decodeAutomationResponse[automationHTTPPage[T]](t, response)
	if page.Items == nil {
		t.Fatal("empty collection must be [] not null")
	}
	return page
}

func TestAutomationHTTPDefinitionPagination(t *testing.T) {
	t.Parallel()
	h := newAutomationHTTPHarness(t)
	empty := automationPageResponse[AutomationBody](t, h, "/v1/automations")
	if len(empty.Items) != 0 || empty.NextCursor != "" {
		t.Fatal("nonempty initial page")
	}
	var records []AutomationBody
	for range 3 {
		records = append(records, createHTTPAutomation(t, h, readAutomationFixture(t, "valid-power")))
	}
	page := automationPageResponse[AutomationBody](t, h, "/v1/automations?limit=1")
	if len(page.Items) != 1 || page.Items[0].ID != records[0].ID || page.NextCursor == "" {
		t.Fatalf("ascending first page = %#v", page)
	}
	cursor := page.NextCursor
	// Continuation is a position, not a row reference or a snapshot.
	requireAutomationStatus(
		t,
		automationRequest(
			h.router,
			http.MethodDelete,
			"/v1/automations/"+string(records[0].ID)+"?expected_revision=1",
			"",
			"",
		),
		http.StatusNoContent,
	)
	inserted := createHTTPAutomation(t, h, readAutomationFixture(t, "valid-power"))
	next := automationPageResponse[AutomationBody](t, h, "/v1/automations?limit=2&cursor="+cursor)
	if len(next.Items) != 2 || next.Items[0].ID != records[1].ID || next.Items[1].ID != records[2].ID ||
		next.NextCursor == "" {
		t.Fatalf("continuation = %#v", next)
	}
	last := automationPageResponse[AutomationBody](t, h, "/v1/automations?cursor="+next.NextCursor)
	if len(last.Items) != 1 || last.Items[0].ID != inserted.ID || last.NextCursor != "" {
		t.Fatalf("last = %#v", last)
	}
	for _, collection := range []string{"/v1/automations", "/v1/automation-runs"} {
		for _, limit := range []string{"0", "201", "-1", "1.5", "NaN"} {
			requireAutomationStatus(
				t,
				automationRequest(h.router, http.MethodGet, collection+"?limit="+limit, "", ""),
				http.StatusUnprocessableEntity,
			)
		}
		requireAutomationStatus(
			t,
			automationRequest(h.router, http.MethodGet, collection+"?limit=200", "", ""),
			http.StatusOK,
		)
	}
	requireAutomationStatus(
		t,
		automationRequest(h.router, http.MethodGet, "/v1/automation-runs?cursor="+cursor, "", ""),
		http.StatusBadRequest,
	)
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatal(err)
	}
	invalid := []string{"!", cursor + "=", base64.RawURLEncoding.EncodeToString(append(raw, []byte(" {}")...)),
		base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), `"v":1`, `"v":2`, 1))),
		base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), `"v":1`, `"v":1,"extra":true`, 1))),
		base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), `"v":1`, `"v":1,"v":1`, 1))),
		base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), `"v":1`, `"v": 1`, 1))),
	}
	for _, value := range invalid {
		requireAutomationStatus(
			t,
			automationRequest(h.router, http.MethodGet, "/v1/automations?cursor="+value, "", ""),
			http.StatusBadRequest,
		)
	}
}

func startHTTPAutomation(
	t *testing.T,
	h *automationHTTPHarness,
	id automations.AutomationID,
	key string,
) AutomationRunBody {
	t.Helper()
	response := automationRequest(h.router, http.MethodPost, "/v1/automations/"+string(id)+"/runs", "", key)
	requireAutomationStatus(t, response, http.StatusAccepted)
	waitHTTPAutomationWorkers(t, h)
	return decodeAutomationResponse[AutomationRunBody](t, response)
}

// A3/A8: filtering remains historical after delete; pruning removes keys and
// permits new invocation only when a live definition still exists.
func TestAutomationHTTPHistoryPaginationAndPrunedKeys(t *testing.T) {
	t.Parallel()
	h := newAutomationHTTPHarness(t)
	h.unblock()
	record := createHTTPAutomation(t, h, readAutomationFixture(t, "valid-power"))
	other := createHTTPAutomation(t, h, readAutomationFixture(t, "valid-power"))
	var runs []AutomationRunBody
	for i := range 3 {
		runs = append(runs, startHTTPAutomation(t, h, record.ID, fmt.Sprintf("key-%d", i)))
	}
	startHTTPAutomation(t, h, other.ID, "other-key")
	filter := "/v1/automation-runs?automation_id=" + string(record.ID)
	page := automationPageResponse[AutomationRunSummaryBody](t, h, filter+"&limit=1")
	if len(page.Items) != 1 || page.Items[0].ID != runs[2].ID || page.NextCursor == "" {
		t.Fatalf("history first page = %#v", page)
	}
	// A newer insert must not leak into this descending continuation.
	startHTTPAutomation(t, h, record.ID, "newer-key")
	requireAutomationStatus(
		t,
		automationRequest(
			h.router,
			http.MethodDelete,
			"/v1/automations/"+string(record.ID)+"?expected_revision=1",
			"",
			"",
		),
		http.StatusNoContent,
	)
	next := automationPageResponse[AutomationRunSummaryBody](t, h, filter+"&cursor="+page.NextCursor)
	if len(next.Items) != 2 || next.Items[0].ID != runs[1].ID || next.Items[1].ID != runs[0].ID ||
		next.NextCursor != "" {
		t.Fatalf("historical continuation = %#v", next)
	}
	for _, target := range []string{"/v1/automations?cursor=", "/v1/automation-runs?cursor=", "/v1/automation-runs?automation_id=" + string(other.ID) + "&cursor="} {
		requireAutomationStatus(
			t,
			automationRequest(h.router, http.MethodGet, target+page.NextCursor, "", ""),
			http.StatusBadRequest,
		)
	}
	if err := h.service.PruneAutomationHistory(t.Context(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	afterPrune := automationPageResponse[AutomationRunSummaryBody](t, h, filter+"&cursor="+page.NextCursor)
	if len(afterPrune.Items) != 0 || afterPrune.NextCursor != "" {
		t.Fatalf("pruned continuation = %#v", afterPrune)
	}
	requireAutomationStatus(
		t,
		automationRequest(h.router, http.MethodGet, "/v1/automation-runs/"+string(runs[0].ID), "", ""),
		http.StatusNotFound,
	)
	requireAutomationStatus(
		t,
		automationRequest(h.router, http.MethodPost, "/v1/automations/"+string(record.ID)+"/runs", "", "key-0"),
		http.StatusNotFound,
	)
	startHTTPAutomation(t, h, other.ID, "other-key")
}

func TestAutomationRunCursorCanonicalTime(t *testing.T) {
	t.Parallel()
	for _, timestamp := range []string{"", "2026-01-01T01:00:00+01:00", "2026-01-01T00:00:00.000Z", "0001-01-01T00:00:00Z"} {
		raw := `{"v":1,"resource":"automation_runs","automation_id":"","started_at":"` + timestamp + `","id":"arn_01900000-0000-7000-8000-000000000001"}`
		if _, _, err := automationRunListPosition(base64.RawURLEncoding.EncodeToString([]byte(raw)), ""); err == nil {
			t.Fatalf("accepted noncanonical time %s", timestamp)
		}
	}
}
