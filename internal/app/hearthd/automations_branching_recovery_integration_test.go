//go:build integration

package hearthd //nolint:testpackage // Tests exercise app assembly and lifecycle boundaries.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/household"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

// branchingDecisionBarrier wraps the existing repository contract. Both gates
// surround the real transaction; neither manufactures evidence or command order.
type branchingDecisionBarrier struct {
	automations.Repository

	step         automations.StepID
	before       chan struct{}
	commit       chan struct{}
	after        chan struct{}
	resume       chan struct{}
	writeErr     error
	interruptErr error
	writes       atomic.Int64
	interrupts   atomic.Int32
}

func (repository *branchingDecisionBarrier) RecordStep(
	ctx context.Context,
	record automations.StepExecutionRecord,
) error {
	_, isDecision := record.Evidence.(automations.BranchDecided)
	if !isDecision {
		return repository.Repository.RecordStep(ctx, record)
	}
	repository.writes.Add(1)
	if record.StepID == repository.step {
		close(repository.before)
		<-repository.commit
	}
	if err := repository.Repository.RecordStep(ctx, record); err != nil {
		return err
	}
	if record.StepID == repository.step {
		close(repository.after)
		<-repository.resume
		return repository.writeErr
	}
	return nil
}

func (repository *branchingDecisionBarrier) InterruptRun(
	ctx context.Context,
	id automations.RunID,
	at time.Time,
) error {
	repository.interrupts.Add(1)
	if repository.interruptErr != nil {
		return repository.interruptErr
	}
	return repository.Repository.InterruptRun(ctx, id, at)
}

type branchingCommandSeam struct {
	*blockingAutomationDevices

	commands map[devices.CommandID]devices.CommandRecord
	calls    int
	reads    atomic.Int64
}

func (seam *branchingCommandSeam) GetEntityStateSnapshot(
	context.Context,
	[]devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	seam.reads.Add(1)
	return devices.EntityStateSnapshot{}, errors.New("Trigger-only construct must not read State")
}

type branchingFaultSignal struct {
	slog.Handler

	fault chan struct{}
}

func (handler branchingFaultSignal) Enabled(context.Context, slog.Level) bool { return true }

func (handler branchingFaultSignal) Handle(_ context.Context, record slog.Record) error {
	record.Attrs(func(attribute slog.Attr) bool {
		if attribute.Key == "event" && attribute.Value.String() == "automation.executor_fault" {
			select {
			case handler.fault <- struct{}{}:
			default:
			}
		}
		return true
	})
	return nil
}

func (seam *branchingCommandSeam) ExecuteCommand(
	_ context.Context,
	input devices.CommandInput,
) (devices.CommandResult, error) {
	seam.calls++
	now := time.Now().UTC()
	seam.commands[input.ID] = devices.CommandRecord{
		ID:            input.ID,
		CorrelationID: input.CorrelationID,
		Status:        devices.CommandStatusSatisfied,
		CompletedAt:   &now,
		EntityID:      input.EntityID,
		OperationName: input.OperationName,
		Parameters:    bytes.Clone(input.Parameters),
	}
	return devices.CommandResult{CommandID: input.ID, Outcome: devices.OutcomeDispatched}, nil
}

func (seam *branchingCommandSeam) GetCommand(_ context.Context, id devices.CommandID) (devices.CommandRecord, error) {
	record, ok := seam.commands[id]
	if !ok {
		return devices.CommandRecord{}, devices.ErrCommandNotFound
	}
	return record, nil
}

func branchingAwait(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatal("decision boundary did not arrive")
	}
}

func cleanupBranchingWorker(t *testing.T, service *automations.Service, repository *branchingDecisionBarrier) {
	t.Helper()
	t.Cleanup(func() {
		service.StopAdmission()
		for _, gate := range []chan struct{}{repository.commit, repository.resume} {
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := service.Drain(ctx); err != nil {
			t.Error(err)
		}
	})
}

func branchingPublicHandler(service *automations.Service) http.Handler {
	handler, _ := newHTTPHandler(
		nil,
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
		mcpapi.New(mcpapi.Config{Name: mcpName, Version: mcpVersion}), nil, household.Settings{},
	)
	return handler
}

func branchingLocalRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// A6/A15: public history distinguishes committed selection from execution and
// preserves that evidence through interruption and real Core startup. A failed
// interruption leaves startup responsible for the durable running row, without replay.
//
//nolint:gocognit,gocyclo,cyclop // Each lifecycle case crosses the same two transaction barriers.
func TestBranchingDecisionEvidenceSurvivesShutdownAndStartup(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                                         string
		completedChild, ambiguous, interruptionFails bool
	}{
		{name: "committed before child"},
		{name: "completed child pending outer sibling", completedChild: true},
		{name: "ambiguous commit then startup recovery", ambiguous: true, interruptionFails: true},
		{name: "completed child then startup recovery", completedChild: true, ambiguous: true, interruptionFails: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "hearth.db")
			database := dbtest.OpenMigrated(t, path)
			repository := &branchingDecisionBarrier{
				Repository: automationssqlite.NewAutomationRepository(database, automations.Dependencies{}),
				step:       "route",
				before:     make(chan struct{}),
				commit:     make(chan struct{}),
				after:      make(chan struct{}),
				resume:     make(chan struct{}),
			}
			if test.ambiguous {
				repository.writeErr = errors.New("commit result lost")
			}
			if test.interruptionFails {
				repository.interruptErr = errors.New("interruption unavailable")
			}
			seam := &branchingCommandSeam{
				blockingAutomationDevices: newBlockingAutomationDevices(),
				commands:                  make(map[devices.CommandID]devices.CommandRecord),
			}
			fault := make(chan struct{}, 2)
			service := automations.NewService(
				repository,
				seam,
				automations.Dependencies{
					Logger: slog.New(branchingFaultSignal{Handler: slog.DiscardHandler, fault: fault}),
				},
			)
			cleanupBranchingWorker(t, service, repository)
			handler := branchingPublicHandler(service)
			if ready := branchingLocalRequest(t, handler, http.MethodGet, "/readyz", ""); ready.Code != http.StatusOK {
				t.Fatalf("initial readiness = %d", ready.Code)
			}
			const command = `{"kind":"command", "id":"child","entity_id":"ent_01920000-0000-7000-8000-000000000004","operation":"set","parameters":{"value":true}}`
			const condition = `{"id":"manual","kind":"not","child":{"id":"automatic","kind":"trigger","trigger_ids":["trigger"]}}`
			steps := fmt.Sprintf(
				`[{"id":"route","kind":"if","conditions":%s,"then":[%s],"else":[{"kind":"command", "id":"unselected","entity_id":"ent_01920000-0000-7000-8000-000000000004","operation":"set","parameters":{"value":false}}]}]`,
				condition,
				command,
			)
			if test.completedChild {
				steps = fmt.Sprintf(
					`[{"id":"first","kind":"if","conditions":%s,"then":[%s]},{"id":"route","kind":"if","conditions":%s,"then":[{"kind":"command", "id":"outer-sibling","entity_id":"ent_01920000-0000-7000-8000-000000000004","operation":"set","parameters":{"value":false}}]}]`,
					condition,
					command,
					condition,
				)
			}
			definition := fmt.Sprintf(
				`{"name":"Boundary lifecycle","enabled":true,"triggers":[{"id":"trigger","kind":"observation","entity_id":"ent_01920000-0000-7000-8000-000000000004","dispositions":["applied"]}],"steps":%s}`,
				steps,
			)
			created := branchingLocalRequest(t, handler, http.MethodPost, "/v1/automations", definition)
			if created.Code != http.StatusCreated {
				t.Fatalf("create = %d: %s", created.Code, created.Body.String())
			}
			var record struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(created.Body.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			started := branchingLocalRequest(t, handler, http.MethodPost, "/v1/automations/"+record.ID+"/runs", "")
			if started.Code != http.StatusAccepted {
				t.Fatalf("start = %d: %s", started.Code, started.Body.String())
			}
			historyPath := started.Header().Get("Location")
			branchingAwait(t, repository.before)
			before := readBranchingRun(t, branchingLocalRequest(t, handler, http.MethodGet, historyPath, "").Result())
			wantPrevious := 0
			if test.completedChild {
				wantPrevious = 1
			}
			if len(branchingDecisions(t, before)) != wantPrevious || seam.calls != wantPrevious {
				t.Fatalf("before commit: decisions %d, Commands %d", len(branchingDecisions(t, before)), seam.calls)
			}
			busy := branchingLocalRequest(t, handler, http.MethodPost, "/v1/automations/"+record.ID+"/runs", "")
			if busy.Code != http.StatusConflict || !strings.Contains(busy.Body.String(), "automation_busy") {
				t.Fatalf("concurrent invocation = %d: %s", busy.Code, busy.Body.String())
			}
			close(repository.commit)
			branchingAwait(t, repository.after)
			committed := readBranchingRun(
				t,
				branchingLocalRequest(t, handler, http.MethodGet, historyPath, "").Result(),
			)
			if len(branchingDecisions(t, committed)) != wantPrevious+1 || committed.Status != "running" ||
				seam.calls != wantPrevious {
				t.Fatalf("committed selection = %#v, Commands %d", committed, seam.calls)
			}
			catalog, err := devices.NewBuiltinTypeCatalog()
			if err != nil {
				t.Fatal(err)
			}
			deviceService := devices.NewService(
				testDeviceStores(devicessqlite.NewDeviceRepository(database, catalog), database),
				nil,
				catalog,
				devices.Dependencies{},
			)
			shutdown := &coreShutdown{
				runContext:        ctx,
				logger:            slog.New(slog.DiscardHandler),
				automationService: service,
				deviceService:     deviceService,
			}
			// Stop at the committed decision to inspect interruption evidence.
			// automations_drain_order_test.go owns blocked-worker shutdown ordering.
			if !test.ambiguous {
				service.StopAdmission()
			}
			close(repository.resume)
			if test.ambiguous {
				branchingAwait(t, fault)
				if !service.AdmissionOpen() {
					t.Fatal("Run fault closed independent admission after interruption failed")
				}
				ready := branchingLocalRequest(t, handler, http.MethodGet, "/readyz", "")
				if ready.Code != http.StatusOK {
					t.Fatalf("fault readiness = %d", ready.Code)
				}
				busy = branchingLocalRequest(t, handler, http.MethodPost, "/v1/automations/"+record.ID+"/runs", "")
				if busy.Code != http.StatusConflict || !strings.Contains(busy.Body.String(), "automation_busy") {
					t.Fatalf(
						"failed cleanup must retain affected Automation's busy evidence = %d: %s",
						busy.Code,
						busy.Body.String(),
					)
				}
			}
			if err = shutdown.run(); err != nil {
				t.Fatal(err)
			}
			if test.ambiguous && repository.interrupts.Load() != 1 {
				t.Fatalf("fault cleanup attempted %d times", repository.interrupts.Load())
			}
			after := readBranchingRun(t, branchingLocalRequest(t, handler, http.MethodGet, historyPath, "").Result())
			if seam.calls != wantPrevious || repository.writes.Load() != int64(wantPrevious+1) {
				t.Fatalf("Commands %d, decision writes %d", seam.calls, repository.writes.Load())
			}
			wantStatus := "interrupted"
			if test.interruptionFails {
				wantStatus = "running"
			}
			if after.Status != wantStatus {
				t.Fatalf("shutdown Run = %#v", after)
			}
			if test.interruptionFails {
				if after.FailureCode != nil {
					t.Fatalf("failed interruption retained failure code %q", *after.FailureCode)
				}
			} else if after.FailureCode == nil || *after.FailureCode != "core_stopping" {
				t.Fatalf("shutdown Run = %#v", after)
			}
			if test.completedChild {
				if commandID := requireVerifiedCommandLink(t, after.Executions, "child"); commandID == "" {
					t.Fatal("completed child command link is empty")
				}
			}
			for _, stepID := range []string{"child", "unselected", "outer-sibling"} {
				if test.completedChild && stepID == "child" {
					continue
				}
				if _, _, reached := branchingExecutionForStep(t, after.Executions, stepID); reached {
					t.Fatalf("branch drain reached pending Step %q: %#v", stepID, after.Executions)
				}
			}
			if service.AdmissionOpen() {
				t.Fatal("shutdown/fault left admission open")
			}
			if seam.reads.Load() != 0 {
				t.Fatal("Trigger-only branch read State")
			}
			if err = database.Close(); err != nil {
				t.Fatal(err)
			}
			server := startLifecycleNATSServer(t)
			address, stop, coreErrors := startDeviceFactsCore(ctx, t, server.ClientURL(), path)
			defer stopDeviceFactsCore(t, stop, coreErrors)
			waitForCoreHealthz(ctx, t, address, coreErrors)
			waitForCoreReady(ctx, t, address, coreErrors)
			recovered := readBranchingRun(t, sliceRequest(ctx, t, http.MethodGet, address, historyPath, nil))
			wantRecoveredCode := "core_stopping"
			if test.interruptionFails {
				wantRecoveredCode = "core_restarted"
			}
			if recovered.Status != "interrupted" ||
				recovered.FailureCode == nil || *recovered.FailureCode != wantRecoveredCode ||
				!reflect.DeepEqual(recovered.Executions, after.Executions) {
				t.Fatalf("startup Run = %#v", recovered)
			}
			var count int
			db, openErr := platformdb.Open(ctx, path)
			if openErr != nil {
				t.Fatal(openErr)
			}
			defer db.Close()
			if err = db.QueryRowContext(ctx, "SELECT count(*) FROM commands").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("startup dispatched %d Commands", count)
			}
		})
	}
}
