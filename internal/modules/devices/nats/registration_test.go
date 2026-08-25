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
	accepted := RegistrationHandler(registrarFunc(func(_ context.Context, adapterID string, registration devices.Registration) (devices.Binding, error) {
		if adapterID != "simulator" || registration.BindingKey != "light" || string(registration.Entities[0].Support) != `{"state":{}}` {
			t.Fatalf("registration = %q %#v", adapterID, registration)
		}
		return devices.Binding{
			BindingKey: registration.BindingKey,
			DeviceID:   "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			Entities: []devices.EntityBinding{{
				Key: registration.Entities[0].Key, EntityID: "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			}},
		}, nil
	}))
	response, err := accepted(context.Background(), "simulator", platformnats.Registration{
		BindingKey: "light",
		Device:     platformnats.DeviceDescriptor{Name: "Light", Kind: "light"},
		Entities: []platformnats.EntityDescriptor{{
			Key: "power", ExternalID: "light.one", Name: "Power", Type: string(devices.EntityTypePowerV1),
			Support: []byte(`{"state":{}}`),
		}},
	})
	if err != nil || response.Status != "accepted" || response.Binding == nil || response.Binding.DeviceID == "" {
		t.Fatalf("response = %#v, error = %v", response, err)
	}

	rejected := RegistrationHandler(registrarFunc(func(context.Context, string, devices.Registration) (devices.Binding, error) {
		return devices.Binding{}, &devices.RegistrationRejectedError{
			Code: devices.RegistrationIdentityConflict, Message: "conflict",
		}
	}))
	response, err = rejected(context.Background(), "simulator", platformnats.Registration{})
	if err != nil || response.Status != "rejected" || response.Error == nil || response.Error.Code != "identity_conflict" {
		t.Fatalf("response = %#v, error = %v", response, err)
	}

	sentinel := errors.New("SQLite busy")
	failed := RegistrationHandler(registrarFunc(func(context.Context, string, devices.Registration) (devices.Binding, error) {
		return devices.Binding{}, sentinel
	}))
	if _, err := failed(context.Background(), "simulator", platformnats.Registration{}); !errors.Is(err, sentinel) {
		t.Fatalf("error = %v", err)
	}
}
