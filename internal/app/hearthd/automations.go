package hearthd

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	automationsnats "github.com/mholtzscher/hearth/internal/modules/automations/nats"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

// automationActivity gives readiness a read-only view of Device Fact consumption.
type automationActivity interface {
	Active() bool
}

// automationConsumers owns the Device Fact consumer and its callback context.
// Like [coreConsumers], it keeps in-flight admissions alive during shutdown.
type automationConsumers struct {
	callbackContext context.Context
	cancelCallbacks context.CancelFunc
	logger          *slog.Logger
	deviceFacts     *platformnats.Consumer
}

// newAutomationConsumers detaches admission callbacks from parent cancellation.
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

// start subscribes under the callback context. Call it before starting inbound
// Fact producers so automatic admission is ready for their first Fact.
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
func (consumers *automationConsumers) Active() bool {
	return consumers != nil && consumers.deviceFacts != nil && consumers.deviceFacts.Active()
}

// close drains before canceling callbacks so in-flight admissions can commit.
func (consumers *automationConsumers) close() {
	if consumers.deviceFacts != nil {
		logCleanupFailure(
			context.Background(), consumers.logger, "drain_automation_consumer",
			consumers.deviceFacts.Drain(context.Background()),
		)
	}
	consumers.cancelCallbacks()
}
