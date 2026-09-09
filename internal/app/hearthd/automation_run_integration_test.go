package hearthd //nolint:testpackage // Exercises the actual process lifecycle with embedded NATS and socket-backed HTTP.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/api"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

// A1/A9: real Run cancellation closes admission but keeps the NATS connection,
// Observation consumer and SQLite alive until the current linked outcome persists.
// Unlike the drain-helper test this protects the application's actual defer order.
//
//nolint:gocognit,gocyclo,cyclop // One process lifecycle keeps admission, barriers, and teardown assertions together.
func TestCoreAutomationShutdownDrainsLinkedObservation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	httpAddress := unusedLoopbackAddress(t)
	runContext, stopCore := context.WithCancel(ctx)
	defer stopCore()
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{HouseholdTimezone: "UTC", HTTPAddr: httpAddress,
			NATSURL: server.ClientURL(), SQLitePath: databasePath}, slog.New(slog.DiscardHandler))
	}()
	waitForCoreHealthz(ctx, t, httpAddress, runErrors)
	baseURL := "http://" + httpAddress
	pollReadyz(ctx, t, baseURL+"/readyz")

	session, err := adapter.Connect(ctx, adapter.Config{AdapterID: "simulator", SoftwareName: "automation-drain",
		SoftwareVersion: "1", NATSURL: server.ClientURL()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	support := contractpowerv1.Support{State: contractpowerv1.StateSupport{},
		Operations: contractpowerv1.OperationSupport{Set: contractpowerv1.SetSupport{}}}
	descriptor, err := sdkpowerv1.NewEntityDescriptor(adapter.EntityMetadata{
		Key: "power", ExternalID: "drain.power", Name: "Power"}, support)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := session.Register(ctx, adapter.Registration{
		BindingKey: "drain",
		Device:     adapter.DeviceDescriptor{Name: "Drain", Kind: "light"},
		Entities:   []adapter.EntityDescriptor{descriptor},
	})
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	var dispatches atomic.Int64
	handler, err := sdkpowerv1.NewCommandHandler(entityID, support, sdkpowerv1.Handlers{
		Set: func(commandContext context.Context, command sdkpowerv1.SetCommand, responder adapter.Responder) error {
			dispatches.Add(1)
			evidence, acceptErr := responder.Accept()
			if acceptErr != nil {
				return acceptErr
			}
			close(entered)
			select {
			case <-release:
			case <-commandContext.Done():
				return commandContext.Err()
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
	defer stopServing()
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- session.ServeCommands(serveContext, handler) }()
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		subscriptions, subscriptionErr := server.Subsz(&natsserver.SubszOptions{Subscriptions: true})
		if subscriptionErr != nil {
			return false, subscriptionErr
		}
		for _, subscription := range subscriptions.Subs {
			if strings.Contains(subscription.Subject, ".command.") {
				return true, nil
			}
		}
		return false, nil
	})
	step := fmt.Sprintf(`{"entity_id":%q,"operation_name":"set","parameters":{"value":true}}`, entityID)
	definition := `{"name":"Drain","triggers":[{"id":"daily","kind":"cron","expression":"0 19 * * *"}],"steps":[` +
		step + "," + step + "]}"
	created := automationProcessRequest[automationsapi.AutomationBody](ctx, t, http.MethodPost,
		baseURL+"/v1/automations", definition, "", http.StatusCreated)
	invocationURL := baseURL + "/v1/automations/" + string(created.ID) + "/runs"
	admitted := automationProcessRequest[automationsapi.AutomationRunBody](ctx, t, http.MethodPost,
		invocationURL, "", "drain", http.StatusAccepted)
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("Command never reached adapter")
	}
	observer, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = observer.Close() }()
	var committed int
	if err = observer.QueryRowContext(ctx, `SELECT count(*) FROM automation_run_steps s JOIN commands c
		ON c.id = s.reserved_command_id AND c.correlation_id = s.reserved_correlation_id
		WHERE s.run_id = ? AND s.status = 'running'`, admitted.ID).Scan(&committed); err != nil || committed != 1 {
		t.Fatalf("adapter received Command without committed owned intent: count=%d err=%v", committed, err)
	}
	stopCore()
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/readyz", nil)
		if requestErr != nil {
			return false, requestErr
		}
		request.Close = true
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			return false, requestErr
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return response.StatusCode == http.StatusServiceUnavailable, nil
	})
	automationProcessRequest[map[string]any](ctx, t, http.MethodPost,
		invocationURL, "", "during-shutdown", http.StatusServiceUnavailable)
	select {
	case runErr := <-runErrors:
		t.Fatalf("process exited before current outcome: %v", runErr)
	default:
	}
	release <- struct{}{}
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-ctx.Done():
		t.Fatal("process did not drain")
	}
	run, err := automations.NewSQLiteRepository(observer).GetAutomationRun(ctx, admitted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != automations.AutomationRunStatusInterrupted || run.FailureCode == nil ||
		*run.FailureCode != automations.AutomationFailureCoreStopping ||
		run.Steps[0].Status != automations.AutomationStepStatusSatisfied || run.Steps[0].Outcome == nil ||
		*run.Steps[0].Outcome != devices.OutcomeObserved ||
		run.Steps[1].Status != automations.AutomationStepStatusNotAttempted || dispatches.Load() != 1 {
		t.Fatalf("shutdown lost linked outcome or started later Step: %#v", run)
	}
	stopServing()
	select {
	case serveErr := <-serveErrors:
		if !errors.Is(serveErr, context.Canceled) {
			t.Fatal(serveErr)
		}
	case <-ctx.Done():
		t.Fatal("adapter did not stop")
	}
}

func automationProcessRequest[T any](ctx context.Context, t *testing.T, method, url, body, key string, status int) T {
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
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != status {
		t.Fatalf("%s %s status=%d want=%d", method, url, response.StatusCode, status)
	}
	var result T
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
