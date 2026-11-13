package levoit //nolint:testpackage // Exercises the Adapter's private poll/command boundary.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

type recordingSession struct {
	mu            sync.Mutex
	bindings      map[string]adapter.Binding
	registrations []adapter.Registration
	observations  []adapter.Observation
	availability  map[string]adapter.EntityAvailabilityReport
	health        adapter.HealthReport
}

func newRecordingSession() *recordingSession {
	return &recordingSession{
		bindings:     make(map[string]adapter.Binding),
		availability: make(map[string]adapter.EntityAvailabilityReport),
	}
}

func (s *recordingSession) Register(_ context.Context, registration adapter.Registration) (adapter.Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registrations = append(s.registrations, registration)
	if binding, ok := s.bindings[registration.BindingKey]; ok {
		return binding, nil
	}
	binding := adapter.Binding{BindingKey: registration.BindingKey, DeviceID: "dev_" + uuid.Must(uuid.NewV7()).String()}
	for _, entity := range registration.Entities {
		binding.Entities = append(
			binding.Entities,
			adapter.EntityBinding{Key: entity.Key, EntityID: "ent_" + uuid.Must(uuid.NewV7()).String(), Enabled: true},
		)
	}
	s.bindings[registration.BindingKey] = binding
	return binding, nil
}

func (s *recordingSession) ListOwnedMappings(
	_ context.Context,
	_ adapter.OwnedMappingPageRequest,
) (adapter.OwnedMappingPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var page adapter.OwnedMappingPage
	for key, binding := range s.bindings {
		for _, entity := range binding.Entities {
			page.Items = append(
				page.Items,
				adapter.OwnedMapping{
					BindingKey: key,
					DeviceID:   binding.DeviceID,
					EntityKey:  entity.Key,
					EntityID:   entity.EntityID,
				},
			)
		}
	}
	return page, nil
}

func (s *recordingSession) SetHealth(_ context.Context, report adapter.HealthReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health = report
	return nil
}

func (s *recordingSession) ReportEntityAvailability(
	_ context.Context,
	reports []adapter.EntityAvailabilityReport,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, report := range reports {
		s.availability[report.EntityID] = report
	}
	return nil
}

func (s *recordingSession) PublishObservation(
	_ context.Context,
	report adapter.Observation,
) (adapter.ObservationID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observations = append(s.observations, report)
	return "obs-synthetic", nil
}

type recordingResponder struct {
	accepted bool
	rejected string
	linked   []adapter.Observation
}

func (r *recordingResponder) Accept() (adapter.CommandEvidence, error) {
	r.accepted = true
	return r, nil
}
func (r *recordingResponder) Reject(message string) error { r.rejected = message; return nil }
func (r *recordingResponder) RejectUnavailable(message string) error {
	r.rejected = message
	return nil
}

func (r *recordingResponder) PublishObservation(
	_ context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	r.linked = append(r.linked, observation)
	return "obs-linked", nil
}

func newTestAdapter(t *testing.T, session *recordingSession, transport http.RoundTripper) *Adapter {
	t.Helper()
	a, err := New(
		session,
		Config{PollInterval: time.Minute, TimeZone: "UTC"},
		Credentials{
			Email:    "synthetic@example.com",
			Password: "unused",
		},
		transport,
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatal(err)
	}
	a.client.session = authSession{token: "token", accountID: "account"}
	return a
}

func syntheticInventory(name string) string {
	return fmt.Sprintf(
		`{"code":0,"result":{"total":1,"list":[{"cid":"purifier","deviceType":"Core400S","deviceName":%q,"configModule":"module","connectionStatus":"online"}]}}`,
		name,
	)
}

func TestSensorDescriptorsDeclareUnitsAndKind(t *testing.T) {
	t.Parallel()
	a := newTestAdapter(t, newRecordingSession(), nil)
	plans, err := a.entities(device{CID: "purifier"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct{ unit, kind string }{
		"pm25":        {"µg/m³", "particulate_matter"},
		"filter-life": {"%", ""},
	}
	for _, plan := range plans {
		expected, ok := want[plan.descriptor.Key]
		if !ok {
			continue
		}
		var support struct {
			State struct {
				Unit string  `json:"unit"`
				Kind *string `json:"quantity_kind"`
			} `json:"state"`
		}
		if err = json.Unmarshal(plan.descriptor.Support, &support); err != nil {
			t.Fatal(err)
		}
		if support.State.Unit != expected.unit || (expected.kind == "" && support.State.Kind != nil) ||
			(expected.kind != "" && (support.State.Kind == nil || *support.State.Kind != expected.kind)) {
			t.Errorf("%s support = %s", plan.descriptor.Key, plan.descriptor.Support)
		}
		delete(want, plan.descriptor.Key)
	}
	if len(want) != 0 {
		t.Errorf("missing sensor descriptors: %v", want)
	}
}

//nolint:gocognit // One ordered recovery journey with explicit assertions.
func TestRegistrationPartialStateAndRestartMembership(
	t *testing.T,
) {
	t.Parallel()
	session := newRecordingSession()
	name := "Original purifier"
	missing, invalid := false, false
	transport := syntheticTransport(func(r *http.Request) (*http.Response, error) {
		if invalid {
			return response(`{"code":-11103000}`), nil
		}
		if r.URL.Path == inventoryPath {
			if missing {
				return response(`{"code":0,"result":{"total":0,"list":[]}}`), nil
			}
			return response(syntheticInventory(name)), nil
		}
		return response(
			`{"code":0,"result":{"code":0,"result":{"enabled":true,"mode":"manual","level":2,"configuration":{"display":false},"child_lock":false,"air_quality_value":12.5,"filter_life":null}}}`,
		), nil
	})
	a := newTestAdapter(t, session, transport)
	if err := a.cycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(session.registrations) != 1 || session.registrations[0].Device.Kind != "air-purifier" ||
		len(session.registrations[0].Entities) != 7 ||
		len(session.observations) != 6 ||
		session.health.Status != adapter.HealthHealthy {
		t.Fatal("incorrect Device registration or partial publication")
	}
	if len(session.registrations[0].BindingKey) > 63 {
		t.Fatal("Binding exceeds slug limit")
	}
	var filterID string
	for _, mapping := range session.bindings[bindingKey("purifier")].Entities {
		if mapping.Key == "filter-life" {
			filterID = mapping.EntityID
		}
	}
	if session.availability[filterID].Status != adapter.AvailabilityUnavailable {
		t.Fatal("missing filter became available")
	}
	name = "Renamed purifier"
	if err := a.cycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if session.registrations[1].BindingKey != session.registrations[0].BindingKey ||
		session.registrations[1].Device.Name != name {
		t.Fatal("rename changed identity or was ignored")
	}
	invalid = true
	if err := a.cycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if session.health.Status != adapter.HealthUnhealthy || len(a.routes) != 5 {
		t.Fatal("failed inventory removed membership or remained healthy")
	}
	invalid, missing = false, true
	restarted := newTestAdapter(t, session, transport)
	if err := restarted.loadMappings(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.cycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, report := range session.availability {
		if report.Status != adapter.AvailabilityUnavailable || !strings.HasSuffix(report.ReasonCode, "device_missing") {
			t.Fatal("restart forgot omitted Device")
		}
	}
}

func TestAcknowledgedCommandPublishesOptimisticTargetWithoutReading(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	reads, writes := 0, 0
	transport := syntheticTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == inventoryPath {
			return response(syntheticInventory("Purifier")), nil
		}
		body := requestBody(t, r)
		var payload struct {
			Method string `json:"method"`
			Data   struct {
				Enabled bool `json:"enabled"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body["payload"], &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Method == "setSwitch" {
			writes++
			return response(`{"code":0,"result":{"code":0}}`), nil
		}
		reads++
		return response(
			`{"code":0,"result":{"code":0,"result":{"enabled":false,"mode":"manual","level":1,"air_quality_value":4}}}`,
		), nil
	})
	a := newTestAdapter(t, session, transport)
	if err := a.cycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	id := a.devices[bindingKey("purifier")].ids["power"]
	responder := &recordingResponder{}
	command := adapter.Command{
		EntityID:      id,
		OperationName: "set",
		Parameters:    json.RawMessage(`{"value":true}`),
		Deadline:      time.Now().Add(10 * time.Second).UTC().Format(time.RFC3339Nano),
	}
	if err := a.HandleCommand(t.Context(), command, responder); err != nil {
		t.Fatal(err)
	}
	if writes != 1 || reads != 1 || !responder.accepted || len(responder.linked) != 1 ||
		responder.linked[0].EntityID != id ||
		string(responder.linked[0].Value) != "true" {
		t.Fatalf("command evidence = %+v", responder)
	}
	if err := a.CloseCommands(t.Context()); err != nil {
		t.Fatal(err)
	}
	stopped := &recordingResponder{}
	if err := a.HandleCommand(t.Context(), command, stopped); err != nil || stopped.accepted || stopped.rejected == "" {
		t.Fatal("accepted command after admission closed")
	}
}

func TestDeviceGateDoesNotSpendExpiredCommandOrLeaveUnjoinedWork(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	a := newTestAdapter(
		t,
		session,
		syntheticTransport(func(_ *http.Request) (*http.Response, error) { return nil, errors.New("must not mutate") }),
	)
	runtime := &deviceRuntime{gate: make(chan struct{}, 1), active: true}
	runtime.gate <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	mutated := false
	if err := a.execute(
		ctx,
		runtime,
		"power",
		&recordingResponder{},
		func(device) error { mutated = true; return nil },
		status{},
	); !errors.Is(err, context.Canceled) ||
		mutated {
		t.Fatal("expired operation reached mutation")
	}
	<-runtime.gate
}

func TestAmbiguousMutationDoesNotClaimRejection(t *testing.T) {
	t.Parallel()
	a := newTestAdapter(t, newRecordingSession(), nil)
	runtime := &deviceRuntime{gate: make(chan struct{}, 1), active: true}
	for _, failure := range []*providerError{
		{code: reasonProviderUnavailable, uncertain: true},
		{code: reasonInvalidResponse, uncertain: true},
	} {
		responder := &recordingResponder{}
		calls := 0
		err := a.execute(
			t.Context(),
			runtime,
			"power",
			responder,
			func(device) error { calls++; return failure },
			status{},
		)
		if err == nil || responder.accepted || responder.rejected != "" || calls != 1 || len(responder.linked) != 0 {
			t.Fatal("ambiguous mutation acquired false outcome evidence")
		}
	}
	definite := &recordingResponder{}
	if err := a.execute(
		t.Context(),
		runtime,
		"power",
		definite,
		func(device) error { return &providerError{code: "device_offline"} },
		status{},
	); err != nil ||
		definite.rejected == "" {
		t.Fatal("explicit provider rejection was lost")
	}
}

func TestMembershipWithdrawsRoutesBeforeDeviceGateWait(t *testing.T) {
	t.Parallel()
	a := newTestAdapter(t, newRecordingSession(), nil)
	runtime := &deviceRuntime{gate: make(chan struct{}, 1), active: true, ids: map[string]string{"power": "old-entity"}}
	runtime.gate <- struct{}{}
	a.devices[bindingKey("old")] = runtime
	a.routes["old-entity"] = func(context.Context, adapter.Command, adapter.Responder) error {
		t.Error("omitted route invoked")
		return nil
	}
	missing := a.reconcileMembership([]device{{CID: "remaining"}})
	if len(missing) != 1 || runtime.active || len(a.routes) != 0 {
		t.Fatal("omitted membership waited for the Device gate")
	}
	responder := &recordingResponder{}
	if err := a.HandleCommand(
		t.Context(),
		adapter.Command{EntityID: "old-entity"},
		responder,
	); err != nil ||
		responder.rejected == "" {
		t.Fatal("new command reached omitted Device")
	}
	<-runtime.gate
}
