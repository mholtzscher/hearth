//go:build integration

package nats //nolint:testpackage // Tests exercise package-private consumer termination.

import (
	"context"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// Broker-reported termination must close the real service's admission gate.
// Wait for an outstanding pull so deletion reaches it as a terminal status;
// deletion between pulls may leave nats.go retrying without reporting closure.
func TestDeviceFactConsumerLatchesAdmissionWhenConsumerIsDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	js := startDeviceFactServer(t)
	validator := testValidator(t)
	resource, err := ProvisionDeviceFactConsumer(ctx, js, testDeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	service := automations.NewService(nil, nil, automations.Dependencies{})
	if !service.AdmissionOpen() {
		t.Fatal("a freshly assembled service reports admission closed")
	}
	running, err := StartDeviceFactConsumer(ctx, resource, service, validator, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { drainDeviceFactConsumer(t, running) })

	// A malformed Fact is terminated by the consumer and never reaches
	// admission, so the delivery cycle is observable without a repository.
	publishDeviceFact(t, js, testDeviceFactMessage{
		subject:   rawDeviceFactSubject(testEntityAID, "observation", "applied"),
		messageID: testFactOneID,
		payload:   []byte(`{"id":`),
	})
	waitForConsumerInfo(t, resource, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Stream == 1 && info.NumAckPending == 0 && info.NumWaiting > 0
	})

	stream, err := js.Stream(ctx, testDeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	if deleteErr := stream.DeleteConsumer(ctx, DeviceFactConsumerName); deleteErr != nil {
		t.Fatalf("delete device fact consumer: %v", deleteErr)
	}

	waitForConsumerClosed(t, running)
	if service.AdmissionOpen() {
		t.Fatal("automation admission stayed open after the consumer was deleted")
	}
	if running.Active() {
		t.Fatal("a consumer whose broker resource was deleted still reports itself active")
	}
}
