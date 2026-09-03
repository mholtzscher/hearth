package zigbee2mqtt //nolint:testpackage // Command tests exercise the private runtime state machine.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This test protects the asynchronous boundary: /set must receive PUBACK and acceptance must publish before the handler
// returns, while /get, State matching, and linked JetStream acknowledgement remain coordinator-owned work.
func TestCommandReturnsAfterSetAndAcceptBeforeRefreshOrEvidence(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapter(t, recorder, session)
	getStarted := make(chan struct{})
	releaseGet := make(chan struct{})
	linkedStarted := make(chan struct{})
	releaseLinked := make(chan struct{})
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if strings.HasSuffix(topic, "/get") {
			close(getStarted)
			select {
			case <-releaseGet:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	session.publishHook = func(ctx context.Context, _ adapter.Observation, linked bool) error {
		if linked {
			close(linkedStarted)
			select {
			case <-releaseLinked:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}

	responder := newFakeResponder(recorder, session)
	result := make(chan error, 1)
	go func() {
		result <- z2m.HandleCommand(
			context.Background(),
			testCommand(device.entities[0].entityID, `{"value":true}`),
			responder,
		)
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("handler waited for /get")
	}
	if responder.accepted != 1 {
		t.Fatalf("accepted = %d", responder.accepted)
	}
	select {
	case <-getStarted:
	case <-time.After(time.Second):
		t.Fatal("mandatory /get did not start")
	}
	if err := publishState(
		context.Background(),
		z2m,
		device,
		`{"state":"ON"}`,
		time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	select {
	case <-linkedStarted:
	case <-time.After(time.Second):
		t.Fatal("linked publication did not start")
	}
	close(releaseGet)
	close(releaseLinked)
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.linked) == 1
	})
}

// This test protects matcher installation before /set launch, early report ownership, sibling ordinary disposition,
// Accept-before-linking, and mandatory /get launch.
func TestCommandClaimsEarlyMatchExactlyOnceAfterAccept(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapter(t, recorder, session)
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if strings.HasSuffix(topic, "/set") {
			return publishState(ctx, z2m, device, `{"state":"ON","brightness":63.75}`, time.Now().UTC())
		}
		return nil
	}
	responder := newFakeResponder(recorder, session)
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(device.entities[0].entityID, `{"value":true}`),
		responder,
	); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.linked) == 1 && len(session.observations) == 1
	})
	if session.linked[0].EntityID != device.entities[0].entityID ||
		session.observations[0].EntityID != device.entities[1].entityID {
		t.Fatalf("linked=%#v ordinary=%#v", session.linked, session.observations)
	}
	events := recorder.snapshot()
	assertOrdered(
		t,
		events,
		"mqtt:zigbee2mqtt/fixture-light/set",
		"accept",
		"linked-observation",
	)
	if indexOf(events, "mqtt:zigbee2mqtt/fixture-light/get", 0) < 0 {
		t.Fatalf("mandatory /get was not published: %v", events)
	}
}

// This integration test protects native-mired set/get passthrough, typed Observation publication, and exact matching.
// It fails if a nearby value satisfies the Command or if either Zigbee2MQTT property payload is converted or renamed.
func TestColorTempCommandPassesThroughAndRequiresExactReport(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapter(t, recorder, session)
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		switch {
		case strings.HasSuffix(topic, "/set"):
			return publishState(ctx, z2m, device, `{"color_temp":369}`, time.Now().UTC())
		case strings.HasSuffix(topic, "/get"):
			return publishState(ctx, z2m, device, `{"color_temp":370}`, time.Now().UTC())
		default:
			return nil
		}
	}
	responder := newFakeResponder(recorder, session)
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(device.entities[2].entityID, `{"value":370}`),
		responder,
	); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.observations) == 1 && len(session.linked) == 1
	})
	connection.mutex.Lock()
	publications := append([]mqttPublication(nil), connection.published...)
	connection.mutex.Unlock()
	if len(publications) != 2 || publications[0].topic != "zigbee2mqtt/fixture-light/set" ||
		publications[0].payload != `{"color_temp":370}` || publications[0].qos != mqttQoS || publications[0].retained ||
		publications[1].topic != "zigbee2mqtt/fixture-light/get" ||
		publications[1].payload != `{"color_temp":""}` || publications[1].qos != mqttQoS || publications[1].retained {
		t.Fatalf("MQTT publications = %#v", publications)
	}
	if responder.accepted != 1 || string(session.observations[0].Value) != "369" ||
		string(session.linked[0].Value) != "370" || session.linked[0].EntityID != device.entities[2].entityID {
		t.Fatalf(
			"responder=%#v ordinary=%#v linked=%#v",
			responder,
			session.observations,
			session.linked,
		)
	}
}

// This test protects every freshness and identity discriminator required before a report may be claimed.
//
//nolint:gocognit // One table keeps all State eligibility discriminators under the same active matcher setup.
func TestCommandLeavesIneligibleStateOrdinary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		generation      uint64
		revision        uint64
		candidateEntity int
		stateEntity     int
		payload         string
		retained        bool
		receivedAt      func(time.Time, time.Time) time.Time
	}{
		{name: "retained", generation: 1, revision: 1, payload: `{"state":"ON"}`, retained: true},
		{
			name: "pre-dispatch", generation: 1, revision: 1, payload: `{"state":"ON"}`,
			receivedAt: func(dispatched, _ time.Time) time.Time { return dispatched },
		},
		{name: "stale generation", generation: 2, revision: 1, payload: `{"state":"ON"}`},
		{name: "stale revision", generation: 1, revision: 2, payload: `{"state":"ON"}`},
		{
			name: "wrong entity", generation: 1, revision: 1,
			candidateEntity: 1, stateEntity: 1, payload: `{"brightness":50}`,
		},
		{name: "wrong property", generation: 1, revision: 1, stateEntity: 1, payload: `{"brightness":50}`},
		{name: "wrong value", generation: 1, revision: 1, payload: `{"state":"OFF"}`},
		{
			name: "post deadline", generation: 1, revision: 1, payload: `{"state":"ON"}`,
			receivedAt: func(_, deadline time.Time) time.Time { return deadline.Add(time.Nanosecond) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := &runtimeRecorder{}
			session := newFakeSession(recorder)
			z2m, coordinator, connection, device := commandReadyAdapter(t, recorder, session)
			setStarted := make(chan struct{})
			releaseSet := make(chan struct{})
			connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
				if strings.HasSuffix(topic, "/set") {
					close(setStarted)
					select {
					case <-releaseSet:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			}
			command := testCommand(device.entities[0].entityID, `{"value":true}`)
			result := make(chan error, 1)
			go func() {
				result <- z2m.HandleCommand(context.Background(), command, newFakeResponder(recorder, session))
			}()
			<-setStarted
			var attempt *commandAttempt
			waitFor(t, func() bool {
				attempt = coordinator.matchers[device.entities[0].entityID]
				return attempt != nil
			})
			candidateEntity := device.entities[test.candidateEntity]
			stateEntity := device.entities[test.stateEntity]
			states, _, err := decodeDeviceState([]byte(test.payload), []discoveredEntity{stateEntity.discovered})
			if err != nil || len(states) != 1 {
				t.Fatalf("decode test State: states=%#v err=%v", states, err)
			}
			receivedAt := time.Now().UTC()
			if test.receivedAt != nil {
				receivedAt = test.receivedAt(attempt.dispatchedAt, attempt.deadline)
			}
			candidateResult := make(chan stateDisposition, 1)
			z2m.runtimeEvents <- stateCandidate{
				ctx:           context.Background(),
				generation:    test.generation,
				routeRevision: test.revision,
				entityID:      candidateEntity.entityID,
				state:         states[0],
				retained:      test.retained,
				receivedAt:    receivedAt,
				result:        candidateResult,
			}
			if disposition := <-candidateResult; disposition != stateOrdinary {
				t.Fatalf("disposition = %v, want ordinary", disposition)
			}
			close(releaseSet)
			if err = <-result; err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool {
				session.mutex.Lock()
				defer session.mutex.Unlock()
				return len(session.observations) == 1
			})
			if len(session.linked) != 0 {
				t.Fatalf("ineligible report linked: %#v", session.linked)
			}
		})
	}
}

// This test protects typed unavailable rejection, immediate route invalidation, connection cancellation, and one ordinary
// fallback for an early claim when /set fails outside the Command context.
func TestCommandSetFailureRejectsAndFallsBack(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapter(t, recorder, session)
	connectionContext, disconnect := context.WithCancelCause(context.Background())
	routes := make(map[string]commandRoute)
	for _, entity := range device.entities {
		routes[entity.entityID] = commandRoute{
			entityID: entity.entityID, ieeeAddress: device.ieeeAddress, friendlyName: device.friendly,
			entity: entity.discovered,
		}
	}
	activation := make(chan routeActivationResult, 1)
	z2m.runtimeEvents <- routesActivated{
		generation: 1,
		connection: connection,
		disconnect: disconnect,
		snapshot:   routeSnapshot{routes: routes, devices: map[string]runtimeDevice{device.friendly: device}},
		result:     activation,
	}
	if result := <-activation; result.err != nil {
		t.Fatal(result.err)
	}
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if strings.HasSuffix(topic, "/set") {
			if err := z2m.publishDeviceState(ctx, 1, 2, device, mqttMessage{
				Topic:      "zigbee2mqtt/" + device.friendly,
				Payload:    []byte(`{"state":"ON"}`),
				ReceivedAt: time.Now().UTC(),
			}); err != nil {
				return err
			}
			return errors.New("set PUBACK failed")
		}
		return nil
	}
	responder := newFakeResponder(recorder, session)
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(device.entities[0].entityID, `{"value":true}`),
		responder,
	); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.observations) == 1
	})
	if responder.unavailable != 1 || responder.accepted != 0 || len(session.linked) != 0 {
		t.Fatalf("responder=%#v ordinary=%#v linked=%#v", responder, session.observations, session.linked)
	}
	missing := newFakeResponder(recorder, session)
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(device.entities[0].entityID, `{"value":true}`),
		missing,
	); err != nil {
		t.Fatal(err)
	}
	if missing.unavailable != 1 || missing.accepted != 0 {
		t.Fatalf("route remained dispatchable after /set failure: %#v", missing)
	}
	cause := context.Cause(connectionContext)
	if cause == nil || !strings.Contains(cause.Error(), "set PUBACK failed") {
		t.Fatalf("connection cancellation cause = %v", cause)
	}
}

// This test protects accepted-Command behavior after /get failure: a later natural report must still be linked.
func TestCommandGetFailureStillAllowsNaturalMatch(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapter(t, recorder, session)
	connection.onPublish = func(context.Context, *fakeConnection, string, []byte) error {
		return nil
	}
	connection.onPublish = func(_ context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if strings.HasSuffix(topic, "/get") {
			return errors.New("refresh PUBACK failed")
		}
		return nil
	}
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(device.entities[0].entityID, `{"value":true}`),
		newFakeResponder(recorder, session),
	); err != nil {
		t.Fatal(err)
	}
	if err := publishState(context.Background(), z2m, device, `{"state":"ON"}`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.linked) == 1
	})
}
