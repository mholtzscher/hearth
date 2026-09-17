package ecowitt //nolint:testpackage // Tests exercise package-private lifecycle supervision.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
	"github.com/mholtzscher/hearth/internal/testbroker"
)

// Constants shared with the sanitized real capture and the example config.
const (
	// processAdapterID is the configured Adapter slug.
	processAdapterID = "ecowitt"
	// processTopic is the exact two-segment subscription topic. Its second
	// segment is a fake station suffix, never a household MAC.
	processTopic = "ecowitt/943cc64457a7"
	// processPasskeyHex is the sanitized fixture PASSKEY. It is not a real
	// secret.
	processPasskeyHex = "0123456789abcdef0123456789abcdef"
	// processUploadIntervalSeconds matches the example upload cadence.
	processUploadIntervalSeconds = 16
	// processEntityCount is the complete v1 registrations: four gateway
	// Entities plus fifteen outdoor-array Entities.
	processEntityCount = 19
	// processGatewayEntityCount and processOutdoorEntityCount are the per-slot
	// Entity counts.
	processGatewayEntityCount = 4
	processOutdoorEntityCount = 15
)

// TestRunConnectsNATSAndMosquitto protects end-to-end application assembly
// across an embedded native-NATS server, a real Mosquitto broker, and a real
// Paho MQTT 3.1.1 subscriber. It fails on registration count, retained-report
// acceptance, typed canonical Observation projection, duplicate suppression,
// repeated-equal fresh evidence, disconnect/recovery health, graceful
// supervision, or secret leakage into diagnostics.
func TestRunConnectsNATSAndMosquitto(t *testing.T) {
	t.Parallel()
	logs := &logCapture{}
	logger := slog.New(logs)

	server := startTestNATSServer(t)
	brokerURL := testbroker.StartMosquitto(t).URL()
	proxy := startBrokerProxy(t, brokerURL)
	coreConnection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(coreConnection.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repository := devicessqlite.NewDeviceRepository(database, catalog)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	js, err := jetstream.New(coreConnection)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := devicesnats.ProvisionObservationResources(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	service := devices.NewService(
		devicessqlite.DeviceStores(repository),
		devicesnats.NewCommandSender(coreConnection, validator),
		catalog,
		devices.Dependencies{},
	)
	startCoreTransports(ctx, t, coreConnection, durable, validator, service, slog.New(slog.DiscardHandler))

	passkeyFile := writeProcessPasskey(t)
	config := Config{
		AdapterID: processAdapterID,
		NATSURL:   server.ClientURL(),
		MQTT: MQTTConfig{
			URL:   proxy.URL(),
			Topic: processTopic,
		},
		Station: StationConfig{
			GatewayName:           "Weather Station Gateway",
			OutdoorArrayName:      "Outdoor Weather Array",
			PasskeyFile:           passkeyFile,
			UploadIntervalSeconds: processUploadIntervalSeconds,
		},
	}

	// A retained report published before the Adapter subscribes is replayed on
	// the new subscription with its retain flag set. It must produce no
	// evidence: a distinct station time guarantees it is not later suppressed as
	// a duplicate of the live report.
	fixture := readProcessFixture(t, "gw2000-ws90-report.txt")
	retainedReplay := payloadAt(t, "14%3A29%3A00")
	publisher := connectMQTTClient(t, brokerURL, "ecowitt-process-publisher")
	publishMQTT(t, publisher, true, retainedReplay)

	runContext, stopRun := context.WithCancel(ctx)
	runErrors := make(chan error, 1)
	go func() { runErrors <- Run(runContext, config, logger) }()

	registered := waitForEntities(ctx, t, service, runErrors, processEntityCount)
	assertDeviceSlots(ctx, t, service)
	assertRetainedAndHealthUnknown(ctx, t, service, registered)

	// A live, complete, compatible report produces typed Observations. The
	// report is republished until the Adapter's exact-topic subscription has
	// demonstrably received it.
	observed := waitForLiveObservations(ctx, t, service, runErrors, publisher, fixture)
	assertProjectedValues(t, observed)
	assertAdapterRuntime(ctx, t, service)

	indoorTemperature := entityNamed(t, observed, "Indoor Temperature")
	// Only the live report produced evidence; the retained replay was ignored.
	baseline := 1
	if got := observationCount(ctx, t, database, indoorTemperature.Entity.ID); got != baseline {
		t.Fatalf("Observations after the live report = %d, want %d (retained replay ignored)", got, baseline)
	}

	// An exact duplicate adds no evidence and creates no Observation.
	publishMQTT(t, publisher, false, fixture)
	assertNoNewEvidence(ctx, t, service, database, indoorTemperature, "22200", baseline)

	// The same measurements at a later station date are fresh evidence.
	publishMQTT(t, publisher, false, payloadAt(t, "14%3A30%3A08"))
	baseline++
	waitForObservationCount(ctx, t, database, indoorTemperature.Entity.ID, baseline)

	// Interrupting the MQTT listener must surface as unhealthy
	// hearth.external_system_unavailable and clear effective availability.
	proxy.pause()
	waitForHealth(ctx, t, service, devices.AdapterHealthUnhealthy, "hearth.external_system_unavailable")
	assertEntitiesEffectivelyUnavailable(ctx, t, service)

	// Restoring the broker must recover to healthy before fresh availability
	// and Observations reappear.
	proxy.resume()
	recovery := payloadAt(t, "14%3A31%3A00")
	waitForRecovery(ctx, t, service, publisher, recovery)
	baseline++
	waitForObservationCount(ctx, t, database, indoorTemperature.Entity.ID, baseline)

	stopRun()
	shutdown := time.NewTimer(5 * time.Second)
	defer shutdown.Stop()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatalf("Run returned after parent cancellation: %v", runErr)
		}
	case <-shutdown.C:
		t.Fatal("Run did not stop promptly after parent cancellation")
	case <-ctx.Done():
		t.Fatalf("Run did not stop after parent cancellation: %v", ctx.Err())
	}
	instance, err := service.GetAdapter(ctx, processAdapterID)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Health.Status != devices.AdapterHealthUnhealthy ||
		instance.Health.Runtime == nil || instance.Health.Runtime.Status != "offline" {
		t.Fatalf("released Adapter instance = %#v", instance)
	}
	assertDiagnosticsHideSecrets(t, logs, fixture, passkeyFile)
}

// TestSuperviseCancelsSiblingAndReturnsTerminalError protects the lifecycle
// failure path and fails if the first component's error is discarded.
func TestSuperviseCancelsSiblingAndReturnsTerminalError(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	terminalErr := errors.New("terminal adapter failure")

	err := supervise(
		context.Background(),
		func(context.Context) error {
			<-started
			return terminalErr
		},
		func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	)
	if !errors.Is(err, terminalErr) {
		t.Fatalf("supervise error = %v, want terminal failure", err)
	}
}

// assertDeviceSlots protects the two static Device slots and fails if the
// registration count, kind, or per-slot Entity ownership drifts.
func assertDeviceSlots(ctx context.Context, t *testing.T, service *devices.Service) {
	t.Helper()
	page, err := service.ListDevices(ctx, devices.ListDevicesParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("Devices = %#v, want two static slots", page.Items)
	}
	counts := make(map[devices.DeviceKind]int, len(page.Items))
	entityCounts := make(map[string]int, len(page.Items))
	for _, item := range page.Items {
		aggregate, getErr := service.GetDevice(ctx, devices.GetDeviceParams{ID: item.ID, EntityLimit: 30})
		if getErr != nil {
			t.Fatal(getErr)
		}
		counts[aggregate.Device.Kind]++
		entityCounts[aggregate.Device.Name] = len(aggregate.Entities.Items)
	}
	if counts[devices.DeviceKindSensor] != 2 {
		t.Fatalf("Device kinds = %#v, want two sensors", counts)
	}
	if entityCounts["Weather Station Gateway"] != processGatewayEntityCount ||
		entityCounts["Outdoor Weather Array"] != processOutdoorEntityCount {
		t.Fatalf("per-slot Entity counts = %#v", entityCounts)
	}
}

// assertRetainedAndHealthUnknown protects the retained-ignore contract and
// fails if a retained replay produced State or health before any live report.
func assertRetainedAndHealthUnknown(
	ctx context.Context,
	t *testing.T,
	service *devices.Service,
	registered []devices.EntityWithState,
) {
	t.Helper()
	for _, entity := range registered {
		if entity.State != nil {
			t.Fatalf("Entity %s has State from a retained report", entity.Entity.Name)
		}
	}
	instance, err := service.GetAdapter(ctx, processAdapterID)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Health.Status != devices.AdapterHealthUnknown {
		t.Fatalf("health after retained replay = %q, want unknown", instance.Health.Status)
	}
}

// assertProjectedValues protects the typed MQTT-to-Core path and fails if a
// canonical unit conversion, Entity type, or support shape drifts.
func assertProjectedValues(t *testing.T, observed []devices.EntityWithState) {
	t.Helper()
	oracles := map[string]float64{
		"Indoor Temperature":  22200,
		"Indoor Humidity":     43,
		"Relative Pressure":   29.046 * 33.8638866667,
		"Absolute Pressure":   29.046 * 33.8638866667,
		"Outdoor Temperature": 18800,
		"Outdoor Humidity":    85,
		"Wind Direction":      44,
		"Wind Speed":          3.13 * 0.44704,
		"Wind Gust":           4.03 * 0.44704,
		"Maximum Daily Gust":  6.04 * 0.44704,
		"Solar Radiation":     98.26,
		"UV Index":            0,
		"Rain Rate":           0,
		"Event Rain":          0.150 * 25.4,
		"Hourly Rain":         0,
		"Daily Rain":          0,
		"Weekly Rain":         0,
		"Monthly Rain":        1.390 * 25.4,
		"Yearly Rain":         32.866 * 25.4,
	}
	types := map[string]string{
		"Indoor Temperature": "hearth.temperature/v1",
		"Indoor Humidity":    "hearth.relativehumidity/v1",
		"Relative Pressure":  "hearth.pressure/v1",
		"Wind Speed":         "hearth.speed/v1",
		"Wind Direction":     "hearth.numericsensor/v1",
	}
	if len(observed) != len(oracles) {
		t.Fatalf("observed %d Entities, want %d", len(observed), len(oracles))
	}
	for _, entity := range observed {
		want, present := oracles[entity.Entity.Name]
		if !present {
			t.Fatalf("unexpected Entity %q", entity.Entity.Name)
		}
		if entity.State == nil {
			t.Fatalf("Entity %s has no State", entity.Entity.Name)
		}
		var got float64
		if err := json.Unmarshal([]byte(entity.State.Value), &got); err != nil {
			t.Fatalf("decode %s State %s: %v", entity.Entity.Name, entity.State.Value, err)
		}
		tolerance := 1e-9 * math.Max(1, math.Abs(want))
		if math.Abs(got-want) > tolerance {
			t.Errorf("%s State = %v, want %v", entity.Entity.Name, got, want)
		}
		if wantType, ok := types[entity.Entity.Name]; ok && string(entity.Entity.TypeID) != wantType {
			t.Errorf("%s TypeID = %s, want %s", entity.Entity.Name, entity.Entity.TypeID, wantType)
		}
	}
	assertCanonicalSupports(t, observed)
}

// assertCanonicalSupports protects the registered support of every Entity and
// fails if a reusable physical quantity stops pinning its canonical unit or a
// generic numeric sensor loses its own unit and envelope. The expected shapes
// are hand-written oracles, independent of the Adapter catalog.
func assertCanonicalSupports(t *testing.T, observed []devices.EntityWithState) {
	t.Helper()
	supports := map[string]string{
		"Indoor Temperature":  `{"state":{"unit":"mCel"},"operations":{}}`,
		"Outdoor Temperature": `{"state":{"unit":"mCel"},"operations":{}}`,
		"Indoor Humidity":     `{"state":{"unit":"%"},"operations":{}}`,
		"Outdoor Humidity":    `{"state":{"unit":"%"},"operations":{}}`,
		"Relative Pressure":   `{"state":{"unit":"hPa"},"operations":{}}`,
		"Absolute Pressure":   `{"state":{"unit":"hPa"},"operations":{}}`,
		"Wind Speed":          `{"state":{"unit":"m/s"},"operations":{}}`,
		"Wind Gust":           `{"state":{"unit":"m/s"},"operations":{}}`,
		"Maximum Daily Gust":  `{"state":{"unit":"m/s"},"operations":{}}`,
		"Wind Direction":      `{"state":{"maximum":360,"minimum":0,"unit":"deg"},"operations":{}}`,
	}
	seen := make(map[string]bool, len(supports))
	for _, entity := range observed {
		wantSupport, present := supports[entity.Entity.Name]
		if !present {
			continue
		}
		seen[entity.Entity.Name] = true
		if string(entity.Entity.Support) != wantSupport {
			t.Errorf("%s support = %s, want %s", entity.Entity.Name, entity.Entity.Support, wantSupport)
		}
	}
	for name := range supports {
		if !seen[name] {
			t.Errorf("canonical support oracle Entity %q was not registered", name)
		}
	}
}

// assertAdapterRuntime protects the exact Session software name and version.
func assertAdapterRuntime(ctx context.Context, t *testing.T, service *devices.Service) {
	t.Helper()
	instance, err := service.GetAdapter(ctx, processAdapterID)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Health.Status != devices.AdapterHealthHealthy || instance.Health.Runtime == nil ||
		instance.Health.Runtime.SoftwareName != "hearth-adapter-ecowitt" ||
		instance.Health.Runtime.SoftwareVersion != "0.1.0" {
		t.Fatalf("Adapter instance = %#v", instance)
	}
}

// assertEntitiesEffectivelyUnavailable protects the health-to-availability
// coupling and fails if an unhealthy Adapter left Entities available.
func assertEntitiesEffectivelyUnavailable(ctx context.Context, t *testing.T, service *devices.Service) {
	t.Helper()
	page, err := service.ListEntities(ctx, devices.ListEntitiesParams{Limit: processEntityCount + 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range page.Items {
		if entity.Availability.Status != devices.EntityAvailabilityUnavailable {
			t.Fatalf("Entity %s availability = %q during an unhealthy Adapter",
				entity.Entity.Name, entity.Availability.Status)
		}
	}
}

// assertDiagnosticsHideSecrets protects A4 at process scope and fails if any
// log record repeats the configured topic, the PASSKEY file contents, or the
// raw payload bytes.
func assertDiagnosticsHideSecrets(t *testing.T, logs *logCapture, fixture []byte, passkeyFile string) {
	t.Helper()
	output := logs.output()
	for name, secret := range map[string]string{
		"configured topic": processTopic,
		"passkey value":    processPasskeyHex,
		"raw payload":      string(fixture),
		"passkey path":     passkeyFile,
	} {
		if strings.Contains(output, secret) {
			t.Fatalf("diagnostics repeat the %s: %s", name, output)
		}
	}
}

// waitForEntities polls until the expected number of Entities is registered
// and all of them are owned by the process Adapter.
func waitForEntities(
	ctx context.Context,
	t *testing.T,
	service *devices.Service,
	runErrors <-chan error,
	want int,
) []devices.EntityWithState {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		page, err := service.ListEntities(ctx, devices.ListEntitiesParams{Limit: want + 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == want {
			owned := true
			for _, entity := range page.Items {
				if entity.Entity.AdapterID != processAdapterID {
					owned = false
					break
				}
			}
			if owned {
				return page.Items
			}
		}
		select {
		case runErr := <-runErrors:
			t.Fatalf("Run exited before %d Entities registered: %v", want, runErr)
		case <-ctx.Done():
			t.Fatalf("waiting for %d registered Entities: %v", want, ctx.Err())
		case <-ticker.C:
		}
	}
}

// waitForLiveObservations republishes one report until every Entity has State
// and explicit availability, so a publish that raced the Adapter's
// subscription is retried rather than lost.
func waitForLiveObservations(
	ctx context.Context,
	t *testing.T,
	service *devices.Service,
	runErrors <-chan error,
	publisher paho.Client,
	payload []byte,
) []devices.EntityWithState {
	t.Helper()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		publishMQTT(t, publisher, false, payload)
		page, err := service.ListEntities(ctx, devices.ListEntitiesParams{Limit: processEntityCount + 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == processEntityCount {
			ready := true
			for _, entity := range page.Items {
				if entity.State == nil || entity.Availability.Status != devices.EntityAvailabilityAvailable {
					ready = false
					break
				}
			}
			if ready {
				return page.Items
			}
		}
		select {
		case runErr := <-runErrors:
			t.Fatalf("Run exited before all Entities were available: %v", runErr)
		case <-ctx.Done():
			t.Fatalf("waiting for available Entities: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

// assertNoNewEvidence proves one report was ignored: after a bounded settle the
// Entity has neither a new Observation nor a changed State.
func assertNoNewEvidence(
	ctx context.Context,
	t *testing.T,
	service *devices.Service,
	database *sql.DB,
	entity devices.EntityWithState,
	wantState string,
	wantCount int,
) {
	t.Helper()
	deadline := time.Now().Add(750 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := observationCount(ctx, t, database, entity.Entity.ID); got != wantCount {
		t.Fatalf("ignored report created an Observation: count = %d, want %d", got, wantCount)
	}
	current, err := service.GetEntity(ctx, entity.Entity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State == nil || string(current.State.Value) != wantState {
		t.Fatalf("ignored report changed State: %#v, want %s", current.State, wantState)
	}
}

// waitForHealth polls until the Adapter reports one status and optional reason.
func waitForHealth(
	ctx context.Context,
	t *testing.T,
	service *devices.Service,
	status devices.AdapterHealthStatus,
	reason string,
) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		instance, err := service.GetAdapter(ctx, processAdapterID)
		if err != nil {
			t.Fatal(err)
		}
		if instance.Health.Status == status && (reason == "" ||
			(instance.Health.Reason != nil && instance.Health.Reason.Code == reason)) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for health %q reason %q: %v", status, reason, ctx.Err())
		case <-ticker.C:
		}
	}
}

// waitForRecovery re-publishes a fresh live report until the reconnected
// Adapter reports healthy and every Entity is available again.
func waitForRecovery(
	ctx context.Context,
	t *testing.T,
	service *devices.Service,
	publisher paho.Client,
	payload []byte,
) {
	t.Helper()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		publishMQTT(t, publisher, false, payload)
		instance, err := service.GetAdapter(ctx, processAdapterID)
		if err != nil {
			t.Fatal(err)
		}
		if instance.Health.Status == devices.AdapterHealthHealthy {
			page, listErr := service.ListEntities(ctx, devices.ListEntitiesParams{Limit: processEntityCount + 1})
			if listErr != nil {
				t.Fatal(listErr)
			}
			available := len(page.Items) == processEntityCount
			for _, entity := range page.Items {
				if entity.Availability.Status != devices.EntityAvailabilityAvailable {
					available = false
					break
				}
			}
			if available {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for disconnect recovery: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

// waitForObservationCount polls until an Entity has recorded the expected
// number of Observations, proving fresh evidence rather than suppressed
// equality.
func waitForObservationCount(
	ctx context.Context,
	t *testing.T,
	database *sql.DB,
	entityID devices.EntityID,
	want int,
) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if count := observationCount(ctx, t, database, entityID); count == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for %d Observations: %v", want, ctx.Err())
		case <-ticker.C:
		}
	}
}

// observationCount reads the recorded Observation count for one Entity.
func observationCount(
	ctx context.Context,
	t *testing.T,
	database *sql.DB,
	entityID devices.EntityID,
) int {
	t.Helper()
	var count int
	if err := database.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM observations WHERE entity_id = ?",
		string(entityID),
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// entityNamed returns one Entity by its configured display name.
func entityNamed(t *testing.T, entities []devices.EntityWithState, name string) devices.EntityWithState {
	t.Helper()
	for _, entity := range entities {
		if entity.Entity.Name == name {
			return entity
		}
	}
	t.Fatalf("no Entity named %q", name)
	return devices.EntityWithState{}
}

// readProcessFixture reads one sanitized real capture.
func readProcessFixture(t *testing.T, name string) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "adapters", "ecowitt", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSpace(payload)
}

// payloadAt returns the sanitized capture with its station time replaced.
func payloadAt(t *testing.T, encodedTime string) []byte {
	t.Helper()
	base := readProcessFixture(t, "gw2000-ws90-report.txt")
	return bytes.Replace(base, []byte("14%3A30%3A00"), []byte(encodedTime), 1)
}

// writeProcessPasskey writes the sanitized PASSKEY to a temporary file.
func writeProcessPasskey(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "passkey")
	if err := os.WriteFile(path, []byte(processPasskeyHex+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// logCapture is a thread-safe slog handler that records rendered output for
// secret-leak assertions.
type logCapture struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

// Enabled implements [slog.Handler].
func (capture *logCapture) Enabled(context.Context, slog.Level) bool { return true }

// Handle implements [slog.Handler].
func (capture *logCapture) Handle(_ context.Context, record slog.Record) error {
	capture.mutex.Lock()
	defer capture.mutex.Unlock()
	record.Attrs(func(attribute slog.Attr) bool {
		_, _ = capture.buffer.WriteString(attribute.Key + "=" + attribute.Value.String() + " ")
		return true
	})
	_, _ = capture.buffer.WriteString(record.Message + "\n")
	return nil
}

// WithAttrs implements [slog.Handler].
func (capture *logCapture) WithAttrs(attrs []slog.Attr) slog.Handler {
	capture.mutex.Lock()
	defer capture.mutex.Unlock()
	for _, attribute := range attrs {
		_, _ = capture.buffer.WriteString(attribute.Key + "=" + attribute.Value.String() + " ")
	}
	return capture
}

// WithGroup implements [slog.Handler].
func (capture *logCapture) WithGroup(string) slog.Handler { return capture }

// output returns every captured record as text.
func (capture *logCapture) output() string {
	capture.mutex.Lock()
	defer capture.mutex.Unlock()
	return capture.buffer.String()
}

// startTestNATSServer starts one embedded JetStream NATS server on loopback.
func startTestNATSServer(t *testing.T) *natsserver.Server {
	t.Helper()
	return natstest.StartServer(t)
}

// startCoreTransports starts the Core-side NATS servers and the Observation
// consumer used by the process test.
func startCoreTransports(
	ctx context.Context,
	t *testing.T,
	connection *natsgo.Conn,
	durable jetstream.Consumer,
	validator *contractsv1.Validator,
	service *devices.Service,
	logger *slog.Logger,
) {
	t.Helper()
	sessions, err := devicesnats.StartSessionServer(connection, validator, service, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Drain() })
	registrations, err := devicesnats.StartRegistrationServer(connection, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registrations.Drain() })
	mappings, err := devicesnats.StartOwnedMappingsServer(connection, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mappings.Drain() })
	availability, err := devicesnats.StartEntityAvailabilityServer(connection, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = availability.Drain() })
	observations, err := devicesnats.StartObservationConsumer(ctx, durable, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(observations.Stop)
}

// connectMQTTClient builds one plain MQTT 3.1.1 test publisher.
func connectMQTTClient(t *testing.T, brokerURL, clientID string) paho.Client {
	t.Helper()
	client := paho.NewClient(paho.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID(clientID).
		SetProtocolVersion(4).
		SetCleanSession(true))
	if token := client.Connect(); !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		t.Fatalf("connect MQTT: %v", token.Error())
	}
	t.Cleanup(func() { client.Disconnect(0) })
	return client
}

// publishMQTT publishes one report at QoS 1 and waits for the token.
func publishMQTT(t *testing.T, client paho.Client, retained bool, payload []byte) {
	t.Helper()
	if token := client.Publish(processTopic, 1, retained, payload); !token.WaitTimeout(5*time.Second) ||
		token.Error() != nil {
		t.Errorf("publish MQTT report: %v", token.Error())
	}
}

// brokerProxy is a loopback TCP forwarder in front of the real Mosquitto
// container. Pausing it closes every live connection and refuses new ones, so
// the Adapter observes a real disconnect without stopping the broker; resuming
// lets the Adapter reconnect to the same real broker.
type brokerProxy struct {
	listener net.Listener
	target   string

	mutex  sync.Mutex
	conns  map[net.Conn]struct{}
	paused bool
	closed bool

	waitGroup sync.WaitGroup
}

// startBrokerProxy starts one loopback forwarder to the broker URL.
func startBrokerProxy(t *testing.T, brokerURL string) *brokerProxy {
	t.Helper()
	target := strings.TrimPrefix(strings.TrimPrefix(brokerURL, "tcp://"), "mqtt://")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &brokerProxy{listener: listener, target: target, conns: make(map[net.Conn]struct{})}
	proxy.waitGroup.Add(1)
	go proxy.accept()
	t.Cleanup(proxy.Stop)
	return proxy
}

// URL returns the forwarder's Paho-compatible mqtt:// connection URL.
func (proxy *brokerProxy) URL() string { return "tcp://" + proxy.listener.Addr().String() }

// pause closes every live connection and refuses new ones.
func (proxy *brokerProxy) pause() {
	proxy.mutex.Lock()
	proxy.paused = true
	open := proxy.connectionsLocked()
	proxy.mutex.Unlock()
	for _, connection := range open {
		_ = connection.Close()
	}
}

// resume allows new connections again.
func (proxy *brokerProxy) resume() {
	proxy.mutex.Lock()
	proxy.paused = false
	proxy.mutex.Unlock()
}

// Stop closes the listener and every live connection and joins the forwarder.
func (proxy *brokerProxy) Stop() {
	proxy.mutex.Lock()
	if proxy.closed {
		proxy.mutex.Unlock()
		return
	}
	proxy.closed = true
	open := proxy.connectionsLocked()
	proxy.mutex.Unlock()
	_ = proxy.listener.Close()
	for _, connection := range open {
		_ = connection.Close()
	}
	proxy.waitGroup.Wait()
}

// connectionsLocked copies the tracked connections. The caller holds mutex.
func (proxy *brokerProxy) connectionsLocked() []net.Conn {
	open := make([]net.Conn, 0, len(proxy.conns))
	for connection := range proxy.conns {
		open = append(open, connection)
	}
	return open
}

// accept forwards each accepted connection unless the proxy is paused.
func (proxy *brokerProxy) accept() {
	defer proxy.waitGroup.Done()
	for {
		connection, err := proxy.listener.Accept()
		if err != nil {
			return
		}
		proxy.mutex.Lock()
		refused := proxy.paused || proxy.closed
		if !refused {
			proxy.conns[connection] = struct{}{}
		}
		proxy.mutex.Unlock()
		if refused {
			_ = connection.Close()
			continue
		}
		proxy.waitGroup.Add(1)
		go proxy.forward(connection)
	}
}

// forward pipes one accepted connection to the broker in both directions.
func (proxy *brokerProxy) forward(client net.Conn) {
	defer proxy.waitGroup.Done()
	upstream, err := net.Dial("tcp", proxy.target)
	if err != nil {
		proxy.drop(client)
		return
	}
	proxy.mutex.Lock()
	proxy.conns[upstream] = struct{}{}
	refused := proxy.paused || proxy.closed
	proxy.mutex.Unlock()
	if refused {
		proxy.drop(client)
		proxy.drop(upstream)
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	<-done
	proxy.drop(client)
	proxy.drop(upstream)
}

// drop removes and closes one tracked connection.
func (proxy *brokerProxy) drop(connection net.Conn) {
	proxy.mutex.Lock()
	delete(proxy.conns, connection)
	proxy.mutex.Unlock()
	_ = connection.Close()
}
