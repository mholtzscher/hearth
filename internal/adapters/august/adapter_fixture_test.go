package august //nolint:testpackage // Fixture support for private acquisition and evidence decisions.

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

type recordingSession struct {
	mu             sync.Mutex
	bindings       map[string]adapter.Binding
	registrations  []adapter.Registration
	observations   []adapter.Observation
	availability   map[string]adapter.EntityAvailabilityReport
	health         adapter.HealthReport
	publishErr     error
	requireHealthy bool
}

func newRecordingSession() *recordingSession {
	return &recordingSession{
		bindings:     make(map[string]adapter.Binding),
		availability: make(map[string]adapter.EntityAvailabilityReport),
	}
}

func (s *recordingSession) Register(_ context.Context, r adapter.Registration) (adapter.Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registrations = append(s.registrations, r)
	if binding, ok := s.bindings[r.BindingKey]; ok {
		return binding, nil
	}
	binding := adapter.Binding{BindingKey: r.BindingKey, DeviceID: "dev_" + uuid.Must(uuid.NewV7()).String()}
	for _, entity := range r.Entities {
		binding.Entities = append(
			binding.Entities,
			adapter.EntityBinding{Key: entity.Key, EntityID: "ent_" + uuid.Must(uuid.NewV7()).String(), Enabled: true},
		)
	}
	s.bindings[r.BindingKey] = binding
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

func (s *recordingSession) SetHealth(_ context.Context, r adapter.HealthReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health = r
	return nil
}

func (s *recordingSession) ReportEntityAvailability(
	_ context.Context,
	reports []adapter.EntityAvailabilityReport,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range reports {
		if s.requireHealthy && s.health.Status != adapter.HealthHealthy {
			return fmt.Errorf("synthetic Core: adapter_unhealthy")
		}
		s.availability[r.EntityID] = r
	}
	return nil
}

func (s *recordingSession) PublishObservation(_ context.Context, r adapter.Observation) (adapter.ObservationID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.publishErr != nil {
		return "", s.publishErr
	}
	s.observations = append(s.observations, r)
	return "obs-synthetic", nil
}

func newTestAdapter(t *testing.T, s *recordingSession, transport http.RoundTripper) *Adapter {
	t.Helper()
	a, err := New(s, testConfig(), testCredentials(), transport, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	a.client.session = authenticatedClient(t, nil).session
	return a
}

func syntheticInventory(name string) string {
	return fmt.Sprintf(
		`{"%s":{"LockName":%q,"UserType":"superuser"},"%s":{"LockName":%q,"UserType":"user"}}`,
		testLockID,
		name,
		testOtherID,
		name,
	)
}

func syntheticDetail(name, bridge string) string {
	return fmt.Sprintf(
		`{"LockID":%q,"LockName":%q,"skuNumber":"AUG-SL05-M01-G01","currentFirmwareVersion":"1.1.30","battery":0.7157,"LockStatus":{"status":"locked","doorState":"closed","dateTime":"2020-01-01T00:00:00Z"},"Bridge":%s}`,
		testLockID,
		name,
		bridge,
	)
}

const onlineBridge = `{"operative":true,"status":{"current":"online"}}`

func runCycle(t *testing.T, a *Adapter, refresh bool) {
	t.Helper()
	if _, err := a.cycle(t.Context(), refresh); err != nil {
		t.Fatal(err)
	}
}
