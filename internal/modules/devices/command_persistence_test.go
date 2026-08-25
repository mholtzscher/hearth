package devices

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCommandTransitionsAreMonotonic(t *testing.T) {
	service, _ := newDeviceTestService(t, productionServiceControls())
	runDeviceTestService(t, service, testDelivery(func(context.Context, string, CommandDispatch) (CommandAcceptance, error) {
		return CommandAcceptance{}, errors.New("unexpected delivery")
	}))
	binding, err := service.Register(context.Background(), "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := NewCommandID()
	correlationID, _ := NewCorrelationID()
	now := time.Now().UTC()
	command := commandRecord{
		id: id, entityID: binding.Entities[0].EntityID, adapterID: "simulator",
		operationName: OperationNameSet, parameters: CommandParameters(`{"value":true}`),
		correlationID: correlationID, status: commandStatusRequested,
		requestedAt: now, deadlineAt: now.Add(time.Minute),
	}
	if err := service.createCommand(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	acceptedAt := now.Add(time.Second)
	if err := service.markCommandAccepted(context.Background(), id, acceptedAt); err != nil {
		t.Fatal(err)
	}
	completion := commandCompletion{
		id: id, status: commandStatusRejected, completedAt: now.Add(2 * time.Second),
		failureCode: commandFailureUpstreamRejected,
	}
	if err := service.completeCommand(context.Background(), completion); err != nil {
		t.Fatal(err)
	}
	if err := service.completeCommand(context.Background(), completion); err != nil {
		t.Fatalf("idempotent completion: %v", err)
	}
	if err := service.markCommandAccepted(context.Background(), id, now.Add(3*time.Second)); !errors.Is(err, errCommandTerminal) {
		t.Fatalf("terminal acceptance error = %v", err)
	}
	stored, err := getCommand(context.Background(), service.database, id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.status != commandStatusRejected || stored.acceptedAt == nil || !stored.acceptedAt.Equal(acceptedAt) ||
		stored.failureCode == nil || *stored.failureCode != commandFailureUpstreamRejected {
		t.Fatalf("stored command = %#v", stored)
	}
}
