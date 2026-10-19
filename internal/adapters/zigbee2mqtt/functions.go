package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	refreshUnavailableReason = "adapter_unavailable"
	refreshTimeoutMS         = 10000
)

// Functions exposes the immutable vendor contract for this runtime.
func (z2m *Adapter) Functions() []adapter.Function {
	return []adapter.Function{{
		Descriptor: adapter.FunctionDescriptor{
			Key:         "refresh_entity",
			Description: "Request an Entity's readable upstream properties once. Success means MQTT PUBACK, not fresh Hearth State.",
			InputSchema: json.RawMessage(
				`{"type":"object","additionalProperties":false,"required":["entity_id"],"properties":{"entity_id":{"type":"string","minLength":40,"maxLength":40}}}`,
			),
			OutputSchema: json.RawMessage(
				`{"type":"object","additionalProperties":false,"required":["entity_id","request_status"],"properties":{"entity_id":{"type":"string","minLength":40,"maxLength":40},"request_status":{"const":"published"}}}`,
			),
			TimeoutMS: refreshTimeoutMS,
		},
		Handler: z2m.refreshEntity,
	}}
}

func (z2m *Adapter) refreshEntity(ctx context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
	var arguments struct {
		EntityID string `json:"entity_id"`
	}
	if json.Unmarshal(request.Arguments, &arguments) != nil || !canonicalRefreshEntityID(arguments.EntityID) {
		return adapter.FunctionNotExecuted{ReasonCode: "invalid_entity_id"}
	}
	result := make(chan adapter.FunctionResult, 1)
	event := refreshSubmitted{ctx: ctx, deadline: request.Deadline, entityID: arguments.EntityID, result: result}
	select {
	case <-ctx.Done():
		return refreshNotExecuted(ctx, request.Deadline)
	case <-z2m.runtimeDone:
		return refreshNotExecuted(ctx, request.Deadline)
	case z2m.runtimeEvents <- event:
	}
	select {
	case outcome := <-result:
		return outcome
	case <-z2m.runtimeDone:
		// Shutdown explicitly resolves admitted attempts before closing runtimeDone.
		select {
		case outcome := <-result:
			return outcome
		default:
			return refreshNotExecuted(ctx, request.Deadline)
		}
	}
}

func canonicalRefreshEntityID(value string) bool {
	if len(value) != 40 || !strings.HasPrefix(value, "ent_") {
		return false
	}
	id, err := uuid.Parse(value[4:])
	return err == nil && id.Version() == 7 && id.Variant() == uuid.RFC4122 && id.String() == value[4:]
}
