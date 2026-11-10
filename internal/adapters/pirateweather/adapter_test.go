package pirateweather //nolint:testpackage // Controls the poll clock and observes the SDK seam.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

type recordingSession struct {
	mu           sync.Mutex
	registration adapter.Registration
	health       []adapter.HealthReport
	availability [][]adapter.EntityAvailabilityReport
	observations []adapter.Observation
	publishErr   error
	publishHook  func(context.Context) error
}

func (s *recordingSession) snapshot() *recordingSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &recordingSession{
		registration: s.registration,
		health:       slices.Clone(s.health),
		availability: slices.Clone(s.availability),
		observations: slices.Clone(s.observations),
	}
}

func (s *recordingSession) Register(_ context.Context, r adapter.Registration) (adapter.Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registration = r
	binding := adapter.Binding{BindingKey: r.BindingKey, DeviceID: "device"}
	for i, e := range r.Entities {
		binding.Entities = append(binding.Entities, adapter.EntityBinding{Key: e.Key,
			EntityID: fmt.Sprintf("ent_019a68f8-0000-7000-8000-%012d", i+1)})
	}
	return binding, nil
}

func (s *recordingSession) SetHealth(_ context.Context, r adapter.HealthReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health = append(s.health, r)
	return nil
}

func (s *recordingSession) ReportEntityAvailability(_ context.Context, r []adapter.EntityAvailabilityReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.availability = append(s.availability, r)
	return nil
}

func (s *recordingSession) PublishObservation(
	ctx context.Context,
	o adapter.Observation,
) (adapter.ObservationID, error) {
	if s.publishHook != nil {
		if err := s.publishHook(ctx); err != nil {
			return "", err
		}
	}
	if s.publishErr != nil {
		return "", s.publishErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observations = append(s.observations, o)
	return adapter.ObservationID(fmt.Sprintf("observation-%d", len(s.observations))), nil
}

func TestPollingPublishesFreshEvidenceAndRecovers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &recordingSession{}
		var calls atomic.Int32
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			if calls.Add(1) == 3 {
				return response(http.StatusUnauthorized, "secret"), nil
			}
			return response(
				http.StatusOK,
				fmt.Sprintf(
					`{"flags":{"units":"si"},"currently":{"time":%d,"temperature":12.345,"humidity":0.42,"precipIntensity":0}}`,
					time.Now().Unix(),
				),
			), nil
		})
		instance, err := New(session, testConfig(), "secret", transport, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- instance.Run(ctx) }()
		synctest.Wait()
		recorded := session.snapshot()
		assertRegisteredWeather(t, recorded.registration)
		if calls.Load() != 1 || len(recorded.observations) != 3 || len(recorded.availability) != 1 {
			t.Fatalf("initial: calls=%d observations=%d", calls.Load(), len(recorded.observations))
		}
		assertWeatherPublication(t, recorded)
		firstTime := recorded.observations[0].AdapterReceivedAt
		time.Sleep(15 * time.Minute)
		synctest.Wait()
		recorded = session.snapshot()
		if calls.Load() != 2 || len(recorded.observations) != 6 ||
			recorded.observations[3].AdapterReceivedAt == firstTime {
			t.Fatal("equal readings did not produce fresh evidence")
		}
		time.Sleep(15 * time.Minute)
		synctest.Wait()
		recorded = session.snapshot()
		if calls.Load() != 3 || len(recorded.observations) != 6 ||
			recorded.health[2].Status != adapter.HealthUnhealthy ||
			recorded.health[2].ReasonCode != reasonPrefix+"provider_authentication" {
			t.Fatalf("outage: calls=%d health=%+v", calls.Load(), recorded.health)
		}
		time.Sleep(15 * time.Minute)
		synctest.Wait()
		recorded = session.snapshot()
		if calls.Load() != 4 || len(recorded.observations) != 9 || len(recorded.availability) != 3 ||
			recorded.health[3].Status != adapter.HealthHealthy {
			t.Fatal("recovery did not renew availability and observations")
		}
		cancel()
		if err = <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func assertRegisteredWeather(t *testing.T, r adapter.Registration) {
	t.Helper()
	if r.BindingKey != "location" || r.Device.ExternalID == nil || *r.Device.ExternalID != "location" ||
		r.Device.Kind != "sensor" ||
		len(r.Entities) != 11 {
		t.Fatalf("registration: %+v", r)
	}
	for _, e := range r.Entities {
		if e.ExternalID != "location/"+e.Key || !strings.Contains(string(e.Support), `"operations":{}`) ||
			e.InitiallyEnabled != nil {
			t.Fatalf("descriptor: %+v", e)
		}
	}
}

func assertWeatherPublication(t *testing.T, s *recordingSession) {
	t.Helper()
	if string(s.observations[0].Value) != "12345" || string(s.observations[1].Value) != "42" ||
		string(s.observations[2].Value) != "0" {
		t.Fatalf("values: %+v", s.observations)
	}
	available := 0
	for _, e := range s.availability[0] {
		if e.Status == adapter.AvailabilityAvailable {
			available++
		} else if e.ReasonCode != reasonPrefix+"measurement_unavailable" {
			t.Fatal(e)
		}
	}
	if available != 3 || s.observations[0].SourceUpdatedAt == nil || s.health[0].Status != adapter.HealthHealthy {
		t.Fatal("missing source/availability/health evidence")
	}
}

func TestShutdownCancelsCurrentRequestWithoutFalseHealth(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &recordingSession{}
		entered := make(chan struct{})
		returned := false
		instance, err := New(
			session,
			testConfig(),
			"secret",
			roundTripFunc(func(r *http.Request) (*http.Response, error) {
				close(entered)
				<-r.Context().Done()
				returned = true
				return nil, r.Context().Err()
			}),
			slog.New(slog.DiscardHandler),
		)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- instance.Run(ctx) }()
		<-entered
		cancel()
		if err = <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if !returned || len(session.health) != 0 || len(session.observations) != 0 {
			t.Fatal("shutdown fabricated evidence or failed to join")
		}
	})
}

func TestTerminalSessionStopsWithoutAvailability(t *testing.T) {
	t.Parallel()
	for _, terminal := range []error{adapter.ErrClosed, adapter.ErrRuntimeFenced} {
		session := &recordingSession{publishErr: terminal}
		instance, err := New(
			session,
			testConfig(),
			"secret",
			roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(
					http.StatusOK,
					fmt.Sprintf(`{"flags":{"units":"si"},"currently":{"time":%d,"temperature":0}}`, time.Now().Unix()),
				), nil
			}),
			slog.New(slog.DiscardHandler),
		)
		if err != nil {
			t.Fatal(err)
		}
		if err = instance.Run(t.Context()); !errors.Is(err, terminal) {
			t.Fatal(err)
		}
		if len(session.availability) != 0 {
			t.Fatal("reported availability after failed publication")
		}
	}
}

func TestSDKPublicationTimeoutDoesNotBlockLaterPolls(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var attempts atomic.Int32
		session := &recordingSession{publishHook: func(ctx context.Context) error {
			if attempts.Add(1) == 1 {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}}
		instance, err := New(
			session,
			testConfig(),
			"secret",
			roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(
					http.StatusOK,
					fmt.Sprintf(`{"flags":{"units":"si"},"currently":{"time":%d,"temperature":0}}`, time.Now().Unix()),
				), nil
			}),
			slog.New(slog.DiscardHandler),
		)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- instance.Run(ctx) }()
		synctest.Wait()
		time.Sleep(30 * time.Second)
		synctest.Wait()
		recorded := session.snapshot()
		if attempts.Load() != 1 || len(recorded.availability) != 0 {
			t.Fatal("timeout fabricated availability")
		}
		time.Sleep(15 * time.Minute)
		synctest.Wait()
		recorded = session.snapshot()
		if attempts.Load() != 2 || len(recorded.observations) != 1 || len(recorded.availability) != 1 {
			t.Fatal("publication failure permanently stopped acquisition")
		}
		cancel()
		if err = <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}
