//go:build integration

package nats //nolint:testpackage // Tests exercise package-private mapping, disposition, and lifecycle.

import (
	"context"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
)

// publishDeviceFact includes Nats-Msg-Id and returns the broker's duplicate indication.
func publishDeviceFact(
	t *testing.T,
	js jetstream.JetStream,
	message testDeviceFactMessage,
) *jetstream.PubAck {
	t.Helper()
	headers := make(natsgo.Header)
	headers.Set(natsgo.MsgIdHdr, message.messageID)
	ack, err := js.PublishMsg(
		context.Background(),
		&natsgo.Msg{Subject: message.subject, Header: headers, Data: message.payload},
	)
	if err != nil {
		t.Fatalf("publish device fact: %v", err)
	}
	return ack
}

// startDeviceFactServer starts embedded JetStream with the subjects and duplicate
// window these tests need. Devices owns the production stream configuration.
func startDeviceFactServer(t *testing.T) jetstream.JetStream {
	t.Helper()
	server := natstest.StartServer(t)
	connection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	if _, streamErr := js.CreateStream(context.Background(), jetstream.StreamConfig{
		Name:       testDeviceFactStreamName,
		Subjects:   []string{natswire.DeviceFactWildcard()},
		Storage:    jetstream.FileStorage,
		Duplicates: testDuplicateWindow,
	}); streamErr != nil {
		t.Fatal(streamErr)
	}
	return js
}

// startDeviceFactConsumer provisions and starts one real consumer with the
// supplied receiver, and always drains it before the test ends.
func startDeviceFactConsumer(
	t *testing.T,
	js jetstream.JetStream,
	receiver *fakeDeviceFactReceiver,
) (*platformnats.Consumer, jetstream.Consumer) {
	t.Helper()
	consumer, err := ProvisionDeviceFactConsumer(context.Background(), js, testDeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	running, err := StartDeviceFactConsumer(
		context.Background(), consumer, receiver, testValidator(t), discardLogger(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { drainDeviceFactConsumer(t, running) })
	return running, consumer
}

// waitForConsumerInfo waits for matching broker state within the liveness budget.
func waitForConsumerInfo(
	t *testing.T,
	consumer jetstream.Consumer,
	condition func(*jetstream.ConsumerInfo) bool,
) *jetstream.ConsumerInfo {
	t.Helper()
	deadline := time.Now().Add(testLiveness)
	for {
		info, err := consumer.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if condition(info) {
			return info
		}
		if time.Now().After(deadline) {
			t.Fatalf("consumer condition not met: %#v", info)
		}
		time.Sleep(testPollInterval)
	}
}
