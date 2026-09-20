package zwavejs //nolint:testpackage // Tests exercise package-private lifecycle supervision.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

const (
	// integrationHomeID is the sanitized Home ID of the scripted Z-Wave network.
	// It is not a real household Home ID.
	integrationHomeID = 0x1a2b3c4d

	// integrationSwitchNodeID and integrationDimmerNodeID are the two scripted
	// product endpoints A12 covers: one Binary Switch and one Multilevel Switch.
	integrationSwitchNodeID = 23
	integrationDimmerNodeID = 24

	// integrationBinarySwitchCC and integrationMultilevelSwitchCC are the Z-Wave
	// Command Classes the two scripted nodes expose.
	integrationBinarySwitchCC     = 37
	integrationMultilevelSwitchCC = 38

	// integrationSetValueSuccess is the numeric schema-29 SetValueStatus that
	// reports the device executed the command successfully.
	integrationSetValueSuccess = 255

	integrationRequestBuffer = 8
	integrationTimeout       = 30 * time.Second
)

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

// TestRunAssemblesProcessAcrossScriptedServerAndCore is the A12 proof: one process
// assembly crosses a scripted Z-Wave JS WebSocket server, the Adapter, the SDK
// Session over embedded JetStream NATS, the Core Registration, Observation, and
// Command transports, and SQLite-backed reads for one switch and one dimmer.
//
// The oracles are deliberately Command-evidence-shaped: the switch is satisfied
// only after the scripted server returns a fresh matching polled value, and a
// mismatching polled value is published as linked evidence without satisfying
// the Command.
func TestRunAssemblesProcessAcrossScriptedServerAndCore(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	server := startScriptedZWaveJSServer(t)
	natsServer := startProcessNATSServer(t)
	coreConnection, err := natsgo.Connect(natsServer.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(coreConnection.Close)

	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = platformdb.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
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
	startCoreTransports(ctx, t, coreConnection, durable, validator, service, logger)

	runContext, stopRun := context.WithCancel(ctx)
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{
			AdapterID: "zwavejs",
			NATSURL:   natsServer.ClientURL(),
			ZWaveJS:   ZWaveJSConfig{URL: server.url},
		}, logger)
	}()

	harness := &integrationHarness{
		t: t, ctx: ctx, database: database, service: service, server: server,
		runErrors: runErrors,
	}
	entities := harness.waitForSwitchAndDimmer()
	harness.assertSwitchAndDimmerShape(entities)
	harness.proveSwitchCommandNeedsMatchingPoll(entities.switchPower)
	harness.proveDimmerBrightnessCommand(entities.dimmerBrightness)
	harness.assertSQLiteReads(entities)

	stopRun()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatalf("Run returned after parent cancellation: %v", runErr)
		}
	case <-ctx.Done():
		t.Fatalf("Run did not stop after parent cancellation: %v", ctx.Err())
	}
}

// integrationHarness carries one assembly test's Core service, database, and
// scripted server.
type integrationHarness struct {
	t         *testing.T
	ctx       context.Context
	database  *sql.DB
	service   *devices.Service
	server    *scriptedZWaveJSServer
	runErrors <-chan error
}

// integrationEntities is the switch and dimmer Entity set A12 registers.
type integrationEntities struct {
	switchPower      devices.EntityWithState
	dimmerPower      devices.EntityWithState
	dimmerBrightness devices.EntityWithState
}

// proveSwitchCommandNeedsMatchingPoll drives one binary power Command through
// Core and fails unless satisfaction arrives only from a fresh, matching,
// command-linked poll result. A mismatching poll result must be published as
// linked evidence while the Command stays accepted.
func (harness *integrationHarness) proveSwitchCommandNeedsMatchingPoll(
	switchPower devices.EntityWithState,
) {
	entityID := switchPower.Entity.ID
	command := harness.executeCommand(entityID, `{"value":true}`)

	setRequest := harness.server.nextSetValue()
	assertValueRequest(harness.t, "switch set", setRequest, integrationSwitchNodeID,
		integrationBinarySwitchCC, "targetValue", "true")
	harness.waitForCommandStatus(entityID, devices.CommandStatusAccepted)
	harness.requireCommandStillOpen(command, "before any poll result")

	mismatchPoll := harness.server.nextPollValue()
	assertValueRequest(harness.t, "switch poll", mismatchPoll, integrationSwitchNodeID,
		integrationBinarySwitchCC, "currentValue", "")
	harness.waitForObservationCount(entityID, 1)
	harness.requireCommandStillOpen(command, "before its first poll result was sent")

	// A poll result that does not match the requested State is still linked
	// evidence, but it must not satisfy the Command.
	harness.server.pollReplies <- json.RawMessage("false")
	harness.waitForObservationCount(entityID, 2)
	harness.assertCommandStatus(entityID, devices.CommandStatusAccepted)

	// Only a fresh correlated poll may satisfy the Command, so the ordinary
	// post-dispatch update is a wake hint rather than the outcome.
	harness.server.emitValueUpdated(
		integrationSwitchNodeID, integrationBinarySwitchCC, 0, "currentValue", "true",
	)
	harness.waitForObservationCount(entityID, 3)
	harness.requireCommandStillOpen(command, "after an ordinary post-dispatch value update")

	matchPoll := harness.server.nextPollValue()
	assertValueRequest(harness.t, "switch re-poll", matchPoll, integrationSwitchNodeID,
		integrationBinarySwitchCC, "currentValue", "")
	harness.server.pollReplies <- json.RawMessage("true")

	result := harness.waitForCommandResult(command)
	if result.Outcome != devices.OutcomeObserved || result.Value == nil ||
		string(*result.Value) != "true" {
		harness.t.Fatalf("switch Command result = %#v", result)
	}
	harness.waitForObservationCount(entityID, 4)
	harness.assertStoredState(entityID, "true")
}

// proveDimmerBrightnessCommand drives one native 0-99 brightness Command through
// Core and fails unless the scripted server receives the planned level on the
// Multilevel Switch target Value and the Command is satisfied by the matching
// poll result.
func (harness *integrationHarness) proveDimmerBrightnessCommand(
	dimmerBrightness devices.EntityWithState,
) {
	entityID := dimmerBrightness.Entity.ID
	command := harness.executeCommand(entityID, `{"value":99}`)

	setRequest := harness.server.nextSetValue()
	assertValueRequest(harness.t, "dimmer set", setRequest, integrationDimmerNodeID,
		integrationMultilevelSwitchCC, "targetValue", "99")
	harness.waitForCommandStatus(entityID, devices.CommandStatusAccepted)

	poll := harness.server.nextPollValue()
	assertValueRequest(harness.t, "dimmer poll", poll, integrationDimmerNodeID,
		integrationMultilevelSwitchCC, "currentValue", "")
	harness.server.pollReplies <- json.RawMessage("99")

	result := harness.waitForCommandResult(command)
	if result.Outcome != devices.OutcomeObserved || result.Value == nil ||
		string(*result.Value) != "99" {
		harness.t.Fatalf("dimmer Command result = %#v", result)
	}
	harness.assertStoredState(entityID, "99")
}

// assertSQLiteReads proves the assembly wrote Adapter-owned mappings and durable
// Command history that other Core reads can see.
func (harness *integrationHarness) assertSQLiteReads(entities integrationEntities) {
	harness.t.Helper()
	var mappings, commands int
	if err := harness.database.QueryRowContext(
		harness.ctx,
		"SELECT COUNT(*) FROM adapter_entity_mappings WHERE adapter_id = ?",
		"zwavejs",
	).Scan(&mappings); err != nil {
		harness.t.Fatal(err)
	}
	if mappings != 3 {
		harness.t.Fatalf("adapter_entity_mappings = %d, want 3", mappings)
	}
	if err := harness.database.QueryRowContext(
		harness.ctx,
		"SELECT COUNT(*) FROM commands WHERE entity_id IN (?, ?)",
		string(entities.switchPower.Entity.ID),
		string(entities.dimmerBrightness.Entity.ID),
	).Scan(&commands); err != nil {
		harness.t.Fatal(err)
	}
	if commands != 2 {
		harness.t.Fatalf("commands = %d, want 2", commands)
	}
	for _, entity := range []devices.EntityWithState{entities.switchPower, entities.dimmerBrightness} {
		record, ok := harness.latestCommand(entity.Entity.ID)
		if !ok || record.Status != devices.CommandStatusSatisfied {
			harness.t.Fatalf("Command record for %s = %#v", entity.Entity.ID, record)
		}
	}
}

// assertSwitchAndDimmerShape fails if the switch and dimmer do not register with
// deterministic Device kind, Entity names, support, and initial State.
func (harness *integrationHarness) assertSwitchAndDimmerShape(entities integrationEntities) {
	harness.t.Helper()
	if string(entities.switchPower.Entity.Support) != `{"state":{},"operations":{"set":{}}}` {
		harness.t.Fatalf("switch power support = %s", entities.switchPower.Entity.Support)
	}
	const wantBrightnessSupport = `{"state":{"maximum":99},"operations":{"set":{"step":1}}}`
	if string(entities.dimmerBrightness.Entity.Support) != wantBrightnessSupport {
		harness.t.Fatalf("dimmer brightness support = %s", entities.dimmerBrightness.Entity.Support)
	}
	if string(entities.dimmerPower.Entity.Support) != `{"state":{},"operations":{"set":{}}}` {
		harness.t.Fatalf("dimmer power support = %s", entities.dimmerPower.Entity.Support)
	}
	if string(entities.switchPower.State.Value) != "false" ||
		string(entities.dimmerPower.State.Value) != "true" ||
		string(entities.dimmerBrightness.State.Value) != "15" {
		harness.t.Fatalf(
			"initial State switch=%s dimmer-power=%s dimmer-brightness=%s",
			entities.switchPower.State.Value,
			entities.dimmerPower.State.Value,
			entities.dimmerBrightness.State.Value,
		)
	}
	if entities.switchPower.Entity.Name != "Power" ||
		entities.dimmerBrightness.Entity.Name != "Brightness" {
		harness.t.Fatalf(
			"root Entity names = %q, %q",
			entities.switchPower.Entity.Name, entities.dimmerBrightness.Entity.Name,
		)
	}
	page, err := harness.service.ListDevices(harness.ctx, devices.ListDevicesParams{Limit: 10})
	if err != nil {
		harness.t.Fatal(err)
	}
	kinds := make(map[devices.DeviceID]devices.DeviceKind, len(page.Items))
	names := make(map[devices.DeviceID]string, len(page.Items))
	for _, device := range page.Items {
		kinds[device.ID] = device.Kind
		names[device.ID] = device.Name
	}
	switchKind := kinds[entities.switchPower.Entity.DeviceID]
	dimmerKind := kinds[entities.dimmerPower.Entity.DeviceID]
	if len(page.Items) != 2 || switchKind != devices.DeviceKindRelay ||
		dimmerKind != devices.DeviceKindLight {
		harness.t.Fatalf("Device kinds = %#v (switch %q, dimmer %q)", kinds, switchKind, dimmerKind)
	}
	if names[entities.switchPower.Entity.DeviceID] != "Fixture Switch" ||
		names[entities.dimmerPower.Entity.DeviceID] != "Fixture Dimmer" {
		harness.t.Fatalf("Device names = %#v", names)
	}
}

// waitForSwitchAndDimmer returns the registered switch and dimmer Entities once
// Core reports their initial State and availability through SQLite-backed reads.
func (harness *integrationHarness) waitForSwitchAndDimmer() integrationEntities {
	harness.t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		page, err := harness.service.ListEntities(
			harness.ctx,
			devices.ListEntitiesParams{Limit: 10},
		)
		if err != nil {
			harness.t.Fatal(err)
		}
		if len(page.Items) == 3 {
			if entities, ok := classifyIntegrationEntities(page.Items); ok {
				return entities
			}
		}
		harness.waitForProgress("the switch and dimmer Entities to become ready")
		<-ticker.C
	}
}

// classifyIntegrationEntities selects the switch power, dimmer power, and dimmer
// brightness Entities of one listing. It reports false until all three carry
// State and explicit availability.
func classifyIntegrationEntities(items []devices.EntityWithState) (integrationEntities, bool) {
	var entities integrationEntities
	var foundSwitch, foundDimmerPower, foundDimmer bool
	for _, entity := range items {
		if entity.State == nil || entity.Availability.Status != devices.EntityAvailabilityAvailable {
			continue
		}
		switch {
		case entity.Entity.TypeID == "hearth.power/v1" && string(entity.State.Value) == "false":
			entities.switchPower = entity
			foundSwitch = true
		case entity.Entity.TypeID == "hearth.power/v1" && string(entity.State.Value) == "true":
			entities.dimmerPower = entity
			foundDimmerPower = true
		case entity.Entity.TypeID == "hearth.brightness/v1" && string(entity.State.Value) == "15":
			entities.dimmerBrightness = entity
			foundDimmer = true
		}
	}
	return entities, foundSwitch && foundDimmerPower && foundDimmer
}

// executeCommand runs one Command through Core's public Service seam.
func (harness *integrationHarness) executeCommand(
	entityID devices.EntityID,
	parameters string,
) <-chan commandOutcome {
	harness.t.Helper()
	done := make(chan commandOutcome, 1)
	go func() {
		result, err := harness.service.ExecuteCommand(harness.ctx, devices.CommandInput{
			EntityID:      entityID,
			OperationName: devices.OperationNameSet,
			Parameters:    devices.CommandParameters(parameters),
		})
		done <- commandOutcome{result: result, err: err}
	}()
	return done
}

type commandOutcome struct {
	result devices.CommandResult
	err    error
}

// commandReturned reports whether Core already answered the Command.
func (harness *integrationHarness) requireCommandStillOpen(
	done <-chan commandOutcome,
	stage string,
) {
	harness.t.Helper()
	select {
	case outcome := <-done:
		harness.t.Fatalf(
			"Command completed %s: result=%#v error=%v",
			stage, outcome.result, outcome.err,
		)
	default:
	}
}

// waitForCommandResult blocks until Core answers one Command.
func (harness *integrationHarness) waitForCommandResult(
	done <-chan commandOutcome,
) devices.CommandResult {
	harness.t.Helper()
	select {
	case outcome := <-done:
		if outcome.err != nil {
			harness.t.Fatalf("Command failed: %v", outcome.err)
		}
		return outcome.result
	case <-harness.ctx.Done():
		harness.t.Fatalf("waiting for the Command result: %v", harness.ctx.Err())
		return devices.CommandResult{}
	}
}

// waitForCommandStatus blocks until one Entity's latest Command reaches status.
func (harness *integrationHarness) waitForCommandStatus(
	entityID devices.EntityID,
	status devices.CommandStatus,
) {
	harness.t.Helper()
	for {
		record, ok := harness.latestCommand(entityID)
		if ok && record.Status == status {
			return
		}
		harness.waitForProgress(
			fmt.Sprintf("Command status %s for %s", status, entityID),
		)
		time.Sleep(time.Millisecond)
	}
}

// assertCommandStatus requires one Entity's latest Command to hold status.
func (harness *integrationHarness) assertCommandStatus(
	entityID devices.EntityID,
	status devices.CommandStatus,
) {
	harness.t.Helper()
	record, ok := harness.latestCommand(entityID)
	if !ok || record.Status != status {
		harness.t.Fatalf("Command status for %s = %#v, want %s", entityID, record, status)
	}
}

// latestCommand reads one Entity's newest Command record through the Service.
func (harness *integrationHarness) latestCommand(
	entityID devices.EntityID,
) (devices.CommandRecord, bool) {
	harness.t.Helper()
	page, err := harness.service.ListEntityCommands(
		harness.ctx,
		devices.ListEntityCommandsParams{EntityID: entityID, Limit: 1},
	)
	if err != nil {
		harness.t.Fatal(err)
	}
	if len(page.Items) == 0 {
		return devices.CommandRecord{}, false
	}
	return page.Items[0], true
}

// waitForObservationCount blocks until one Entity has exactly count durable
// Observations, proving which reports Core committed.
func (harness *integrationHarness) waitForObservationCount(
	entityID devices.EntityID,
	count int,
) {
	harness.t.Helper()
	for {
		var seen int
		if err := harness.database.QueryRowContext(
			harness.ctx,
			"SELECT COUNT(*) FROM observations WHERE entity_id = ?",
			string(entityID),
		).Scan(&seen); err != nil {
			harness.t.Fatal(err)
		}
		if seen == count {
			return
		}
		if seen > count {
			harness.t.Fatalf("Observations for %s = %d, want %d", entityID, seen, count)
		}
		harness.waitForProgress(
			fmt.Sprintf("Observation %d for %s", count, entityID),
		)
		time.Sleep(time.Millisecond)
	}
}

// assertStoredState reads one Entity's persisted State straight from SQLite.
func (harness *integrationHarness) assertStoredState(
	entityID devices.EntityID,
	want string,
) {
	harness.t.Helper()
	var value string
	if err := harness.database.QueryRowContext(
		harness.ctx,
		"SELECT value_json FROM entity_states WHERE entity_id = ?",
		string(entityID),
	).Scan(&value); err != nil {
		harness.t.Fatal(err)
	}
	if value != want {
		harness.t.Fatalf("stored State for %s = %s, want %s", entityID, value, want)
	}
}

// waitForProgress fails the test when Run exited or the parent context ended
// before one bounded eventual condition held.
func (harness *integrationHarness) waitForProgress(description string) {
	harness.t.Helper()
	select {
	case runErr := <-harness.runErrors:
		harness.t.Fatalf("Run exited while waiting for %s: %v", description, runErr)
	case <-harness.ctx.Done():
		harness.t.Fatalf("waiting for %s: %v", description, harness.ctx.Err())
	default:
	}
}

// scriptedValueRequest is one decoded node.set_value or node.poll_value request.
type scriptedValueRequest struct {
	NodeID       int
	CommandClass int
	Endpoint     int
	Property     string
	Value        json.RawMessage
}

// assertValueRequest requires one write or poll to name the exact planned Value
// and, for a write, the exact encoded value.
func assertValueRequest(
	t *testing.T,
	name string,
	request scriptedValueRequest,
	nodeID, commandClass int,
	property, value string,
) {
	t.Helper()
	if request.NodeID != nodeID || request.CommandClass != commandClass ||
		request.Endpoint != 0 || request.Property != property {
		t.Fatalf("%s requested %#v", name, request)
	}
	if value != "" && string(request.Value) != value {
		t.Fatalf("%s value = %s, want %s", name, request.Value, value)
	}
}

// scriptedZWaveJSServer is an in-process schema-29 Z-Wave JS server. It scripts
// the version frame, the correlated handshake, the complete start-listening
// snapshot, and every later request, so the process test crosses the real
// WebSocket client instead of a seam.
type scriptedZWaveJSServer struct {
	t            *testing.T
	url          string
	writeMutex   sync.Mutex
	socketMutex  sync.Mutex
	socket       *websocket.Conn
	setRequests  chan scriptedValueRequest
	pollRequests chan scriptedValueRequest
	pollReplies  chan json.RawMessage
}

// startScriptedZWaveJSServer starts one scripted server for one test.
func startScriptedZWaveJSServer(t *testing.T) *scriptedZWaveJSServer {
	t.Helper()
	server := &scriptedZWaveJSServer{
		t:            t,
		setRequests:  make(chan scriptedValueRequest, integrationRequestBuffer),
		pollRequests: make(chan scriptedValueRequest, integrationRequestBuffer),
		pollReplies:  make(chan json.RawMessage, integrationRequestBuffer),
	}
	httpServer := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			socket, err := websocket.Accept(writer, request, nil)
			if err != nil {
				t.Errorf("scripted Z-Wave JS server accept: %v", err)
				return
			}
			defer func() { _ = socket.CloseNow() }()
			server.serve(request.Context(), socket)
		},
	))
	t.Cleanup(httpServer.Close)
	server.url = "ws" + strings.TrimPrefix(httpServer.URL, "http")
	return server
}

// serve scripts one connection generation.
func (server *scriptedZWaveJSServer) serve(ctx context.Context, socket *websocket.Conn) {
	server.socketMutex.Lock()
	server.socket = socket
	server.socketMutex.Unlock()
	server.send(ctx, versionFrame())
	for {
		messageType, payload, err := socket.Read(ctx)
		if err != nil {
			return
		}
		if messageType != websocket.MessageText {
			server.t.Errorf("client wrote a non-text frame of type %d", int(messageType))
			return
		}
		var raw map[string]json.RawMessage
		if err = json.Unmarshal(payload, &raw); err != nil {
			server.t.Errorf("client frame did not decode: %v", err)
			return
		}
		messageID := rawString(raw, "messageId")
		switch command := rawString(raw, "command"); command {
		case "initialize":
			server.replySuccess(ctx, messageID, map[string]any{})
		case "start_listening":
			server.replySuccess(ctx, messageID, startListeningSnapshot())
		case "node.set_value":
			server.recordSetValue(ctx, messageID, raw)
		case "node.poll_value":
			if !server.answerPoll(ctx, messageID, raw) {
				return
			}
		case "node.get_state":
			server.replySuccess(ctx, messageID, map[string]any{
				"state": nodeStateFor(intField(raw, "nodeId")),
			})
		default:
			server.t.Errorf("unexpected client command %q", command)
			return
		}
	}
}

// recordSetValue records one write and answers the documented schema-29
// `{result:{status}}` success shape.
func (server *scriptedZWaveJSServer) recordSetValue(
	ctx context.Context,
	messageID string,
	raw map[string]json.RawMessage,
) {
	server.setRequests <- decodeValueRequest(raw)
	server.replySuccess(ctx, messageID, map[string]any{
		"result": map[string]any{"status": integrationSetValueSuccess},
	})
}

// answerPoll records one poll and answers it only when the test supplies the
// freshly read value, so the test controls exactly when Command evidence lands.
func (server *scriptedZWaveJSServer) answerPoll(
	ctx context.Context,
	messageID string,
	raw map[string]json.RawMessage,
) bool {
	server.pollRequests <- decodeValueRequest(raw)
	select {
	case value := <-server.pollReplies:
		server.replySuccess(ctx, messageID, map[string]any{"value": value})
		return true
	case <-ctx.Done():
		return false
	}
}

// nextSetValue waits for the next correlated write.
func (server *scriptedZWaveJSServer) nextSetValue() scriptedValueRequest {
	server.t.Helper()
	select {
	case request := <-server.setRequests:
		return request
	case <-time.After(integrationTimeout):
		server.t.Fatal("timed out waiting for node.set_value")
		return scriptedValueRequest{}
	}
}

// nextPollValue waits for the next correlated poll.
func (server *scriptedZWaveJSServer) nextPollValue() scriptedValueRequest {
	server.t.Helper()
	select {
	case request := <-server.pollRequests:
		return request
	case <-time.After(integrationTimeout):
		server.t.Fatal("timed out waiting for node.poll_value")
		return scriptedValueRequest{}
	}
}

// emitValueUpdated sends one node `value updated` Event, the ordinary post-set
// report a Z-Wave JS server may emit and which must never satisfy a Command.
func (server *scriptedZWaveJSServer) emitValueUpdated(
	nodeID, commandClass, endpoint int,
	property string,
	newValue string,
) {
	server.t.Helper()
	server.send(context.Background(), map[string]any{
		"type": "event",
		"event": map[string]any{
			"source": "node",
			"event":  "value updated",
			"nodeId": nodeID,
			"args": map[string]any{
				"commandClass": commandClass,
				"endpoint":     endpoint,
				"property":     property,
				"newValue":     json.RawMessage(newValue),
			},
		},
	})
}

// replySuccess answers one request with a successful result.
func (server *scriptedZWaveJSServer) replySuccess(
	ctx context.Context,
	messageID string,
	result any,
) {
	server.send(ctx, map[string]any{
		"type": "result", "messageId": messageID, "success": true, "result": result,
	})
}

// send writes one text frame under the write lock shared with event emission.
func (server *scriptedZWaveJSServer) send(ctx context.Context, value any) {
	server.t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		server.t.Errorf("encode scripted frame: %v", err)
		return
	}
	server.socketMutex.Lock()
	socket := server.socket
	server.socketMutex.Unlock()
	if socket == nil {
		return
	}
	server.writeMutex.Lock()
	defer server.writeMutex.Unlock()
	if err = socket.Write(ctx, websocket.MessageText, payload); err != nil {
		server.t.Logf("scripted server write stopped: %v", err)
	}
}

// versionFrame is the version frame of Z-Wave JS server 3.10.1: schema 0..50
// over the scripted Home ID.
func versionFrame() map[string]any {
	return map[string]any{
		"type":             "version",
		"driverVersion":    "15.0.0",
		"serverVersion":    "3.10.1",
		"homeId":           integrationHomeID,
		"minSchemaVersion": 0,
		"maxSchemaVersion": 50,
	}
}

// startListeningSnapshot is the complete scripted inventory: one ready Binary
// Switch and one ready Multilevel Switch.
func startListeningSnapshot() map[string]any {
	return map[string]any{
		"state": map[string]any{
			"controller": map[string]any{"homeId": integrationHomeID},
			"driver":     map[string]any{"ready": true},
			"nodes":      []any{nodeStateFor(integrationSwitchNodeID), nodeStateFor(integrationDimmerNodeID)},
		},
	}
}

// nodeStateFor is the scripted dump of one switch or dimmer node.
func nodeStateFor(nodeID int) map[string]any {
	switch nodeID {
	case integrationSwitchNodeID:
		return map[string]any{
			"nodeId":           nodeID,
			"ready":            true,
			"status":           4,
			"interviewStage":   "Complete",
			"isControllerNode": false,
			"isListening":      true,
			"name":             "Fixture Switch",
			"location":         "Fixture",
			"label":            "Switch",
			"manufacturerId":   271,
			"productType":      1,
			"productId":        2,
			"endpoints":        []any{map[string]any{"index": 0}},
			"values": []any{
				binaryValue("currentValue", true, false, false),
				binaryValue("targetValue", false, true, false),
			},
		}
	case integrationDimmerNodeID:
		return map[string]any{
			"nodeId":           nodeID,
			"ready":            true,
			"status":           4,
			"interviewStage":   "Complete",
			"isControllerNode": false,
			"isListening":      true,
			"name":             "Fixture Dimmer",
			"location":         "Fixture",
			"label":            "Dimmer",
			"manufacturerId":   271,
			"productType":      1,
			"productId":        3,
			"endpoints":        []any{map[string]any{"index": 0}},
			"values": []any{
				numberValue("currentValue", true, false, 0, 99, 15),
				numberValue("targetValue", false, true, 0, 99, 80),
			},
		}
	default:
		return map[string]any{}
	}
}

// binaryValue is one Binary Switch Value of the scripted node inventory.
func binaryValue(property string, readable, writeable, value bool) map[string]any {
	return map[string]any{
		"commandClass": integrationBinarySwitchCC,
		"property":     property,
		"metadata": map[string]any{
			"type": "boolean", "readable": readable, "writeable": writeable,
		},
		"value": value,
	}
}

// numberValue is one Multilevel Switch Value of the scripted node inventory.
func numberValue(
	property string,
	readable, writeable bool,
	minimum, maximum, value int,
) map[string]any {
	return map[string]any{
		"commandClass": integrationMultilevelSwitchCC,
		"property":     property,
		"metadata": map[string]any{
			"type": "number", "readable": readable, "writeable": writeable,
			"min": minimum, "max": maximum,
		},
		"value": value,
	}
}

// decodeValueRequest decodes one node.set_value or node.poll_value request.
func decodeValueRequest(raw map[string]json.RawMessage) scriptedValueRequest {
	request := scriptedValueRequest{NodeID: intField(raw, "nodeId"), Value: raw["value"]}
	var valueID struct {
		CommandClass int    `json:"commandClass"`
		Endpoint     int    `json:"endpoint"`
		Property     string `json:"property"`
	}
	if err := json.Unmarshal(raw["valueId"], &valueID); err == nil {
		request.CommandClass = valueID.CommandClass
		request.Endpoint = valueID.Endpoint
		request.Property = valueID.Property
	}
	return request
}

// rawString decodes one string field of a client frame.
func rawString(raw map[string]json.RawMessage, name string) string {
	var value string
	if err := json.Unmarshal(raw[name], &value); err != nil {
		return ""
	}
	return value
}

// intField decodes one integer field of a client frame.
func intField(raw map[string]json.RawMessage, name string) int {
	var value int
	if err := json.Unmarshal(raw[name], &value); err != nil {
		return 0
	}
	return value
}

// startProcessNATSServer starts one embedded JetStream server for the assembly.
func startProcessNATSServer(t *testing.T) *natsserver.Server {
	t.Helper()
	server, err := natsserver.NewServer(&natsserver.Options{
		ServerName: "hearth-zwavejs-process-test",
		Host:       "127.0.0.1",
		Port:       -1,
		NoSigs:     true,
		NoLog:      true,
		JetStream:  true,
		StoreDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(10 * time.Second) {
		server.Shutdown()
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})
	return server
}

// startCoreTransports starts every Core-side device transport the assembly
// crosses.
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
