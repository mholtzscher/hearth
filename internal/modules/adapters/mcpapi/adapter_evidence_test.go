package mcpapi //nolint:testpackage // Recorder requests pin management projection and cursor ownership.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/adapters/internal/apicursor"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

//nolint:gocognit // Follow-up cursors, full evidence, and summary privacy are one management contract.
func TestManagementSummariesDetailAndScopedHistory(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 29, 15, 0, 1, 0, time.UTC)
	source := at.Add(-time.Second)
	item := adapters.AdapterInstance{ID: "simulator", Health: adapters.AdapterHealth{
		Status:           adapters.AdapterHealthUnhealthy,
		Source:           "adapter",
		Since:            at,
		EvidenceAt:       at,
		SourceObservedAt: &source,
		Reason:           &adapters.HealthReason{Code: "hearth.network_unreachable"},
		Runtime: &adapters.RuntimeEvidence{
			ID:              "private-runtime",
			Status:          "online",
			SoftwareName:    "simulator",
			SoftwareVersion: "1",
			ClaimedAt:       at.Add(-time.Hour),
			LeaseExpiresAt:  at.Add(time.Minute),
		},
	}}
	listCalls, historyCalls := 0, 0
	stub := &stubAdapters{
		listAdapters: func(_ context.Context, in adapters.ListAdaptersParams) (adapters.Page[adapters.AdapterInstance], error) {
			listCalls++
			if in.Limit != 50 || (listCalls == 1 && in.AfterID != nil) ||
				(listCalls == 2 && (in.AfterID == nil || *in.AfterID != "simulator")) {
				t.Fatalf("list=%+v", in)
			}
			return adapters.Page[adapters.AdapterInstance]{
				Items:   []adapters.AdapterInstance{item},
				HasMore: listCalls == 1,
			}, nil
		},
		getAdapter: func(_ context.Context, id string) (adapters.AdapterInstance, error) {
			if id != "simulator" {
				t.Fatalf("id=%s", id)
			}
			return item, nil
		},
		listAdapterHealthHistory: func(_ context.Context, in adapters.ListAdapterHealthParams) (adapters.Page[adapters.HealthTransition], error) {
			historyCalls++
			if in.AdapterID != "simulator" || in.Limit != 50 || (historyCalls == 1 && in.BeforeReceiveOrder != nil) ||
				(historyCalls == 2 && (in.BeforeReceiveOrder == nil || *in.BeforeReceiveOrder != 12)) {
				t.Fatalf("history=%+v", in)
			}
			return adapters.Page[adapters.HealthTransition]{
				Items: []adapters.HealthTransition{
					{
						Status:           "unhealthy",
						Source:           "adapter",
						ObservedAt:       at,
						SourceObservedAt: &source,
						Reason:           item.Health.Reason,
						ReceiveOrder:     12,
					},
				},
				HasMore: historyCalls == 1,
			}, nil
		},
	}
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
	Register(server, stub, nil)
	page := functionToolWire(t, server, "list_adapters", `{}`)
	var first struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(page, &first); err != nil {
		t.Fatal(err)
	}
	wantCursor, err := apicursor.EncodeAdapters("simulator")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"items":[{"adapter_id":"simulator","health":{"status":"unhealthy","reason_code":"hearth.network_unreachable"}}],"next_cursor":"` + wantCursor + `"}`
	if string(page) != want || first.NextCursor != wantCursor {
		t.Fatalf("summary=%s", page)
	}
	last := functionToolWire(t, server, "list_adapters", `{"cursor":"`+first.NextCursor+`"}`)
	if strings.Contains(string(last), "next_cursor") || listCalls != 2 {
		t.Fatalf("last=%s calls=%d", last, listCalls)
	}
	detail := functionToolWire(t, server, "get_adapter", `{"adapter_id":"simulator"}`)
	wantDetail := `{"adapter_id":"simulator","health":{"status":"unhealthy","reason_code":"hearth.network_unreachable","source":"adapter","since":"2026-08-29T15:00:01Z","evidence_at":"2026-08-29T15:00:01Z","source_observed_at":"2026-08-29T15:00:00Z","runtime":{"status":"online","software_name":"simulator","software_version":"1","last_heartbeat_at":null,"lease_expires_at":"2026-08-29T15:01:01Z"}}}`
	if string(detail) != wantDetail {
		t.Fatalf("detail=%s", detail)
	}
	history := functionToolWire(t, server, "list_adapter_health_history", `{"adapter_id":"simulator"}`)
	var historyPage struct {
		NextCursor string `json:"next_cursor"`
	}
	if err = json.Unmarshal(history, &historyPage); err != nil {
		t.Fatal(err)
	}
	wantCursor, err = apicursor.EncodeHealth("simulator", 12)
	if err != nil {
		t.Fatal(err)
	}
	wantHistory := `{"items":[{"status":"unhealthy","source":"adapter","observed_at":"2026-08-29T15:00:01Z","source_observed_at":"2026-08-29T15:00:00Z","reason_code":"hearth.network_unreachable"}],"next_cursor":"` + wantCursor + `"}`
	if string(history) != wantHistory || historyPage.NextCursor != wantCursor {
		t.Fatalf("history=%s", history)
	}
	last = functionToolWire(
		t,
		server,
		"list_adapter_health_history",
		`{"adapter_id":"simulator","cursor":"`+wantCursor+`"}`,
	)
	if strings.Contains(string(last), "next_cursor") || historyCalls != 2 {
		t.Fatalf("last history=%s calls=%d", last, historyCalls)
	}
	_, err = (&handler{adapters: &stubAdapters{}}).listAdapterHealthHistory(
		t.Context(),
		ListAdapterHealthHistoryInput{
			AdapterID: "other",
			pageInput: pageInput{Cursor: wantCursor}, //nolint:modernize // Keep the cursor scope fixture explicit.
		},
	)
	if failure, ok := errors.AsType[*mcptools.ToolError](err); !ok || failure.Code != "invalid_request" {
		t.Fatalf("cross-parent error=%v", err)
	}
}

func TestManagementFailuresAreClassifiedWithoutHTTP(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		err  error
		code string
	}{
		{adapters.ErrAdapterNotFound, "adapter_not_found"}, {adapters.ErrInvalidPage, "invalid_request"}, {errors.New("private-database-path"), "internal_error"},
	} {
		t.Run(test.code, func(t *testing.T) {
			t.Parallel()
			_, err := (&handler{adapters: &stubAdapters{getAdapter: func(context.Context, string) (adapters.AdapterInstance, error) {
				return adapters.AdapterInstance{}, test.err
			}}}).getAdapter(
				t.Context(),
				GetAdapterInput{AdapterID: "demo"},
			)
			failure, ok := errors.AsType[*mcptools.ToolError](err)
			if !ok || failure.Code != test.code || strings.Contains(failure.Message, "private") {
				t.Fatalf("failure=%+v", failure)
			}
		})
	}
}
