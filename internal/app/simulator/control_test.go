package simulator_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	appsimulator "github.com/mholtzscher/hearth/internal/app/simulator"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

type controlFakeSession struct {
	mu                    sync.Mutex
	observations          []adapter.Observation
	observationIDs        []adapter.ObservationID
	events                []adapter.EntityEvent
	eventIDs              []adapter.EntityEventID
	publishObservationErr error
	publishEventErr       error
}

func (session *controlFakeSession) PublishObservation(
	_ context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.observations = append(session.observations, observation)
	issued := adapter.ObservationID(fmt.Sprintf("obs_%d", len(session.observations)))
	session.observationIDs = append(session.observationIDs, issued)
	// The SDK can return the ID it minted alongside a failure; the control
	// channel must not surface that as a success ID.
	if session.publishObservationErr != nil {
		return issued, session.publishObservationErr
	}
	return issued, nil
}

func (session *controlFakeSession) PublishEntityEvent(
	_ context.Context,
	event adapter.EntityEvent,
) (adapter.EntityEventID, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.events = append(session.events, event)
	issued := adapter.EntityEventID(fmt.Sprintf("evt_%d", len(session.events)))
	session.eventIDs = append(session.eventIDs, issued)
	if session.publishEventErr != nil {
		return issued, session.publishEventErr
	}
	return issued, nil
}

func (session *controlFakeSession) SetHealth(_ context.Context, _ adapter.HealthReport) error {
	return nil
}

func (session *controlFakeSession) ReportEntityAvailability(
	_ context.Context,
	_ []adapter.EntityAvailabilityReport,
) error {
	return nil
}

// issuedObservationIDs returns the Observation IDs the fake Session minted, in
// publication order: the oracle publish responses are compared against.
func (session *controlFakeSession) issuedObservationIDs() []adapter.ObservationID {
	session.mu.Lock()
	defer session.mu.Unlock()
	return append([]adapter.ObservationID(nil), session.observationIDs...)
}

func (session *controlFakeSession) issuedEventIDs() []adapter.EntityEventID {
	session.mu.Lock()
	defer session.mu.Unlock()
	return append([]adapter.EntityEventID(nil), session.eventIDs...)
}

// failPublishes makes every later publication fail while still minting an ID,
// mirroring the SDK's ambiguous-failure behaviour.
func (session *controlFakeSession) failPublishes(err error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.publishObservationErr = err
	session.publishEventErr = err
}

func controlPowerDevice() scripted.DeviceSpec {
	return scripted.DeviceSpec{
		BindingKey: "simulated-light",
		Name:       "Simulated light",
		Kind:       "light",
		Entities: []scripted.EntitySpec{{
			Key:     "power",
			Name:    "Power",
			Type:    "hearth.power/v1",
			Support: map[string]any{"state": map[string]any{}, "operations": map[string]any{"set": map[string]any{}}},
			Initial: true,
		}},
	}
}

func controlEventDevice() scripted.DeviceSpec {
	return scripted.DeviceSpec{
		BindingKey: "simulated-button",
		Name:       "Simulated button",
		Kind:       "sensor",
		Entities: []scripted.EntitySpec{{
			Key:  "events",
			Name: "Events",
			Type: "hearth.enumevent/v1",
			Support: map[string]any{
				"state":      map[string]any{},
				"operations": map[string]any{},
				"events":     map[string]any{"names": []any{"single_press", "double_press"}},
			},
		}},
	}
}

// startControlServer runs the control channel on addr against a runtime built
// from devices. Every Entity gets the Entity ID "ent_" + its key.
func startControlServer(
	t *testing.T,
	addr string,
	devices []scripted.DeviceSpec,
) (*controlFakeSession, string) {
	t.Helper()
	session := &controlFakeSession{}
	runtime, newErr := scripted.New(session, devices)
	if newErr != nil {
		t.Fatal(newErr)
	}
	bindings := make([]adapter.Binding, 0, len(devices))
	for _, device := range devices {
		binding := adapter.Binding{BindingKey: device.BindingKey}
		for _, entity := range device.Entities {
			binding.Entities = append(binding.Entities, adapter.EntityBinding{
				Key:      entity.Key,
				EntityID: "ent_" + entity.Key,
			})
		}
		bindings = append(bindings, binding)
	}
	if attachErr := runtime.Attach(bindings); attachErr != nil {
		t.Fatal(attachErr)
	}
	if initErr := runtime.Initialize(context.Background()); initErr != nil {
		t.Fatal(initErr)
	}
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go func() {
		_ = appsimulator.ServeControl(ctx, addr, runtime, nil)
	}()
	base := "http://" + addr
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, getErr := http.Get(base + "/v1/sim/entities")
		if getErr == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return session, base
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("control channel did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

//nolint:unparam // Path is retained for other control GET endpoints.
func controlGet(t *testing.T, base, path string) (int, []byte) {
	t.Helper()
	response, getErr := http.Get(base + path)
	if getErr != nil {
		t.Fatal(getErr)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		t.Fatal(readErr)
	}
	return response.StatusCode, body
}

func controlPost(t *testing.T, base, path, body string) (int, []byte) {
	t.Helper()
	response, postErr := http.Post(base+path, "application/json", strings.NewReader(body))
	if postErr != nil {
		t.Fatal(postErr)
	}
	defer response.Body.Close()
	payload, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		t.Fatal(readErr)
	}
	return response.StatusCode, payload
}

//nolint:gocognit // Checks inventory ownership and publication routing in one scenario.
func TestControlChannelRoutesAcrossAdapters(t *testing.T) {
	t.Parallel()
	runtimes := make(map[string]*scripted.Runtime)
	sessions := make(map[string]*controlFakeSession)
	for _, id := range []string{"healthy", "faulted"} {
		session := &controlFakeSession{}
		runtime, err := scripted.New(session, []scripted.DeviceSpec{controlPowerDevice()})
		if err != nil {
			t.Fatal(err)
		}
		if attachErr := runtime.Attach([]adapter.Binding{{
			BindingKey: "simulated-light",
			Entities:   []adapter.EntityBinding{{Key: "power", EntityID: "ent_" + id}},
		}}); attachErr != nil {
			t.Fatal(attachErr)
		}
		if initializeErr := runtime.Initialize(context.Background()); initializeErr != nil {
			t.Fatal(initializeErr)
		}
		runtimes[id], sessions[id] = runtime, session
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	base := "http://127.0.0.1:18091"
	go func() { _ = appsimulator.ServeAdapterControl(ctx, "127.0.0.1:18091", runtimes, nil) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := http.Get(base + "/v1/sim/entities")
		if err == nil {
			response.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("multi-adapter control listener did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, body := controlGet(t, base, "/v1/sim/entities")
	var inventory []map[string]json.RawMessage
	if err := json.Unmarshal(body, &inventory); err != nil {
		t.Fatal(err)
	}
	if len(inventory) != 2 {
		t.Fatalf("inventory = %s, want both adapters", body)
	}
	for _, entry := range inventory {
		var id, entityID string
		_ = json.Unmarshal(entry["adapter_id"], &id)
		_ = json.Unmarshal(entry["entity_id"], &entityID)
		if entityID != "ent_"+id || (id != "healthy" && id != "faulted") {
			t.Fatalf("incorrect adapter ownership: %s", body)
		}
	}
	status, published := controlPost(t, base, "/v1/sim/entities/ent_faulted/publish", `{"value":false}`)
	if status != http.StatusOK || !strings.Contains(string(published), `"adapter_id":"faulted"`) {
		t.Fatalf("faulted adapter publish: status=%d body=%s", status, published)
	}
	if len(sessions["faulted"].issuedObservationIDs()) != 2 || len(sessions["healthy"].issuedObservationIDs()) != 1 {
		t.Fatal("publish crossed adapter session boundary")
	}
}

// controlPublishBody decodes a control publish response. The ID fields are
// pointers so a test can tell an absent field from an empty one.
type controlPublishBody struct {
	BindingKey    string          `json:"binding_key"`
	Key           string          `json:"key"`
	EntityID      string          `json:"entity_id"`
	EntityType    string          `json:"entity_type"`
	EventSource   bool            `json:"event_source"`
	Paused        bool            `json:"paused"`
	Current       json.RawMessage `json:"current"`
	ObservationID *string         `json:"observation_id"`
	EventID       *string         `json:"event_id"`
}

func decodeControlPublish(t *testing.T, body []byte) controlPublishBody {
	t.Helper()
	var decoded controlPublishBody
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode publish response %s: %v", body, err)
	}
	return decoded
}

func assertNoPublicationIDKeys(t *testing.T, label string, body []byte) {
	t.Helper()
	var bodyFields map[string]json.RawMessage
	if err := json.Unmarshal(body, &bodyFields); err != nil {
		t.Fatalf("%s body %s is not a JSON object: %v", label, body, err)
	}
	for _, field := range []string{"observation_id", "event_id"} {
		if _, present := bodyFields[field]; present {
			t.Fatalf("%s body %s carries publication ID field %s", label, body, field)
		}
	}
	if _, present := bodyFields["error"]; !present {
		t.Fatalf("%s body %s carries no error field", label, body)
	}
}

// assertControlSnapshotShape protects the flat snapshot contract: every
// snapshot keeps its seven Entity fields and never carries a publication ID.
func assertControlSnapshotShape(t *testing.T, label string, snapshot map[string]json.RawMessage) {
	t.Helper()
	for _, field := range []string{
		"binding_key", "key", "entity_id", "entity_type", "event_source", "paused", "current",
	} {
		if _, present := snapshot[field]; !present {
			t.Fatalf("%s dropped snapshot field %s: %+v", label, field, snapshot)
		}
	}
	for _, field := range []string{"observation_id", "event_id"} {
		if _, present := snapshot[field]; present {
			t.Fatalf("%s exposes publication ID field %s: %+v", label, field, snapshot)
		}
	}
}

func decodeControlObject(t *testing.T, label string, body []byte) map[string]json.RawMessage {
	t.Helper()
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("%s body %s is not a JSON object: %v", label, body, err)
	}
	return decoded
}

func TestControlChannelListsPublishesAndPauses(t *testing.T) {
	t.Parallel()
	_, base := startControlServer(t, "127.0.0.1:18081", []scripted.DeviceSpec{controlPowerDevice()})
	status, body := controlGet(t, base, "/v1/sim/entities")
	if status != http.StatusOK {
		t.Fatalf("list status = %d, body %s", status, body)
	}
	var entities []scripted.EntityInfo
	if err := json.Unmarshal(body, &entities); err != nil {
		t.Fatal(err)
	}
	if len(entities) != 1 || entities[0].EntityID != "ent_power" || string(entities[0].Current) != "true" {
		t.Fatalf("entities = %s, want one power Entity with current true", body)
	}
	status, body = controlPost(t, base, "/v1/sim/entities/ent_power/publish", `{"value":false}`)
	if status != http.StatusOK {
		t.Fatalf("publish status = %d, body %s", status, body)
	}
	published := decodeControlPublish(t, body)
	if string(published.Current) != "false" {
		t.Fatalf("published current = %s, want false", published.Current)
	}
	status, _ = controlPost(t, base, "/v1/sim/entities/ent_power/pause", "")
	if status != http.StatusOK {
		t.Fatalf("pause status = %d", status)
	}
	status, body = controlGet(t, base, "/v1/sim/entities")
	if status != http.StatusOK {
		t.Fatal("list after pause failed")
	}
	if err := json.Unmarshal(body, &entities); err != nil {
		t.Fatal(err)
	}
	if !entities[0].Paused {
		t.Fatal("Entity is not paused after pause")
	}
	status, _ = controlPost(t, base, "/v1/sim/entities/ent_power/resume", "")
	if status != http.StatusOK {
		t.Fatalf("resume status = %d", status)
	}
	status, body = controlPost(t, base, "/v1/sim/entities/ent_missing/publish", `{"value":true}`)
	if status != http.StatusNotFound {
		t.Fatalf("unknown Entity publish status = %d, body %s, want 404", status, body)
	}
	status, body = controlPost(t, base, "/v1/sim/entities/ent_power/publish", `{"value":"on"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid value publish status = %d, body %s, want 400", status, body)
	}
}

// TestControlPublishReturnsSessionIssuedObservationID protects that a State
// publish response keeps the flat snapshot fields and adds exactly the
// Observation ID the Session issued for that publication, so consecutive
// publishes differ.
func TestControlPublishReturnsSessionIssuedObservationID(t *testing.T) {
	t.Parallel()
	session, base := startControlServer(t, "127.0.0.1:18082", []scripted.DeviceSpec{controlPowerDevice()})
	status, body := controlPost(t, base, "/v1/sim/entities/ent_power/publish", `{"value":false}`)
	if status != http.StatusOK {
		t.Fatalf("publish status = %d, body %s", status, body)
	}
	first := decodeControlPublish(t, body)
	issued := session.issuedObservationIDs()
	// One Observation at Initialize, then one per publish.
	if len(issued) != 2 {
		t.Fatalf("Session issued %d Observation IDs, want 2", len(issued))
	}
	if first.ObservationID == nil || *first.ObservationID != string(issued[1]) {
		t.Fatalf("observation_id = %v, want the Session-issued %q", first.ObservationID, issued[1])
	}
	if first.EventID != nil {
		t.Fatalf("State publish response carries event_id %q", *first.EventID)
	}
	if first.EntityID != "ent_power" || first.BindingKey != "simulated-light" ||
		first.Key != "power" || first.EntityType != "hearth.power/v1" {
		t.Fatalf("snapshot identity fields lost: %+v", first)
	}
	if first.EventSource || first.Paused || string(first.Current) != "false" {
		t.Fatalf("snapshot state fields wrong: %+v", first)
	}

	status, body = controlPost(t, base, "/v1/sim/entities/ent_power/publish", `{"value":true}`)
	if status != http.StatusOK {
		t.Fatalf("second publish status = %d, body %s", status, body)
	}
	second := decodeControlPublish(t, body)
	issued = session.issuedObservationIDs()
	if len(issued) != 3 {
		t.Fatalf("Session issued %d Observation IDs, want 3", len(issued))
	}
	if second.ObservationID == nil || *second.ObservationID != string(issued[2]) {
		t.Fatalf("second observation_id = %v, want the Session-issued %q", second.ObservationID, issued[2])
	}
	if first.ObservationID == nil || *first.ObservationID == *second.ObservationID {
		t.Fatalf("consecutive publishes returned observation_id %v then %v; the ID is stale or constant",
			first.ObservationID, second.ObservationID)
	}
	if second.EventID != nil {
		t.Fatalf("second State publish response carries event_id %q", *second.EventID)
	}
}

// TestControlPublishReturnsSessionIssuedEventID protects the event-source half:
// the response adds exactly the Entity Event ID the Session issued.
func TestControlPublishReturnsSessionIssuedEventID(t *testing.T) {
	t.Parallel()
	session, base := startControlServer(t, "127.0.0.1:18083", []scripted.DeviceSpec{controlEventDevice()})
	status, body := controlPost(t, base, "/v1/sim/entities/ent_events/publish", `{"name":"single_press"}`)
	if status != http.StatusOK {
		t.Fatalf("publish status = %d, body %s", status, body)
	}
	first := decodeControlPublish(t, body)
	issued := session.issuedEventIDs()
	// An event source with no script publishes nothing at Initialize.
	if len(issued) != 1 {
		t.Fatalf("Session issued %d Entity Event IDs, want 1", len(issued))
	}
	if first.EventID == nil || *first.EventID != string(issued[0]) {
		t.Fatalf("event_id = %v, want the Session-issued %q", first.EventID, issued[0])
	}
	if first.ObservationID != nil {
		t.Fatalf("event publish response carries observation_id %q", *first.ObservationID)
	}
	if first.EntityID != "ent_events" || first.BindingKey != "simulated-button" ||
		first.Key != "events" || first.EntityType != "hearth.enumevent/v1" {
		t.Fatalf("snapshot identity fields lost: %+v", first)
	}
	if !first.EventSource || first.Paused || string(first.Current) != `"single_press"` {
		t.Fatalf("snapshot state fields wrong: %+v", first)
	}

	status, body = controlPost(t, base, "/v1/sim/entities/ent_events/publish", `{"name":"double_press"}`)
	if status != http.StatusOK {
		t.Fatalf("second publish status = %d, body %s", status, body)
	}
	second := decodeControlPublish(t, body)
	issued = session.issuedEventIDs()
	if len(issued) != 2 {
		t.Fatalf("Session issued %d Entity Event IDs, want 2", len(issued))
	}
	if second.EventID == nil || *second.EventID != string(issued[1]) {
		t.Fatalf("second event_id = %v, want the Session-issued %q", second.EventID, issued[1])
	}
	if first.EventID == nil || *first.EventID == *second.EventID {
		t.Fatalf("consecutive publishes returned event_id %v then %v; the ID is stale or constant",
			first.EventID, second.EventID)
	}
}

// TestControlPublishErrorsCarryNoPublicationIDs protects that a failed publish
// keeps the existing error status and body and never reports a success ID, even
// when the Session minted one before failing.
func TestControlPublishErrorsCarryNoPublicationIDs(t *testing.T) {
	t.Parallel()
	session, base := startControlServer(t, "127.0.0.1:18084", []scripted.DeviceSpec{controlPowerDevice()})
	cases := []struct {
		label  string
		path   string
		body   string
		status int
	}{
		{"invalid value", "/v1/sim/entities/ent_power/publish", `{"value":"on"}`, http.StatusBadRequest},
		{"unknown entity", "/v1/sim/entities/ent_missing/publish", `{"value":true}`, http.StatusNotFound},
		{"malformed body", "/v1/sim/entities/ent_power/publish", `not json`, http.StatusBadRequest},
	}
	for _, testCase := range cases {
		status, body := controlPost(t, base, testCase.path, testCase.body)
		if status != testCase.status {
			t.Fatalf("%s status = %d, body %s, want %d",
				testCase.label, status, body, testCase.status)
		}
		assertNoPublicationIDKeys(t, testCase.label, body)
	}

	session.failPublishes(errors.New("jetstream unavailable"))
	status, body := controlPost(t, base, "/v1/sim/entities/ent_power/publish", `{"value":false}`)
	if status != http.StatusBadRequest {
		t.Fatalf("publisher failure status = %d, body %s, want 400", status, body)
	}
	assertNoPublicationIDKeys(t, "publisher failure", body)
	issued := session.issuedObservationIDs()
	if len(issued) == 0 || issued[len(issued)-1] == "" {
		t.Fatal("fake Session minted no Observation ID before failing, so the test proves nothing")
	}
}

// TestControlEventPublishErrorsCarryNoPublicationIDs protects the event-source
// half of the failure contract: an unsupported event name and an event
// publisher failure both keep the existing error status and body and never
// report a success event_id, even when the Session minted one before failing.
// It also pins that an unsupported name is rejected locally and never reaches
// the Session, so the broker sees no such event.
func TestControlEventPublishErrorsCarryNoPublicationIDs(t *testing.T) {
	t.Parallel()
	session, base := startControlServer(t, "127.0.0.1:18086", []scripted.DeviceSpec{controlEventDevice()})
	status, body := controlPost(t, base, "/v1/sim/entities/ent_events/publish", `{"name":"triple_press"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("unsupported event name status = %d, body %s, want 400", status, body)
	}
	assertNoPublicationIDKeys(t, "unsupported event name", body)
	if issued := session.issuedEventIDs(); len(issued) != 0 {
		t.Fatalf("unsupported event name reached the Session: issued %v", issued)
	}

	session.failPublishes(errors.New("jetstream unavailable"))
	status, body = controlPost(t, base, "/v1/sim/entities/ent_events/publish", `{"name":"single_press"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("event publisher failure status = %d, body %s, want 400", status, body)
	}
	assertNoPublicationIDKeys(t, "event publisher failure", body)
	issued := session.issuedEventIDs()
	if len(issued) == 0 || issued[len(issued)-1] == "" {
		t.Fatal("fake Session minted no Entity Event ID before failing, so the test proves nothing")
	}
}

// TestControlListPauseResumeCarryNoPublicationIDs protects that the unchanged
// list, pause, and resume endpoints keep the flat snapshot shape and publish
// nothing.
func TestControlListPauseResumeCarryNoPublicationIDs(t *testing.T) {
	t.Parallel()
	session, base := startControlServer(t, "127.0.0.1:18085", []scripted.DeviceSpec{controlPowerDevice()})
	status, body := controlGet(t, base, "/v1/sim/entities")
	if status != http.StatusOK {
		t.Fatalf("list status = %d, body %s", status, body)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("list body %s is not a JSON array: %v", body, err)
	}
	if len(rows) != 1 {
		t.Fatalf("list body %s has %d rows, want 1", body, len(rows))
	}
	assertControlSnapshotShape(t, "list snapshot", rows[0])

	status, body = controlPost(t, base, "/v1/sim/entities/ent_power/pause", "")
	if status != http.StatusOK {
		t.Fatalf("pause status = %d, body %s", status, body)
	}
	paused := decodeControlObject(t, "pause", body)
	if string(paused["paused"]) != "true" {
		t.Fatalf("pause body %s does not report paused true", body)
	}
	assertControlSnapshotShape(t, "pause body", paused)

	status, body = controlPost(t, base, "/v1/sim/entities/ent_power/resume", "")
	if status != http.StatusOK {
		t.Fatalf("resume status = %d, body %s", status, body)
	}
	resumed := decodeControlObject(t, "resume", body)
	if string(resumed["paused"]) != "false" {
		t.Fatalf("resume body %s does not report paused false", body)
	}
	assertControlSnapshotShape(t, "resume body", resumed)

	if issued := session.issuedObservationIDs(); len(issued) != 1 {
		t.Fatalf("list, pause, and resume published %d Observations, want the Initialize one only", len(issued))
	}
}
