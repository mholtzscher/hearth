package hearthd //nolint:testpackage // Tests application dependency teardown ordering with real persisted automation work.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

type automationDrainSender struct {
	dependencyContext context.Context
	entered           chan struct{}
	release           chan struct{}
}

func (sender automationDrainSender) Send(
	context.Context,
	string,
	devices.RuntimeID,
	devices.CommandRequest,
) (devices.CommandAcceptance, error) {
	close(sender.entered)
	<-sender.release
	// A premature dependency cancellation produces a persisted failure instead
	// of the required dispatched result, independently of the worker's context.
	if err := sender.dependencyContext.Err(); err != nil {
		return devices.CommandAcceptance{}, err
	}
	return devices.CommandAcceptance{Accepted: true}, nil
}

// A9: the app's shared normal/error-exit drain helper closes admission and joins
// Step-result persistence before canceling dependencies. A blocked Operation is
// not cut short by the HTTP timeout or process shutdown cancellation.
func TestAutomationDrainPrecedesDependencyCancellation(t *testing.T) {
	t.Parallel()
	database, err := platformdb.Open(t.Context(), filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = platformdb.Migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	records := devices.NewSQLiteRepository(database, catalog)
	dependencyContext, cancelDependencies := context.WithCancel(context.Background())
	defer cancelDependencies()
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	deviceService := devices.NewService(
		devices.SQLiteStores(records),
		automationDrainSender{dependencyContext: dependencyContext, entered: entered, release: release},
		catalog,
		devices.Dependencies{},
	)
	runtime := devices.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if err = deviceService.ClaimAdapterRuntime(
		t.Context(),
		devices.ClaimAdapterRuntimeParams{
			AdapterID:       "simulator",
			RuntimeID:       runtime,
			SoftwareName:    "drain-test",
			SoftwareVersion: "1",
		},
	); err != nil {
		t.Fatal(err)
	}
	binding, err := deviceService.Register(t.Context(), "simulator", runtime, devices.Registration{
		BindingKey: "drain-test",
		Device:     devices.DeviceDescriptor{Name: "Drain light", Kind: devices.DeviceKindLight},
		Entities: []devices.EntityDescriptor{
			{
				Key:        "effect",
				ExternalID: "effect",
				Name:       "Effect",
				TypeID:     devices.EntityTypeEnumactionV1,
				Support:    devices.EntitySupport(`{"state":{},"operations":{"trigger":{"values":["blink"]}}}`),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := automations.NewSQLiteRepository(database)
	codec, err := automations.NewAutomationDefinitionCodec()
	if err != nil {
		t.Fatal(err)
	}
	service := automations.NewService(repo, deviceService, records, codec, time.UTC, nil)
	step := automations.AutomationStep{
		EntityID:      binding.Entities[0].EntityID,
		OperationName: "trigger",
		Parameters:    devices.CommandParameters(`{"name":"blink"}`),
	}
	automation, err := service.CreateAutomation(
		t.Context(),
		automations.AutomationDefinition{
			Name: "Drain",
			Triggers: []automations.AutomationTrigger{
				{ID: "daily", Kind: automations.AutomationTriggerKindCron, Expression: "0 19 * * *"},
			},
			Steps: []automations.AutomationStep{step, step},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := service.StartManualRun(
		t.Context(),
		automations.AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: "drain"},
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first command did not enter sender")
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		drainAutomationExecution(service, func() {
			run, readErr := repo.GetAutomationRun(context.Background(), admission.Run.ID)
			if readErr != nil {
				t.Error(readErr)
			} else if run.Status != automations.AutomationRunStatusInterrupted || run.Steps[0].Status != automations.AutomationStepStatusDispatched || run.Steps[1].Status != automations.AutomationStepStatusNotAttempted {
				t.Errorf("dependency teardown preceded result persistence: %#v", run)
			}
			cancelDependencies()
		})
	}()
	// The accessor is synchronized by the same gate that drain closes. The
	// condition cannot become true merely because the sender happened to run.
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) { return !service.AutomationExecutionReady(), nil })
	if dependencyContext.Err() != nil {
		t.Fatal("dependencies canceled before current command returned")
	}
	select {
	case <-stopped:
		t.Fatal("worker drain returned while command blocked")
	default:
	}
	release <- struct{}{}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("worker drain did not finish")
	}
	if dependencyContext.Err() == nil {
		t.Fatal("drain leaked dependency lifecycle")
	}
}
