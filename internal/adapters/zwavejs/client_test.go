package zwavejs //nolint:testpackage // Tests exercise the private schema-29 protocol client.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const (
	// testHomeID is the sanitized Home ID of the scripted network. It is not a
	// real household Home ID.
	testHomeID = 0x1a2b3c4d
	// testNodeID is the node the scripted node state describes.
	testNodeID = 23
	// testCommandClassBinarySwitch and testCommandClassMultilevelSwitch are the
	// Z-Wave Command Class numbers the Adapter plans.
	testCommandClassBinarySwitch     = 37
	testCommandClassMultilevelSwitch = 38
	// testTimeout bounds one scripted test.
	testTimeout = 10 * time.Second
	// testQuietPeriod is how long a test waits to prove nothing was written.
	testQuietPeriod = 250 * time.Millisecond
	// testRequestDeadline is the deadline of a scripted request that never
	// answers.
	testRequestDeadline = 150 * time.Millisecond
	// testBlockedWriteDeadline bounds a scripted blocked write. It is long enough
	// that the request reaches its frame write before the deadline fires.
	testBlockedWriteDeadline = time.Second
	// testBlockingPayloadBytes is larger than the kernel socket buffers a
	// loopback peer can hold, so a request frame that carries it blocks once the
	// scripted server stops reading.
	testBlockingPayloadBytes = 8 << 20
	// scriptedRequestBuffer holds requests a script reads for the test.
	scriptedRequestBuffer = 64
)

// scriptedRequest is one decoded client request frame.
type scriptedRequest struct {
	Payload []byte
	Raw     map[string]json.RawMessage
}

// decodeScriptedRequest decodes one client frame.
func decodeScriptedRequest(payload []byte) (scriptedRequest, error) {
	request := scriptedRequest{Payload: payload}
	if err := json.Unmarshal(payload, &request.Raw); err != nil {
		return scriptedRequest{}, err
	}
	return request, nil
}

// messageID returns the correlation ID the client assigned.
func (request scriptedRequest) messageID() string { return request.stringField("messageId") }

// command returns the Z-Wave JS server command name.
func (request scriptedRequest) command() string { return request.stringField("command") }

// stringField decodes one string field of the request.
func (request scriptedRequest) stringField(name string) string {
	var value string
	if err := json.Unmarshal(request.Raw[name], &value); err != nil {
		return ""
	}
	return value
}

// scriptedServer is an in-process Z-Wave JS server for one test. The script
// function runs on the server goroutine and drives every frame the client sees.
type scriptedServer struct {
	t        *testing.T
	url      string
	requests chan scriptedRequest
}

// startScriptedServer starts one scripted Z-Wave JS server.
func startScriptedServer(t *testing.T, script func(session *scriptedSession)) *scriptedServer {
	t.Helper()
	server := &scriptedServer{
		t:        t,
		requests: make(chan scriptedRequest, scriptedRequestBuffer),
	}
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		socket, err := websocket.Accept(writer, request, nil)
		if err != nil {
			if request.Context().Err() == nil {
				t.Errorf("scripted server accept: %v", err)
			}
			return
		}
		session := &scriptedSession{t: t, server: server, socket: socket, ctx: request.Context()}
		defer func() { _ = socket.CloseNow() }()
		script(session)
	}))
	t.Cleanup(httpServer.Close)
	server.url = "ws" + strings.TrimPrefix(httpServer.URL, "http")
	return server
}

// nextRequest waits for the next request the script read.
func (server *scriptedServer) nextRequest() scriptedRequest {
	server.t.Helper()
	select {
	case request := <-server.requests:
		return request
	case <-time.After(testTimeout):
		server.t.Fatal("timed out waiting for a client request")
		return scriptedRequest{}
	}
}

// drainHandshake removes and checks the initialize and start_listening requests
// that completeHandshake recorded, so a test can inspect the requests after them.
func (server *scriptedServer) drainHandshake() {
	server.t.Helper()
	if request := server.nextRequest(); request.command() != commandInitialize {
		server.t.Fatalf("first recorded request = %q, want %q", request.command(), commandInitialize)
	}
	if request := server.nextRequest(); request.command() != commandStartListening {
		server.t.Fatalf("second recorded request = %q, want %q", request.command(), commandStartListening)
	}
}

// expectNoRequest fails when the client writes a request within the quiet
// period.
func (server *scriptedServer) expectNoRequest() {
	server.t.Helper()
	select {
	case request := <-server.requests:
		server.t.Fatalf("unexpected client request %q", request.command())
	case <-time.After(testQuietPeriod):
	}
}

// scriptedSession is the server half of one connection. Every read is recorded
// for the test; every write is an exact frame.
type scriptedSession struct {
	t      *testing.T
	server *scriptedServer
	socket *websocket.Conn
	ctx    context.Context
}

// awaitRequest reads and records the next client frame.
func (session *scriptedSession) awaitRequest() (scriptedRequest, bool) {
	session.t.Helper()
	messageType, payload, err := session.socket.Read(session.ctx)
	if err != nil {
		// The read stops when the test's connection cleanup closes the socket,
		// so this handler must not touch testing.T here: that would race with the
		// test's own teardown.
		return scriptedRequest{}, false
	}
	if messageType != websocket.MessageText {
		session.t.Errorf("client wrote a non-text frame of type %d", int(messageType))
		return scriptedRequest{}, false
	}
	request, err := decodeScriptedRequest(payload)
	if err != nil {
		session.t.Errorf("client request did not decode: %v", err)
		return scriptedRequest{}, false
	}
	select {
	case session.server.requests <- request:
	default:
	}
	return request, true
}

// waitForClose blocks until the client closes the connection.
func (session *scriptedSession) waitForClose() {
	<-session.ctx.Done()
}

// sendJSON writes one text frame. A write failure only means the test's
// connection cleanup already closed the socket, so it is ignored rather than
// reported through [testing.T] from a handler goroutine.
func (session *scriptedSession) sendJSON(value any) {
	session.t.Helper()
	_ = wsjson.Write(session.ctx, session.socket, value)
}

// sendRaw writes one text frame with exact bytes.
func (session *scriptedSession) sendRaw(payload string) {
	session.t.Helper()
	_ = session.socket.Write(session.ctx, websocket.MessageText, []byte(payload))
}

// sendBinary writes one binary frame.
func (session *scriptedSession) sendBinary(payload []byte) {
	session.t.Helper()
	_ = session.socket.Write(session.ctx, websocket.MessageBinary, payload)
}

// replySuccess answers one request with a successful result.
func (session *scriptedSession) replySuccess(messageID string, result any) {
	session.sendJSON(map[string]any{
		"type": "result", "messageId": messageID, "success": true, "result": result,
	})
}

// replyRejection answers one request with a schema-29 Z-Wave error, which is the
// only error shape schema 29 sends.
func (session *scriptedSession) replyRejection(messageID, message string) {
	session.sendJSON(map[string]any{
		"type": "result", "messageId": messageID, "success": false,
		"errorCode": "zwave_error", "zwaveErrorCode": -1, "zwaveErrorMessage": message,
	})
}

// completeHandshake answers the version frame, initialize, and start_listening
// of a compatible server. It reports whether the client continued.
func (session *scriptedSession) completeHandshake() bool {
	session.sendJSON(compatibleVersionFrame())
	initialize, ok := session.awaitRequest()
	if !ok {
		return false
	}
	if initialize.command() != commandInitialize {
		session.t.Errorf("second request = %q, want %q", initialize.command(), commandInitialize)
		return false
	}
	session.replySuccess(initialize.messageID(), map[string]any{})
	startListening, ok := session.awaitRequest()
	if !ok {
		return false
	}
	if startListening.command() != commandStartListening {
		session.t.Errorf("third request = %q, want %q", startListening.command(), commandStartListening)
		return false
	}
	session.replySuccess(startListening.messageID(), compatibleSnapshot())
	return true
}

// compatibleVersionFrame is the version frame of Z-Wave JS server 3.10.1 with
// schema 0..50. It carries an unknown field, which the client must ignore.
func compatibleVersionFrame() map[string]any {
	return map[string]any{
		"type":                 "version",
		"driverVersion":        "15.0.0",
		"serverVersion":        "3.10.1",
		"homeId":               testHomeID,
		"minSchemaVersion":     0,
		"maxSchemaVersion":     50,
		"unknownServerField":   "ignored",
		"enableDNSServiceDisc": false,
	}
}

// compatibleSnapshot is a sanitized start_listening snapshot: one ready,
// listening dimmer node with a root Multilevel Switch current/target pair. It
// carries unknown driver, node, and value fields, which the client must ignore.
func compatibleSnapshot() map[string]any {
	return map[string]any{
		"state": map[string]any{
			"controller": map[string]any{"homeId": testHomeID, "isHealNetworkActive": false},
			"driver":     map[string]any{"ready": true},
			"nodes":      []any{testNodeState()},
		},
	}
}

// testNodeState is the sanitized node inventory of the test dimmer.
func testNodeState() map[string]any {
	return map[string]any{
		"nodeId":           testNodeID,
		"index":            0,
		"ready":            true,
		"status":           4,
		"interviewStage":   "Complete",
		"isControllerNode": false,
		"isListening":      true,
		"name":             "Hallway Dimmer",
		"location":         "Hallway",
		"label":            "Dimmer",
		"manufacturerId":   271,
		"productType":      1,
		"productId":        2,
		"firmwareVersion":  "1.2",
		"endpoints": []any{
			map[string]any{"index": 0},
			map[string]any{"index": 1, "endpointLabel": "Second"},
		},
		"values": []any{
			map[string]any{
				"commandClass": testCommandClassMultilevelSwitch,
				"property":     "currentValue",
				"metadata": map[string]any{
					"type": "number", "readable": true, "writeable": false, "min": 0, "max": 99,
				},
				"value": 15,
			},
			map[string]any{
				"commandClass": testCommandClassMultilevelSwitch,
				"property":     "targetValue",
				"metadata":     map[string]any{"type": "number", "readable": false, "writeable": true},
				"value":        80,
			},
		},
	}
}

// valueID is one planned Value ID of the test node.
func testValueID(commandClass int, endpoint int, property string) valueID {
	return valueID{
		CommandClass: commandClass,
		Endpoint:     endpoint,
		Property:     valueProperty{Name: property},
	}
}

// dialConnection opens one production connection generation against a scripted
// server.
func dialConnection(t *testing.T, server *scriptedServer) zwaveConnection {
	t.Helper()
	var dialer zwaveDialer = websocketDialer{}
	connection, err := dialer.Dial(
		t.Context(),
		server.url,
		schemaVersion29,
		map[string]string{hearthUserAgentComponent: hearthAdapterVersion},
	)
	if err != nil {
		t.Fatalf("dial scripted server: %v", err)
	}
	t.Cleanup(connection.Close)
	return connection
}

// testContext bounds one test's client calls.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	t.Cleanup(cancel)
	return ctx
}

// startListening completes the handshake of one scripted connection.
func startListening(t *testing.T, connection zwaveConnection) (serverVersion, networkSnapshot) {
	t.Helper()
	version, snapshot, err := connection.StartListening(testContext(t))
	if err != nil {
		t.Fatalf("StartListening: %v", err)
	}
	return version, snapshot
}

// awaitLost waits for the terminal error of one connection generation.
func awaitLost(t *testing.T, connection zwaveConnection) error {
	t.Helper()
	select {
	case err, ok := <-connection.Lost():
		if !ok {
			t.Fatal("Lost closed without reporting a terminal error")
		}
		return err
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for the connection generation to end")
		return nil
	}
}

// requireErrorSameType asserts that err has the same type as want, which is a
// zero value of the expected error type.
func requireErrorSameType(t *testing.T, err error, want error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: no error, want %T", what, want)
	}
	if reflect.TypeOf(err) != reflect.TypeOf(want) {
		t.Fatalf("%s: error = %T (%v), want %T", what, err, err, want)
	}
}

// productionConnection exposes the concrete connection of one scripted
// generation, so a test can assert the bounded correlation bookkeeping the
// production client keeps.
func productionConnection(t *testing.T, connection zwaveConnection) *websocketConnection {
	t.Helper()
	internal, ok := connection.(*websocketConnection)
	if !ok {
		t.Fatalf("connection %T is not the production WebSocket client", connection)
	}
	return internal
}

// connectionCorrelations reports the live result waiters and the abandoned
// correlation tombstones of one generation.
func (connection *websocketConnection) connectionCorrelations() (int, int) {
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	return len(connection.pending), len(connection.abandoned)
}

// requireGenerationAlive asserts that one generation did not end within the
// quiet period, so a silent terminal failure cannot pass as "no error".
func requireGenerationAlive(t *testing.T, connection zwaveConnection) {
	t.Helper()
	select {
	case err, ok := <-connection.Lost():
		if !ok {
			t.Fatal("Lost closed without reporting a terminal error")
		}
		t.Fatalf("generation ended: %v", err)
	case <-time.After(testQuietPeriod):
	}
}

// stallAfterHandshake completes the handshake and then stops reading, so the
// next client frame larger than the socket buffers blocks.
func stallAfterHandshake(session *scriptedSession) {
	if !session.completeHandshake() {
		return
	}
	session.waitForClose()
}

// hugeJSONValue returns one valid JSON value larger than the socket buffers, so
// a request that carries it blocks until the peer reads or its write ends.
func hugeJSONValue() json.RawMessage {
	return json.RawMessage(`"` + strings.Repeat("x", testBlockingPayloadBytes) + `"`)
}

// hugePropertyValueID returns the test Value ID with a property name too large
// to fit in the socket buffers, so a poll that carries it blocks the same way.
func hugePropertyValueID() valueID {
	return valueID{
		CommandClass: testCommandClassMultilevelSwitch,
		Property:     valueProperty{Name: strings.Repeat("x", testBlockingPayloadBytes)},
	}
}

// requireWaiterReserved waits until one request has reserved its correlation
// waiter, which proves its frame write is at or past the write call.
func requireWaiterReserved(t *testing.T, connection *websocketConnection) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if pending, _ := connection.connectionCorrelations(); pending == 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for the request waiter to be reserved")
}

// This test protects the schema-29 handshake, snapshot decoding, and request
// wire shape, and fails if the client stops negotiating schema 29, stops
// identifying Hearth, ignores unknown JSON fields, or renumbers its requests.
func TestStartListeningNegotiatesSchema29AndReturnsSnapshot(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		session.waitForClose()
	})
	connection := dialConnection(t, server)

	version, snapshot := startListening(t, connection)

	requireCompatibleVersion(t, version)
	requireSnapshotNode(t, snapshot)
	if snapshot.receivedAt.IsZero() || snapshot.receivedAt.Location() != time.UTC {
		t.Fatalf("snapshot receive time = %v, want a non-zero UTC result-frame time", snapshot.receivedAt)
	}
	requireInitializeRequest(t, server.nextRequest())
	requireStartListeningRequest(t, server.nextRequest())
}

// This test protects an empty but complete network inventory and fails if
// explicit nodes: [] is treated like a missing or null node inventory.
func TestSnapshotAllowsAnEmptyNodeInventory(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		session.sendJSON(compatibleVersionFrame())
		initialize, ok := session.awaitRequest()
		if !ok {
			return
		}
		session.replySuccess(initialize.messageID(), map[string]any{})
		startListening, ok := session.awaitRequest()
		if !ok {
			return
		}
		session.replySuccess(startListening.messageID(), map[string]any{
			"state": map[string]any{
				"controller": map[string]any{"homeId": testHomeID},
				"nodes":      []any{},
			},
		})
		session.waitForClose()
	})
	connection := dialConnection(t, server)
	_, snapshot := startListening(t, connection)
	if snapshot.State.Nodes == nil || len(snapshot.State.Nodes) != 0 {
		t.Fatalf("snapshot nodes = %#v, want a present empty inventory", snapshot.State.Nodes)
	}
}

// requireCompatibleVersion asserts the consumed version-frame fields.
func requireCompatibleVersion(t *testing.T, version serverVersion) {
	t.Helper()
	if version.ServerVersion != "3.10.1" || version.DriverVersion != "15.0.0" {
		t.Fatalf("version = %#v, want server 3.10.1 and driver 15.0.0", version)
	}
	if version.MinSchemaVersion == nil || version.MaxSchemaVersion == nil ||
		*version.MinSchemaVersion != 0 || *version.MaxSchemaVersion != 50 {
		t.Fatalf("schema range = %v..%v, want 0..50", version.MinSchemaVersion, version.MaxSchemaVersion)
	}
	if version.HomeID == nil || *version.HomeID != testHomeID {
		t.Fatalf("version home id = %v, want %#x", version.HomeID, testHomeID)
	}
}

// requireSnapshotNode asserts the consumed snapshot and node fields of the
// compatible fixture.
func requireSnapshotNode(t *testing.T, snapshot networkSnapshot) {
	t.Helper()
	controller := snapshot.State.Controller
	if controller.HomeID == nil || *controller.HomeID != testHomeID {
		t.Fatalf("snapshot home id = %v, want %#x", controller.HomeID, testHomeID)
	}
	if len(snapshot.State.Nodes) != 1 {
		t.Fatalf("snapshot nodes = %d, want 1", len(snapshot.State.Nodes))
	}
	node := snapshot.State.Nodes[0]
	if node.NodeID != testNodeID || !node.Ready || !node.IsListening || node.IsController {
		t.Fatalf("node = %#v, want a ready listening non-controller node %d", node, testNodeID)
	}
	if node.InterviewStage != "Complete" || node.Name != "Hallway Dimmer" {
		t.Fatalf("node = %#v, want interview stage Complete and the node name", node)
	}
	if node.ManufacturerID == nil || *node.ManufacturerID != 271 {
		t.Fatalf("manufacturer id = %v, want 271", node.ManufacturerID)
	}
	if len(node.Endpoints) != 2 || node.Endpoints[1].EndpointLabel != "Second" {
		t.Fatalf("endpoints = %#v, want a labeled endpoint 1", node.Endpoints)
	}
	requireSnapshotValues(t, node.Values)
}

// requireSnapshotValues asserts the consumed Value fields of the fixture.
func requireSnapshotValues(t *testing.T, values []valueState) {
	t.Helper()
	if len(values) != 2 {
		t.Fatalf("values = %d, want 2", len(values))
	}
	current := values[0]
	if current.CommandClass != testCommandClassMultilevelSwitch || current.Endpoint != 0 {
		t.Fatalf("current value id = %#v, want multilevel switch root", current.valueID)
	}
	if current.Property.Name != "currentValue" || current.Property.Numeric {
		t.Fatalf("current property = %#v, want currentValue", current.Property)
	}
	if current.Metadata.Type != "number" || !current.Metadata.Readable || current.Metadata.Writeable {
		t.Fatalf("current metadata = %#v, want readable numeric metadata", current.Metadata)
	}
	if current.Metadata.Min == nil || *current.Metadata.Min != 0 ||
		current.Metadata.Max == nil || *current.Metadata.Max != 99 {
		t.Fatalf("current metadata bounds = %#v, want 0..99", current.Metadata)
	}
	if string(current.Value) != "15" {
		t.Fatalf("current value = %s, want 15", current.Value)
	}
}

// requireInitializeRequest asserts the exact initialize wire shape of the first
// request.
func requireInitializeRequest(t *testing.T, request scriptedRequest) {
	t.Helper()
	if request.command() != commandInitialize {
		t.Fatalf("first request = %q, want %q", request.command(), commandInitialize)
	}
	if request.messageID() != "1" {
		t.Fatalf("first message id = %q, want 1", request.messageID())
	}
	var payload struct {
		SchemaVersion                 int               `json:"schemaVersion"`
		AdditionalUserAgentComponents map[string]string `json:"additionalUserAgentComponents"`
	}
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != schemaVersion29 {
		t.Fatalf("initialize schema version = %d, want %d", payload.SchemaVersion, schemaVersion29)
	}
	if !reflect.DeepEqual(payload.AdditionalUserAgentComponents, map[string]string{"hearth": "0.1.0"}) {
		t.Fatalf("initialize user agent = %#v, want hearth 0.1.0", payload.AdditionalUserAgentComponents)
	}
}

// requireStartListeningRequest asserts the exact second request.
func requireStartListeningRequest(t *testing.T, request scriptedRequest) {
	t.Helper()
	if request.command() != commandStartListening {
		t.Fatalf("second request = %q, want %q", request.command(), commandStartListening)
	}
	if request.messageID() != "2" {
		t.Fatalf("second message id = %q, want 2", request.messageID())
	}
}

// This test protects request correlation under concurrent calls and fails if a
// result is routed by arrival order, by command name, or by any key other than
// the message ID it answers.
func TestConcurrentRequestsCorrelateOutOfOrderResults(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, scriptOutOfOrderReplies)
	connection := dialConnection(t, server)
	startListening(t, connection)
	server.drainHandshake()
	ctx := testContext(t)

	currentDone := make(chan polledValue, 1)
	targetDone := make(chan polledValue, 1)
	go func() {
		value, at, err := connection.PollValue(
			ctx, testNodeID, testValueID(testCommandClassMultilevelSwitch, 0, "currentValue"),
		)
		currentDone <- polledValue{value: value, at: at, err: err}
	}()
	go func() {
		value, at, err := connection.PollValue(
			ctx, testNodeID, testValueID(testCommandClassMultilevelSwitch, 0, "targetValue"),
		)
		targetDone <- polledValue{value: value, at: at, err: err}
	}()
	requireCorrelatedResults(t, <-currentDone, <-targetDone)
	requireRequestMessageIDs(t, server, []string{"3", "4"})
	requireInterleavedEvent(t, connection)
}

// polledValue is one PollValue outcome collected from a concurrent call.
type polledValue struct {
	value json.RawMessage
	at    time.Time
	err   error
}

// scriptOutOfOrderReplies answers two concurrent requests in the reverse order
// they arrived and interleaves an unrelated Event.
func scriptOutOfOrderReplies(session *scriptedSession) {
	if !session.completeHandshake() {
		return
	}
	batch := make([]scriptedRequest, 0, 2)
	for range 2 {
		request, ok := session.awaitRequest()
		if !ok {
			return
		}
		batch = append(batch, request)
	}
	session.sendJSON(map[string]any{
		"type": "event",
		"event": map[string]any{
			"source": "node", "event": "value updated", "nodeId": testNodeID,
			"args": map[string]any{
				"commandClass": testCommandClassMultilevelSwitch,
				"property":     "currentValue",
				"newValue":     15,
			},
		},
	})
	for _, request := range slices.Backward(batch) {
		replyToScriptedRequest(session, request)
	}
	session.waitForClose()
}

// replyToScriptedRequest answers one request with the result its Value ID names,
// so a result routed by anything but its message ID is visible in the caller's
// value.
func replyToScriptedRequest(session *scriptedSession, request scriptedRequest) {
	var valueIdentifier struct {
		Property string `json:"property"`
	}
	if err := json.Unmarshal(request.Raw["valueId"], &valueIdentifier); err != nil {
		session.t.Errorf("valueId did not decode: %v", err)
		return
	}
	if valueIdentifier.Property == "currentValue" {
		session.replySuccess(request.messageID(), map[string]any{"value": 15})
		return
	}
	session.replySuccess(request.messageID(), map[string]any{"value": 80})
}

// requireCorrelatedResults asserts each concurrent caller received its own
// result.
func requireCorrelatedResults(t *testing.T, current, target polledValue) {
	t.Helper()
	if current.err != nil || string(current.value) != "15" {
		t.Fatalf("currentValue poll = %s (%v), want 15", current.value, current.err)
	}
	if target.err != nil || string(target.value) != "80" {
		t.Fatalf("targetValue poll = %s (%v), want 80", target.value, target.err)
	}
	if current.at.IsZero() || current.at.Location() != time.UTC {
		t.Fatalf("poll receive time = %v, want a UTC receive time", current.at)
	}
}

// requireRequestMessageIDs asserts the unique decimal message IDs the client
// assigned to the last requests.
func requireRequestMessageIDs(t *testing.T, server *scriptedServer, want []string) {
	t.Helper()
	messageIDs := make([]string, 0, len(want))
	for range want {
		messageIDs = append(messageIDs, server.nextRequest().messageID())
	}
	if !isPermutation(messageIDs, want) {
		t.Fatalf("message ids = %v, want the unique decimal IDs %v", messageIDs, want)
	}
}

// requireInterleavedEvent asserts an Event interleaved between results is still
// delivered with its node identity and receive time.
func requireInterleavedEvent(t *testing.T, connection zwaveConnection) {
	t.Helper()
	select {
	case event := <-connection.Events():
		if event.Event.Event.Source != "node" || event.Event.Event.Event != "value updated" {
			t.Fatalf("event = %#v, want a node value updated event", event.Event)
		}
		if event.Event.Event.NodeID != testNodeID || string(event.Event.Event.Args) == "" {
			t.Fatalf("event = %#v, want the test node ID and raw args", event.Event)
		}
		if event.ReceivedAt.IsZero() || event.ReceivedAt.Location() != time.UTC {
			t.Fatalf("event receive time = %v, want a UTC receive time", event.ReceivedAt)
		}
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for the interleaved event")
	}
}

// isPermutation reports whether both slices contain the same values.
func isPermutation(got, want []string) bool {
	counts := make(map[string]int, len(want))
	for _, value := range want {
		counts[value]++
	}
	for _, value := range got {
		counts[value]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

// This test protects routing integrity against a result no request can own and
// fails if an unknown message ID is discarded instead of ending the generation.
func TestUnknownResultMessageIDEndsGeneration(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		if _, ok := session.awaitRequest(); !ok {
			return
		}
		session.replySuccess("99", map[string]any{"value": 1})
		session.waitForClose()
	})
	connection := dialConnection(t, server)
	startListening(t, connection)

	_, _, err := connection.PollValue(
		testContext(t), testNodeID, testValueID(testCommandClassMultilevelSwitch, 0, "currentValue"),
	)
	requireErrorSameType(t, err, &unknownResultMessageIDError{}, "pending poll")
	requireErrorSameType(t, awaitLost(t, connection), &unknownResultMessageIDError{}, "lost error")
}

// This test protects routing integrity against a second result for one message
// ID and fails if a duplicate is silently allowed to satisfy or shadow a
// request.
func TestDuplicateResultMessageIDEndsGeneration(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		poll, ok := session.awaitRequest()
		if !ok {
			return
		}
		session.replySuccess(poll.messageID(), map[string]any{"value": 15})
		session.replySuccess(poll.messageID(), map[string]any{"value": 80})
		session.waitForClose()
	})
	connection := dialConnection(t, server)
	startListening(t, connection)

	value, _, err := connection.PollValue(
		testContext(t), testNodeID, testValueID(testCommandClassMultilevelSwitch, 0, "currentValue"),
	)
	if err != nil || string(value) != "15" {
		t.Fatalf("poll = %s (%v), want the first result 15", value, err)
	}
	requireErrorSameType(t, awaitLost(t, connection), &duplicateResultMessageIDError{}, "lost error")
}

// This test protects the message-ID space and fails if the canonical zero ID,
// which this generation never allocates, is classified as a duplicate result for
// an already-completed correlation instead of an unknown one.
func TestZeroResultMessageIDIsUnknownNotDuplicate(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		session.sendRaw(`{"type":"result","messageId":"0","success":true,"result":{}}`)
		session.waitForClose()
	})
	connection := dialConnection(t, server)
	startListening(t, connection)

	lost := awaitLost(t, connection)
	requireErrorSameType(t, lost, &unknownResultMessageIDError{}, "lost error")
	var unknown *unknownResultMessageIDError
	if !errors.As(lost, &unknown) || unknown.MessageID != 0 {
		t.Fatalf("lost error = %#v (%v), want unknown message ID 0", unknown, lost)
	}
	_, _, err := connection.PollValue(
		testContext(t), testNodeID, testValueID(testCommandClassMultilevelSwitch, 0, "currentValue"),
	)
	requireErrorSameType(t, err, &unknownResultMessageIDError{}, "request after a zero message ID")
}

// This test protects the message-ID parser directly and fails if it accepts zero
// or any non-canonical decimal text.
func TestParseMessageIDRejectsZeroAndNonCanonicalText(t *testing.T) {
	t.Parallel()
	if _, err := parseMessageID("0"); err == nil {
		t.Fatal("message ID 0 was accepted, want it rejected")
	}
	for _, raw := range []string{"", "abc", "01", "-1", "1.0", " 1", "+1"} {
		if _, err := parseMessageID(raw); err == nil {
			t.Fatalf("message ID %q was accepted, want it rejected", raw)
		}
	}
	for _, raw := range []string{"1", "2", "23", "18446744073709551615"} {
		id, err := parseMessageID(raw)
		if err != nil || strconv.FormatUint(id, 10) != raw {
			t.Fatalf("message ID %q = %d (%v), want it accepted", raw, id, err)
		}
	}
}

// This test protects duplicate detection after a correlation has completed,
// without any per-success tombstone. After more completions than the abandoned
// bound, a repeated result for the first completed poll must still be diagnosed
// as a duplicate: deliverResult classifies it from the bounded nextMessageID
// counter instead of a set that grows with every success.
func TestCompletedIDsRemainDuplicatesWithoutResolvedMap(t *testing.T) {
	t.Parallel()
	completions := maximumInFlightRequests + maximumAbandonedRequests
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		first := ""
		for index := range completions {
			request, ok := session.awaitRequest()
			if !ok {
				return
			}
			if index == 0 {
				first = request.messageID()
			}
			session.replySuccess(request.messageID(), map[string]any{"value": index})
		}
		session.replySuccess(first, map[string]any{"value": 0})
		session.waitForClose()
	})
	connection := dialConnection(t, server)
	startListening(t, connection)
	server.drainHandshake()

	ctx := testContext(t)
	current := testValueID(testCommandClassMultilevelSwitch, 0, "currentValue")
	for index := range completions {
		if _, _, err := connection.PollValue(ctx, testNodeID, current); err != nil {
			t.Fatalf("poll %d: %v", index, err)
		}
	}
	requireErrorSameType(t, awaitLost(t, connection), &duplicateResultMessageIDError{}, "lost error")
}

// This test protects fail-safe frame handling and fails if a malformed or
// unrouteable frame leaves the client running with corrupted protocol state.
func TestMalformedFramesEndGeneration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		frame string
		want  error
	}{
		{"truncated json", `{"type":"result"`, &malformedFrameError{}},
		{"not an object", `[1,2,3]`, &malformedFrameError{}},
		{"unknown frame type", `{"type":"greeting"}`, &malformedFrameError{}},
		{"missing message id", `{"type":"result","success":true,"result":{}}`, &malformedFrameError{}},
		{"non numeric message id", `{"type":"result","messageId":"abc","success":true,"result":{}}`,
			&malformedFrameError{}},
		{"padded message id", `{"type":"result","messageId":"01","success":true,"result":{}}`,
			&malformedFrameError{}},
		{"negative message id", `{"type":"result","messageId":"-1","success":true,"result":{}}`,
			&malformedFrameError{}},
		{"second version frame", `{"type":"version","homeId":1,"minSchemaVersion":0,"maxSchemaVersion":50}`,
			&malformedFrameError{}},
		{"undocumented event source", `{"type":"event","event":{"source":"unknown","event":"ready","nodeId":23}}`,
			&malformedFrameError{}},
		{"empty event name", `{"type":"event","event":{"source":"node","event":"","nodeId":23}}`,
			&malformedFrameError{}},
		{"controller event without node state",
			`{"type":"event","event":{"source":"controller","event":"node removed"}}`,
			&malformedFrameError{}},
		{"controller event without node id",
			`{"type":"event","event":{"source":"controller","event":"node added","node":{"ready":false}}}`,
			&malformedFrameError{}},
		{"controller event with conflicting node ids",
			`{"type":"event","event":{"source":"controller","event":"node removed","nodeId":7,"node":{"nodeId":23}}}`,
			&malformedFrameError{}},
		{"node event without node id",
			`{"type":"event","event":{"source":"node","event":"value updated","args":{"newValue":15}}}`,
			&malformedFrameError{}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server := startScriptedServer(t, func(session *scriptedSession) {
				if !session.completeHandshake() {
					return
				}
				session.sendRaw(testCase.frame)
				session.waitForClose()
			})
			connection := dialConnection(t, server)
			startListening(t, connection)

			requireErrorSameType(t, awaitLost(t, connection), testCase.want, "lost error")
			_, _, err := connection.PollValue(
				testContext(t), testNodeID, testValueID(testCommandClassMultilevelSwitch, 0, "currentValue"),
			)
			requireErrorSameType(t, err, testCase.want, "request after malformed frame")
		})
	}
}

// This test protects the first-frame contract and fails if a session is
// initialized before a valid version frame arrives.
func TestFirstFrameMustBeVersion(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		session.sendRaw(`{"type":"event","event":{"source":"driver","event":"all nodes ready"}}`)
		session.waitForClose()
	})
	connection := dialConnection(t, server)

	_, _, err := connection.StartListening(testContext(t))
	requireErrorSameType(t, err, &malformedFrameError{}, "handshake")
	requireErrorSameType(t, awaitLost(t, connection), &malformedFrameError{}, "lost error")
	server.expectNoRequest()
}

// This test protects schema negotiation and fails if an incompatible server is
// initialized anyway.
func TestIncompatibleSchemaRangeFailsHandshake(t *testing.T) {
	t.Parallel()
	ranges := []struct{ minimum, maximum int }{{30, 50}, {0, 28}, {5, 5}}
	for _, schemaRange := range ranges {
		t.Run(
			strconv.Itoa(schemaRange.minimum)+".."+strconv.Itoa(schemaRange.maximum),
			func(t *testing.T) {
				t.Parallel()
				server := startScriptedServer(t, func(session *scriptedSession) {
					frame := compatibleVersionFrame()
					frame["minSchemaVersion"] = schemaRange.minimum
					frame["maxSchemaVersion"] = schemaRange.maximum
					session.sendJSON(frame)
					session.waitForClose()
				})
				connection := dialConnection(t, server)

				_, _, err := connection.StartListening(testContext(t))
				requireErrorSameType(t, err, &incompatibleSchemaVersionError{}, "handshake")
				var incompatibility *incompatibleSchemaVersionError
				if !errors.As(err, &incompatibility) {
					t.Fatalf("handshake error = %v, want schema incompatibility", err)
				}
				if incompatibility.Minimum != schemaRange.minimum || incompatibility.Maximum != schemaRange.maximum {
					t.Fatalf("schema range = %#v, want %#v", incompatibility, schemaRange)
				}
				requireErrorSameType(
					t, awaitLost(t, connection), &incompatibleSchemaVersionError{}, "lost error",
				)
				server.expectNoRequest()
			},
		)
	}
}

// This test protects strict version-frame validation and fails if a version
// frame without a Home ID or with malformed fields is accepted.
func TestMalformedVersionFrameFailsHandshake(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		frame string
		want  error
	}{
		{"no home id", `{"type":"version","driverVersion":"15.0.0","minSchemaVersion":0,"maxSchemaVersion":50}`,
			&malformedVersionFrameError{}},
		{"null home id",
			`{"type":"version","homeId":null,"minSchemaVersion":0,"maxSchemaVersion":50}`,
			&malformedVersionFrameError{}},
		{"string home id", `{"type":"version","homeId":"1a2b3c4d","minSchemaVersion":0,"maxSchemaVersion":50}`,
			&malformedVersionFrameError{}},
		{"string schema version",
			`{"type":"version","homeId":1,"minSchemaVersion":"0","maxSchemaVersion":50}`,
			&malformedVersionFrameError{}},
		{"no schema range",
			`{"type":"version","driverVersion":"15.0.0","homeId":1}`,
			&malformedVersionFrameError{}},
		{"no minimum schema version",
			`{"type":"version","homeId":1,"maxSchemaVersion":50}`,
			&malformedVersionFrameError{}},
		{"no maximum schema version",
			`{"type":"version","homeId":1,"minSchemaVersion":0}`,
			&malformedVersionFrameError{}},
		{"array frame", `[]`, &malformedFrameError{}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server := startScriptedServer(t, func(session *scriptedSession) {
				session.sendRaw(testCase.frame)
				session.waitForClose()
			})
			connection := dialConnection(t, server)

			_, _, err := connection.StartListening(testContext(t))
			requireErrorSameType(t, err, testCase.want, "handshake")
			requireErrorSameType(t, awaitLost(t, connection), testCase.want, "lost error")
			server.expectNoRequest()
		})
	}
}

// This test protects the snapshot contract and fails if an incomplete snapshot
// or a snapshot of another network is accepted as usable protocol state.
func TestSnapshotFailuresEndHandshake(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		response string
		want     error
	}{
		{
			"missing result",
			`{"type":"result","messageId":"%s","success":true}`,
			&malformedSnapshotError{},
		},
		{
			"no controller home id",
			`{"type":"result","messageId":"%s","success":true,"result":{"state":{"controller":{},"nodes":[]}}}`,
			&malformedSnapshotError{},
		},
		{
			"empty result",
			`{"type":"result","messageId":"%s","success":true,"result":{}}`,
			&malformedSnapshotError{},
		},
		{
			"missing node inventory",
			`{"type":"result","messageId":"%s","success":true,"result":{"state":{"controller":{"homeId":439041101}}}}`,
			&malformedSnapshotError{},
		},
		{
			"null node inventory",
			`{"type":"result","messageId":"%s","success":true,"result":{"state":{"controller":{"homeId":439041101},"nodes":null}}}`,
			&malformedSnapshotError{},
		},
		{
			"non object state",
			`{"type":"result","messageId":"%s","success":true,"result":{"state":"ready"}}`,
			&malformedSnapshotError{},
		},
		{
			"malformed node",
			`{"type":"result","messageId":"%s","success":true,"result":{"state":` +
				`{"controller":{"homeId":439041101},"nodes":[{"nodeId":"23"}]}}}`,
			&malformedSnapshotError{},
		},
		{
			"different network",
			`{"type":"result","messageId":"%s","success":true,"result":{"state":` +
				`{"controller":{"homeId":1},"nodes":[]}}}`,
			&homeIDMismatchError{},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server := startScriptedServer(t, func(session *scriptedSession) {
				session.sendJSON(compatibleVersionFrame())
				initialize, ok := session.awaitRequest()
				if !ok {
					return
				}
				session.replySuccess(initialize.messageID(), map[string]any{})
				startListeningRequest, ok := session.awaitRequest()
				if !ok {
					return
				}
				session.sendRaw(strings.Replace(testCase.response, "%s", startListeningRequest.messageID(), 1))
				session.waitForClose()
			})
			connection := dialConnection(t, server)

			_, _, err := connection.StartListening(testContext(t))
			requireErrorSameType(t, err, testCase.want, "handshake")
			requireErrorSameType(t, awaitLost(t, connection), testCase.want, "lost error")
		})
	}
}

// This test protects the full WebSocket handshake deadline and fails if a peer
// that never sends its version frame can hold StartListening beyond its bound.
func TestHandshakeTimeoutEndsGeneration(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		session.waitForClose()
	})
	connection := dialConnection(t, server)
	internal := productionConnection(t, connection)
	internal.handshakeTimeout = testRequestDeadline

	startedAt := time.Now()
	_, _, err := connection.StartListening(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StartListening error = %v, want handshake deadline", err)
	}
	requireErrorSameType(t, err, &requestTimeoutError{}, "handshake")
	if time.Since(startedAt) > testTimeout {
		t.Fatal("StartListening exceeded the test bound")
	}
	requireErrorSameType(t, awaitLost(t, connection), &requestTimeoutError{}, "lost error")
	server.expectNoRequest()
}

// This test protects the schema-29 error shape: a rejection must be classified
// as an upstream rejection, expose the Z-Wave error message, end the handshake
// generation, and never be mistaken for a successful initialize.
func TestRejectedInitializeEndsHandshake(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		session.sendJSON(compatibleVersionFrame())
		initialize, ok := session.awaitRequest()
		if !ok {
			return
		}
		session.replyRejection(initialize.messageID(), "The driver is not ready")
		session.waitForClose()
	})
	connection := dialConnection(t, server)

	_, _, err := connection.StartListening(testContext(t))
	requireErrorSameType(t, err, &upstreamRejectionError{}, "handshake")
	var rejection *upstreamRejectionError
	if !errors.As(err, &rejection) {
		t.Fatalf("handshake error = %v, want an upstream rejection", err)
	}
	if rejection.ErrorCode != "zwave_error" || rejection.ZWaveErrorCode == nil ||
		*rejection.ZWaveErrorCode != -1 || rejection.ZWaveErrorMessage != "The driver is not ready" {
		t.Fatalf("rejection = %#v, want the schema-29 zwave_error fields", rejection)
	}
	if !strings.Contains(err.Error(), "The driver is not ready") {
		t.Fatalf("rejection %v does not carry the upstream message", err)
	}
	requireErrorSameType(t, awaitLost(t, connection), &upstreamRejectionError{}, "lost error")
	// A rejected initialize is terminal: start_listening is never written.
	recorded := server.nextRequest()
	if recorded.command() != commandInitialize {
		t.Fatalf("recorded request = %q, want %q", recorded.command(), commandInitialize)
	}
	server.expectNoRequest()
}

// This test protects the rule that an ordinary upstream rejection is not a
// transport failure and fails if a refused request ends the generation or
// leaves the connection unusable.
func TestRejectedRequestKeepsGenerationUsable(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		set, ok := session.awaitRequest()
		if !ok {
			return
		}
		if set.command() != commandSetValue {
			session.t.Errorf("request = %q, want %q", set.command(), commandSetValue)
			return
		}
		session.replyRejection(set.messageID(), "The node did not respond")
		poll, ok := session.awaitRequest()
		if !ok {
			return
		}
		session.replySuccess(poll.messageID(), map[string]any{"value": 42})
		session.waitForClose()
	})
	connection := dialConnection(t, server)
	startListening(t, connection)
	ctx := testContext(t)

	_, err := connection.SetValue(
		ctx,
		testNodeID,
		testValueID(testCommandClassMultilevelSwitch, 0, "targetValue"),
		json.RawMessage("42"),
	)
	requireErrorSameType(t, err, &upstreamRejectionError{}, "set value")

	value, at, err := connection.PollValue(
		ctx, testNodeID, testValueID(testCommandClassMultilevelSwitch, 0, "currentValue"),
	)
	if err != nil || string(value) != "42" {
		t.Fatalf("poll after rejection = %s (%v), want 42", value, err)
	}
	if at.IsZero() {
		t.Fatal("poll receive time is zero, want the frame receive time")
	}
}

// This test protects the exact schema-29 set status encoding and fails if a
// non-success status, an undocumented status, or a string status is accepted as
// success.
func TestSetValueAcceptsOnlyDocumentedSuccessStatuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		status   string
		accepted bool
		want     setValueStatus
	}{
		{"working", `1`, true, setValueStatusWorking},
		{"success unsupervised", `254`, true, setValueStatusSuccessUnsupervised},
		{"success", `255`, true, setValueStatusSuccess},
		{"no device support", `0`, false, setValueStatusNoDeviceSupport},
		{"fail", `2`, false, setValueStatusFail},
		{"endpoint not found", `3`, false, setValueStatusEndpointNotFound},
		{"not implemented", `4`, false, setValueStatusNotImplemented},
		{"invalid value", `5`, false, setValueStatusInvalidValue},
		{"undocumented", `7`, false, setValueStatusUnrecognized},
		{"fraction", `1.5`, false, setValueStatusUnrecognized},
		{"string encoding", `"Success"`, false, setValueStatusUnrecognized},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			runSetValueStatusCase(t, testCase.status, testCase.accepted, testCase.want)
		})
	}
}

// runSetValueStatusCase answers one node.set_value with one raw status and
// asserts both the wire shape of the write and the classification of the reply.
func runSetValueStatusCase(t *testing.T, rawStatus string, accepted bool, want setValueStatus) {
	t.Helper()
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		set, ok := session.awaitRequest()
		if !ok {
			return
		}
		session.sendRaw(`{"type":"result","messageId":"` + set.messageID() +
			`","success":true,"result":{"result":{"status":` + rawStatus + `}}}`)
		session.waitForClose()
	})
	connection := dialConnection(t, server)
	startListening(t, connection)
	server.drainHandshake()

	status, err := connection.SetValue(
		testContext(t),
		testNodeID,
		testValueID(testCommandClassMultilevelSwitch, 1, "targetValue"),
		json.RawMessage("64"),
	)
	requireSetValueWireShape(t, server.nextRequest())
	if !accepted {
		requireRefusedStatus(t, err, want)
		return
	}
	if err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	if status != want {
		t.Fatalf("set status = %d, want %d", int(status), int(want))
	}
}

// requireSetValueWireShape asserts the exact node.set_value payload, including
// the planned endpoint, so an endpoint-scoped write that silently becomes a root
// write cannot pass.
func requireSetValueWireShape(t *testing.T, set scriptedRequest) {
	t.Helper()
	var sent struct {
		NodeID  int             `json:"nodeId"`
		Value   json.RawMessage `json:"value"`
		ValueID struct {
			CommandClass int    `json:"commandClass"`
			Endpoint     *int   `json:"endpoint"`
			Property     string `json:"property"`
		} `json:"valueId"`
	}
	if err := json.Unmarshal(set.Payload, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.NodeID != testNodeID || string(sent.Value) != "64" {
		t.Fatalf("set payload = %s, want node %d value 64", set.Payload, testNodeID)
	}
	if sent.ValueID.Endpoint == nil || *sent.ValueID.Endpoint != 1 {
		t.Fatalf("set value id = %s, want endpoint 1", set.Raw["valueId"])
	}
	if sent.ValueID.CommandClass != testCommandClassMultilevelSwitch || sent.ValueID.Property != "targetValue" {
		t.Fatalf("set value id = %s, want the planned target value", set.Raw["valueId"])
	}
}

// requireRefusedStatus asserts an exact setValueStatus refusal.
func requireRefusedStatus(t *testing.T, err error, want setValueStatus) {
	t.Helper()
	requireErrorSameType(t, err, &setValueRefusedError{}, "set value")
	var refused *setValueRefusedError
	if !errors.As(err, &refused) || refused.Status != want {
		t.Fatalf("refusal = %#v (%v), want status %d", refused, err, int(want))
	}
}

// This test protects the root-endpoint wire rule and fails if a root Value ID
// is written with an explicit endpoint or an endpoint Value ID loses its index.
func TestValueIDWireEncoding(t *testing.T) {
	t.Parallel()
	root := testValueID(testCommandClassBinarySwitch, 0, "currentValue")
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"commandClass":37,"property":"currentValue"}` {
		t.Fatalf("root value id = %s, want the endpoint omitted", encoded)
	}
	scoped := testValueID(testCommandClassBinarySwitch, 2, "targetValue")
	encoded, err = json.Marshal(scoped)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"commandClass":37,"endpoint":2,"property":"targetValue"}` {
		t.Fatalf("endpoint value id = %s, want endpoint 2", encoded)
	}
	numeric := valueID{CommandClass: testCommandClassMultilevelSwitch, Property: valueProperty{Numeric: true}}
	if _, err = json.Marshal(numeric); err == nil {
		t.Fatal("numeric property marshalled without error, want a loud refusal")
	}
}

// This test protects snapshot decoding against the numeric property names real
// Z-Wave JS networks report and fails if one such Value makes the whole snapshot
// undecodable. It also protects the propertyKey marker that isolates ambiguous
// values.
func TestSnapshotDecodesNumericAndAmbiguousValues(t *testing.T) {
	t.Parallel()
	frame := `{"state":{"controller":{"homeId":439041101},"nodes":[{"nodeId":23,"values":[` +
		`{"commandClass":112,"endpoint":0,"property":9,"metadata":{"type":"number"},"value":3},` +
		`{"commandClass":37,"property":"currentValue","propertyKey":2,` +
		`"metadata":{"type":"boolean","readable":true},"value":true},` +
		`{"commandClass":37,"property":"currentValue","metadata":{"type":"boolean"},"value":true}]}]}}`
	var snapshot networkSnapshot
	if err := json.Unmarshal([]byte(frame), &snapshot); err != nil {
		t.Fatalf("snapshot did not decode: %v", err)
	}
	values := snapshot.State.Nodes[0].Values
	if len(values) != 3 {
		t.Fatalf("values = %d, want 3", len(values))
	}
	if !values[0].Property.Numeric || values[0].Property.Name != "" {
		t.Fatalf("numeric property = %#v, want a recorded numeric name", values[0].Property)
	}
	if string(values[0].Value) != "3" {
		t.Fatalf("numeric property value = %s, want 3", values[0].Value)
	}
	if len(values[1].PropertyKey) == 0 {
		t.Fatal("propertyKey = empty, want the key marker used to isolate the value")
	}
	if values[2].Property.Name != "currentValue" || values[2].Property.Numeric {
		t.Fatalf("string property = %#v, want currentValue", values[2].Property)
	}
	if len(values[2].PropertyKey) != 0 {
		t.Fatalf("propertyKey = %s, want no key", values[2].PropertyKey)
	}
}

// This test protects value property decoding, and fails if an explicit JSON null
// property is classified as numeric, if a numeric property name stops being
// recorded for per-capability isolation, or if either shape makes the enclosing
// snapshot undecodable.
func TestValuePropertyClassifiesNullAndNumeric(t *testing.T) {
	t.Parallel()
	var numeric valueProperty
	if err := json.Unmarshal([]byte(`9`), &numeric); err != nil {
		t.Fatalf("numeric property: %v", err)
	}
	if !numeric.Numeric || numeric.Invalid || numeric.Name != "" {
		t.Fatalf("numeric property = %#v, want a recorded numeric name", numeric)
	}
	var named valueProperty
	if err := json.Unmarshal([]byte(`"currentValue"`), &named); err != nil {
		t.Fatalf("string property: %v", err)
	}
	if named.Numeric || named.Invalid || named.Name != "currentValue" {
		t.Fatalf("string property = %#v, want currentValue", named)
	}
	var null valueProperty
	if err := json.Unmarshal([]byte(`null`), &null); err != nil {
		t.Fatalf("null property did not decode: %v", err)
	}
	if !null.Invalid || null.Numeric || null.Name != "" {
		t.Fatalf("null property = %#v, want a recorded invalid property", null)
	}
	// A null property is invalid, not fatal: it is confined to its own Value
	// instead of failing the enclosing snapshot or its siblings.
	frame := `{"state":{"controller":{"homeId":439041101},"nodes":[{"nodeId":23,"values":[` +
		`{"commandClass":37,"property":null,"metadata":{"type":"boolean"},"value":true},` +
		`{"commandClass":37,"property":"currentValue","metadata":{"type":"boolean"},"value":true}]}]}}`
	var snapshot networkSnapshot
	if err := json.Unmarshal([]byte(frame), &snapshot); err != nil {
		t.Fatalf("snapshot with a null property did not decode: %v", err)
	}
	values := snapshot.State.Nodes[0].Values
	if len(values) != 2 {
		t.Fatalf("values = %d, want 2", len(values))
	}
	if !values[0].Property.Invalid {
		t.Fatalf("null property = %#v, want an invalid property", values[0].Property)
	}
	if values[1].Property.Name != "currentValue" || values[1].Property.Invalid {
		t.Fatalf("sibling property = %#v, want currentValue", values[1].Property)
	}
}

// This test protects the local planned-Value-ID guard, and fails if a Value ID
// this Adapter never plans (a numeric property name, a null/empty property, or a
// propertyKey) is written upstream.
func TestValidatePlannedValueIDRejectsUnplannableValues(t *testing.T) {
	t.Parallel()
	planned := testValueID(testCommandClassBinarySwitch, 0, valuePropertyTargetValue)
	if err := validatePlannedValueID(planned); err != nil {
		t.Fatalf("planned Value ID rejected: %v", err)
	}
	keyed := planned
	keyed.PropertyKey = json.RawMessage("2")
	if err := validatePlannedValueID(keyed); err == nil {
		t.Fatal("a propertyKey-bearing Value ID was accepted")
	}
	numeric := valueID{
		CommandClass: testCommandClassMultilevelSwitch,
		Property:     valueProperty{Numeric: true},
	}
	if err := validatePlannedValueID(numeric); err == nil {
		t.Fatal("a numeric property Value ID was accepted")
	}
	// A caller-built null property, whether it records an explicit null or is an
	// unfilled zero value, must still be refused.
	nullProperty := planned
	nullProperty.Property = valueProperty{Invalid: true}
	if err := validatePlannedValueID(nullProperty); err == nil {
		t.Fatal("a null property Value ID was accepted")
	}
	emptyProperty := planned
	emptyProperty.Property = valueProperty{}
	if err := validatePlannedValueID(emptyProperty); err == nil {
		t.Fatal("an empty property Value ID was accepted")
	}
	// An explicit JSON null key counts as absent, exactly as it does during
	// planning and Event decoding.
	nullKeyed := planned
	nullKeyed.PropertyKey = json.RawMessage("null")
	if err := validatePlannedValueID(nullKeyed); err != nil {
		t.Fatalf("an explicit null key was rejected: %v", err)
	}
}

// This test protects rejection diagnostics, and fails if a schema-29 Z-Wave
// error reports the generic message instead of its specific zwaveErrorMessage, or
// if a rejection without a Z-Wave detail loses its generic message.
func TestUpstreamRejectionPrefersZWaveErrorMessage(t *testing.T) {
	t.Parallel()
	withZWave := resultEnvelope{
		ErrorCode:         "zwave_error",
		ZWaveErrorMessage: "The node did not respond",
		Message:           "generic detail",
	}.rejection()
	if !strings.Contains(withZWave.Error(), "The node did not respond") ||
		strings.Contains(withZWave.Error(), "generic detail") {
		t.Fatalf("rejection = %q, want the zwaveErrorMessage detail", withZWave)
	}
	genericOnly := resultEnvelope{ErrorCode: "unknown", Message: "generic detail"}.rejection()
	if !strings.Contains(genericOnly.Error(), "generic detail") {
		t.Fatalf("rejection = %q, want the generic message fallback", genericOnly)
	}
	noDetail := resultEnvelope{ErrorCode: "unknown"}.rejection()
	if !strings.Contains(noDetail.Error(), "no detail") {
		t.Fatalf("rejection = %q, want the no-detail fallback", noDetail)
	}
}

// This test protects fresh poll evidence and fails if a poll result loses its
// value, or if an unusable poll result corrupts routing instead of ending the
// generation.
func TestPollValueReturnsFreshValueOrFailsGeneration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		reply  string
		value  string
		fatal  bool
		report string
	}{
		{"value", `{"type":"result","messageId":"%s","success":true,"result":{"value":42}}`, "42", false, ""},
		{"no value", `{"type":"result","messageId":"%s","success":true,"result":{}}`, "", false, ""},
		{"null value", `{"type":"result","messageId":"%s","success":true,"result":{"value":null}}`, "null", false, ""},
		{
			"missing result", `{"type":"result","messageId":"%s","success":true}`,
			"", true, "malformed poll result",
		},
		{
			"non object result", `{"type":"result","messageId":"%s","success":true,"result":"42"}`,
			"", true, "malformed poll result",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			runPollValueCase(t, testCase.reply, testCase.value, testCase.fatal)
		})
	}
}

// runPollValueCase answers one node.poll_value with one raw reply and asserts the
// translated value or the fail-safe generation failure.
func runPollValueCase(t *testing.T, reply, wantValue string, fatal bool) {
	t.Helper()
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		poll, ok := session.awaitRequest()
		if !ok {
			return
		}
		session.sendRaw(strings.Replace(reply, "%s", poll.messageID(), 1))
		session.waitForClose()
	})
	connection := dialConnection(t, server)
	startListening(t, connection)

	before := time.Now().UTC()
	value, at, err := connection.PollValue(
		testContext(t), testNodeID, testValueID(testCommandClassBinarySwitch, 0, "currentValue"),
	)
	if fatal {
		requireErrorSameType(t, err, &malformedResultError{}, "malformed poll result")
		requireErrorSameType(t, awaitLost(t, connection), &malformedResultError{}, "lost error")
		return
	}
	if err != nil {
		t.Fatalf("PollValue: %v", err)
	}
	if string(value) != wantValue {
		t.Fatalf("poll value = %q, want %q", value, wantValue)
	}
	if at.Before(before) || at.After(time.Now().UTC()) {
		t.Fatalf("poll receive time = %v, want the frame receive time", at)
	}
}

// This test protects the bounded waiter pool and fails if the bound is missing,
// off by one, or lets an over-limit request reach the server.
func TestInFlightRequestLimitIsExact(t *testing.T) {
	t.Parallel()
	received := make(chan struct{}, maximumInFlightRequests)
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		for session.ctx.Err() == nil {
			if _, ok := session.awaitRequest(); !ok {
				return
			}
			received <- struct{}{}
		}
	})
	connection := dialConnection(t, server)
	startListening(t, connection)
	ctx, cancel := context.WithCancel(testContext(t))
	defer cancel()

	var waitGroup sync.WaitGroup
	for range maximumInFlightRequests {
		waitGroup.Go(func() {
			_, _ = connection.SetValue(ctx, testNodeID,
				testValueID(testCommandClassBinarySwitch, 0, "targetValue"), json.RawMessage("true"))
		})
	}
	for range maximumInFlightRequests {
		select {
		case <-received:
		case <-time.After(testTimeout):
			t.Fatal("timed out waiting for the in-flight requests")
		}
	}

	_, err := connection.SetValue(ctx, testNodeID,
		testValueID(testCommandClassBinarySwitch, 0, "targetValue"), json.RawMessage("true"))
	requireErrorSameType(t, err, &inFlightRequestLimitError{}, "request above the limit")
	select {
	case <-received:
		t.Fatal("a request above the in-flight limit was written upstream")
	case <-time.After(testQuietPeriod):
	}

	cancel()
	requireErrorSameType(t, awaitLost(t, connection), &requestTimeoutError{}, "lost error")
	waitGroup.Wait()
}

// This test protects the owned-request cancellation rule and fails if a request
// whose result must reach an owner, such as node.set_value, is abandoned instead
// of ending its generation. A timed-out write may have reached the radio, so the
// generation may not be reused.
func TestRequestTimeoutEndsGeneration(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		write, ok := session.awaitRequest()
		if !ok {
			return
		}
		if write.command() != commandSetValue {
			session.t.Errorf("request = %q, want %q", write.command(), commandSetValue)
			return
		}
		time.Sleep(testRequestDeadline * 4)
		session.replySuccess(write.messageID(), setValueSuccessResult())
		session.waitForClose()
	})
	connection := dialConnection(t, server)
	startListening(t, connection)

	ctx, cancel := context.WithTimeout(t.Context(), testRequestDeadline)
	defer cancel()
	_, err := connection.SetValue(
		ctx,
		testNodeID,
		testValueID(testCommandClassMultilevelSwitch, 0, "targetValue"),
		json.RawMessage("40"),
	)
	requireErrorSameType(t, err, &requestTimeoutError{}, "timed out write")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("write error = %v, want the context deadline cause", err)
	}
	lost := awaitLost(t, connection)
	requireErrorSameType(t, lost, &requestTimeoutError{}, "lost error")
	if !errors.Is(lost, context.DeadlineExceeded) {
		t.Fatalf("lost error = %v, want the context deadline cause", lost)
	}
}

// This test protects the owned-request write binding and fails if a strict
// request write is detached from its caller's deadline. A write blocked by a
// peer that stopped reading must end at the caller's deadline and close the
// generation instead of outliving every deadline.
func TestStrictWriteDeadlineEndsGeneration(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, stallAfterHandshake)
	connection := dialConnection(t, server)
	startListening(t, connection)
	server.drainHandshake()
	internal := productionConnection(t, connection)
	payload := hugeJSONValue()

	ctx, cancel := context.WithTimeout(t.Context(), testBlockedWriteDeadline)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := connection.SetValue(
			ctx,
			testNodeID,
			testValueID(testCommandClassMultilevelSwitch, 0, "targetValue"),
			payload,
		)
		done <- err
	}()
	requireWaiterReserved(t, internal)

	select {
	case err := <-done:
		requireErrorSameType(t, err, &writeFailureError{}, "blocked strict write")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("write error = %v, want the caller's deadline cause", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("a strict write to a non-reading peer outlived its caller's deadline")
	}
	if err := awaitLost(t, connection); err == nil {
		t.Fatal("the blocked strict write ended the generation without a failure")
	}
}

// This test protects context-bounded writer-slot acquisition and fails if a
// request whose deadline ends while another write is blocked waits on the writer
// slot instead of observing its own request context. Exactly one writer holds the
// slot, so only a context-aware gate can bound the second request.
func TestWriteGateWaitIsContextBounded(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, stallAfterHandshake)
	connection := dialConnection(t, server)
	startListening(t, connection)
	server.drainHandshake()
	internal := productionConnection(t, connection)

	// The first write holds the writer slot on a peer that stopped reading.
	blockedContext, cancelBlocked := context.WithTimeout(t.Context(), testTimeout)
	defer cancelBlocked()
	blocked := make(chan error, 1)
	go func() {
		_, err := connection.SetValue(
			blockedContext,
			testNodeID,
			testValueID(testCommandClassMultilevelSwitch, 0, "targetValue"),
			hugeJSONValue(),
		)
		blocked <- err
	}()
	requireWaiterReserved(t, internal)

	// A second request with a much shorter deadline must return at its own
	// deadline, while the first write still holds the slot.
	waitingContext, cancelWaiting := context.WithTimeout(t.Context(), testBlockedWriteDeadline)
	defer cancelWaiting()
	waiting := make(chan error, 1)
	go func() {
		_, err := connection.SetValue(waitingContext, testNodeID,
			testValueID(testCommandClassBinarySwitch, 0, "targetValue"), json.RawMessage("true"))
		waiting <- err
	}()

	select {
	case err := <-waiting:
		requireErrorSameType(t, err, &writeFailureError{}, "request behind a held writer slot")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("writer-slot wait error = %v, want the waiting request's deadline cause", err)
		}
	case <-time.After(testTimeout / 2):
		t.Fatal("a request waited on the writer slot past its own deadline")
	}
	if err := awaitLost(t, connection); err == nil {
		t.Fatal("the bounded writer-slot wait ended the generation without a failure")
	}
	select {
	case <-blocked:
	case <-time.After(testTimeout):
		t.Fatal("the blocked write did not end when the generation closed")
	}
}

// This test protects the terminal signal of a request waiting on the writer slot
// and fails if that wait outlives its generation. The slot is occupied directly,
// so no frame write can release it: only the generation's terminal signal can.
func TestWriteGateWaitEndsWithTheConnection(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		session.waitForClose()
	})
	connection := dialConnection(t, server)
	startListening(t, connection)
	internal := productionConnection(t, connection)

	internal.writeGate <- struct{}{}
	waiting := make(chan error, 1)
	go func() {
		_, err := connection.SetValue(testContext(t), testNodeID,
			testValueID(testCommandClassBinarySwitch, 0, "targetValue"), json.RawMessage("true"))
		waiting <- err
	}()
	select {
	case err := <-waiting:
		t.Fatalf("the writer-slot wait returned %v before the generation ended", err)
	case <-time.After(testQuietPeriod):
	}

	connection.Close()
	select {
	case err := <-waiting:
		requireErrorSameType(t, err, &connectionClosedError{}, "writer-slot wait after close")
	case <-time.After(testTimeout):
		t.Fatal("a request waiting on the writer slot outlived its generation")
	}
	// A request that never acquired the slot never reserved a correlation.
	if pending, _ := internal.connectionCorrelations(); pending != 0 {
		t.Fatalf("waiters = %d, want 0 (the gate wait reserved no correlation)", pending)
	}
}

// This test protects the abandonable poll write binding and fails if a Command
// cancellation that ends while the poll frame is being written is allowed to
// close a healthy generation.
func TestAbandonablePollWriteSurvivesCommandCancellation(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, stallAfterHandshake)
	connection := dialConnection(t, server)
	startListening(t, connection)
	server.drainHandshake()
	internal := productionConnection(t, connection)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	hugeValueID := hugePropertyValueID()
	done := make(chan error, 1)
	go func() {
		_, _, err := connection.PollValue(ctx, testNodeID, hugeValueID)
		done <- err
	}()
	requireWaiterReserved(t, internal)
	cancel()

	// The Command cancellation must not close the socket: the write stays
	// blocked under its own bounded context, so the poll neither returns nor
	// ends the generation.
	select {
	case err := <-done:
		t.Fatalf("abandonable poll ended at the Command's cancellation: %v", err)
	case <-time.After(testQuietPeriod):
	}
	requireGenerationAlive(t, connection)

	// Closing the connection releases the still-blocked write.
	connection.Close()
	select {
	case <-done:
	case <-time.After(testTimeout):
		t.Fatal("the blocked poll write did not end when the connection closed")
	}
}

// This test protects the abandonable write bound and fails if the poll write
// context is cancelled with its caller — which would let a Command deadline
// close a healthy generation — or is left unbounded, which would let a
// non-reading peer block a poll write forever.
func TestAbandonableWriteContextIsDetachedAndBounded(t *testing.T) {
	t.Parallel()
	parent, cancelParent := context.WithCancel(t.Context())
	writeContext, cancelWrite := abandonableWriteContext(parent)
	defer cancelWrite()

	deadline, bounded := writeContext.Deadline()
	if !bounded {
		t.Fatal("abandonable write context is unbounded")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > pollWriteTimeout {
		t.Fatalf(
			"abandonable write bound = %v, want a positive bound within %v",
			remaining, pollWriteTimeout,
		)
	}

	cancelParent()
	select {
	case <-writeContext.Done():
		t.Fatal("abandonable write context ended with its caller")
	case <-time.After(testQuietPeriod):
	}
}

// This test protects the abandonable poll rule and fails if a poll whose owner
// stopped waiting keeps a waiter and a goroutine for the connection's lifetime,
// ends a healthy generation, or lets the late result of the abandoned
// correlation be mistaken for an unknown result.
func TestAbandonedPollKeepsTheGenerationUsable(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		abandoned, ok := session.awaitRequest()
		if !ok {
			return
		}
		// The owner abandons this poll at its deadline, and the server answers
		// that abandoned correlation late.
		time.Sleep(testRequestDeadline * 3)
		session.replySuccess(abandoned.messageID(), map[string]any{"value": 15})
		next, ok := session.awaitRequest()
		if !ok {
			return
		}
		session.replySuccess(next.messageID(), map[string]any{"value": 40})
		session.waitForClose()
	})
	connection := dialConnection(t, server)
	startListening(t, connection)
	internal := productionConnection(t, connection)
	current := testValueID(testCommandClassMultilevelSwitch, 0, "currentValue")

	ctx, cancel := context.WithTimeout(t.Context(), testRequestDeadline)
	defer cancel()
	if _, _, err := connection.PollValue(ctx, testNodeID, current); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("abandoned poll error = %v, want the context deadline cause", err)
	}
	pending, abandoned := internal.connectionCorrelations()
	if pending != 0 {
		t.Fatalf("poll waiters = %d, want 0 (the deadline released the waiter)", pending)
	}
	if abandoned != 1 {
		t.Fatalf("abandoned correlations = %d, want 1", abandoned)
	}

	// The generation stays usable, and the late result of the abandoned poll is
	// recognized and ignored instead of ending it.
	value, _, err := connection.PollValue(testContext(t), testNodeID, current)
	if err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if string(value) != "40" {
		t.Fatalf("second poll value = %q, want 40", value)
	}
	if pending, abandoned = internal.connectionCorrelations(); pending != 0 || abandoned != 0 {
		t.Fatalf("correlations = %d pending %d abandoned, want both released", pending, abandoned)
	}
	requireGenerationAlive(t, connection)
}

// This test protects duplicate detection on the abandonable path and fails if a
// repeated result for an abandoned correlation is silently ignored instead of
// ending the generation.
func TestAbandonedPollRejectsADuplicateLateResult(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		abandoned, ok := session.awaitRequest()
		if !ok {
			return
		}
		time.Sleep(testRequestDeadline * 3)
		session.replySuccess(abandoned.messageID(), map[string]any{"value": 15})
		session.replySuccess(abandoned.messageID(), map[string]any{"value": 15})
		session.waitForClose()
	})
	connection := dialConnection(t, server)
	startListening(t, connection)
	current := testValueID(testCommandClassMultilevelSwitch, 0, "currentValue")

	ctx, cancel := context.WithTimeout(t.Context(), testRequestDeadline)
	defer cancel()
	if _, _, err := connection.PollValue(ctx, testNodeID, current); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("abandoned poll error = %v, want the context deadline cause", err)
	}
	if _, _, err := connection.PollValue(testContext(t), testNodeID, current); err == nil {
		t.Fatal("poll after a duplicate result reported success")
	}
	requireErrorSameType(
		t,
		awaitLost(t, connection),
		&duplicateResultMessageIDError{},
		"lost error",
	)
}

// This test protects the bounded abandoned-correlation set and fails if
// unanswered polls leave an unbounded tombstone instead of ending the generation
// once the bound is reached.
func TestAbandonedPollLimitEndsTheGeneration(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		for session.ctx.Err() == nil {
			if _, ok := session.awaitRequest(); !ok {
				return
			}
		}
	})
	connection := dialConnection(t, server)
	startListening(t, connection)
	server.drainHandshake()
	internal := productionConnection(t, connection)
	current := testValueID(testCommandClassMultilevelSwitch, 0, "currentValue")

	// Each abandoned poll uses one bounded tombstone. The request is written
	// before its waiter is abandoned, so one recorded frame proves the
	// correlation exists when its owner stops waiting.
	for index := range maximumAbandonedRequests {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			_, _, err := connection.PollValue(ctx, testNodeID, current)
			done <- err
		}()
		select {
		case request := <-server.requests:
			if request.command() != commandPollValue {
				t.Fatalf("iteration %d: request = %q, want %q", index, request.command(), commandPollValue)
			}
		case err := <-done:
			t.Fatalf("iteration %d: poll returned %v without writing a request", index, err)
		case <-time.After(testTimeout):
			t.Fatalf("iteration %d: the client wrote no request", index)
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("abandoned poll error = %v, want context.Canceled", err)
		}
	}
	if pending, abandoned := internal.connectionCorrelations(); pending != 0 || abandoned != maximumAbandonedRequests {
		t.Fatalf(
			"correlations = %d pending %d abandoned, want 0 pending and %d abandoned",
			pending, abandoned, maximumAbandonedRequests,
		)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := connection.PollValue(ctx, testNodeID, current)
		done <- err
	}()
	if request := server.nextRequest(); request.command() != commandPollValue {
		t.Fatalf("request = %q, want %q", request.command(), commandPollValue)
	}
	cancel()
	requireErrorSameType(t, <-done, &abandonedRequestLimitError{}, "poll past the bound")
	requireErrorSameType(t, awaitLost(t, connection), &abandonedRequestLimitError{}, "lost error")
}

// This test protects read-failure classification and fails if a server close
// code, which the Adapter maps to its health reasons, is lost.
func TestServerCloseReportsItsCloseCode(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		_ = session.socket.Close(websocket.StatusTryAgainLater, "driver not ready")
	})
	connection := dialConnection(t, server)
	startListening(t, connection)

	lost := awaitLost(t, connection)
	requireErrorSameType(t, lost, &readFailureError{}, "lost error")
	var readFailure *readFailureError
	if !errors.As(lost, &readFailure) {
		t.Fatalf("lost error = %v, want a read failure", lost)
	}
	if readFailure.CloseCode != int(websocket.StatusTryAgainLater) {
		t.Fatalf("close code = %d, want %d", readFailure.CloseCode, int(websocket.StatusTryAgainLater))
	}
}

// This test protects deliberate shutdown and fails if Close leaves waiters,
// reports a failure, or does not stop later requests.
func TestCloseEndsGenerationWithoutFailure(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		session.waitForClose()
	})
	connection := dialConnection(t, server)
	startListening(t, connection)

	connection.Close()
	connection.Close()

	err, ok := <-connection.Lost()
	if !ok || err != nil {
		t.Fatalf("lost error = %v (%v), want a deliberate close", err, ok)
	}
	if _, ok = <-connection.Lost(); ok {
		t.Fatal("Lost reported a second terminal error")
	}
	_, _, err = connection.PollValue(
		testContext(t), testNodeID, testValueID(testCommandClassMultilevelSwitch, 0, "currentValue"),
	)
	requireErrorSameType(t, err, &connectionClosedError{}, "request after close")
}

// This test protects Event routing and fails if a consumed Event loses its node
// ID, its raw payload, or its receive time.
func TestEventsAreRoutedWithNodeIdentity(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, scriptNodeEvents)
	connection := dialConnection(t, server)
	startListening(t, connection)

	events := make([]receivedEvent, 0, 6)
	for range 6 {
		select {
		case event := <-connection.Events():
			events = append(events, event)
		case <-time.After(testTimeout):
			t.Fatalf("timed out after %d events", len(events))
		}
	}
	requireNodeEvents(t, events)
}

// scriptNodeEvents sends every consumed Event shape of schema 29.
func scriptNodeEvents(session *scriptedSession) {
	if !session.completeHandshake() {
		return
	}
	session.sendJSON(map[string]any{
		"type": "event",
		"event": map[string]any{
			"source": "controller", "event": "node added",
			"node": testNodeState(), "result": map[string]any{"nodeId": testNodeID},
		},
	})
	session.sendJSON(map[string]any{
		"type": "event",
		"event": map[string]any{
			"source": "controller", "event": "node removed",
			"node": map[string]any{"nodeId": testNodeID}, "reason": 1,
		},
	})
	session.sendJSON(map[string]any{
		"type": "event",
		"event": map[string]any{
			"source": "node", "event": "ready", "nodeId": testNodeID, "nodeState": testNodeState(),
		},
	})
	session.sendJSON(map[string]any{
		"type": "event",
		"event": map[string]any{
			"source": "node", "event": "value updated", "nodeId": testNodeID,
			"args": map[string]any{
				"commandClass": testCommandClassBinarySwitch, "property": "currentValue", "newValue": true,
			},
		},
	})
	session.sendJSON(map[string]any{
		"type":  "event",
		"event": map[string]any{"source": "driver", "event": "all nodes ready"},
	})
	session.sendJSON(map[string]any{
		"type": "event",
		"event": map[string]any{
			"source": "node", "event": "interview progress", "nodeId": testNodeID, "stage": "ProtocolInfo",
		},
	})
	session.waitForClose()
}

// requireNodeEvents asserts every routed Event keeps its node identity, raw
// payload, and receive time.
func requireNodeEvents(t *testing.T, events []receivedEvent) {
	t.Helper()
	added, removed, ready, updated, driver, unknown :=
		events[0], events[1], events[2], events[3], events[4], events[5]
	// A controller node added Event carries the node state and no event.nodeId,
	// so the node ID must be derived from node.nodeId.
	if added.Event.Event.Source != "controller" || added.Event.Event.Event != eventNodeAdded {
		t.Fatalf("first event = %#v, want a controller node added event", added.Event)
	}
	if added.Event.Event.NodeID != testNodeID || string(added.Event.Event.Node) == "" {
		t.Fatalf("node added event = %#v, want node %d and its node state", added.Event, testNodeID)
	}
	if removed.Event.Event.NodeID != testNodeID || string(removed.Event.Event.Node) == "" {
		t.Fatalf("node removed event = %#v, want node %d and its node state", removed.Event, testNodeID)
	}
	if ready.Event.Event.NodeID != testNodeID || string(ready.Event.Event.State) == "" {
		t.Fatalf("ready event = %#v, want node %d and its node state", ready.Event, testNodeID)
	}
	if updated.Event.Event.NodeID != testNodeID ||
		string(updated.Event.Event.Args) != `{"commandClass":37,"newValue":true,"property":"currentValue"}` {
		t.Fatalf("value updated event = %#v, want the raw args", updated.Event)
	}
	if driver.Event.Event.Source != "driver" {
		t.Fatalf("driver event = %#v, want a driver event", driver.Event)
	}
	if unknown.Event.Event.Event != "interview progress" {
		t.Fatalf("unknown event = %#v, want it delivered for bounded diagnostics", unknown.Event)
	}
	for _, event := range events {
		if event.ReceivedAt.IsZero() || event.ReceivedAt.Location() != time.UTC {
			t.Fatalf("event receive time = %v, want a UTC receive time", event.ReceivedAt)
		}
	}
}

// This test protects the bounded Event queue and fails if overflow is silent or
// if the bound is off by one.
func TestEventQueueOverflowEndsGeneration(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		session.sendRaw(`{"type":"version","driverVersion":"15.0.0","serverVersion":"3.10.1",` +
			`"homeId":439041101,"minSchemaVersion":0,"maxSchemaVersion":50}`)
		for index := range maximumQueuedEvents + 1 {
			session.sendJSON(map[string]any{
				"type": "event",
				"event": map[string]any{
					"source": "node", "event": "alive", "nodeId": index + 1,
				},
			})
		}
		session.waitForClose()
	})
	connection := dialConnection(t, server)

	requireErrorSameType(t, awaitLost(t, connection), &eventQueueOverflowError{}, "lost error")
	queued := 0
	for {
		select {
		case <-connection.Events():
			queued++
		default:
			if queued != maximumQueuedEvents {
				t.Fatalf("queued events = %d, want exactly %d", queued, maximumQueuedEvents)
			}
			return
		}
	}
}

// This test protects the read boundary and fails if a binary frame, which
// schema 29 never sends, is parsed as text or ignored.
func TestBinaryFrameEndsGeneration(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		session.sendBinary([]byte(`{"type":"event","event":{"source":"node","event":"alive","nodeId":23}}`))
		session.waitForClose()
	})
	connection := dialConnection(t, server)
	startListening(t, connection)

	requireErrorSameType(t, awaitLost(t, connection), &binaryFrameError{}, "lost error")
}

// This test protects the 16 MiB frame bound and fails if an oversized frame is
// truncated and parsed instead of ending the generation.
func TestOversizedFrameEndsGeneration(t *testing.T) {
	t.Parallel()
	oversized := strings.Repeat(`{"nodeId":23}`, maximumFrameBytes/13+1)
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		session.sendRaw(oversized)
		session.waitForClose()
	})
	connection := dialConnection(t, server)
	startListening(t, connection)

	requireErrorSameType(t, awaitLost(t, connection), &oversizedFrameError{}, "lost error")
}

// This test protects the local request guards and fails if the client writes a
// request it cannot correlate or a set without a value.
func TestInvalidRequestsAreRefusedLocally(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, func(session *scriptedSession) {
		if !session.completeHandshake() {
			return
		}
		for session.ctx.Err() == nil {
			poll, ok := session.awaitRequest()
			if !ok {
				return
			}
			session.replySuccess(poll.messageID(), map[string]any{"value": true})
		}
	})
	connection := dialConnection(t, server)
	startListening(t, connection)
	server.drainHandshake()
	ctx := testContext(t)

	if _, _, err := connection.PollValue(
		ctx, 0, testValueID(testCommandClassBinarySwitch, 0, "currentValue"),
	); err == nil {
		t.Error("poll without a node ID unexpectedly accepted")
	}
	if _, _, err := connection.PollValue(
		ctx, testNodeID, testValueID(testCommandClassBinarySwitch, 0, "currentValue"),
	); err != nil {
		t.Errorf("poll with a node ID unexpectedly rejected: %v", err)
	}
	_, err := connection.SetValue(
		ctx, testNodeID, testValueID(testCommandClassBinarySwitch, 0, "targetValue"), nil,
	)
	requireErrorSameType(t, err, &invalidRequestError{}, "set without a value")
	_, err = connection.SetValue(
		ctx, testNodeID, testValueID(testCommandClassBinarySwitch, 0, "targetValue"),
		json.RawMessage("not json"),
	)
	requireErrorSameType(t, err, &invalidRequestError{}, "set with an unusable payload")
	// A Value ID that cannot be written upstream must fail locally: a numeric
	// property has no request encoding, so writing one would corrupt the write
	// or end the generation on an encode failure.
	numeric := valueID{CommandClass: testCommandClassMultilevelSwitch, Property: valueProperty{Numeric: true}}
	if _, _, err = connection.PollValue(ctx, testNodeID, numeric); err == nil {
		t.Error("poll with a numeric property unexpectedly accepted")
	}
	if _, err = connection.SetValue(ctx, testNodeID, numeric, json.RawMessage("42")); err == nil {
		t.Error("set with a numeric property unexpectedly accepted")
	}
	// None of the refusals above is a generation failure.
	if _, _, err = connection.PollValue(
		ctx, testNodeID, testValueID(testCommandClassBinarySwitch, 0, "currentValue"),
	); err != nil {
		t.Fatalf("poll after local refusals: %v", err)
	}
	if _, _, repeatErr := connection.StartListening(ctx); repeatErr == nil {
		t.Error("second StartListening unexpectedly accepted")
	}
	server.nextRequest()
}
