package powerv1_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
	"github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

func TestFacadeSupportsTypedConsumerFlow(t *testing.T) {
	support := powerv1.Support{}
	descriptor, err := powerv1.NewEntityDescriptor(adapter.EntityMetadata{
		Key: "power", ExternalID: "light.office", Name: "Power",
	}, support)
	if err != nil {
		t.Fatal(err)
	}
	registration := adapter.Registration{
		BindingKey: "office-light",
		Device:     adapter.DeviceDescriptor{Name: "Office light", Kind: "light"},
		Entities:   []adapter.EntityDescriptor{descriptor},
	}
	if registration.Entities[0].Type != "hearth.power/v1" || string(registration.Entities[0].Support) != `{"state":{},"operations":{"set":{}}}` {
		t.Fatalf("typed registration = %#v", registration)
	}

	handler, err := powerv1.NewCommandHandler("ent_power", support, powerv1.Handlers{
		Set: func(_ context.Context, command powerv1.SetCommand, _ adapter.Responder) error {
			if !command.Parameters.Value {
				return errors.New("expected enabled power")
			}
			return nil
		},
	})
	if err != nil || handler == nil {
		t.Fatalf("typed command handler = %v, %v", handler, err)
	}

	sourceTime := time.Date(2026, 8, 22, 11, 59, 0, 0, time.FixedZone("source", -5*60*60))
	observation, err := powerv1.NewObservation(powerv1.ObservationInput{
		EntityID: "ent_power", State: true,
		AdapterReceivedAt: time.Date(2026, 8, 22, 12, 0, 0, 0, time.FixedZone("adapter", -5*60*60)),
		SourceUpdatedAt:   &sourceTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(observation.Value) != "true" || observation.AdapterReceivedAt != "2026-08-22T17:00:00Z" ||
		observation.SourceUpdatedAt == nil || *observation.SourceUpdatedAt != "2026-08-22T16:59:00Z" {
		t.Fatalf("typed observation = %#v", observation)
	}
}

func TestFacadeReportsLocalValidationErrors(t *testing.T) {
	_, err := powerv1.NewCommandHandler("ent_power", powerv1.Support{}, powerv1.Handlers{})
	var validation *adapter.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("missing handler error = %v", err)
	}
	_, err = powerv1.NewObservation(powerv1.ObservationInput{EntityID: "ent_power", State: true})
	if !errors.As(err, &validation) {
		t.Fatalf("missing timestamp error = %v", err)
	}
}
