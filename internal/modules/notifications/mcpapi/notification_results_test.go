package mcpapi_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func TestNotificationSummaryAndDetailHaveIndependentContracts(t *testing.T) {
	t.Parallel()
	completed := time.Date(2026, 10, 21, 12, 0, 0, 123, time.UTC)
	r := &inputRecorder{attempt: notifications.Attempt{
		ID: "ntf_01920000-0000-7000-8000-000000000001", DestinationID: "ndst_01920000-0000-7000-8000-000000000002",
		DestinationName: "Freezer", Message: notifications.Message{Title: "Alert", Text: "Private body"},
		Outcome: notifications.Accepted, StartedAt: completed.Add(-time.Second), Deadline: completed.Add(time.Minute),
		CompletedAt: &completed, Receipt: "provider-receipt",
	}}
	client := inputMCP(t, r)
	result, page := call(t, client, "list_notifications", map[string]any{})
	if result.IsError {
		t.Fatal(result)
	}
	items := page["items"].([]any)
	want := map[string]any{
		"notification_id": string(
			r.attempt.ID,
		),
		"destination_id":   string(r.attempt.DestinationID),
		"destination_name": "Freezer",
		"title":            "Alert",
		"status":           "accepted",
		"started_at":       "2026-10-21T11:59:59.000000123Z",
		"completed_at":     "2026-10-21T12:00:00.000000123Z",
	}
	if len(page) != 1 || len(items) != 1 || !reflect.DeepEqual(items[0], want) {
		t.Fatalf("summary page = %+v", page)
	}
	if r.lists[0].Limit != 50 {
		t.Fatalf("default limit = %d", r.lists[0].Limit)
	}
	result, detail := call(t, client, "get_notification", map[string]any{"id": string(r.attempt.ID)})
	want["message"], want["deadline"], want["receipt"] = "Private body", "2026-10-21T12:01:00.000000123Z", "provider-receipt"
	if result.IsError || !reflect.DeepEqual(detail, want) || r.gets[0] != r.attempt.ID {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestNotificationDiscoveryPaginationAndRunningNull(t *testing.T) {
	t.Parallel()
	r := &inputRecorder{
		destination: notifications.Destination{
			ID:        "ndst_01920000-0000-7000-8000-000000000002",
			Name:      "Freezer",
			Available: true,
		},
		pageCursor: "opaque-next",
	}
	client := inputMCP(t, r)
	result, page := call(t, client, "list_notification_destinations", map[string]any{})
	want := map[string]any{"destination_id": string(r.destination.ID), "name": "Freezer", "available": true}
	if result.IsError || page["next_cursor"] != "opaque-next" || r.pageLimit != 50 ||
		!reflect.DeepEqual(page["items"], []any{want}) {
		t.Fatal(page)
	}
	result, detail := call(
		t,
		client,
		"get_notification_destination",
		map[string]any{"destination_id": string(r.destination.ID)},
	)
	if result.IsError || !reflect.DeepEqual(detail, want) {
		t.Fatal(detail)
	}
	empty := inputMCP(t, &inputRecorder{})
	for _, name := range []string{"list_notifications", "list_notification_destinations"} {
		result, page = call(t, empty, name, map[string]any{})
		if result.IsError || !reflect.DeepEqual(page, map[string]any{"items": []any{}}) {
			t.Fatalf("empty %s = %+v", name, page)
		}
	}
	running := &inputRecorder{attempt: notifications.Attempt{ID: "ntf_01920000-0000-7000-8000-000000000001"}}
	result, detail = call(t, inputMCP(t, running), "get_notification", map[string]any{"id": string(running.attempt.ID)})
	completed, exists := detail["completed_at"]
	if result.IsError || !exists || completed != nil || detail["status"] != "running" {
		t.Fatal(detail)
	}
}

func TestNotificationPageAndReadFailures(t *testing.T) {
	t.Parallel()
	for _, args := range []map[string]any{{"limit": 0}, {"limit": 201}, {"limit": nil}, {"cursor": nil}, {"destination_id": nil}} {
		r := &inputRecorder{}
		client := inputMCP(t, r)
		result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "list_notifications", Arguments: args})
		if err == nil && !result.IsError {
			t.Fatalf("invalid page accepted: %+v", args)
		}
		r.assertCalls(t, 0, 0, 0)
	}
	r := &inputRecorder{getErr: notifications.ErrAttemptNotFound, listErr: notifications.ErrInvalidInput}
	client := inputMCP(t, r)
	result, fields := call(t, client, "get_notification", map[string]any{"id": "missing"})
	if !result.IsError || fields["failure_code"] != "not_found" {
		t.Fatal(fields)
	}
	result, fields = call(t, client, "list_notifications", map[string]any{"cursor": "malformed"})
	if !result.IsError || fields["failure_code"] != "invalid_input" || r.lists[0].Cursor != "malformed" {
		t.Fatal(fields)
	}
	r.assertCalls(t, 0, 1, 1)
}
