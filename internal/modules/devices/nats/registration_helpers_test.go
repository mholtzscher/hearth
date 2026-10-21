package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and fixtures.

import (
	"context"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type registrarFunc func(
	context.Context,
	string,
	adapters.RuntimeID,
	devices.Registration,
) (devices.Binding, error)

func (registrar registrarFunc) Register(
	ctx context.Context,
	adapterID string,
	runtimeID adapters.RuntimeID,
	registration devices.Registration,
) (devices.Binding, error) {
	return registrar(ctx, adapterID, runtimeID, registration)
}

func validRegistrationEnvelope() natswire.Envelope[registration] {
	return natswire.Envelope[registration]{
		ID:            "reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Schema:        contractsv1.RegistrationRequestSchemaID,
		EmittedAt:     "2026-08-20T12:34:56Z",
		CorrelationID: testCorrelationID,
		Data: registration{
			BindingKey: "office-light",
			Device:     deviceDescriptor{Name: "Office light", Kind: "light"},
			Entities: []entityDescriptor{{
				Key: "power", ExternalID: "light.office", Name: "Power", Type: "hearth.power/v1",
				Support: []byte(`{"state":{},"operations":{"set":{}}}`),
			}},
		},
	}
}
