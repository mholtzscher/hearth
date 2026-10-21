package nats //nolint:testpackage // Tests the real durable callback and acknowledgement boundary.

import (
	"context"

	natsgo "github.com/nats-io/nats.go"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type healthApplierFunc func(context.Context, devices.AdapterHealthUpdate) (devices.HealthProjectionResult, error)

func (f healthApplierFunc) ApplyAdapterHealth(
	ctx context.Context,
	update devices.AdapterHealthUpdate,
) (devices.HealthProjectionResult, error) {
	return f(ctx, update)
}

func healthConsumerMessage(idSuffix string) *natsgo.Msg {
	id := "ahu_01890f47-7a6b-7c4d-8e9f-0123456789" + idSuffix
	message := natsgo.NewMsg("hearth.v1.core.adapter-health.zigbee-main")
	message.Header.Set(natsgo.MsgIdHdr, id)
	message.Data = []byte(
		`{"id":"` + id + `","schema":"urn:hearth:schema:adapter-health-update:v1","emitted_at":"2026-10-18T12:00:00Z","correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab","data":{"adapter_id":"zigbee-main","runtime_id":null,"revision":9223372036854775807,"status":"unknown","source":"core","reason_code":"hearth.starting","changed_at":"2026-10-18T12:00:00Z","evidence_at":"2026-10-18T12:00:00Z"}}`,
	)
	return message
}
