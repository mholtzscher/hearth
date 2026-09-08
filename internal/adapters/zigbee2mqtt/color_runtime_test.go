package zigbee2mqtt //nolint:testpackage // Command tests exercise the private runtime state machine.

import (
	"context"
	"strings"
	"testing"
	"time"

	contractcolorhsv1 "github.com/mholtzscher/hearth/entitytypes/colorhsv1"
	contractcolortempv1 "github.com/mholtzscher/hearth/entitytypes/colortempv1"
	contractcolorxyv1 "github.com/mholtzscher/hearth/entitytypes/colorxyv1"
)

func entityByKey(device runtimeDevice, key string) runtimeEntity {
	for _, entity := range device.entities {
		if entity.plan.Descriptor.Key == key {
			return entity
		}
	}
	return runtimeEntity{}
}

// echoColorState mirrors one set payload back as observed State, adding the
// companion mode a real bulb reports in the same message. The runtime never
// assembles State across messages, so the echo carries the mode inline.
func echoColorState(payload string) string {
	var mode string
	switch {
	case strings.Contains(payload, `"hue"`):
		mode = `"hs"`
	case strings.Contains(payload, `"color_temp"`):
		mode = `"color_temp"`
	case strings.Contains(payload, `"color"`):
		mode = `"xy"`
	default:
		return payload
	}
	return payload[:len(payload)-1] + `,"color_mode":` + mode + `}`
}

// This test protects exact native XY translation: the set payload carries
// only the discovered color property with exact base-10 decimals, the
// mandatory get carries the discovered property, and the within-tolerance
// active report links the command.
func TestColorXYCommandExactPayloadAndRefresh(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapterFor(t, recorder, session, "bridge-devices-color-dual.json")
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, payload []byte) error {
		if !strings.HasSuffix(topic, "/set") {
			return nil
		}
		return publishState(ctx, z2m, device, echoColorState(string(payload)), time.Now().UTC())
	}
	responder := newFakeResponder(recorder, session)
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(entityByKey(device, "colorxy").entityID, `{"x":3125,"y":3291}`),
		responder,
	); err != nil {
		t.Fatal(err)
	}
	// The satisfying set echo links before the mandatory get is published,
	// so wait for both the link and the refresh publication.
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		connection.mutex.Lock()
		defer connection.mutex.Unlock()
		return len(session.linked) == 1 && len(connection.published) == 2
	})
	connection.mutex.Lock()
	publications := append([]mqttPublication(nil), connection.published...)
	connection.mutex.Unlock()
	if len(publications) != 2 || publications[0].topic != "zigbee2mqtt/fixture-color-dual/set" ||
		publications[0].payload != `{"color":{"x":0.3125,"y":0.3291}}` ||
		publications[1].topic != "zigbee2mqtt/fixture-color-dual/get" ||
		publications[1].payload != `{"color":""}` {
		t.Fatalf("MQTT publications = %#v", publications)
	}
	if responder.accepted != 1 || string(session.linked[0].Value) != `{"active":true,"x":3125,"y":3291}` ||
		session.linked[0].EntityID != entityByKey(device, "colorxy").entityID {
		t.Fatalf("responder=%#v linked=%#v", responder, session.linked)
	}
}

// This test protects exact native HS translation with the same no-leakage
// and mandatory-refresh contract as XY.
func TestColorHSCommandExactPayloadAndRefresh(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapterFor(t, recorder, session, "bridge-devices-color-dual.json")
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, payload []byte) error {
		if !strings.HasSuffix(topic, "/set") {
			return nil
		}
		return publishState(ctx, z2m, device, echoColorState(string(payload)), time.Now().UTC())
	}
	responder := newFakeResponder(recorder, session)
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(entityByKey(device, "colorhs").entityID, `{"hue":120,"saturation":80}`),
		responder,
	); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		connection.mutex.Lock()
		defer connection.mutex.Unlock()
		return len(session.linked) == 1 && len(connection.published) == 2
	})
	connection.mutex.Lock()
	publications := append([]mqttPublication(nil), connection.published...)
	connection.mutex.Unlock()
	if len(publications) != 2 ||
		publications[0].payload != `{"color":{"hue":120,"saturation":80}}` ||
		publications[1].payload != `{"color":""}` {
		t.Fatalf("MQTT publications = %#v", publications)
	}
	if string(session.linked[0].Value) != `{"active":true,"hue":120,"saturation":80}` {
		t.Fatalf("linked=%#v", session.linked)
	}
}

// This test protects typed parameter validation before MQTT publication:
// hue 360 and partial HS parameters fail without any publication.
func TestColorCommandValidationRejectsBeforePublish(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		entity     string
		parameters string
	}{
		{name: "hue 360 is invalid", entity: "colorhs", parameters: `{"hue":360,"saturation":80}`},
		{name: "partial HS is invalid", entity: "colorhs", parameters: `{"hue":120}`},
		{name: "XY out of range is invalid", entity: "colorxy", parameters: `{"x":10001,"y":0}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := &runtimeRecorder{}
			session := newFakeSession(recorder)
			z2m, _, connection, device := commandReadyAdapterFor(t, recorder, session, "bridge-devices-color-dual.json")
			if err := z2m.HandleCommand(
				context.Background(),
				testCommand(entityByKey(device, test.entity).entityID, test.parameters),
				newFakeResponder(recorder, session),
			); err == nil {
				t.Fatal("invalid color command was accepted")
			}
			connection.mutex.Lock()
			defer connection.mutex.Unlock()
			if len(connection.published) != 0 {
				t.Fatalf("invalid command reached MQTT: %#v", connection.published)
			}
		})
	}
}

// This test protects mode-gated matching: a native HS request answered with
// exact coordinates in XY mode never links, while a later complete active
// within-tolerance observation does.
func TestColorHSCommandIgnoresWrongModeAndLinksOnActive(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapterFor(t, recorder, session, "bridge-devices-color-dual.json")
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if strings.HasSuffix(topic, "/set") {
			// Exact HS coordinates reported in XY mode: not HS success.
			return publishState(
				ctx,
				z2m,
				device,
				`{"color":{"x":0.3125,"y":0.3291,"hue":120,"saturation":80},"color_mode":"xy"}`,
				time.Now().UTC(),
			)
		}
		return nil
	}
	responder := newFakeResponder(recorder, session)
	result := make(chan error, 1)
	go func() {
		result <- z2m.HandleCommand(
			context.Background(),
			testCommand(entityByKey(device, "colorhs").entityID, `{"hue":120,"saturation":80}`),
			responder,
		)
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not return after acceptance")
	}
	time.Sleep(100 * time.Millisecond)
	session.mutex.Lock()
	linked := len(session.linked)
	session.mutex.Unlock()
	if linked != 0 {
		t.Fatalf("wrong-mode exact coordinates linked: %#v", session.linked)
	}
	if err := publishState(
		context.Background(),
		z2m,
		device,
		`{"color":{"hue":121,"saturation":80},"color_mode":"hs"}`,
		time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.linked) == 1
	})
	if string(session.linked[0].Value) != `{"active":true,"hue":121,"saturation":80}` {
		t.Fatalf("linked=%#v", session.linked)
	}
}

// This test protects mode-aware temperature outcomes at runtime: an exact
// value in color mode stays ordinary, and only the color_temp-mode report
// links the command.
func TestColorTempCommandRequiresActiveModeReport(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapterFor(t, recorder, session, "bridge-devices-color-dual.json")
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if strings.HasSuffix(topic, "/set") {
			return publishState(
				ctx,
				z2m,
				device,
				`{"color_temp":370,"color_mode":"xy"}`,
				time.Now().UTC(),
			)
		}
		return nil
	}
	responder := newFakeResponder(recorder, session)
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(entityByKey(device, "colortemp").entityID, `{"value":370}`),
		responder,
	); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	session.mutex.Lock()
	linked := len(session.linked)
	session.mutex.Unlock()
	if linked != 0 {
		t.Fatalf("inactive exact temperature linked: %#v", session.linked)
	}
	if err := publishState(
		context.Background(),
		z2m,
		device,
		`{"color_temp":370,"color_mode":"color_temp"}`,
		time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.linked) == 1
	})
	if string(session.linked[0].Value) != `{"active":true,"value":370}` {
		t.Fatalf("linked=%#v", session.linked)
	}
}

// This test protects retained exclusion for color: a retained satisfying
// report stays ordinary, and only a later live report links.
func TestColorCommandLeavesRetainedReportOrdinary(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, coordinator, connection, device := commandReadyAdapterFor(
		t,
		recorder,
		session,
		"bridge-devices-color-dual.json",
	)
	setStarted := make(chan struct{})
	releaseSet := make(chan struct{})
	var once bool
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if strings.HasSuffix(topic, "/set") && !once {
			once = true
			close(setStarted)
			select {
			case <-releaseSet:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	command := testCommand(entityByKey(device, "colorxy").entityID, `{"x":3125,"y":3291}`)
	result := make(chan error, 1)
	go func() {
		result <- z2m.HandleCommand(context.Background(), command, newFakeResponder(recorder, session))
	}()
	<-setStarted
	var attempt *commandAttempt
	waitFor(t, func() bool {
		attempt = coordinator.matchers[entityByKey(device, "colorxy").entityID]
		return attempt != nil
	})
	states, _, err := decodeDeviceState(
		[]byte(`{"color":{"x":0.3125,"y":0.3291},"color_mode":"xy"}`),
		[]runtimeEntity{entityByKey(device, "colorxy")},
		time.Now().UTC(),
	)
	if err != nil || len(states) != 1 {
		t.Fatalf("decode test State: states=%#v err=%v", states, err)
	}
	candidateResult := make(chan stateDisposition, 1)
	z2m.runtimeEvents <- stateCandidate{
		ctx:           context.Background(),
		generation:    attempt.generation,
		routeRevision: attempt.routeRevision,
		entityID:      entityByKey(device, "colorxy").entityID,
		report:        states[0].report,
		retained:      true,
		receivedAt:    time.Now().UTC(),
		result:        candidateResult,
	}
	if disposition := <-candidateResult; disposition != stateOrdinary {
		t.Fatalf("retained disposition = %v, want ordinary", disposition)
	}
	close(releaseSet)
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	// The live report still links after the retained one stayed ordinary.
	if publishErr := publishState(
		context.Background(),
		z2m,
		device,
		`{"color":{"x":0.3125,"y":0.3291},"color_mode":"xy"}`,
		time.Now().UTC(),
	); publishErr != nil {
		t.Fatal(publishErr)
	}
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.linked) == 1
	})
}

// This test protects the read-only mode contract: no mode command is
// registered, so a mode command is unavailable without any publication.
func TestColorModeEntityHasNoCommandRoute(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapterFor(t, recorder, session, "bridge-devices-color-dual.json")
	responder := newFakeResponder(recorder, session)
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(entityByKey(device, "colormode").entityID, `{"operation":"set"}`),
		responder,
	); err != nil {
		t.Fatal(err)
	}
	if responder.unavailable != 1 || responder.accepted != 0 {
		t.Fatalf("responder=%#v", responder)
	}
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	if len(connection.published) != 0 {
		t.Fatalf("mode command reached MQTT: %#v", connection.published)
	}
}

// This test protects color timeouts: an accepted command with no reports
// never links from publication acceptance alone, and a satisfying report
// arriving after the deadline stays ordinary.
func TestColorCommandTimesOutWithoutReports(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapterFor(t, recorder, session, "bridge-devices-color-dual.json")
	connection.onPublish = func(context.Context, *fakeConnection, string, []byte) error { return nil }
	responder := newFakeResponder(recorder, session)
	command := testCommand(entityByKey(device, "colorxy").entityID, `{"x":3125,"y":3291}`)
	command.Deadline = time.Now().Add(50 * time.Millisecond).UTC().Format(time.RFC3339Nano)
	if err := z2m.HandleCommand(context.Background(), command, responder); err != nil {
		t.Fatal(err)
	}
	if responder.accepted != 1 {
		t.Fatalf("accepted = %d", responder.accepted)
	}
	time.Sleep(300 * time.Millisecond)
	if err := publishState(
		context.Background(),
		z2m,
		device,
		`{"color":{"x":0.3125,"y":0.3291},"color_mode":"xy"}`,
		time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if len(session.linked) != 0 {
		t.Fatalf("after-deadline report linked: %#v", session.linked)
	}
	// The satisfying report and its mode sibling stay ordinary.
	if len(session.observations) != 2 ||
		string(session.observations[0].Value) != `{"active":true,"x":3125,"y":3291}` ||
		string(session.observations[1].Value) != `"xy"` {
		t.Fatalf("after-deadline observations = %#v", session.observations)
	}
}

// This deterministic integration test protects per-IEEE FIFO across power,
// brightness, temperature, XY, and HS until evidence disposition completes.
// Unrelated devices stay independent by construction of per-IEEE queues.
func TestColorCommandsForSameIEEEStaySerialized(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapterFor(t, recorder, session, "bridge-devices-color-dual.json")
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, payload []byte) error {
		if !strings.HasSuffix(topic, "/set") {
			return nil
		}
		return publishState(ctx, z2m, device, echoColorState(string(payload)), time.Now().UTC())
	}
	commands := []struct {
		entity     string
		parameters string
	}{
		{entity: "power", parameters: `{"value":true}`},
		{entity: "brightness", parameters: `{"value":50}`},
		{entity: "colortemp", parameters: `{"value":370}`},
		{entity: "colorxy", parameters: `{"x":3125,"y":3291}`},
		{entity: "colorhs", parameters: `{"hue":120,"saturation":80}`},
	}
	for _, command := range commands {
		if err := z2m.HandleCommand(
			context.Background(),
			testCommand(entityByKey(device, command.entity).entityID, command.parameters),
			newFakeResponder(recorder, session),
		); err != nil {
			t.Fatalf("command %s: %v", command.entity, err)
		}
	}
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		connection.mutex.Lock()
		defer connection.mutex.Unlock()
		return len(session.linked) == len(commands) && len(connection.published) == 2*len(commands)
	})
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	var setPayloads []string
	getPayloads := make(map[string]int)
	for _, publication := range connection.published {
		switch {
		case strings.HasSuffix(publication.topic, "/set"):
			setPayloads = append(setPayloads, publication.payload)
		case strings.HasSuffix(publication.topic, "/get"):
			getPayloads[publication.payload]++
		}
	}
	wantSets := []string{
		`{"state":"ON"}`,
		`{"brightness":127}`,
		`{"color_temp":370}`,
		`{"color":{"x":0.3125,"y":0.3291}}`,
		`{"color":{"hue":120,"saturation":80}}`,
	}
	if len(setPayloads) != len(wantSets) {
		t.Fatalf("set payloads = %v, want %v", setPayloads, wantSets)
	}
	for index := range wantSets {
		if setPayloads[index] != wantSets[index] {
			t.Fatalf("set payloads = %v, want %v", setPayloads, wantSets)
		}
	}
	wantGets := map[string]int{
		`{"state":""}`:      1,
		`{"brightness":""}`: 1,
		`{"color_temp":""}`: 1,
		`{"color":""}`:      2,
	}
	if len(getPayloads) != len(wantGets) {
		t.Fatalf("get payloads = %v, want %v", getPayloads, wantGets)
	}
	for payload, count := range wantGets {
		if getPayloads[payload] != count {
			t.Fatalf("get payloads = %v, want %v", getPayloads, wantGets)
		}
	}
}

// This test protects exact command-value encoding at the unit boundary:
// XY decimal rendering without float arithmetic and direct HS rendering.
// Hearth-range command rejection lives in the color NewCommandHandler
// contracts, covered at the translator boundary below and in
// light_validation_boundary_test.go.
func TestColorCommandValueEncoding(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		x, y int64
		want string
	}{
		{x: 0, y: 0, want: `{"x":0,"y":0}`},
		{x: 3125, y: 3291, want: `{"x":0.3125,"y":0.3291}`},
		{x: 5000, y: 100, want: `{"x":0.5,"y":0.01}`},
		{x: 10000, y: 10000, want: `{"x":1,"y":1}`},
	} {
		if encoded := colorXYCommandValue(test.x, test.y); string(encoded) != test.want {
			t.Errorf("colorXYCommandValue(%d,%d) = %s; want %s", test.x, test.y, encoded, test.want)
		}
	}
	if encoded := colorHSCommandValue(120, 80); string(encoded) != `{"hue":120,"saturation":80}` {
		t.Fatalf("colorHSCommandValue(120,80) = %s", encoded)
	}
}

// This test protects representation routing: an HS command against an
// XY-only bulb gains no fabricated route and stays unavailable without any
// publication.
func TestColorUnsupportedRepresentationHasNoRoute(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapterFor(t, recorder, session, "bridge-devices-color-xy-only.json")
	if entityByKey(device, "colorhs").entityID != "" {
		t.Fatal("XY-only bulb planned an HS Entity")
	}
	responder := newFakeResponder(recorder, session)
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand("entity-colorhs", `{"hue":120,"saturation":80}`),
		responder,
	); err != nil {
		t.Fatal(err)
	}
	if responder.unavailable != 1 || responder.accepted != 0 {
		t.Fatalf("responder=%#v", responder)
	}
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	if len(connection.published) != 0 {
		t.Fatalf("unsupported representation reached MQTT: %#v", connection.published)
	}
}

// This test protects matcher type safety: a semantic-type mismatch returns
// false and never panics, so one representation can never satisfy another.
func TestColorMatcherRejectsForeignSemantics(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-color-dual.json")
	entities := bindPlans(device.Entities)
	byKey := make(map[string]entityPlan, len(entities))
	for _, entity := range entities {
		byKey[entity.plan.Descriptor.Key] = entity.plan
	}
	translate := func(plan entityPlan, parameters string) plannedCommand {
		t.Helper()
		planned, err := plan.TranslateCommand(
			context.Background(),
			"entity-test",
			testCommand("entity-test", parameters),
			newFakeResponder(&runtimeRecorder{}, newFakeSession(&runtimeRecorder{})),
		)
		if err != nil {
			t.Fatal(err)
		}
		return planned
	}
	xy := translate(byKey["colorxy"], `{"x":3125,"y":3291}`)
	if !xy.Matches(stateReport{semantic: contractcolorxyv1.State{Active: true, X: 3125, Y: 3291}}) {
		t.Fatal("XY matcher rejected its own satisfying State")
	}
	for _, foreign := range []stateReport{
		{semantic: contractcolorhsv1.State{Active: true, Hue: 120, Saturation: 80}},
		{semantic: contractcolortempv1.State{Active: true, Value: 370}},
		{semantic: true},
	} {
		if xy.Matches(foreign) {
			t.Fatalf("XY matcher accepted foreign semantics %#v", foreign.semantic)
		}
	}
	hs := translate(byKey["colorhs"], `{"hue":120,"saturation":80}`)
	if hs.Matches(stateReport{semantic: contractcolorxyv1.State{Active: true, X: 3125, Y: 3291}}) {
		t.Fatal("HS matcher accepted XY semantics")
	}
	temp := translate(byKey["colortemp"], `{"value":370}`)
	if temp.Matches(stateReport{semantic: contractcolorxyv1.State{Active: true, X: 3125, Y: 3291}}) {
		t.Fatal("temperature matcher accepted XY semantics")
	}
}

// This test protects discovered endpoint property routing: set and refresh
// payload keys use the exposed scoped properties, and the companion mode
// suffix follows the exact upstream endpoint label.
func TestColorEndpointCommandUsesDiscoveredProperties(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapterFor(t, recorder, session, "bridge-devices-color-endpoints.json")
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, payload []byte) error {
		if !strings.HasSuffix(topic, "/set") {
			return nil
		}
		echo := string(payload)
		echo = echo[:len(echo)-1] + `,"color_mode_left":"xy"}`
		return publishState(ctx, z2m, device, echo, time.Now().UTC())
	}
	responder := newFakeResponder(recorder, session)
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(entityByKey(device, "colorxy-ep1").entityID, `{"x":3125,"y":3291}`),
		responder,
	); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		connection.mutex.Lock()
		defer connection.mutex.Unlock()
		return len(session.linked) == 1 && len(connection.published) == 2
	})
	connection.mutex.Lock()
	publications := append([]mqttPublication(nil), connection.published...)
	connection.mutex.Unlock()
	if len(publications) != 2 ||
		publications[0].payload != `{"color_left":{"x":0.3125,"y":0.3291}}` ||
		publications[1].payload != `{"color_left":""}` {
		t.Fatalf("MQTT publications = %#v", publications)
	}
	if string(session.linked[0].Value) != `{"active":true,"x":3125,"y":3291}` {
		t.Fatalf("linked=%#v", session.linked)
	}
}
