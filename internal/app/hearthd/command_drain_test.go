package hearthd //nolint:testpackage // Tests joint automation/direct drain ordering with real persisted work.

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

type jointDrainFixture struct {
	records            *devices.SQLiteRepository
	repo               *automations.SQLiteRepository
	deviceService      *devices.Service
	automationService  *automations.Service
	runtime            devices.RuntimeID
	powerEntity        devices.EntityID
	automationID       automations.AutomationID
	entered            chan devices.CommandRequest
	release            chan struct{}
	dependencyContext  context.Context
	cancelDependencies context.CancelFunc
}

type jointDirectOutcome struct {
	result devices.CommandResult
	err    error
}

// A direct Command admitted while an automation worker is blocked must drain
// alongside it: both admissions close before either wait, the shared dependency
// lifecycle stays alive until both workers finish, and observation persistence
// still satisfies the current direct Command during the drain. The five-second
// HTTP shutdown timeout never proves this; the two waits do.
func TestDirectCommandDrainsWithBlockedAutomationBeforeDependencyCancel(t *testing.T) {
	t.Parallel()
	fixture := setupJointDrain(t)
	admission, directRequest, directDone := fixture.admitBlockedPair(t)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		drainExecution(fixture.deviceService, fixture.automationService, fixture.cancelDependencies)
	}()
	fixture.requireDrainStarted(t, stopped)
	fixture.requireNewWorkRejected(t)
	close(fixture.release)
	fixture.satisfyDirectCommand(t, directRequest, directDone)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not finish after commands drained")
	}
	if fixture.dependencyContext.Err() == nil {
		t.Fatal("drain leaked dependency lifecycle")
	}
	fixture.requireAutomationInterrupted(t, admission.Run.ID)
}

func setupJointDrain(t *testing.T) *jointDrainFixture {
	t.Helper()
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
	t.Cleanup(cancelDependencies)
	entered := make(chan devices.CommandRequest, 2)
	release := make(chan struct{})
	deviceService := devices.NewService(
		devices.SQLiteStores(records),
		blockingJointSender(dependencyContext, entered, release),
		catalog,
		devices.Dependencies{},
	)
	runtime := devices.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if err = deviceService.ClaimAdapterRuntime(
		t.Context(),
		devices.ClaimAdapterRuntimeParams{
			AdapterID:       "simulator",
			RuntimeID:       runtime,
			SoftwareName:    "joint-drain-test",
			SoftwareVersion: "1",
		},
	); err != nil {
		t.Fatal(err)
	}
	binding, err := deviceService.Register(t.Context(), "simulator", runtime, devices.Registration{
		BindingKey: "joint-drain-test",
		Device:     devices.DeviceDescriptor{Name: "Joint drain light", Kind: devices.DeviceKindLight},
		Entities: []devices.EntityDescriptor{
			{
				Key:        "power",
				ExternalID: "power",
				Name:       "Power",
				TypeID:     devices.EntityTypePowerV1,
				Support:    devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
			},
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
	automationService := automations.NewService(
		repo,
		deviceService,
		records,
		codec,
		time.UTC,
		nil,
	)
	effectStep := automations.AutomationStep{
		EntityID:      binding.Entities[1].EntityID,
		OperationName: "trigger",
		Parameters:    devices.CommandParameters(`{"name":"blink"}`),
	}
	automation, err := automationService.CreateAutomation(
		t.Context(),
		automations.AutomationDefinition{
			Name: "Joint drain",
			Triggers: []automations.AutomationTrigger{
				{ID: "daily", Kind: automations.AutomationTriggerKindCron, Expression: "0 19 * * *"},
			},
			Steps: []automations.AutomationStep{effectStep, effectStep},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return &jointDrainFixture{
		records:            records,
		repo:               repo,
		deviceService:      deviceService,
		automationService:  automationService,
		runtime:            runtime,
		powerEntity:        binding.Entities[0].EntityID,
		automationID:       automation.ID,
		entered:            entered,
		release:            release,
		dependencyContext:  dependencyContext,
		cancelDependencies: cancelDependencies,
	}
}

// admitBlockedPair starts an automation Run and a direct Command that both
// block inside the sender, returning the admitted identities.
func (fixture *jointDrainFixture) admitBlockedPair(
	t *testing.T,
) (automations.AutomationAdmission, devices.CommandRequest, chan jointDirectOutcome) {
	t.Helper()
	admission, err := fixture.automationService.StartManualRun(
		t.Context(),
		automations.AutomationManualRequest{AutomationID: fixture.automationID, IdempotencyKey: "joint-drain"},
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-fixture.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("automation command did not enter sender")
	}
	directDone := make(chan jointDirectOutcome, 1)
	go func() {
		result, executeErr := fixture.deviceService.ExecuteCommand(context.Background(), devices.CommandInput{
			EntityID:      fixture.powerEntity,
			OperationName: "set",
			Parameters:    devices.CommandParameters(`{"value":true}`),
		})
		directDone <- jointDirectOutcome{result: result, err: executeErr}
	}()
	select {
	case request := <-fixture.entered:
		return admission, request, directDone
	case <-time.After(5 * time.Second):
		t.Fatal("direct command did not enter sender while automation blocked")
		return automations.AutomationAdmission{}, devices.CommandRequest{}, nil
	}
}

// requireDrainStarted asserts both admission gates closed before either wait
// can return, while shared dependencies stay alive and the drain stays blocked.
func (fixture *jointDrainFixture) requireDrainStarted(t *testing.T, stopped chan struct{}) {
	t.Helper()
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		return !fixture.automationService.AutomationExecutionReady() &&
			!fixture.deviceService.CommandAdmissionOpen(), nil
	})
	if fixture.dependencyContext.Err() != nil {
		t.Fatal("dependencies canceled before admitted commands drained")
	}
	select {
	case <-stopped:
		t.Fatal("drain returned while commands blocked")
	default:
	}
}

// requireNewWorkRejected asserts both gates reject new work while admitted
// work drains.
func (fixture *jointDrainFixture) requireNewWorkRejected(t *testing.T) {
	t.Helper()
	if _, err := fixture.deviceService.ExecuteCommand(context.Background(), devices.CommandInput{
		EntityID:      fixture.powerEntity,
		OperationName: "set",
		Parameters:    devices.CommandParameters(`{"value":true}`),
	}); !errors.Is(err, devices.ErrCommandUnavailable) {
		t.Fatalf("direct error after closure = %v, want %v", err, devices.ErrCommandUnavailable)
	}
	if _, err := fixture.automationService.StartManualRun(
		context.Background(),
		automations.AutomationManualRequest{AutomationID: fixture.automationID, IdempotencyKey: "joint-drain-late"},
	); !errors.Is(err, automations.ErrAutomationUnavailable) {
		t.Fatalf("manual run error after closure = %v, want %v", err, automations.ErrAutomationUnavailable)
	}
}

// satisfyDirectCommand projects the observation that completes the admitted
// direct Command through the dependencies retained by the drain.
func (fixture *jointDrainFixture) satisfyDirectCommand(
	t *testing.T,
	directRequest devices.CommandRequest,
	directDone chan jointDirectOutcome,
) {
	t.Helper()
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		stored, readErr := fixture.records.GetCommand(context.Background(), directRequest.ID)
		if readErr != nil {
			return false, readErr
		}
		return stored.Status == devices.CommandStatusAccepted, nil
	})
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.deviceService.ProjectObservation(
		context.Background(),
		"simulator",
		fixture.runtime,
		devices.Observation{
			ID: observationID, EntityID: fixture.powerEntity, Value: devices.Value(`true`),
			AdapterReceivedAt: time.Now().UTC(), RefreshForCommand: &directRequest.ID,
		},
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("observation during direct drain = %v", err)
	}
	select {
	case outcome := <-directDone:
		if outcome.err != nil || outcome.result.Outcome != devices.OutcomeObserved {
			t.Fatalf("direct outcome = %#v, %v", outcome.result, outcome.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("direct command did not complete after observation")
	}
}

func (fixture *jointDrainFixture) requireAutomationInterrupted(t *testing.T, runID automations.AutomationRunID) {
	t.Helper()
	run, err := fixture.repo.GetAutomationRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != automations.AutomationRunStatusInterrupted ||
		run.Steps[0].Status != automations.AutomationStepStatusDispatched ||
		run.Steps[1].Status != automations.AutomationStepStatusNotAttempted {
		t.Fatalf("automation did not drain before interruption: %#v", run)
	}
}

// Readiness degrades when either admission gate closes, so the load balancer
// stops sending work the drain would reject.
func TestReadyzReportsClosedAdmissionGates(t *testing.T) {
	t.Parallel()
	stub := &stubDevices{}
	automationsStub := &stubHTTPAutomations{}
	handler, _ := NewHTTPHandler(stub, automationsStub, testHTTPAutomationCodec(t), &testReadiness{}, stub)
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusOK {
		t.Fatalf("ready status = %d, body = %s", response.Code, response.Body.String())
	}
	stub.admissionClosed = true
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("command-draining status = %d, body = %s", response.Code, response.Body.String())
	}
	stub.admissionClosed = false
	automationsStub.unavailable = true
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("automation-draining status = %d, body = %s", response.Code, response.Body.String())
	}
}

func blockingJointSender(
	dependencyContext context.Context,
	entered chan devices.CommandRequest,
	release chan struct{},
) devices.CommandSender {
	return jointBlockingSender{
		dependencyContext: dependencyContext,
		entered:           entered,
		release:           release,
	}
}

type jointBlockingSender struct {
	dependencyContext context.Context
	entered           chan devices.CommandRequest
	release           chan struct{}
}

func (sender jointBlockingSender) Send(
	_ context.Context,
	_ string,
	_ devices.RuntimeID,
	request devices.CommandRequest,
) (devices.CommandAcceptance, error) {
	select {
	case sender.entered <- request:
	case <-sender.release:
	}
	select {
	case <-sender.release:
	case <-time.After(5 * time.Second):
		return devices.CommandAcceptance{}, errors.New("sender release timed out")
	}
	if err := sender.dependencyContext.Err(); err != nil {
		return devices.CommandAcceptance{}, err
	}
	return devices.CommandAcceptance{Accepted: true}, nil
}
