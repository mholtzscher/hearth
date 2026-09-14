# Managed durable consumer

## Purpose

Share JetStream subscription lifecycle across Devices and Automations without
moving message-processing or fault policy into platform.

## Ownership and API

`internal/platform/nats/managed_consumer.go` owns:

```go
func StartConsumer(consumer jetstream.Consumer, handler func(jetstream.Msg), options ConsumerOptions) (*Consumer, error)
func (*Consumer) Active() bool
func (*Consumer) Stop()
func (*Consumer) Drain(context.Context) error
func (*Consumer) Closed() <-chan struct{}
```

`ConsumerOptions` supplies optional `OnConsumeError func(error)` and
`OnUnexpectedTermination func()` hooks. Modules retain decoding, tracing,
Ack/Nak/Term decisions, diagnostics, and admission closure. The primitive does
not provision, restart, or resubscribe consumers.

## Contract

- Startup failure returns no managed consumer. Nil and zero-value lifecycle
  receivers are inert; initialized consumers must not be copied.
- Active clears when shutdown starts or subscription termination is observed.
  It is not a broker-health probe.
- Stop requests immediate shutdown without waiting for the current callback.
- Drain requests graceful shutdown and joins callbacks and the termination
  watcher. Context cancellation ends only that wait, not shutdown. Callers can
  join again and must retain dependencies while callbacks use them.
- Repeated and concurrent shutdown calls are safe. nats.go's first Stop or Drain
  request wins: Stop cannot interrupt an existing drain, and Drain cannot
  restore messages discarded by Stop. Graceful draining processes buffered
  messages as well as the current callback.
- The watcher reads the intentional-shutdown latch once after subscription
  closure. If shutdown was not observed, it calls OnUnexpectedTermination once.
  A shutdown request after this decision cannot retract the hook.
- Closed closes after callbacks, the watcher, and the termination hook return.
  An AdmissionGroup tracks callbacks independently of nats.go's Closed signal,
  which can report an invalid subscription before its callback returns.
  Neither hook may wait on the same consumer's Drain or Closed.
- nats.go v1.53.1 may keep its consume context open when broker deletion occurs
  between pull requests, despite reporting heartbeat errors. This primitive
  observes closure; it does not add a broker-resource health check.

## Integration

Devices' StartEntityEventConsumer and StartObservationConsumer and Automations'
StartDeviceFactConsumer return *platformnats.Consumer directly. Module constructors
retain validation, message handlers, and policy hooks; no embedding-only consumer
types remain. Automations closes admission before logging unexpected termination.

Core keeps its existing shutdown order. Automation consumers join callbacks
before canceling their contexts. Device consumers retain their bounded wait;
expiration does not mean their callbacks have finished.

## Acceptance

Tests cover failed startup, blocked callbacks, watcher/hook completion ordering,
canceled and repeated drains, concurrent stop/drain arbitration, unexpected
termination exactly once, intentional shutdown, and inert receivers. Module
integration tests retain message disposition and admission fault behavior.
Broker-deletion coverage waits for an outstanding pull before deleting and
bounds its termination wait.
