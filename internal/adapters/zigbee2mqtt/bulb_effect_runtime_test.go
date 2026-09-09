package zigbee2mqtt //nolint:testpackage // Command tests exercise the private runtime state machine.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// This test protects the dispatched effect path and fails if a trigger
// publishes anything other than one QoS 1 set, if a /get refresh is
// published, if a matcher is installed, if any observation (ordinary or
// linked) is published, or if the handler does not return on acceptance.
func TestEffectTriggerDispatchesWithoutRefreshOrObservation(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, coordinator, connection, device := commandReadyAdapterFor(
		t,
		recorder,
		session,
		"bridge-devices-wanda-synthetic.json",
	)
	effect := entityByKey(device, "effect")
	if effect.entityID == "" {
		t.Fatal("effect Entity was not discovered")
	}
	responder := newFakeResponder(recorder, session)
	if err := z2m.HandleCommand(
		context.Background(),
		testTriggerCommand(effect.entityID, `{"name":"breathe"}`),
		responder,
	); err != nil {
		t.Fatal(err)
	}
	if responder.accepted != 1 {
		t.Fatalf("accepted = %d", responder.accepted)
	}
	connection.mutex.Lock()
	publications := append([]mqttPublication(nil), connection.published...)
	connection.mutex.Unlock()
	if len(publications) != 1 || publications[0].topic != "zigbee2mqtt/synthetic-wanda-bulb/set" ||
		publications[0].payload != `{"effect":"breathe"}` || publications[0].qos != mqttQoS || publications[0].retained {
		t.Fatalf("MQTT publications = %#v", publications)
	}
	if _, installed := coordinator.matchers[effect.entityID]; installed {
		t.Fatal("dispatched effect installed an outcome matcher")
	}
	// A later broadcast carrying effect and sibling state must publish only
	// sibling observations: the stateless effect claims nothing.
	if err := publishState(
		context.Background(),
		z2m,
		device,
		`{"state":"ON","effect":"breathe","linkquality":18}`,
		time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.observations) == 2
	})
	if len(session.linked) != 0 {
		t.Fatalf("dispatched effect linked State: %#v", session.linked)
	}
	for _, event := range recorder.snapshot() {
		if strings.HasSuffix(event, "/get") {
			t.Fatalf("dispatched effect published a refresh: %v", recorder.snapshot())
		}
	}
}

// This test protects per-IEEE FIFO across dispatched and observed kinds and
// fails if an effect overlaps a same-device command, if the effect slot is
// not released on acceptance, or if the queued observed command loses its
// linked satisfaction flow.
func TestEffectJoinsPerIEEEFIFOAndReleasesOnAccept(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapterFor(
		t,
		recorder,
		session,
		"bridge-devices-wanda-synthetic.json",
	)
	effect := entityByKey(device, "effect")
	power := entityByKey(device, "power")
	releaseSet := make(chan struct{})
	connection.onPublish = func(_ context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if strings.HasSuffix(topic, "/set") {
			<-releaseSet
		}
		return nil
	}
	setCount := func() int {
		connection.mutex.Lock()
		defer connection.mutex.Unlock()
		sets := 0
		for _, publication := range connection.published {
			if strings.HasSuffix(publication.topic, "/set") {
				sets++
			}
		}
		return sets
	}
	effectResult := make(chan error, 1)
	go func() {
		effectResult <- z2m.HandleCommand(
			context.Background(),
			testTriggerCommand(effect.entityID, `{"name":"breathe"}`),
			newFakeResponder(recorder, session),
		)
	}()
	// Queue the observed command only after the effect /set is in flight,
	// so the effect deterministically heads the per-IEEE queue.
	waitFor(t, func() bool { return setCount() == 1 })
	// The queued observed command must wait while the effect /set is in flight.
	powerResult := make(chan error, 1)
	go func() {
		powerResult <- z2m.HandleCommand(
			context.Background(),
			testCommand(power.entityID, `{"value":true}`),
			newFakeResponder(recorder, session),
		)
	}()
	select {
	case err := <-powerResult:
		t.Fatalf("queued Command completed before effect disposition: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if sets := setCount(); sets != 1 {
		t.Fatalf("same-IEEE set publications overlapped: %d", sets)
	}
	close(releaseSet)
	if err := <-effectResult; err != nil {
		t.Fatal(err)
	}
	// The effect slot released on accept: the queued power command publishes
	// its set and completes its observed flow with no effect observation.
	if err := <-powerResult; err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		connection.mutex.Lock()
		defer connection.mutex.Unlock()
		gets := 0
		for _, publication := range connection.published {
			if strings.HasSuffix(publication.topic, "/get") {
				gets++
			}
		}
		return gets == 1
	})
	if err := publishState(context.Background(), z2m, device, `{"state":"ON"}`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.linked) == 1
	})
	assertEffectPowerDisposition(t, connection, session, power)
}

// assertEffectPowerDisposition checks the terminal FIFO order after the
// queued power report links: effect set first, power set second, exactly
// one linked power observation, and no ordinary observations.
func assertEffectPowerDisposition(
	t *testing.T,
	connection *fakeConnection,
	session *fakeSession,
	power runtimeEntity,
) {
	t.Helper()
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	var setPayloads []string
	for _, publication := range connection.published {
		if strings.HasSuffix(publication.topic, "/set") {
			setPayloads = append(setPayloads, publication.payload)
		}
	}
	if len(setPayloads) != 2 || setPayloads[0] != `{"effect":"breathe"}` || setPayloads[1] != `{"state":"ON"}` {
		t.Fatalf("set payloads = %v", setPayloads)
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if len(session.linked) != 1 || session.linked[0].EntityID != power.entityID ||
		string(session.linked[0].Value) != "true" {
		t.Fatalf("linked = %#v", session.linked)
	}
	if len(session.observations) != 0 {
		t.Fatalf("ordinary observations = %#v", session.observations)
	}
}

// This test protects the observed startup-setting flow and fails if a set
// does not publish its exact wire value with refresh, or if a fresh
// post-dispatch report does not link-satisfy the command.
func TestStartupSetSatisfiesViaFreshLinkedObservation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		parameters string
		setPayload string
		echo       string
		linked     string
	}{
		{
			name: "value", parameters: `{"mode":"value","value":250}`,
			setPayload: `{"color_temp_startup":250}`, echo: `{"color_temp_startup":250}`,
			linked: `{"mode":"value","value":250}`,
		},
		{
			name: "previous", parameters: `{"mode":"choice","choice":"previous"}`,
			setPayload: `{"color_temp_startup":65535}`, echo: `{"color_temp_startup":65535}`,
			linked: `{"choice":"previous","mode":"choice"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := &runtimeRecorder{}
			session := newFakeSession(recorder)
			z2m, _, connection, device := commandReadyAdapterFor(
				t,
				recorder,
				session,
				"bridge-devices-wanda-synthetic.json",
			)
			startup := entityByKey(device, "startupcolortemp")
			connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
				if strings.HasSuffix(topic, "/set") {
					return publishState(ctx, z2m, device, test.echo, time.Now().UTC())
				}
				return nil
			}
			responder := newFakeResponder(recorder, session)
			if err := z2m.HandleCommand(
				context.Background(),
				testCommand(startup.entityID, test.parameters),
				responder,
			); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool {
				session.mutex.Lock()
				defer session.mutex.Unlock()
				return len(session.linked) == 1
			})
			// The mandatory refresh publishes on its own effect and may
			// complete after linked disposition.
			waitFor(t, func() bool {
				connection.mutex.Lock()
				defer connection.mutex.Unlock()
				return len(connection.published) == 2
			})
			connection.mutex.Lock()
			publications := append([]mqttPublication(nil), connection.published...)
			connection.mutex.Unlock()
			if len(publications) != 2 || publications[0].topic != "zigbee2mqtt/synthetic-wanda-bulb/set" ||
				publications[0].payload != test.setPayload || publications[0].qos != mqttQoS ||
				publications[1].topic != "zigbee2mqtt/synthetic-wanda-bulb/get" ||
				publications[1].payload != `{"color_temp_startup":""}` {
				t.Fatalf("MQTT publications = %#v", publications)
			}
			if responder.accepted != 1 || session.linked[0].EntityID != startup.entityID ||
				string(session.linked[0].Value) != test.linked {
				t.Fatalf("responder=%#v linked=%#v", responder, session.linked)
			}
		})
	}
}

// wandaReconciled builds the Wanda fixture route snapshot and requests
// startup state in one step for reconciliation assertions.
func wandaReconciled(t *testing.T) (routeSnapshot, *fakeConnection) {
	t.Helper()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m := newRuntimeAdapter(t, session, &fakeDialer{})
	catalog := mustEmbeddedProfileCatalog(t)
	inventory, err := discoverInventory(readFixture(t, "bridge-devices-wanda-synthetic.json"), catalog)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := z2m.buildRouteSnapshot(context.Background(), 1, inventory)
	if err != nil {
		t.Fatal(err)
	}
	connection := newFakeConnection(recorder)
	if err = z2m.requestCurrentState(context.Background(), connection, inventory, snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot, connection
}

// This test protects read-only reconciliation for the new sensor and the
// effect action: neither creates a command route, while every controllable
// entity does.
func TestBulbReconcileSeparatesNewRoutes(t *testing.T) {
	t.Parallel()
	snapshot, _ := wandaReconciled(t)
	for _, entity := range snapshot.devices["synthetic-wanda-bulb"].entities {
		_, routed := snapshot.routes[entity.entityID]
		switch entity.plan.Descriptor.Key {
		case "linkquality", "colormode":
			if routed {
				t.Fatalf("read-only %q created a command route", entity.plan.Descriptor.Key)
			}
		default:
			if !routed {
				t.Fatalf("%q is missing its command route", entity.plan.Descriptor.Key)
			}
		}
	}
}

// This test protects the startup refresh contract for the new settings:
// startup and power-on behavior request refresh like other observed
// entities, while linkquality, effect, and mode request none.
func TestBulbReconcileRefreshesNewSettings(t *testing.T) {
	t.Parallel()
	_, connection := wandaReconciled(t)
	refreshes := make(map[string]bool)
	for _, publication := range connection.published {
		if !strings.HasSuffix(publication.topic, "/get") {
			t.Fatalf("unexpected publication: %#v", publication)
		}
		refreshes[publication.payload] = true
	}
	for _, payload := range []string{
		`{"state":""}`,
		`{"brightness":""}`,
		`{"color_temp":""}`,
		`{"color_temp_startup":""}`,
		`{"power_on_behavior":""}`,
	} {
		if !refreshes[payload] {
			t.Fatalf("refresh payloads = %v", refreshes)
		}
	}
	for _, payload := range []string{`{"linkquality":""}`, `{"effect":""}`, `{"color_mode":""}`} {
		if refreshes[payload] {
			t.Fatalf("read-only/stateless refresh published %q: %v", payload, refreshes)
		}
	}
}
