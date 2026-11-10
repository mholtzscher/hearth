//go:build e2e

package pirateweather //nolint:testpackage // Injects the provider transport into real companion assembly.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/app/hearthd"
	adaptersapi "github.com/mholtzscher/hearth/internal/modules/adapters/httpapi"
	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/httpapi"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
)

type syntheticTransport func(*http.Request) (*http.Response, error)

func (f syntheticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// This is a public production-Core journey, not a hand-assembled module fixture.
func TestWeatherCompanionCoreJourney(t *testing.T) {
	t.Setenv("HEARTH_OPENAI_API_KEY", "synthetic-unused-model-key")
	coreURL, brokerURL := startWeatherCore(t)
	keyPath := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyPath, []byte("synthetic-weather-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := validConfig()
	config.NATSURL, config.APIKeyFile = brokerURL, keyPath
	var mode atomic.Int32
	entered, returned := make(chan struct{}), make(chan struct{})
	transport := weatherProviderFixture(&mode, entered, returned)
	stop, join := startWeatherCompanion(t, config, transport)
	entities := waitWeatherEntities(t, coreURL)
	var temperature devicesapi.EntityBody
	var humidity devicesapi.EntityBody
	for _, entity := range entities {
		if entity.Name == "Temperature" {
			temperature = entity
		}
		if entity.Name == "Relative Humidity" {
			humidity = entity
		}
	}
	if temperature.State == nil || temperature.State.Value != float64(12345) ||
		temperature.State.SourceUpdatedAt == nil {
		t.Fatalf("persisted current temperature: %+v", temperature)
	}
	observationID := temperature.State.ObservationID
	assertRetainedWeatherForecasts(t, coreURL, &mode)
	mode.Store(0)
	assertWeatherMCP(t, coreURL, config.AdapterID)
	var after devicesapi.EntityBody
	weatherJSON(t, coreURL, http.MethodGet, "/v1/entities/"+temperature.ID, "", &after)
	if after.State == nil || after.State.ObservationID != observationID {
		t.Fatal("forecast Functions changed current State")
	}

	// A live forecast must return before the companion releases its Session.
	mode.Store(2)
	var descriptor adaptersapi.FunctionBody
	weatherJSON(
		t,
		coreURL,
		http.MethodGet,
		"/v1/adapters/"+config.AdapterID+"/functions/get_hourly_forecast",
		"",
		&descriptor,
	)
	invoked := make(chan error, 1)
	go func() {
		_, _, err := weatherRequest(t.Context(), coreURL, http.MethodPost,
			"/v1/adapters/"+config.AdapterID+"/functions/get_hourly_forecast/invocations",
			`{"catalog_revision":"`+descriptor.CatalogRevision+`","arguments":{"hours":1}}`)
		invoked <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("forecast did not start")
	}
	stop()
	join()
	select {
	case <-returned:
	default:
		t.Fatal("Session released before provider handler joined")
	}
	select {
	case err := <-invoked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("invocation did not finish")
	}

	// Existing enablement and canonical IDs survive restart and coordinate/name correction.
	weatherJSON(t, coreURL, http.MethodPatch, "/v1/entities/"+temperature.ID, `{"enabled":false}`, &after)
	mode.Store(0)
	config.Location.Name = "Renamed Weather"
	latitude := 1.0
	config.Location.Latitude = &latitude
	stopAgain, joinAgain := startWeatherCompanion(t, config, transport)
	waitWeatherCondition(t, func() bool {
		weatherJSON(t, coreURL, http.MethodGet, "/v1/entities/"+temperature.ID, "", &after)
		var refreshed devicesapi.EntityBody
		weatherJSON(t, coreURL, http.MethodGet, "/v1/entities/"+humidity.ID, "", &refreshed)
		return after.Availability.Status == "available" && refreshed.State != nil &&
			refreshed.State.ObservationID != humidity.State.ObservationID
	})
	if after.Enabled || after.DeviceID != temperature.DeviceID || after.State == nil ||
		after.State.ObservationID != observationID {
		t.Fatalf("restart changed identity, enablement or disabled State: %+v", after)
	}
	stopAgain()
	joinAgain()
}

func weatherResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func weatherProviderFixture(mode *atomic.Int32, entered, returned chan struct{}) syntheticTransport {
	return func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Apikey") != "synthetic-weather-key" {
			return nil, errors.New("missing test credential")
		}
		exclude := r.URL.Query().Get("exclude")
		if !strings.Contains(exclude, "currently") {
			return weatherResponse(
				http.StatusOK,
				fmt.Sprintf(
					`{"flags":{"units":"si"},"currently":{"time":%d,"temperature":12.345,"apparentTemperature":11,"humidity":0.42,"pressure":1013,"windSpeed":3,"windGust":5,"windBearing":180,"precipIntensity":0,"precipProbability":0.2,"cloudCover":0.3,"uvIndex":2}}`,
					time.Now().Unix(),
				),
			), nil
		}
		switch mode.Load() {
		case 1:
			return weatherResponse(http.StatusUnauthorized, "secret provider body"), nil
		case 2:
			close(entered)
			<-r.Context().Done()
			close(returned)
			return nil, r.Context().Err()
		}
		if !strings.Contains(exclude, "hourly") {
			return weatherResponse(
				http.StatusOK,
				`{"flags":{"units":"si"},"timezone":"America/Toronto","hourly":{"data":[{"time":1794225600,"temperature":12.5,"precipProbability":0.2}]}}`,
			), nil
		}
		return weatherResponse(
			http.StatusOK,
			`{"flags":{"units":"si"},"timezone":"America/Toronto","daily":{"data":[{"time":1794196800,"temperatureMin":4,"temperatureMax":15}]}}`,
		), nil
	}
}

func assertRetainedWeatherForecasts(t *testing.T, coreURL string, mode *atomic.Int32) {
	t.Helper()
	hourly := invokeWeather(t, coreURL, "get_hourly_forecast", `{"hours":48}`)
	if hourly.Status != "succeeded" || !strings.Contains(string(hourly.Result), `"temperature_c":12.5`) {
		t.Fatalf("hourly=%+v", hourly)
	}
	daily := invokeWeather(t, coreURL, "get_daily_forecast", `{"days":8}`)
	if daily.Status != "succeeded" || !strings.Contains(string(daily.Result), `"temperature_min_c":4`) {
		t.Fatalf("daily=%+v", daily)
	}
	var persisted adaptersapi.InvocationBody
	weatherJSON(t, coreURL, http.MethodGet, "/v1/adapter-function-invocations/"+hourly.ID, "", &persisted)
	if persisted.Status != "succeeded" || string(persisted.Result) != string(hourly.Result) {
		t.Fatal("forecast result not retained")
	}
	mode.Store(1)
	failed := invokeWeather(t, coreURL, "get_hourly_forecast", `{"hours":1}`)
	if failed.Status != "failed" || failed.ReasonCode != "provider_authentication" {
		t.Fatalf("failure=%+v", failed)
	}
	weatherJSON(t, coreURL, http.MethodGet, "/v1/adapter-function-invocations/"+failed.ID, "", &persisted)
	if persisted.Status != "failed" || persisted.ReasonCode != "provider_authentication" {
		t.Fatal("failure not retained")
	}
}

func startWeatherCore(t *testing.T) (string, string) {
	t.Helper()
	broker := natstest.StartServer(t)
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	databasePath := dbtest.MigratedPath(t)
	go func() {
		done <- hearthd.Run(ctx, hearthd.Config{HouseholdTimezone: "UTC", HTTPAddr: address,
			NATSURL: broker.ClientURL(), SQLitePath: databasePath}, slog.New(slog.DiscardHandler))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case runErr := <-done:
			if runErr != nil {
				t.Error(runErr)
			}
		case <-time.After(20 * time.Second):
			t.Error("Core failed to join")
		}
	})
	base := "http://" + address
	waitWeatherCondition(t, func() bool {
		status, _, requestErr := weatherRequest(t.Context(), base, http.MethodGet, "/readyz", "")
		return requestErr == nil && status == http.StatusOK
	})
	return base, broker.ClientURL()
}

func startWeatherCompanion(t *testing.T, config Config, transport http.RoundTripper) (context.CancelFunc, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	var result error
	go func() { result = run(ctx, config, slog.New(slog.DiscardHandler), transport); close(done) }()
	join := func() {
		t.Helper()
		select {
		case <-done:
			if result != nil {
				t.Error(result)
			}
		case <-time.After(10 * time.Second):
			t.Error("companion failed to join")
		}
	}
	t.Cleanup(func() { cancel(); join() })
	return cancel, join
}

func waitWeatherCondition(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("weather journey condition timed out")
		}
	}
}

func weatherRequest(ctx context.Context, base, method, path, body string) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if method == http.MethodPatch {
		request.Header.Set("Content-Type", "application/merge-patch+json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	return response.StatusCode, data, err
}

func weatherJSON(t *testing.T, base, method, path, body string, target any) {
	t.Helper()
	status, data, err := weatherRequest(t.Context(), base, method, path, body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("%s %s: %d %s %v", method, path, status, data, err)
	}
	if err = json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func waitWeatherEntities(t *testing.T, base string) []devicesapi.EntityBody {
	t.Helper()
	var page struct {
		Items []devicesapi.EntityBody `json:"items"`
	}
	waitWeatherCondition(t, func() bool {
		weatherJSON(t, base, http.MethodGet, "/v1/entities", "", &page)
		if len(page.Items) != 11 {
			return false
		}
		for _, entity := range page.Items {
			if entity.State == nil || entity.Availability.Status != "available" {
				return false
			}
		}
		return true
	})
	return page.Items
}

func invokeWeather(t *testing.T, base, function, args string) adaptersapi.InvocationBody {
	t.Helper()
	path := "/v1/adapters/pirateweather-home/functions/" + function
	var descriptor adaptersapi.FunctionBody
	weatherJSON(t, base, http.MethodGet, path, "", &descriptor)
	if !descriptor.Callable {
		t.Fatal("Function is not callable")
	}
	var result adaptersapi.InvocationBody
	weatherJSON(
		t,
		base,
		http.MethodPost,
		path+"/invocations",
		`{"catalog_revision":"`+descriptor.CatalogRevision+`","arguments":`+args+`}`,
		&result,
	)
	return result
}

func assertWeatherMCP(t *testing.T, base, adapterID string) {
	t.Helper()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "weather-journey", Version: "1"}, nil).
		Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: base + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_adapter_function",
		Arguments: map[string]any{"adapter_id": adapterID, "function_key": "get_hourly_forecast"}})
	if err != nil || result.IsError {
		t.Fatalf("MCP discovery: %+v %v", result, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(encoded), "get_hourly_forecast") ||
		!strings.Contains(string(encoded), "hours") {
		t.Fatalf("MCP omitted forecast contract: %s %v", encoded, err)
	}
	var detail struct {
		CatalogRevision string `json:"catalog_revision"`
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &detail); err != nil || detail.CatalogRevision == "" {
		t.Fatalf("MCP revision: %s %v", data, err)
	}
	invocation, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "invoke_adapter_function",
		Arguments: map[string]any{"adapter_id": adapterID, "function_key": "get_hourly_forecast",
			"catalog_revision": detail.CatalogRevision, "arguments": map[string]any{"hours": 1}}})
	if err != nil || invocation.IsError {
		t.Fatalf("MCP forecast: %+v %v", invocation, err)
	}
	data, err = json.Marshal(invocation.StructuredContent)
	if err != nil || !strings.Contains(string(data), `"temperature_c":12.5`) {
		t.Fatalf("MCP forecast data: %s %v", data, err)
	}
}
