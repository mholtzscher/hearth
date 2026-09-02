package zigbee2mqtt //nolint:testpackage // Command tests exercise package-private matchers and route snapshots.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This test protects typed facade dispatch, early post-dispatch matching, Accept-before-linked-observation, exact-once
// claiming, sibling ordinary projection, and the mandatory /set then /get publication order.
func TestCommandClaimsEarlyMatchExactlyOnceAfterAccept(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, connection, device := commandReadyAdapter(t, recorder, session)
	var receivedAt time.Time
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if topic == "zigbee2mqtt/fixture-light/set" {
			receivedAt = time.Now().UTC()
			return z2m.publishDeviceState(ctx, 1, device, mqttMessage{
				Topic:      "zigbee2mqtt/fixture-light",
				Payload:    []byte(`{"state":"ON","brightness":63.75}`),
				ReceivedAt: receivedAt,
			})
		}
		return nil
	}
	responder := &fakeResponder{recorder: recorder}
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(device.entities[0].entityID, `{"value":true}`),
		responder,
	); err != nil {
		t.Fatal(err)
	}
	if responder.accepted != 1 || responder.rejected != 0 || responder.unavailable != 0 {
		t.Fatalf("responder = %#v", responder)
	}
	if len(connection.published) != 2 || connection.published[0].topic != "zigbee2mqtt/fixture-light/set" ||
		connection.published[0].payload != `{"state":"ON"}` || connection.published[1].topic != "zigbee2mqtt/fixture-light/get" ||
		connection.published[1].payload != `{"state":""}` {
		t.Fatalf("publications = %#v", connection.published)
	}
	if len(session.observations) != 2 {
		t.Fatalf("observations = %#v", session.observations)
	}
	var linked, ordinary int
	for _, observation := range session.observations {
		if observation.RefreshForCommand == nil {
			ordinary++
			if observation.EntityID != device.entities[1].entityID {
				t.Fatalf("ordinary Observation claimed wrong property: %#v", observation)
			}
		} else {
			linked++
			if observation.EntityID != device.entities[0].entityID || *observation.RefreshForCommand != "cmd-test" {
				t.Fatalf("linked Observation = %#v", observation)
			}
		}
	}
	if linked != 1 || ordinary != 1 {
		t.Fatalf("linked=%d ordinary=%d", linked, ordinary)
	}
	assertOrdered(
		t,
		recorder.snapshot(),
		"mqtt:zigbee2mqtt/fixture-light/set",
		"accept",
		"mqtt:zigbee2mqtt/fixture-light/get",
		"linked-observation",
	)
}

// This test protects freshness and exact matching. It fails if retained replay or a fresh wrong target satisfies the
// Command instead of the non-retained matching report produced by active refresh.
func TestCommandRequiresFreshNonRetainedExactMatch(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, connection, device := commandReadyAdapter(t, recorder, session)
	retainedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	var freshAt time.Time
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		switch topic {
		case "zigbee2mqtt/fixture-light/set":
			for _, message := range []mqttMessage{
				{
					Topic: "zigbee2mqtt/fixture-light", Payload: []byte(`{"state":"ON"}`),
					Retained: true, ReceivedAt: retainedAt,
				},
				{
					Topic: "zigbee2mqtt/fixture-light", Payload: []byte(`{"state":"ON"}`),
					ReceivedAt: retainedAt.Add(time.Second),
				},
				{
					Topic: "zigbee2mqtt/fixture-light", Payload: []byte(`{"state":"OFF"}`),
					ReceivedAt: time.Now().UTC(),
				},
			} {
				if err := z2m.publishDeviceState(ctx, 1, device, message); err != nil {
					return err
				}
			}
			return nil
		case "zigbee2mqtt/fixture-light/get":
			freshAt = time.Now().UTC()
			return z2m.publishDeviceState(ctx, 1, device, mqttMessage{
				Topic: "zigbee2mqtt/fixture-light", Payload: []byte(`{"state":"ON"}`), ReceivedAt: freshAt,
			})
		}
		return nil
	}
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(device.entities[0].entityID, `{"value":true}`),
		&fakeResponder{recorder: recorder},
	); err != nil {
		t.Fatal(err)
	}
	var linked adapter.Observation
	for _, observation := range session.observations {
		if observation.RefreshForCommand != nil {
			linked = observation
		}
	}
	if linked.AdapterReceivedAt != freshAt.Format(time.RFC3339Nano) {
		t.Fatalf(
			"linked receive time = %q, want fresh %q; observations=%#v",
			linked.AdapterReceivedAt,
			freshAt,
			session.observations,
		)
	}
	if len(session.observations) != 4 {
		t.Fatalf(
			"observations = %#v, want retained, pre-dispatch, wrong ordinary, and fresh linked",
			session.observations,
		)
	}
}

// This test protects typed rejection on an unacknowledged /set. It fails if broker failure is reported as a generic
// upstream rejection, leaves routes enabled, or permits a stale matcher to survive.
func TestCommandSetFailureRejectsUnavailableAndDisablesRoutes(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, connection, device := commandReadyAdapter(t, recorder, session)
	connectionContext, cancelConnection := context.WithCancelCause(context.Background())
	defer cancelConnection(nil)
	z2m.mutex.Lock()
	z2m.connectionCancel = cancelConnection
	z2m.mutex.Unlock()
	connection.onPublish = func(context.Context, *fakeConnection, string, []byte) error {
		return errors.New("set PUBACK failed")
	}
	responder := &fakeResponder{recorder: recorder}
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(device.entities[0].entityID, `{"value":true}`),
		responder,
	); err != nil {
		t.Fatal(err)
	}
	z2m.mutex.Lock()
	defer z2m.mutex.Unlock()
	if responder.unavailable != 1 || responder.accepted != 0 || z2m.healthy || len(z2m.routes) != 0 ||
		len(z2m.matchers) != 0 {
		t.Fatalf(
			"set failure responder=%#v healthy=%t routes=%d matchers=%d",
			responder,
			z2m.healthy,
			len(z2m.routes),
			len(z2m.matchers),
		)
	}
	cause := context.Cause(connectionContext)
	if cause == nil || !strings.Contains(cause.Error(), "set PUBACK failed") {
		t.Fatalf("connection cancellation cause = %v", cause)
	}
}

// This test protects held early State and command-local cancellation. It fails if an unacknowledged set drops valid
// ordinary evidence or if one expired Command tears down an otherwise healthy broker route.
//
//nolint:gocognit // The shared setup compares two distinct failure classifications.
func TestCommandSetFailurePreservesHeldStateAndContextFailureDoesNotDisconnect(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name            string
		publishErr      error
		wantHealthy     bool
		wantObservation bool
	}{
		{name: "broker failure", publishErr: errors.New("PUBACK failed"), wantObservation: true},
		{name: "command deadline", publishErr: context.DeadlineExceeded, wantHealthy: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := &runtimeRecorder{}
			session := newFakeSession(recorder)
			z2m, connection, device := commandReadyAdapter(t, recorder, session)
			connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
				if topic == "zigbee2mqtt/fixture-light/set" && test.wantObservation {
					if err := z2m.publishDeviceState(ctx, 1, device, mqttMessage{
						Topic: "zigbee2mqtt/fixture-light", Payload: []byte(`{"state":"ON"}`),
						ReceivedAt: time.Now().UTC(),
					}); err != nil {
						return err
					}
				}
				return test.publishErr
			}
			responder := &fakeResponder{recorder: recorder}
			if err := z2m.HandleCommand(
				context.Background(),
				testCommand(device.entities[0].entityID, `{"value":true}`),
				responder,
			); err != nil {
				t.Fatal(err)
			}
			z2m.mutex.Lock()
			healthy := z2m.healthy
			z2m.mutex.Unlock()
			if healthy != test.wantHealthy || responder.unavailable != 1 {
				t.Fatalf("healthy=%t responder=%#v", healthy, responder)
			}
			if got := len(session.observations) == 1; got != test.wantObservation {
				t.Fatalf("ordinary held Observation present=%t, want %t", got, test.wantObservation)
			}
			if test.wantObservation && session.observations[0].RefreshForCommand != nil {
				t.Fatalf("held State was incorrectly linked: %#v", session.observations[0])
			}
		})
	}
}

// This test protects accepted-Command behavior when active refresh publication fails. It fails if /get failure removes
// the matcher and prevents a later natural non-retained State report from satisfying the Command.
func TestCommandGetFailureStillAllowsNaturalMatch(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, connection, device := commandReadyAdapter(t, recorder, session)
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if topic != "zigbee2mqtt/fixture-light/get" {
			return nil
		}
		go func() {
			_ = z2m.publishDeviceState(ctx, 1, device, mqttMessage{
				Topic: "zigbee2mqtt/fixture-light", Payload: []byte(`{"state":"ON"}`),
				ReceivedAt: time.Now().UTC(),
			})
		}()
		return errors.New("refresh PUBACK failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := z2m.HandleCommand(
		ctx,
		testCommand(device.entities[0].entityID, `{"value":true}`),
		&fakeResponder{recorder: recorder},
	); err != nil {
		t.Fatal(err)
	}
	if len(session.observations) != 1 || session.observations[0].RefreshForCommand == nil {
		t.Fatalf("natural match was not linked after /get failure: %#v", session.observations)
	}
}
