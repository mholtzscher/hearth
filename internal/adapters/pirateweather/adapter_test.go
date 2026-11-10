package pirateweather //nolint:testpackage // Controls the poll clock and observes the SDK seam.

import (
	"context"
	"encoding/json"
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
		len(r.Entities) != 13 {
		t.Fatalf("registration: %+v", r)
	}
	for _, e := range r.Entities {
		if e.ExternalID != "location/"+e.Key || !strings.Contains(string(e.Support), `"operations":{}`) ||
			e.InitiallyEnabled != nil {
			t.Fatalf("descriptor: %+v", e)
		}
	}
	if r.Entities[11].Key != hourlyForecastKey || r.Entities[11].Name != "Hourly forecast" ||
		r.Entities[11].Type != "hearth.hourlyforecast/v1" || r.Entities[12].Key != dailyForecastKey ||
		r.Entities[12].Name != "Daily forecast" || r.Entities[12].Type != "hearth.dailyforecast/v1" {
		t.Fatalf("forecast descriptors: %+v", r.Entities[11:])
	}
}

func TestPollForecastPublicationAndPartialFailures(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	body := fullForecastBody(t, now, 50, 10)
	calls := 0
	session := &recordingSession{}
	instance, err := New(session, testConfig(), "secret", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("exclude") != "minutely,alerts" {
			t.Errorf("exclude: %s", r.URL.RawQuery)
		}
		return response(http.StatusOK, body), nil
	}), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	instance.client.now = func() time.Time { return now }
	ids, err := instance.register(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = instance.poll(t.Context(), ids); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(session.observations) != 13 || len(session.availability) != 1 ||
		len(session.availability[0]) != 13 ||
		session.health[0].Status != adapter.HealthHealthy {
		t.Fatalf(
			"poll: calls=%d health=%+v obs=%d availability=%+v",
			calls,
			session.health,
			len(session.observations),
			session.availability,
		)
	}
	assertFullForecastPublication(t, session, now)
	var document map[string]any
	if err = json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatal(err)
	}
	document["hourly"] = map[string]any{"data": []any{map[string]any{"time": now.Unix(), "summary": "no measurements"}}}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	body = string(encoded)
	if err = instance.poll(t.Context(), ids); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(session.observations) != 25 || len(session.availability) != 2 ||
		session.health[1].Status != adapter.HealthHealthy {
		t.Fatalf("partial poll: %+v", session)
	}
	assertPartialForecastAvailability(t, session, ids)
	body = `{"flags":{"units":"us"},"currently":{"time":1,"temperature":1}}`
	if err = instance.poll(t.Context(), ids); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(session.observations) != 25 || len(session.availability) != 2 ||
		session.health[2].Status != adapter.HealthUnhealthy ||
		session.health[2].ReasonCode != reasonPrefix+"invalid_response" {
		t.Fatalf(
			"response failure: health=%+v obs=%d availability=%d",
			session.health,
			len(session.observations),
			len(session.availability),
		)
	}
}

func assertFullForecastPublication(t *testing.T, session *recordingSession, now time.Time) {
	t.Helper()
	for i, count := range []int{48, 8} {
		o := session.observations[11+i]
		var state struct {
			Periods []map[string]any `json:"periods"`
		}
		if err := json.Unmarshal(o.Value, &state); err != nil {
			t.Fatal(err)
		}
		if len(state.Periods) != count || state.Periods[0]["condition"] != "rain" ||
			state.Periods[0]["humidity"] != float64(
				42,
			) || state.Periods[0]["precipitation_probability"] != float64(70) ||
			state.Periods[0]["icon"] != nil || state.Periods[0]["precipitation_type"] != nil ||
			o.SourceUpdatedAt != nil || o.AdapterReceivedAt != now.Format(time.RFC3339Nano) {
			t.Fatalf("forecast: %+v periods=%+v", o, state.Periods)
		}
		if i == 0 && state.Periods[0]["temperature"] != 12.5 ||
			i == 1 &&
				(state.Periods[0]["date"] != "2026-11-09" || state.Periods[0]["temperature_min"] != float64(4)) {
			t.Fatalf("forecast values: %+v", state.Periods[0])
		}
		assertFullForecastFields(t, state.Periods[0], i == 0)
	}
	for _, entry := range session.availability[0] {
		if entry.Status != adapter.AvailabilityAvailable || entry.ReasonCode != "" {
			t.Fatalf("availability: %+v", entry)
		}
	}
}

func assertFullForecastFields(t *testing.T, period map[string]any, hourly bool) {
	t.Helper()
	want := map[string]any{
		"starts_at": "2026-11-09T12:00:00Z", "summary": "Rain expected", "pressure": float64(1013),
		"wind_speed": float64(3), "wind_gust": float64(5), "wind_direction": float64(180),
		"precipitation_rate": 0.5, "cloud_cover": float64(30), "uv_index": float64(2),
	}
	if hourly {
		want["apparent_temperature"] = 11.5
	} else {
		want["temperature_max"] = float64(15)
	}
	for key, value := range want {
		if period[key] != value {
			t.Errorf("%s = %v, want %v", key, period[key], value)
		}
	}
}

func TestPollPublishesForecastsWithEmptySummaries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	body := fmt.Sprintf(
		`{"flags":{"units":"si"},"timezone":"UTC","currently":{"time":%d,"temperature":1},"hourly":{"data":[{"time":%d,"temperature":1,"summary":""}]},"daily":{"data":[{"time":%d,"temperatureMin":1,"summary":""}]}}`,
		now.Unix(),
		now.Unix(),
		now.Unix(),
	)
	session := &recordingSession{}
	instance, err := New(session, testConfig(), "secret", roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, body), nil
	}), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	instance.client.now = func() time.Time { return now }
	ids, err := instance.register(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = instance.poll(t.Context(), ids); err != nil {
		t.Fatal(err)
	}
	if len(session.observations) != 3 || len(session.availability) != 1 {
		t.Fatalf("poll: %+v", session)
	}
	for _, observation := range session.observations[1:] {
		if strings.Contains(string(observation.Value), `"summary"`) {
			t.Fatalf("empty summary in %s", observation.Value)
		}
	}
	for _, entry := range session.availability[0][11:] {
		if entry.Status != adapter.AvailabilityAvailable {
			t.Fatal(entry)
		}
	}
}

func assertPartialForecastAvailability(t *testing.T, session *recordingSession, ids map[string]string) {
	t.Helper()
	for _, entry := range session.availability[1] {
		if entry.EntityID == ids[hourlyForecastKey] {
			if entry.Status != adapter.AvailabilityUnavailable ||
				entry.ReasonCode != reasonPrefix+"forecast_unavailable" {
				t.Fatal(entry)
			}
		} else if entry.Status != adapter.AvailabilityAvailable {
			t.Fatal(entry)
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
		} else if e.ReasonCode != reasonPrefix+"measurement_unavailable" && e.ReasonCode != reasonPrefix+"forecast_unavailable" {
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
