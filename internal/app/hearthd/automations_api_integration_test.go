//go:build integration

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type scheduleBranchDevices struct {
	*blockingAutomationDevices

	reads atomic.Int64
}

func (seam *scheduleBranchDevices) GetEntityStateSnapshot(
	context.Context,
	[]devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	seam.reads.Add(1)
	return devices.EntityStateSnapshot{}, errors.New("scheduled Trigger-only branch must not read State")
}

// This protects the app's calendar-worker to HTTP integration. A UTC instant
// matching Chicago's 07:00 must produce schedule history through the assembled
// handler, with no device evidence or empty Entity fields. Multiple matched IDs
// must reach branch selection, whose evidence contains only the leaf's intersection.
func TestRuntimeScheduleWorkerHistoryThroughHTTP(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	location, err := (Config{HouseholdTimezone: "America/Chicago"}).LoadHouseholdTimezone()
	if err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 2, 11, 59, 0, 0, time.UTC).UnixNano())
	dependencies := automations.Dependencies{
		Now: func() time.Time { return time.Unix(0, clock.Load()).UTC() }, HouseholdLocation: location,
	}
	seam := &scheduleBranchDevices{blockingAutomationDevices: newBlockingAutomationDevices()}
	service := automations.NewService(
		automationssqlite.NewAutomationRepository(openOrderingDatabase(t), dependencies), seam, dependencies,
	)
	t.Cleanup(func() {
		close(seam.release)
		if drainErr := service.Drain(ctx); drainErr != nil {
			t.Error(drainErr)
		}
	})
	handler, _ := newHTTPHandler(
		&stubDevices{},
		service,
		&stubNotifications{},
		&stubDevices{},
		&stubFunctions{},
		&stubAgent{},
		&testReadiness{},
		&stubDevices{},
		service,
		&stubFunctions{},
		newMCPServer(&stubDevices{}, service, &stubNotifications{}, &stubDevices{}, &stubFunctions{}, nil),
	)
	const definition = `{"name":"Morning","enabled":true,"triggers":[
		{"id":"morning","kind":"cron","expression":" * 7 * * fri "},
		{"id":"minute","kind":"cron","expression":"* * * * *"},
		{"id":"midnight","kind":"cron","expression":"0 0 * * *"}],
		"steps":[{"id":"route","kind":"if",
			"conditions":{"id":"source","kind":"trigger","trigger_ids":["minute","midnight"]},
			"then":[{"kind":"command", "id":"on","entity_id":"ent_01920000-0000-7000-8000-000000000004","operation":"set","parameters":{"value":true}}]}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/automations", strings.NewReader(definition))
	request.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("runtime create = %d: %s", created.Code, created.Body.String())
	}
	var record struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	ticks := make(chan time.Time)
	worker, err := startScheduleScheduling(ctx, slog.New(slog.DiscardHandler), service, dependencies.Now,
		func() (<-chan time.Time, func()) { return ticks, func() {} })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if stopErr := worker.Stop(ctx); stopErr != nil {
			t.Error(stopErr)
		}
	})
	clock.Store(time.Date(2026, 10, 2, 12, 0, 20, 0, time.UTC).UnixNano())
	select {
	case ticks <- time.Time{}:
	case <-time.After(5 * time.Second):
		t.Fatal("calendar worker did not receive tick")
	}
	select {
	case <-seam.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("calendar worker did not execute admitted Run")
	}
	clock.Store(time.Date(2026, 10, 2, 12, 1, 20, 0, time.UTC).UnixNano())
	select {
	case ticks <- time.Time{}:
	case <-time.After(5 * time.Second):
		t.Fatal("calendar worker did not receive second tick")
	}
	assertRuntimeScheduleHistory(t, handler, "/v1/automations/"+record.ID+"/history")
	if seam.reads.Load() != 0 {
		t.Fatal("scheduled Trigger-only branch read State")
	}
}

func assertRuntimeScheduleHistory(t *testing.T, handler http.Handler, path string) {
	t.Helper()
	var history struct {
		Items []struct {
			ID    string          `json:"id"`
			Kind  string          `json:"kind"`
			Cause conditionsCause `json:"cause"`
		} `json:"items"`
	}
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		response := appRequest(handler, path)
		if response.Code != http.StatusOK {
			return false, fmt.Errorf("history = %d: %s", response.Code, response.Body.String())
		}
		if decodeErr := json.Unmarshal(response.Body.Bytes(), &history); decodeErr != nil {
			return false, decodeErr
		}
		return len(history.Items) == 2, nil
	})
	for _, item := range history.Items {
		if item.Cause.Kind != "schedule" {
			t.Fatalf("history cause = %#v", item.Cause)
		}
		assertRuntimeScheduleDetail(t, appRequest(handler, path+"/"+item.ID), item.Kind)
	}
}

func assertRuntimeScheduleDetail(t *testing.T, response *httptest.ResponseRecorder, kind string) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("history detail = %d: %s", response.Code, response.Body.String())
	}
	// The envelope also has kind and $schema strings, so decode its selected payload separately.
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(envelope[kind], &body); err != nil {
		t.Fatal(err)
	}
	cause, ok := body["cause"].(map[string]any)
	if !ok || cause["kind"] != "schedule" {
		t.Fatalf("detail cause = %v", body)
	}
	for _, field := range []string{"fact", "held_state"} {
		if _, present := body[field]; present {
			t.Fatalf("schedule detail contains %s", field)
		}
	}
	var triggers []any
	wantTriggers := 2
	if kind == "run" {
		var run branchingHistoryRun
		if err := json.Unmarshal(envelope[kind], &run); err != nil {
			t.Fatal(err)
		}
		assertBranchingTriggerEvidence(t, run, "schedule", []string{"morning", "minute"}, []string{"minute"})
		triggers = body["snapshot"].(map[string]any)["triggers"].([]any)
		wantTriggers = 3 // The immutable definition also contains the unmatched midnight Trigger.
	} else {
		if body["reason"] != "automation_busy" {
			t.Fatalf("Skip reason = %v", body["reason"])
		}
		triggers = body["matched_triggers"].([]any)
	}
	if len(triggers) != wantTriggers {
		t.Fatalf("Triggers = %v, want %d", triggers, wantTriggers)
	}
	trigger := triggers[0].(map[string]any)
	if len(trigger) != 3 || trigger["expression"] != "* 7 * * fri" {
		t.Fatalf("cron output = %v", trigger)
	}
}
