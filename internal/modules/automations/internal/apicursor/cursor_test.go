package apicursor_test

import (
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/internal/apicursor"
	"github.com/mholtzscher/hearth/internal/platform/pagination"
)

func TestScopedAutomationAndHistoryPositions(t *testing.T) {
	t.Parallel()
	id := automations.AutomationID("aut_01980000-0000-7000-8000-000000000001")
	encoded, err := apicursor.EncodeAutomations(id)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := apicursor.DecodeAutomations(encoded)
	if err != nil || decoded == nil || *decoded != id {
		t.Fatalf("automation position = %v, %v", decoded, err)
	}
	at := time.Date(2026, 10, 21, 12, 0, 0, 123456789, time.UTC)
	history, err := apicursor.EncodeHistory(id, at, "arn_01980000-0000-7000-8000-000000000003")
	if err != nil {
		t.Fatal(err)
	}
	recordedAt, entryID, err := apicursor.DecodeHistory(history, id)
	if err != nil || recordedAt == nil || !recordedAt.Equal(at) || entryID == nil ||
		*entryID != "arn_01980000-0000-7000-8000-000000000003" {
		t.Fatalf("history position = %v, %v, %v", recordedAt, entryID, err)
	}
}

func TestCursorScopeAndMalformedPositions(t *testing.T) {
	t.Parallel()

	id := automations.AutomationID("aut_01980000-0000-7000-8000-000000000001")
	other := automations.AutomationID("aut_01980000-0000-7000-8000-000000000002")
	at := time.Date(2026, 10, 21, 12, 0, 0, 123456789, time.UTC)
	encoded, encodeErr := apicursor.EncodeAutomations(id)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	history, historyErr := apicursor.EncodeHistory(id, at, "entry")
	if historyErr != nil {
		t.Fatal(historyErr)
	}
	for _, bad := range []string{"not-a-cursor", history} {
		if _, decodeErr := apicursor.DecodeAutomations(bad); decodeErr == nil {
			t.Fatalf("accepted automation cursor %q", bad)
		}
	}
	for _, test := range []struct {
		cursor string
		parent automations.AutomationID
	}{{history, other}, {encoded, id}, {"not-a-cursor", id}} {
		if _, _, decodeErr := apicursor.DecodeHistory(test.cursor, test.parent); decodeErr == nil {
			t.Fatalf("accepted history cursor %+v", test)
		}
	}
}

func TestMalformedCursorFields(t *testing.T) {
	t.Parallel()
	id := automations.AutomationID("aut_01980000-0000-7000-8000-000000000001")
	at := time.Date(2026, 10, 21, 12, 0, 0, 123456789, time.UTC)
	for _, bad := range []map[string]any{
		{"v": 2, "resource": "automations", "id": string(id)},
		{"v": 1, "resource": "automations", "id": "invalid"},
		{"v": 1, "resource": "automation_history", "automation_id": string(id), "recorded_at": "2026-10-21T12:00:00+00:00", "id": "entry"},
		{"v": 1, "resource": "automation_history", "automation_id": string(id), "recorded_at": at.Format(time.RFC3339Nano), "id": ""},
	} {
		raw, encodeErr := pagination.EncodeCursor(bad)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if bad["resource"] == "automations" {
			if _, decodeErr := apicursor.DecodeAutomations(raw); decodeErr == nil {
				t.Fatalf("accepted %v", bad)
			}
		} else if _, _, decodeErr := apicursor.DecodeHistory(raw, id); decodeErr == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
}
