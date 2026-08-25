package devices

import (
	"context"
	"errors"
	"testing"
)

func TestNewInterruptsCommandsLeftByGracefulStop(t *testing.T) {
	service, database := newDeviceTestService(t, productionServiceControls())
	delivery := testDelivery(func(ctx context.Context, _ string, _ CommandDispatch) (CommandAcceptance, error) {
		<-ctx.Done()
		return CommandAcceptance{}, ctx.Err()
	})
	runContext, stopRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- service.Run(runContext, delivery) }()
	binding, err := service.Register(context.Background(), "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	executeDone := make(chan error, 1)
	go func() {
		_, err := service.ExecuteCommand(
			context.Background(), binding.Entities[0].EntityID,
			OperationNameSet, CommandParameters(`{"value":true}`),
		)
		executeDone <- err
	}()
	call := <-delivery.calls
	stopRun()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if err := <-executeDone; !errors.Is(err, ErrServiceStopped) {
		t.Fatalf("ExecuteCommand error = %v", err)
	}
	stored, err := getCommand(context.Background(), database, call.Dispatch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.status != commandStatusRequested {
		t.Fatalf("graceful stop status = %q", stored.status)
	}
	if _, err := newService(context.Background(), database, nil, productionServiceControls()); err != nil {
		t.Fatal(err)
	}
	stored, err = getCommand(context.Background(), database, call.Dispatch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.status != commandStatusInterrupted || stored.failureCode == nil || *stored.failureCode != commandFailureCoreRestarted {
		t.Fatalf("recovered command = %#v", stored)
	}
}
