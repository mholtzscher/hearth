package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and fixtures.

import (
	"context"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type entityEnablementSetterFunc func(
	context.Context,
	string,
	adapters.RuntimeID,
	devices.EntityID,
	bool,
) (bool, error)

func (setter entityEnablementSetterFunc) SetOwnedEntityEnabled(
	ctx context.Context,
	adapterID string,
	runtimeID adapters.RuntimeID,
	entityID devices.EntityID,
	enabled bool,
) (bool, error) {
	return setter(ctx, adapterID, runtimeID, entityID, enabled)
}

func validEntityEnablementEnvelope(enabled bool) natswire.Envelope[entityEnablementRequest] {
	return natswire.Envelope[entityEnablementRequest]{
		ID:     "ena_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Schema: contractsv1.EntityEnablementRequestSchemaID, EmittedAt: "2026-08-26T12:00:00Z",
		CorrelationID: testCorrelationID,
		Data:          entityEnablementRequest{EntityID: testEntityID, Enabled: enabled},
	}
}
