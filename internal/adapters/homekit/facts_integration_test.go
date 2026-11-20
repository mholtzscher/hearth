//go:build integration

package homekit_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/adapters/homekit"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
)

func TestFactFeedStartsAtTailFiltersAndSurfacesConsumerDeletion(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	broker := natstest.StartServer(t)
	conn, err := natsgo.Connect(broker.ClientURL())
	require.NoError(t, err)
	defer conn.Close()
	js, err := jetstream.New(conn)
	require.NoError(t, err)
	require.NoError(t, devicesnats.ProvisionDeviceFactStream(ctx, js))
	stream, err := js.Stream(ctx, devicesnats.DeviceFactStreamName)
	require.NoError(t, err)

	publishFact(ctx, t, js, testEntityID, 1)
	source, err := homekit.NewFactSource(js, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	feed, err := source.Open(ctx)
	require.NoError(t, err)
	defer func() { assert.NoError(t, feed.Close(context.WithoutCancel(ctx))) }()
	// These arrive before Run: no handoff gap between opening the feed and reading inventory.
	publishFact(ctx, t, js, "ent_01900000-0000-7000-8000-000000000002", 2) // subject disagrees with payload
	publishFact(ctx, t, js, testEntityID, 3)

	facts := make(chan homekit.ObservationFact, 2)
	finished := make(chan error, 1)
	go func() {
		finished <- feed.Run(ctx, func(ctx context.Context, fact homekit.ObservationFact) error {
			select {
			case facts <- fact:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case fact := <-facts:
		assert.Equal(t, testEntityID, fact.EntityID)
		assert.Equal(t, "obs_01900000-0000-7000-8000-000000000003", fact.ObservationID)
		assert.JSONEq(t, "true", string(fact.Value))
	case <-ctx.Done():
		t.Fatal("valid post-open Fact was not delivered")
	}
	names := stream.ConsumerNames(ctx)
	var consumers []string
	for name := range names.Name() {
		consumers = append(consumers, name)
	}
	require.NoError(t, names.Err())
	require.Len(t, consumers, 1)
	require.NoError(t, stream.DeleteConsumer(ctx, consumers[0]))
	select {
	case runErr := <-finished:
		require.ErrorIs(t, runErr, jetstream.ErrConsumerDeleted)
	case <-ctx.Done():
		t.Fatal("Fact feed did not report consumer loss")
	}
}

func publishFact(ctx context.Context, t *testing.T, js jetstream.JetStream, subjectEntity string, suffix int) {
	t.Helper()
	subject, err := natswire.ObservationFactSubject(subjectEntity, natswire.ObservationFactApplied)
	require.NoError(t, err)
	observationID := fmt.Sprintf("obs_01900000-0000-7000-8000-%012x", suffix)
	factID := fmt.Sprintf("fct_01900000-0000-7000-8000-%012x", suffix)
	body := fmt.Sprintf(`{
		"id": %q, "schema": "urn:hearth:schema:observation-fact:v1",
		"emitted_at": "2026-11-19T10:00:01Z", "correlation_id": "cor_01900000-0000-7000-8000-000000000001",
		"causation_id": %q,
		"data": {
			"observation_id": %q, "entity_id": %q, "disposition": "applied", "value": true,
			"adapter_received_at": "2026-11-19T10:00:00Z", "observed_at": "2026-11-19T10:00:01Z"
		}}`, factID, observationID, observationID, testEntityID)
	_, err = js.Publish(ctx, subject, []byte(body), jetstream.WithMsgID(factID))
	require.NoError(t, err)
}
