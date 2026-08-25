package nats

import (
	"context"
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

type registrarFunc func(context.Context, string, devices.Registration) (devices.Binding, error)

func (register registrarFunc) Register(ctx context.Context, adapterID string, registration devices.Registration) (devices.Binding, error) {
	return register(ctx, adapterID, registration)
}

func TestRegistrationHandlerMapsAcceptedAndRejectedResponses(t *testing.T) {
	wire := platformnats.Registration{
		BindingKey: "office-light",
		Device:     platformnats.DeviceDescriptor{Name: "Office light", Kind: "light"},
		Entities: []platformnats.EntityDescriptor{{
			Key: "power", ExternalID: "light.office", Name: "Power", Type: "hearth.power/v1",
			Support: []byte(`{"state":{},"operations":{"set":{}}}`),
		}},
	}
	handler := RegistrationHandler(registrarFunc(func(_ context.Context, adapterID string, registration devices.Registration) (devices.Binding, error) {
		if adapterID != "simulator" || registration.BindingKey != wire.BindingKey ||
			string(registration.Entities[0].Support) != string(wire.Entities[0].Support) {
			t.Fatalf("mapped Registration = %q %#v", adapterID, registration)
		}
		return devices.Binding{
			BindingKey: wire.BindingKey,
			DeviceID:   "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			Entities: []devices.EntityBinding{{
				Key: "power", EntityID: "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			}},
		}, nil
	}))
	response, err := handler(context.Background(), "simulator", wire)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != "accepted" || response.Binding == nil || response.Binding.Entities[0].EntityID == "" {
		t.Fatalf("accepted response = %#v", response)
	}

	rejected := RegistrationHandler(registrarFunc(func(context.Context, string, devices.Registration) (devices.Binding, error) {
		return devices.Binding{}, &devices.RegistrationRejectedError{
			Code: devices.RegistrationIdentityConflict, Message: "conflict",
		}
	}))
	response, err = rejected(context.Background(), "simulator", wire)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != "rejected" || response.Error == nil || response.Error.Code != "identity_conflict" {
		t.Fatalf("rejected response = %#v", response)
	}

	sentinel := errors.New("database unavailable")
	failed := RegistrationHandler(registrarFunc(func(context.Context, string, devices.Registration) (devices.Binding, error) {
		return devices.Binding{}, sentinel
	}))
	if _, err := failed(context.Background(), "simulator", wire); !errors.Is(err, sentinel) {
		t.Fatalf("infrastructure error = %v", err)
	}
}
