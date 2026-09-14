package hearthd

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	automationsnats "github.com/mholtzscher/hearth/internal/modules/automations/nats"
)

// AutomationAdmissionChecker is the narrow readiness seam for Automation
// admission. Like [CommandAdmissionChecker] it stays separate from the HTTP
// Automations seam so a transport handler can never bypass admission.
type AutomationAdmissionChecker interface {
	AdmissionOpen() bool
}

// automationActivity reports whether the automations-owned Device Fact consumer
// is still consuming. Readiness observes it; the consumer's drain owns stopping
// it, so readiness never mutates the consumer it reports on.
type automationActivity interface {
	Active() bool
}

// automationConsumers owns the automations-owned Device Fact consumer and the
// lifecycle context its callbacks run under. Its callback context is detached
// from process cancellation, so a Fact already dispatched to admission reaches
// its commit; it is canceled only after the consumer has drained. This mirrors
// how [coreConsumers] protects Observation and Entity Event callbacks.
type automationConsumers struct {
	callbackContext context.Context
	cancelCallbacks context.CancelFunc
	logger          *slog.Logger
	deviceFacts     *automationsnats.DeviceFactConsumer
	// drainOnce makes the drain idempotent: the ordered shutdown path and the
	// deferred error-exit cleanup both stop the same consumer.
	drainOnce sync.Once
}

// newAutomationConsumers returns the lifecycle the automation Device Fact
// consumer runs under, detached from parent cancellation so a canceled process
// context never reaches an in-flight admission.
func newAutomationConsumers(parent context.Context, logger *slog.Logger) *automationConsumers {
	if logger == nil {
		logger = slog.Default()
	}
	callbackContext, cancelCallbacks := context.WithCancel(context.WithoutCancel(parent))
	return &automationConsumers{
		callbackContext: callbackContext,
		cancelCallbacks: cancelCallbacks,
		logger:          logger,
	}
}

// start subscribes the automations-owned Device Fact consumer under the consumer
// lifecycle context. It starts before the inbound Observation and Entity Event
// consumers so automatic admission is live before any new Fact can be committed.
func (consumers *automationConsumers) start(
	resource jetstream.Consumer,
	receiver automationsnats.DeviceFactReceiver,
	validator *contractsv1.Validator,
) error {
	deviceFacts, err := automationsnats.StartDeviceFactConsumer(
		consumers.callbackContext, resource, receiver, validator, consumers.logger,
	)
	if err != nil {
		return fmt.Errorf("start automation device fact consumer: %w", err)
	}
	consumers.deviceFacts = deviceFacts
	return nil
}

// Active reports whether the automation Device Fact consumer is still consuming.
// It is the readiness view of the same consumer shutdown drains.
func (consumers *automationConsumers) Active() bool {
	return consumers != nil && consumers.deviceFacts != nil && consumers.deviceFacts.Active()
}

// drain stops the automation Device Fact consumer so no new automatic admission
// enters, and waits for in-flight callbacks within the consumer's own bounded
// admission window. It is idempotent so both the ordered shutdown path and the
// deferred error-exit cleanup can call it, and a drain failure is recorded once
// without changing the caller's teardown order.
func (consumers *automationConsumers) drain() {
	consumers.drainOnce.Do(func() {
		if consumers.deviceFacts == nil {
			return
		}
		logCleanupFailure(
			context.Background(), consumers.logger, "drain_automation_consumer",
			consumers.deviceFacts.Drain(),
		)
	})
}

// close drains the consumer and only then cancels the context its callbacks run
// under. Canceling first would abort an already-dispatched admission with
// [context.Canceled]; draining first lets it reach its commit.
func (consumers *automationConsumers) close() {
	consumers.drain()
	consumers.cancelCallbacks()
}
