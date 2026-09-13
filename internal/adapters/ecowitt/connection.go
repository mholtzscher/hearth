package ecowitt

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"
)

// MQTT subscribe QoS. The captured gateway publishes at effective QoS 0; a
// QoS 1 subscription is the strongest request the Adapter can make and never
// weakens delivery.
const mqttQoS = byte(1)

// runConnections owns the Adapter-side reconnect loop. Paho's automatic
// reconnect is disabled, so each generation is dialled explicitly with bounded
// exponential backoff and jitter, following the Zigbee2MQTT Adapter precedent.
func (ecowitt *Adapter) runConnections(ctx context.Context, coordinator *runtimeCoordinator) error {
	delay := reconnectMinimum
	var generation uint64
	for {
		generation++
		err := ecowitt.runConnection(ctx, coordinator, generation)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, errRuntimeStopped) {
			return err
		}
		if failureErr := ecowitt.reportUpstreamUnavailable(ctx, coordinator, generation, err); failureErr != nil {
			return failureErr
		}
		wait := ecowitt.retryDelay(delay)
		ecowitt.logger.DebugContext(
			ctx,
			"Ecowitt MQTT connection retrying",
			slog.String(eventKey, "dependency.retrying"),
			slog.String("dependency", adapterComponent),
			slog.String(errorCodeKey, ecowittConnectionErrorCode(err)),
			slog.Int64("retry_in_ms", max(wait.Milliseconds(), 0)),
		)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if delay < reconnectMaximum {
			delay *= 2
			if delay > reconnectMaximum {
				delay = reconnectMaximum
			}
		}
	}
}

// reportUpstreamUnavailable tells the coordinator that one MQTT generation
// failed, so health, deadline, and pending-work state stay coordinator-owned.
// Every failure is recoverable: the reconnect loop continues.
func (ecowitt *Adapter) reportUpstreamUnavailable(
	ctx context.Context,
	coordinator *runtimeCoordinator,
	generation uint64,
	cause error,
) error {
	ecowitt.logger.WarnContext(
		ctx,
		"Ecowitt MQTT generation ended",
		slog.String(eventKey, "adapter.upstream_unavailable"),
		slog.String(errorCodeKey, ecowittConnectionErrorCode(cause)),
	)
	return coordinator.submit(ctx, upstreamUnavailable{generation: generation})
}

// ecowittConnectionErrorCode classifies one connection failure without
// repeating a broker URL, topic, or address.
func ecowittConnectionErrorCode(err error) string {
	if errors.Is(err, errMQTTRelayOverflow) {
		return relayOverflowErrorCode
	}
	if errors.Is(err, errPendingReportOverflow) {
		return pendingWorkErrorCode
	}
	return upstreamErrorCode
}

// runConnection runs one MQTT generation: announce the attempt, dial, monitor
// loss, subscribe at QoS 1 to exactly the configured topic, announce SUBACK,
// then wait for the generation to end. The deferred Close seals the transport
// relay and waits for every admitted delivery to drain, so this function never
// returns while copied reports are still on their way to the coordinator; the
// supervisor reports the generation unavailable only afterwards.
func (ecowitt *Adapter) runConnection(
	ctx context.Context,
	coordinator *runtimeCoordinator,
	generation uint64,
) error {
	connectionContext, cancelConnection := context.WithCancelCause(ctx)
	defer cancelConnection(nil)

	if err := ecowitt.beginGeneration(connectionContext, coordinator, generation); err != nil {
		return err
	}
	connection, err := ecowitt.dialer.Dial(
		connectionContext,
		mqttConfig{URL: ecowitt.config.MQTTURL, ClientID: ecowitt.config.MQTTClientID},
		func(message mqttMessage) {
			// An already-copied delivery outlives its connection: the relay drains
			// every admitted message after a loss, so the submission is bounded by
			// the Adapter's run context rather than the generation's cancellable
			// one. The deferred connection.Close below waits for that drain, so
			// these reports reach the coordinator before the generation is
			// reported unavailable.
			_ = coordinator.submit(ctx, reportReceived{
				generation: generation, message: message,
			})
		},
	)
	if err != nil {
		return preferConnectionError(ctx, connectionContext, err)
	}
	defer connection.Close()
	go monitorMQTTLoss(connectionContext, cancelConnection, connection)

	if err = connection.Subscribe(connectionContext, ecowitt.config.MQTTTopic, mqttQoS); err != nil {
		return preferConnectionError(ctx, connectionContext, err)
	}
	if err = coordinator.submit(connectionContext, generationEstablished{
		generation:   generation,
		disconnect:   cancelConnection,
		subscribedAt: time.Now(),
	}); err != nil {
		return err
	}
	<-connectionContext.Done()
	return context.Cause(connectionContext)
}

// beginGeneration synchronizes the attempt number with the coordinator before
// any socket work, so a connect failure is attributed to the current
// generation rather than the previous one.
func (ecowitt *Adapter) beginGeneration(
	ctx context.Context,
	coordinator *runtimeCoordinator,
	generation uint64,
) error {
	result := make(chan error, 1)
	return coordinator.submitSync(ctx, generationStarting{generation: generation, result: result}, result)
}

// monitorMQTTLoss converts one connection loss into generation cancellation so
// the connection loop tears the generation down exactly once.
func monitorMQTTLoss(ctx context.Context, cancel context.CancelCauseFunc, connection mqttConnection) {
	select {
	case err := <-connection.Lost():
		if err == nil {
			err = errors.New("ecowitt MQTT connection lost")
		}
		cancel(err)
	case <-ctx.Done():
	}
}

// preferConnectionError keeps the generation cause when the parent context is
// still live, so a relay overflow or broker disconnect is not masked by the
// derived context's plain cancellation.
func preferConnectionError(parent context.Context, connectionContext context.Context, operationErr error) error {
	if parent.Err() == nil && connectionContext.Err() != nil {
		if cause := context.Cause(connectionContext); cause != nil {
			return cause
		}
	}
	return operationErr
}

// jitterReconnect spreads reconnect attempts over the upper half of one delay.
//
//nolint:gosec // Backoff jitter needs no cryptographic randomness.
func jitterReconnect(delay time.Duration) time.Duration {
	half := delay / jitterDivisor
	if half <= 0 {
		return delay
	}
	return half + time.Duration(rand.Int64N(int64(delay-half)+1))
}
