package zigbee2mqtt

import (
	"context"
	"errors"
	"log/slog"
)

// adapterComponent is the only component value emitted from this package. It
// is attached once in newAdapter so every record carries a bounded subsystem
// label without repeating app or pid, which belong to the root application
// logger.
const adapterComponent = "zigbee2mqtt"

// eventKey is the shared slog attribute key carrying the stable dotted event
// name on every record emitted from this package. Event values stay whole
// literals at each emission site so searching a value finds its site.
const eventKey = "event"

// zigbee2MQTTErrorCode maps a retry-loop failure to a fixed diagnostic
// classification. The code never carries MQTT URLs, topics, payloads, or
// broker free-form text.
func zigbee2MQTTErrorCode(err error) string {
	if _, ok := errors.AsType[*sessionOperationError](err); ok {
		return "session_operation_failed"
	}
	return "upstream_connection_failed"
}

// logReconcileCompleted summarizes successfully activated routes. Counts come
// from already available reconciliation results; a supported-device count of
// zero is explicit in the summary.
func (z2m *Adapter) logReconcileCompleted(
	ctx context.Context,
	snapshot routeSnapshot,
	isolated int,
) {
	entityCount := 0
	for _, device := range snapshot.devices {
		entityCount += len(device.entities)
	}
	z2m.logger.InfoContext(
		ctx,
		"Zigbee2MQTT reconciliation completed",
		slog.String(eventKey, "adapter.reconcile_completed"),
		slog.Int("device_count", len(snapshot.devices)),
		slog.Int("entity_count", entityCount),
		slog.Int("isolated_device_count", isolated),
	)
}

// logIsolatedDevice records one device left out of the activated routes. Only
// the fixed reason classification is kept; IEEE addresses, friendly names, and
// model identifiers are vendor identity and never logged.
func (z2m *Adapter) logIsolatedDevice(ctx context.Context, reasonCode string) {
	z2m.logger.WarnContext(
		ctx,
		"isolated Zigbee2MQTT device",
		slog.String(eventKey, "adapter.device_isolated"),
		slog.String("reason_code", reasonCode),
	)
}
