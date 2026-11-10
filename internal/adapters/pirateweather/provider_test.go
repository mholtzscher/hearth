package pirateweather //nolint:testpackage // Exercises the private provider boundary and transport seam.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testConfig() Config {
	return Config{LocationName: "Home Weather", Latitude: 0, Longitude: -75.69, PollInterval: 15 * time.Minute}
}

func testClient(t *testing.T, transport http.RoundTripper) *client {
	t.Helper()
	c, err := newClient(testConfig(), "secret-test-key", transport)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestConfigValidation(t *testing.T) {
	t.Parallel()
	c := testConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Config){
		func(c *Config) { c.Latitude = math.NaN() }, func(c *Config) { c.Longitude = 181 },
		func(c *Config) { c.LocationName = " name " }, func(c *Config) { c.LocationName = "" },
		func(c *Config) { c.PollInterval = 14 * time.Minute }, func(c *Config) { c.PollInterval = 25 * time.Hour },
	} {
		bad := c
		change(&bad)
		if bad.Validate() == nil {
			t.Errorf("accepted invalid config: %+v", bad)
		}
	}
}

func TestPollRequestAndConversions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	c := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.pirateweather.net" ||
			r.URL.Path != "/forecast/KeyInHeader/0,-75.69" ||
			r.Header.Get("Apikey") != "secret-test-key" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		q := r.URL.Query()
		if q.Get("units") != "si" || q.Get("version") != "2" || q.Get("lang") != "en" ||
			q.Get("exclude") != "minutely,alerts" {
			t.Errorf("query: %v", q)
		}
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("missing deadline")
		}
		return response(
			200,
			fmt.Sprintf(
				`{"flags":{"units":"si"},"currently":{"time":%d,"temperature":12.345,"apparentTemperature":-0.0005,"humidity":0.42,"pressure":1013,"windSpeed":0,"windGust":null,"windBearing":361,"precipIntensity":0,"precipProbability":0.7,"cloudCover":"bad","uvIndex":-999}}`,
				now.Unix(),
			),
		), nil
	}))
	c.now = func() time.Time { return now }
	got, err := c.poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{
		"temperature":               12345,
		"apparent-temperature":      -1,
		"relative-humidity":         42,
		"pressure":                  1013,
		"wind-speed":                0,
		"precipitation-rate":        0,
		"precipitation-probability": 70,
	}
	if len(got.Current.Values) != len(want) {
		t.Fatalf("values: %#v", got.Current.Values)
	}
	for key, value := range want {
		if actual, ok := got.Current.Values[key]; !ok || math.Abs(actual-value) > 1e-9 {
			t.Errorf("%s = %v, present %v", key, actual, ok)
		}
	}
	if !got.Current.SourceTime.Equal(now) || !got.Current.ReceivedAt.Equal(now) {
		t.Errorf("times: %+v", got)
	}
}

func TestCurrentRejectsInvalidWholeResponse(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	for _, body := range []string{
		`{"flags":{"units":"us"},"currently":{"time":1794225600,"temperature":0}}`,
		`{"flags":{"units":"si"},"currently":null}`,
		fmt.Sprintf(`{"flags":{"units":"si"},"currently":{"time":%d,"temperature":0}} {}`, now.Unix()),
		fmt.Sprintf(`{"flags":{"units":"si"},"currently":{"time":%d,"temperature":0}}`, now.Add(-31*time.Minute).Unix()),
		fmt.Sprintf(`{"flags":{"units":"si"},"currently":{"time":%d,"temperature":0}}`, now.Add(2*time.Minute).Unix()),
		fmt.Sprintf(`{"flags":{"units":"si"},"currently":{"time":%d,"temperature":null}}`, now.Unix()),
		`{"flags":{"units":"si"},"currently":{"time":1.5,"temperature":0}}`,
	} {
		if _, err := decodeCurrent([]byte(body), now, 15*time.Minute); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func TestForecastFunctions(t *testing.T) {
	t.Parallel()
	var calls int
	c := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		switch {
		case r.URL.Query().Get("exclude") == "currently,minutely,daily,alerts":
			return response(
				200,
				`{"flags":{"units":"si"},"timezone":"America/Toronto","hourly":{"data":[{"time":1794225600,"temperature":0,"humidity":0.42},{"time":1794229200,"temperature":2},{"time":1794232800,"temperature":3}]}}`,
			), nil
		case r.URL.Query().Get("exclude") == "currently,minutely,hourly,alerts":
			return response(
				200,
				`{"flags":{"units":"si"},"timezone":"America/Toronto","daily":{"data":[{"time":1794225600,"temperatureMin":-3,"temperatureMax":5,"precipProbability":0.5}]}}`,
			), nil
		default:
			t.Fatal("no selected block")
			return nil, errors.New("unexpected block")
		}
	}))
	functions := c.functions()
	if len(functions) != 2 || functions[0].Descriptor.Key != "get_hourly_forecast" ||
		functions[1].Descriptor.Key != "get_daily_forecast" {
		t.Fatalf("functions: %+v", functions)
	}
	result := functions[0].Handler(
		context.Background(),
		adapter.FunctionRequest{Arguments: json.RawMessage(`{"hours":2}`)},
	)
	success, ok := result.(adapter.FunctionSucceeded)
	if !ok {
		t.Fatalf("hourly: %#v", result)
	}
	var hourly HourlyForecast
	if err := json.Unmarshal(success.Result, &hourly); err != nil {
		t.Fatal(err)
	}
	if len(hourly.Periods) != 2 || hourly.Periods[0].TemperatureC == nil || *hourly.Periods[0].TemperatureC != 0 ||
		hourly.Periods[0].HumidityPercent == nil ||
		*hourly.Periods[0].HumidityPercent != 42 {
		t.Fatalf("hourly: %+v", hourly)
	}
	result = functions[1].Handler(
		context.Background(),
		adapter.FunctionRequest{Arguments: json.RawMessage(`{"days":8}`)},
	)
	success, ok = result.(adapter.FunctionSucceeded)
	if !ok {
		t.Fatalf("daily: %#v", result)
	}
	var daily DailyForecast
	if err := json.Unmarshal(success.Result, &daily); err != nil {
		t.Fatal(err)
	}
	if len(daily.Periods) != 1 || daily.Periods[0].Date != "2026-11-09" || *daily.Periods[0].TemperatureMinC != -3 ||
		*daily.Periods[0].PrecipitationProbability != 50 {
		t.Fatalf("daily: %+v", daily)
	}
	for _, args := range []string{`{}`, `{"hours":0}`, `{"hours":49}`, `{"hours":1,"extra":true}`, `{"hours":"1"}`} {
		if _, rejected := functions[0].Handler(context.Background(), adapter.FunctionRequest{Arguments: json.RawMessage(args)}).(adapter.FunctionNotExecuted); !rejected {
			t.Errorf("accepted %s", args)
		}
	}
	if calls != 2 {
		t.Errorf("calls = %d", calls)
	}
}

func TestForecastRejectsBadPeriodsAndTimezone(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"flags":{"units":"si"},"timezone":"not/a-zone","hourly":{"data":[{"time":1,"temperature":0}]}}`,
		`{"flags":{"units":"si"},"timezone":"UTC","hourly":{"data":[]}}`,
		`{"flags":{"units":"si"},"timezone":"UTC","hourly":{"data":[{"time":1,"summary":"text"}]}}`,
		`{"flags":{"units":"si"},"timezone":"UTC","hourly":{"data":[{"time":2,"temperature":1},{"time":2,"temperature":1}]}}`,
	} {
		if _, err := decodeHourly([]byte(body), "Home", time.Now(), 2); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func TestForecastAcceptsSchemaIntegralRepresentations(t *testing.T) {
	t.Parallel()
	for _, literal := range []string{"1", "1.0", "1e0"} {
		t.Run(literal, func(t *testing.T) {
			t.Parallel()
			calls := 0
			c := testClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return response(
					http.StatusOK,
					`{"flags":{"units":"si"},"timezone":"UTC","hourly":{"data":[{"time":1,"temperature":2}]},"daily":{"data":[{"time":1,"temperatureMin":3}]}}`,
				), nil
			}))
			for _, function := range c.functions() {
				key := "hours"
				if function.Descriptor.Key == "get_daily_forecast" {
					key = "days"
				}
				result := function.Handler(
					t.Context(),
					adapter.FunctionRequest{Arguments: json.RawMessage(fmt.Sprintf(`{"%s":%s}`, key, literal))},
				)
				if _, ok := result.(adapter.FunctionSucceeded); !ok {
					t.Errorf("%s rejected %s: %#v", key, literal, result)
				}
			}
			if calls != 2 {
				t.Errorf("provider calls = %d, want 2", calls)
			}
		})
	}
}

func TestThrottleSharedAcrossRequestsAndSafeErrors(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	calls := 0
	c := testClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			r := response(429, "secret body")
			r.Header.Set("Retry-After", "60")
			r.Header.Set("Ratelimit-Remaining", "0")
			r.Header.Set("Ratelimit-Reset", strconv.FormatInt(int64(2*time.Hour/time.Second), 10))
			return r, nil
		}
		return nil, errors.New("secret-test-key https://secret.example coordinates")
	}))
	c.now = func() time.Time { return now }
	_, err := c.poll(context.Background())
	if err == nil || err.Error() != "provider_throttled" {
		t.Fatal(err)
	}
	result := c.hourly(context.Background(), adapter.FunctionRequest{Arguments: json.RawMessage(`{"hours":1}`)})
	if got, ok := result.(adapter.FunctionNotExecuted); !ok || got.ReasonCode != "provider_throttled" || calls != 1 {
		t.Fatalf("cooldown: %#v calls=%d", result, calls)
	}
	now = now.Add(time.Hour)
	result = c.hourly(context.Background(), adapter.FunctionRequest{Arguments: json.RawMessage(`{"hours":1}`)})
	if got, ok := result.(adapter.FunctionNotExecuted); !ok || got.ReasonCode != "provider_throttled" || calls != 1 {
		t.Fatalf("monthly reset must outlast Retry-After: %#v calls=%d", result, calls)
	}
	now = now.Add(time.Hour)
	_, err = c.poll(context.Background())
	if err == nil || err.Error() != "provider_unavailable" || calls != 2 {
		t.Fatalf("network: %v calls=%d", err, calls)
	}
}

func TestCooldownFallbackAndPreflightCancellation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	calls := 0
	c := testClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		r := response(http.StatusTooManyRequests, "")
		r.Header.Set("Retry-After", "999999999999999")
		return r, nil
	}))
	c.now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	args := adapter.FunctionRequest{Arguments: json.RawMessage(`{"hours":1}`)}
	if result, ok := c.hourly(ctx, args).(adapter.FunctionNotExecuted); !ok || result.ReasonCode != "canceled" ||
		calls != 0 {
		t.Fatalf("canceled before start: %#v, calls=%d", result, calls)
	}
	if result, ok := c.hourly(context.Background(), args).(adapter.FunctionFailed); !ok ||
		result.ReasonCode != "provider_throttled" ||
		calls != 1 {
		t.Fatalf("429: %#v, calls=%d", result, calls)
	}
	now = now.Add(14 * time.Minute)
	if result, ok := c.daily(context.Background(), adapter.FunctionRequest{Arguments: json.RawMessage(`{"days":1}`)}).(adapter.FunctionNotExecuted); !ok ||
		result.ReasonCode != "provider_throttled" ||
		calls != 1 {
		t.Fatalf("fallback cooldown: %#v, calls=%d", result, calls)
	}
	now = now.Add(time.Minute)
	if _, ok := c.hourly(context.Background(), args).(adapter.FunctionFailed); !ok || calls != 2 {
		t.Fatalf("fallback expiration: calls=%d", calls)
	}
}

func TestForecastOptionalFieldsAndAuthentication(t *testing.T) {
	t.Parallel()
	body := fmt.Sprintf(
		`{"flags":{"units":"si"},"timezone":"UTC","hourly":{"data":[{"time":1,"temperature":0,"summary":%q,"icon":null,"humidity":1.1,"windSpeed":0}]}}`,
		strings.Repeat("x", 513),
	)
	forecast, err := decodeHourly([]byte(body), "Home", time.Now(), 1)
	if err != nil {
		t.Fatal(err)
	}
	period := forecast.Periods[0]
	if period.Summary != nil || period.Icon != nil || period.HumidityPercent != nil || period.WindSpeedMPS == nil ||
		*period.WindSpeedMPS != 0 {
		t.Fatalf("optional fields: %+v", period)
	}
	c := testClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusUnauthorized, "secret response"), nil
	}))
	result := c.daily(context.Background(), adapter.FunctionRequest{Arguments: json.RawMessage(`{"days":1}`)})
	if failed, ok := result.(adapter.FunctionFailed); !ok || failed.ReasonCode != "provider_authentication" {
		t.Fatalf("auth: %#v", result)
	}
}

func TestEncodedForecastLimit(t *testing.T) {
	t.Parallel()
	text := strings.Repeat("\u0001", 512)
	periods := make([]map[string]any, 48)
	for i := range periods {
		periods[i] = map[string]any{
			"time":        time.Date(2026, 1, 1, i, 0, 0, 0, time.UTC).Unix(),
			"summary":     text,
			"temperature": 0,
		}
	}
	body, err := json.Marshal(
		map[string]any{
			"flags":    map[string]any{"units": "si"},
			"timezone": "UTC",
			"hourly":   map[string]any{"data": periods},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	c := testClient(
		t,
		roundTripFunc(func(*http.Request) (*http.Response, error) { return response(200, string(body)), nil }),
	)
	result := c.hourly(context.Background(), adapter.FunctionRequest{Arguments: json.RawMessage(`{"hours":48}`)})
	if got, ok := result.(adapter.FunctionFailed); !ok || got.ReasonCode != "invalid_response" {
		t.Fatalf("oversized: %#v", result)
	}
}
