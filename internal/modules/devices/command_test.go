package devices

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExecuteCommandCommitsBeforeDeliveryAndHandlesAcceptanceRace(t *testing.T) {
	service, database := newDeviceTestService(t, productionServiceControls())
	var delivery *inMemoryCommandDelivery
	delivery = testDelivery(func(ctx context.Context, adapterID string, dispatch CommandDispatch) (CommandAcceptance, error) {
		var status string
		if err := database.QueryRowContext(ctx, "SELECT status FROM commands WHERE id = ?", dispatch.ID).Scan(&status); err != nil {
			return CommandAcceptance{}, err
		}
		if status != "requested" {
			return CommandAcceptance{}, errors.New("command was not committed before delivery")
		}
		observation := newObservation(t, dispatch.EntityID, `true`, time.Now().UTC())
		observation.RefreshForCommand = &dispatch.ID
		_, err := service.ReceiveObservation(ctx, ReceivedObservation{
			AdapterID: adapterID, Observation: observation, ObservedAt: time.Now().UTC(),
		})
		return CommandAcceptance{Accepted: true}, err
	})
	runDeviceTestService(t, service, delivery)
	binding, err := service.Register(context.Background(), "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ExecuteCommand(
		context.Background(), binding.Entities[0].EntityID, OperationNameSet, CommandParameters(`{"value":true}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.CommandID == "" || result.ObservationID == "" || string(result.Value) != "true" {
		t.Fatalf("result = %#v", result)
	}
	var status string
	var acceptedAt, observationID *string
	if err := database.QueryRow(
		"SELECT status, accepted_at, outcome_observation_id FROM commands WHERE id = ?", result.CommandID,
	).Scan(&status, &acceptedAt, &observationID); err != nil {
		t.Fatal(err)
	}
	if status != "satisfied" || acceptedAt == nil || observationID == nil || *observationID != string(result.ObservationID) {
		t.Fatalf("stored command = status %q, accepted %v, observation %v", status, acceptedAt, observationID)
	}
}

func TestExecuteCommandFailureMatrixIsDurable(t *testing.T) {
	tests := []struct {
		name       string
		acceptance CommandAcceptance
		deliverErr error
		deadline   bool
		wantErr    error
		wantStatus string
		wantCode   string
	}{
		{"adapter unavailable", CommandAcceptance{}, ErrAdapterUnavailable, false, ErrAdapterUnavailable, "adapter_unavailable", "adapter_unavailable"},
		{"upstream rejected", CommandAcceptance{Accepted: false}, nil, false, ErrUpstreamRejected, "rejected", "upstream_rejected"},
		{"internal", CommandAcceptance{}, errors.New("invalid response"), false, nil, "internal_failure", "internal_error"},
		{"outcome timeout", CommandAcceptance{Accepted: true}, nil, true, ErrOutcomeTimeout, "outcome_timeout", "outcome_timeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controls := productionServiceControls()
			if test.deadline {
				controls.withDeadline = func(parent context.Context, _ time.Time) (context.Context, context.CancelFunc) {
					ctx, cancel := context.WithCancelCause(parent)
					cancel(context.DeadlineExceeded)
					return ctx, func() {}
				}
			}
			service, database := newDeviceTestService(t, controls)
			delivery := testDelivery(func(context.Context, string, CommandDispatch) (CommandAcceptance, error) {
				return test.acceptance, test.deliverErr
			})
			runDeviceTestService(t, service, delivery)
			binding, err := service.Register(context.Background(), "simulator", validDomainRegistration())
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.ExecuteCommand(
				context.Background(), binding.Entities[0].EntityID, OperationNameSet, CommandParameters(`{"value":true}`),
			)
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr == nil && err == nil {
				t.Fatal("internal delivery error was lost")
			}
			var execution *CommandExecutionError
			if !errors.As(err, &execution) {
				t.Fatalf("error = %v, want CommandExecutionError", err)
			}
			var status string
			var code *string
			if err := database.QueryRow("SELECT status, failure_code FROM commands WHERE id = ?", execution.CommandID).Scan(&status, &code); err != nil {
				t.Fatal(err)
			}
			if status != test.wantStatus || code == nil || *code != test.wantCode {
				t.Fatalf("stored status/code = %q/%v", status, code)
			}
		})
	}
}

func TestExecuteCommandContinuesAfterCallerCancellation(t *testing.T) {
	service, database := newDeviceTestService(t, productionServiceControls())
	releaseDelivery := make(chan struct{})
	delivery := testDelivery(func(context.Context, string, CommandDispatch) (CommandAcceptance, error) {
		<-releaseDelivery
		return CommandAcceptance{Accepted: true}, nil
	})
	runContext, stopRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- service.Run(runContext, delivery) }()
	binding, err := service.Register(context.Background(), "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	caller, cancelCaller := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() {
		_, err := service.ExecuteCommand(caller, binding.Entities[0].EntityID, OperationNameSet, CommandParameters(`{"value":true}`))
		returned <- err
	}()
	call := <-delivery.calls
	cancelCaller()
	if err := <-returned; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller error = %v", err)
	}
	close(releaseDelivery)
	observation := newObservation(t, call.Dispatch.EntityID, `true`, time.Now().UTC())
	observation.RefreshForCommand = &call.Dispatch.ID
	if _, err := service.ReceiveObservation(context.Background(), ReceivedObservation{
		AdapterID: call.AdapterID, Observation: observation, ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	stopRun()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	var status string
	if err := database.QueryRow("SELECT status FROM commands WHERE id = ?", call.Dispatch.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "satisfied" {
		t.Fatalf("status = %q", status)
	}
}
