package homeassistant

import (
	"context"
	"errors"
	"time"
)

// adapterComponent is the only component value emitted from this package. It
// is attached once in New so every record carries a bounded subsystem label
// without repeating app or pid, which belong to the root application logger.
const adapterComponent = "homeassistant"

// eventKey is the shared slog attribute key carrying the stable dotted event
// name on every record emitted from this package. Event values stay whole
// literals at each emission site so searching a value finds its site.
const eventKey = "event"

// retryEpisode tracks one consecutive run of retryable connection failures so
// the first failure (or a new failure class) warns once while repeats stay at
// Debug. It is owned by a single Run goroutine; no locking is needed.
type retryEpisode struct {
	attempts  int
	lastCode  string
	startedAt time.Time
}

func (episode *retryEpisode) active() bool { return episode.attempts > 0 }

// homeAssistantErrorCode maps a retry-loop failure to a fixed diagnostic
// classification. The code never carries URLs, tokens, payloads, or upstream
// free-form text.
func homeAssistantErrorCode(err error) string {
	if _, ok := errors.AsType[*AuthenticationError](err); ok {
		return "authentication_failed"
	}
	if _, ok := errors.AsType[*requestRejectedError](err); ok {
		return "upstream_rejected"
	}
	if _, ok := errors.AsType[*sessionOperationError](err); ok {
		return "session_operation_failed"
	}
	return "upstream_connection_failed"
}

// logConnectionRetry records one failed reconnect attempt. The first attempt
// of an episode and any attempt with a distinct failure class log at Warn;
// identical repeats log at Debug.
func (homeAssistant *Adapter) logConnectionRetry(
	ctx context.Context,
	episode *retryEpisode,
	err error,
	wait time.Duration,
) {
	code := homeAssistantErrorCode(err)
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
		homeAssistant.logger.WarnContext(ctx, "Home Assistant connection retrying", args...)
		return
	}
	homeAssistant.logger.DebugContext(ctx, "Home Assistant connection retrying", args...)
}

// logUpstreamReady records a successful subscription, snapshot, and buffer
// reconciliation. It closes any open retry episode with one recovery record
// and then emits adapter readiness exactly once for the connection.
func (homeAssistant *Adapter) logUpstreamReady(ctx context.Context, episode *retryEpisode) {
	if episode.active() {
		homeAssistant.logger.InfoContext(
			ctx,
			"Home Assistant connection recovered",
			eventKey, "dependency.recovered",
			"dependency", adapterComponent,
			"attempts", episode.attempts,
			"duration_ms", max(time.Since(episode.startedAt).Milliseconds(), 0),
		)
		*episode = retryEpisode{}
	}
	homeAssistant.logger.InfoContext(
		ctx,
		"Home Assistant upstream ready",
		eventKey, "adapter.upstream_ready",
		"entity_id", homeAssistant.config.EntityID,
	)
}
