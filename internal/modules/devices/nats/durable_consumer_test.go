package nats //nolint:testpackage // Tests exercise package-private NATS lifecycle behavior.

import "testing"

// durableLifecycle is the shutdown and activity surface every Core durable
// consumer exposes, so one table can prove that a consumer which never
// subscribed is inert instead of nil-panicking.
type durableLifecycle interface {
	Active() bool
	Stop()
	Drain()
	Closed() <-chan struct{}
}

// A consumer that failed before subscribing must report inactivity and no-op on
// shutdown: readiness has to fail, and shutdown must not panic, when Core never
// established the subscription. The Entity Event and Observation consumers
// embed the shared lifecycle by pointer, so their zero values stay inert too.
func TestDurableConsumerWithoutSubscriptionIsInert(t *testing.T) {
	t.Parallel()
	tests := map[string]durableLifecycle{
		"zero shared lifecycle":      &durableConsumer{},
		"nil shared lifecycle":       (*durableConsumer)(nil),
		"zero Entity Event consumer": &EntityEventConsumer{},
		"zero Observation consumer":  &ObservationConsumer{},
	}
	for name, consumer := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if consumer.Active() {
				t.Fatal("consumer without a subscription reports active")
			}
			consumer.Stop()
			consumer.Drain()
			if consumer.Active() {
				t.Fatal("stopped consumer without a subscription reports active")
			}
			select {
			case <-consumer.Closed():
			default:
				t.Fatal("consumer without a subscription never reports closure")
			}
		})
	}
}
