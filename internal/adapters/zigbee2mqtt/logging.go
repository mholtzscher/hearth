package zigbee2mqtt

import (
	"context"
	"errors"
	"time"
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

// retryEpisode tracks one consecutive run of retryable connection failures so
// the first failure (or a new failure class) warns once while repeats stay at
// Debug. It is owned by a single runConnections goroutine; no locking is
// needed.
type retryEpisode struct {
	attempts  int
	lastCode  string
	startedAt time.Time
}

func (episode *retryEpisode) active() bool { return episode.attempts > 0 }

// connectionProgress carries per-connection logging state through the
// connection state machine. The retry episode survives across connections so
// recovery can report attempts and elapsed time; upstreamReady resets for
// every generation and on every unhealthy report so adapter readiness is
// emitted once per recovery when health conditions hold again, including on
// the same connection.
type connectionProgress struct {
	episode       *retryEpisode
	upstreamReady bool
}

// zigbee2MQTTErrorCode maps a retry-loop failure to a fixed diagnostic
// classification. The code never carries MQTT URLs, topics, payloads, or
// broker free-form text.
func zigbee2MQTTErrorCode(err error) string {
	if _, ok := errors.AsType[*sessionOperationError](err); ok {
		return "session_operation_failed"
	}
	return "upstream_connection_failed"
}

// logConnectionRetry records one failed reconnect attempt. The first attempt
// of an episode and any attempt with a distinct failure class log at Warn;
// identical repeats log at Debug.
func (z2m *Adapter) logConnectionRetry(
	ctx context.Context,
	episode *retryEpisode,
	err error,
	wait time.Duration,
) {
	code := zigbee2MQTTErrorCode(err)
	episode.attempts++
	if episode.attempts == 1 {
		episode.startedAt = time.Now()
	}
	warn := episode.attempts == 1 || code != episode.lastCode
	episode.lastCode = code
	args := []any{
		eventKey, "dependency.retrying",
		"dependency", adapterComponent,
		"error_code", code,
		"attempt", episode.attempts,
		"retry_in_ms", max(wait.Milliseconds(), 0),
	}
	if warn {
		z2m.logger.WarnContext(ctx, "Zigbee2MQTT connection retrying", args...)
		return
	}
	z2m.logger.DebugContext(ctx, "Zigbee2MQTT connection retrying", args...)
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
		eventKey, "adapter.reconcile_completed",
		"device_count", len(snapshot.devices),
		"entity_count", entityCount,
		"isolated_device_count", isolated,
	)
}

// logUpstreamReady records that bridge, configuration, and inventory health
// conditions hold after route activation. It closes any open retry episode
// with one recovery record and then emits adapter readiness exactly once for
// the connection; transport reconnect alone never reports readiness.
func (z2m *Adapter) logUpstreamReady(ctx context.Context, progress *connectionProgress) {
	if progress.episode.active() {
		z2m.logger.InfoContext(
			ctx,
			"Zigbee2MQTT connection recovered",
			eventKey, "dependency.recovered",
			"dependency", adapterComponent,
			"attempts", progress.episode.attempts,
			"duration_ms", max(time.Since(progress.episode.startedAt).Milliseconds(), 0),
		)
		*progress.episode = retryEpisode{}
	}
	if progress.upstreamReady {
		return
	}
	progress.upstreamReady = true
	z2m.logger.InfoContext(
		ctx,
		"Zigbee2MQTT upstream ready",
		eventKey, "adapter.upstream_ready",
	)
}

// logIsolatedDevice records one device left out of the activated routes. Only
// the fixed reason classification is kept; IEEE addresses, friendly names, and
// model identifiers are vendor identity and never logged.
func (z2m *Adapter) logIsolatedDevice(ctx context.Context, reasonCode string) {
	z2m.logger.WarnContext(
		ctx,
		"isolated Zigbee2MQTT device",
		eventKey, "adapter.device_isolated",
		"reason_code", reasonCode,
	)
}
