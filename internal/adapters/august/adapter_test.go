package august //nolint:testpackage // Tests acquisition, registration and field ordering at their owner.

import (
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/entitytypes/lockv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func TestMonitoringRegistersOnlyConfiguredLockWithThreeStableEntities(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	name := "Front"
	a := newTestAdapter(t, session, syntheticTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case inventoryPath:
			return response(200, syntheticInventory(name)), nil
		case "/locks/" + string(testLockID):
			return response(200, syntheticDetail(name, onlineBridge)), nil
		case "/locks/" + string(testLockID) + "/status":
			return response(200, `{"status":"locked","doorState":"closed","dateTime":"2020-01-01T00:00:00Z"}`), nil
		default:
			t.Fatal("unconfigured lock requested")
			return nil, errors.New("unexpected route")
		}
	}))
	runCycle(t, a, true)
	registration := session.registrations[0]
	if registration.BindingKey != "front" || registration.Device.Kind != "lock" ||
		*registration.Device.ExternalID != string(testLockID) ||
		len(registration.Entities) != 3 {
		t.Fatalf("incorrect identity/registration: %+v", registration)
	}
	if len(session.availability) != 3 || session.health.Status != adapter.HealthHealthy {
		t.Fatal("missing availability or healthy account")
	}
	for _, entity := range registration.Entities {
		if entity.ExternalID != string(testLockID)+"/"+entity.Key ||
			strings.Contains(string(entity.Support), `"set"`) ||
			entity.InitiallyEnabled != nil {
			t.Fatal("unstable identity, control enabled or enablement overridden")
		}
	}
	name = "Renamed"
	runCycle(t, a, true)
	if session.registrations[1].Device.Name != name || session.registrations[1].BindingKey != registration.BindingKey {
		t.Fatal("rename changed identity or was ignored")
	}
}

func TestOfflineAndMissingBridgeRequireDetailRecoveryBeforeAvailability(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	bridge := onlineBridge
	details := 0
	statusFails := false
	a := newTestAdapter(t, session, syntheticTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case inventoryPath:
			return response(200, syntheticInventory("Front")), nil
		case "/locks/" + string(testLockID):
			details++
			return response(200, syntheticDetail("Front", bridge)), nil
		default:
			if statusFails {
				return response(503, ""), nil
			}
			return response(200, `{"status":"locked","doorState":"closed","dateTime":"2020-01-01T00:00:00Z"}`), nil
		}
	}))
	runCycle(t, a, true)
	statusFails = true
	runCycle(t, a, false)
	assertBindingAvailability(t, session, a.devices["front"], adapter.AvailabilityUnavailable)
	statusFails = false
	bridge = `{"operative":true}`
	count := len(session.observations)
	runCycle(t, a, false)
	assertBindingAvailability(t, session, a.devices["front"], adapter.AvailabilityUnavailable)
	if len(session.observations) != count || details != 2 {
		t.Fatal("cached status restored availability without explicit online detail")
	}
	bridge = onlineBridge
	runCycle(t, a, false)
	assertBindingAvailability(t, session, a.devices["front"], adapter.AvailabilityAvailable)
	if details != 3 {
		t.Fatal("recovery detail was not reacquired on scheduled cycle")
	}
}

func assertBindingAvailability(
	t *testing.T,
	session *recordingSession,
	runtime *deviceRuntime,
	want adapter.EntityAvailabilityStatus,
) {
	t.Helper()
	for _, id := range runtime.ids {
		if got := session.availability[id].Status; got != want {
			t.Fatalf("availability=%s, want %s", got, want)
		}
	}
}

func TestFailedInventoryIsNotOmissionAndMissingBindingDoesNotRecreate(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	inventory := syntheticInventory("Front")
	a := newTestAdapter(t, session, syntheticTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == inventoryPath {
			return response(200, inventory), nil
		}
		if strings.HasSuffix(r.URL.Path, "/status") {
			return response(200, `{"status":"locked","doorState":"closed"}`), nil
		}
		return response(200, syntheticDetail("Front", onlineBridge)), nil
	}))
	runCycle(t, a, true)
	entry := a.devices["front"].entry
	inventory = `{"partial":null}`
	if complete, err := a.cycle(t.Context(), true); err != nil || complete {
		t.Fatal("partial listing was accepted as complete")
	}
	if a.devices["front"].entry != entry || session.health.Status != adapter.HealthUnhealthy {
		t.Fatal("failed listing removed membership or remained healthy")
	}
	inventory = `{}`
	runCycle(t, a, true)
	if a.devices["front"].entry != nil || session.health.Status != adapter.HealthHealthy ||
		len(session.registrations) != 1 {
		t.Fatal("missing target affected account health or recreated registration")
	}
	assertBindingAvailability(t, session, a.devices["front"], adapter.AvailabilityUnavailable)
	restarted := newTestAdapter(t, session, a.client.http.Transport)
	if err := restarted.loadMappings(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertBindingAvailability(t, session, restarted.devices["front"], adapter.AvailabilityUnavailable)
}

func TestFieldOrderingIsIndependentAndOnlySuccessfulPublicationAdvances(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	a := newTestAdapter(t, session, nil)
	runtime := a.devices["front"]
	runtime.ids = map[string]string{
		"lock":      "ent_" + "00000000-0000-4000-8000-000000000001",
		"door-open": "ent_" + "00000000-0000-4000-8000-000000000002",
		"battery":   "ent_" + "00000000-0000-4000-8000-000000000003",
	}
	older := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	initial := report{
		ReceivedAt: time.Now(),
		Lock:       &reading[lockv1.State]{Value: "locked", SourceUpdatedAt: &newer},
		DoorOpen:   &reading[bool]{Value: false, SourceUpdatedAt: &older},
	}
	if err := a.publish(t.Context(), runtime, initial, []string{"lock", "door-open"}); err != nil {
		t.Fatal(err)
	}
	for _, source := range []*time.Time{&older, &newer, nil} {
		r := report{ReceivedAt: time.Now(), Lock: &reading[lockv1.State]{Value: "unlocked", SourceUpdatedAt: source}}
		if err := a.publish(t.Context(), runtime, r, []string{"lock"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(session.observations) != 2 || runtime.fields["lock"].value != `"locked"` {
		t.Fatal("older/equal-conflicting/untimestamped snapshot replaced ordered State")
	}
	if err := a.unavailable(t.Context(), runtime, "device_offline"); err != nil {
		t.Fatal(err)
	}
	if err := a.publish(t.Context(), runtime, initial, []string{"lock", "door-open"}); err != nil {
		t.Fatal(err)
	}
	if !runtime.fields["door-open"].available {
		t.Fatal("older unchanged DoorSense was compared to lock timestamp")
	}
	session.publishErr = errors.New("synthetic publish failure")
	future := newer.Add(time.Hour)
	r := report{ReceivedAt: time.Now(), Lock: &reading[lockv1.State]{Value: "unlocked", SourceUpdatedAt: &future}}
	if err := a.publish(
		t.Context(),
		runtime,
		r,
		[]string{"lock"},
	); err == nil ||
		!runtime.fields["lock"].source.Equal(newer) {
		t.Fatal("failed publication advanced ordering")
	}
}

func TestSameCycleLockReadsDoNotSerializeDifferentLocks(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var details atomic.Int32
	config := testConfig()
	config.Bindings = append(config.Bindings, BindingConfig{BindingKey: "deck", LockID: string(testOtherID)})
	transport := syntheticTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == inventoryPath {
			return response(200, strings.ReplaceAll(syntheticInventory("Front"), `"user"`, `"superuser"`)), nil
		}
		if strings.HasSuffix(r.URL.Path, "/status") {
			return response(200, `{"status":"locked","doorState":"closed"}`), nil
		}
		details.Add(1)
		started <- struct{}{}
		<-release
		body := syntheticDetail("Front", onlineBridge)
		if strings.Contains(r.URL.Path, string(testOtherID)) {
			body = strings.ReplaceAll(body, string(testLockID), string(testOtherID))
		}
		return response(200, body), nil
	})
	a, err := New(session, config, testCredentials(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.client.session = authenticatedClient(t, nil).session
	done := make(chan error, 1)
	go func() { _, cycleErr := a.cycle(t.Context(), true); done <- cycleErr }()
	<-started
	<-started
	close(release)
	if err = <-done; err != nil || details.Load() != 2 {
		t.Fatal("independent detail acquisitions did not complete")
	}
}

func TestStartupDefersAvailabilityUntilHealthyInventory(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	session.requireHealthy = true
	session.bindings["front"] = adapter.Binding{
		BindingKey: "front",
		Entities:   []adapter.EntityBinding{{Key: "lock", EntityID: "ent_existing"}},
	}
	a := newTestAdapter(
		t,
		session,
		syntheticTransport(func(_ *http.Request) (*http.Response, error) { return response(503, ""), nil }),
	)
	if err := a.loadMappings(t.Context()); err != nil {
		t.Fatalf("mapping load reported availability while health was unknown: %v", err)
	}
	if len(session.availability) != 0 {
		t.Fatal("startup availability reported before inventory")
	}
	if _, err := a.cycle(t.Context(), true); err != nil {
		t.Fatalf("failed inventory attempted unavailable reports after unhealthy health: %v", err)
	}
	if session.health.Status != adapter.HealthUnhealthy {
		t.Fatal("failed inventory did not establish unhealthy account")
	}
}

func TestStatusInvalidBatteryWithdrawsOnlyPresentInvalidEvidence(t *testing.T) {
	t.Parallel()
	for _, battery := range []string{`,"battery":null`, `,"battery":1.1`, ""} {
		t.Run(battery, func(t *testing.T) {
			t.Parallel()
			session := newRecordingSession()
			a := newTestAdapter(t, session, syntheticTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == inventoryPath {
					return response(200, syntheticInventory("Front")), nil
				}
				if strings.HasSuffix(r.URL.Path, "/status") {
					return response(200, `{"status":"locked","doorState":"closed"`+battery+`}`), nil
				}
				return response(200, syntheticDetail("Front", onlineBridge)), nil
			}))
			runCycle(t, a, true)
			runtime := a.devices["front"]
			if runtime.fields["battery"].available != (battery == "") || !runtime.fields["lock"].available ||
				!runtime.fields["door-open"].available {
				t.Fatal("present invalid battery was ignored or an absent battery invalidated valid detail evidence")
			}
		})
	}
}

func TestInvalidSiblingDoesNotFabricateOrSuppressValidReadings(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	a := newTestAdapter(t, session, nil)
	runtime := a.devices["front"]
	if err := a.register(
		t.Context(),
		runtime,
		device{ID: testLockID, Name: "Front", Model: supportedModel},
	); err != nil {
		t.Fatal(err)
	}
	first := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	initial := report{
		ReceivedAt: time.Now(),
		Lock:       &reading[lockv1.State]{Value: "locked", SourceUpdatedAt: &first},
		DoorOpen:   &reading[bool]{Value: false, SourceUpdatedAt: &first},
		Battery:    &reading[float64]{Value: 0},
	}
	keys := []string{"lock", "door-open", "battery"}
	if err := a.publish(t.Context(), runtime, initial, keys); err != nil {
		t.Fatal(err)
	}
	initial.DoorOpen = nil
	if err := a.publish(t.Context(), runtime, initial, keys); err != nil {
		t.Fatal(err)
	}
	if runtime.fields["door-open"].available || !runtime.fields["battery"].available ||
		!runtime.fields["lock"].available ||
		len(session.observations) != 5 {
		t.Fatal("invalid door contaminated siblings or fabricated a reading")
	}
	older := first.Add(-time.Hour)
	initial.DoorOpen = &reading[bool]{Value: true, SourceUpdatedAt: &older}
	if err := a.publish(t.Context(), runtime, initial, []string{"door-open"}); err != nil {
		t.Fatal(err)
	}
	if runtime.fields["door-open"].available || len(session.observations) != 5 {
		t.Fatal("out-of-order field restored availability")
	}
}
