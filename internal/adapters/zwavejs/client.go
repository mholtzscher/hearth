// Package zwavejs is the Z-Wave JS Adapter: a stateless bridge between Hearth
// and the schema-versioned Z-Wave JS server WebSocket exposed by Z-Wave JS UI.
//
// client.go owns the private upstream seam: the schema-29 correlated WebSocket
// client and the DTOs it decodes. Every consumed field is validated strictly,
// unknown JSON fields are ignored, and any condition that would make result
// routing unreliable ends the connection generation instead of losing protocol
// state silently.
package zwavejs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const (
	// schemaVersion29 is the Z-Wave JS server API schema this Adapter
	// negotiates. Schema 29 supplies string interview stages, endpoint state,
	// node.poll_value, node.get_state, and structured node.set_value results.
	schemaVersion29 = 29

	// maximumFrameBytes is the WebSocket read limit in bytes: 16 MiB. A larger
	// frame ends the connection generation instead of being truncated.
	maximumFrameBytes = 16 << 20

	// maximumInFlightRequests bounds concurrently awaited results. A further
	// request is refused without being written upstream.
	maximumInFlightRequests = 256

	// pollWriteTimeout bounds one abandonable node.poll_value request write. A
	// poll write is detached from its owner's context so a Command deadline that
	// ends after the frame was written cannot close a healthy generation, but an
	// unbounded detach would let a write to a peer that stopped reading outlive
	// every deadline. This explicit bound makes that write fail and end the
	// generation instead.
	pollWriteTimeout = 5 * time.Second

	// maximumAbandonedRequests bounds the tombstones of correlations whose owner
	// stopped waiting before the result arrived. Exceeding the bound ends the
	// connection generation, because an answer to an abandoned correlation could
	// otherwise no longer be told apart from an unknown result.
	maximumAbandonedRequests = 128

	// maximumQueuedEvents bounds validated Events waiting for the runtime
	// coordinator. An overflow ends the connection generation rather than
	// dropping an Event silently.
	maximumQueuedEvents = 1024

	// hearthUserAgentComponent is the additionalUserAgentComponents key that
	// identifies Hearth to the Z-Wave JS server during initialize.
	hearthUserAgentComponent = "hearth"

	// hearthAdapterVersion is the Hearth Adapter version reported upstream.
	hearthAdapterVersion = "0.1.0"

	// operationVersionFrame names the version-frame wait in a handshake timeout
	// diagnostic.
	operationVersionFrame = "version frame"

	// server command names consumed by schema 29.
	commandInitialize     = "initialize"
	commandStartListening = "start_listening"
	commandSetValue       = "node.set_value"
	commandPollValue      = "node.poll_value"
	commandGetState       = "node.get_state"

	// frame types sent by the Z-Wave JS server.
	frameTypeVersion = "version"
	frameTypeResult  = "result"
	frameTypeEvent   = "event"

	// event sources documented by the schema-29 server. Any other source is a
	// malformed frame, not an ignorable Event.
	eventSourceController = "controller"
	eventSourceNode       = "node"
	eventSourceDriver     = "driver"
	eventSourceZniffer    = "zniffer"

	// controller event names that carry a complete node state instead of a
	// nodeId field.
	eventNodeAdded   = "node added"
	eventNodeRemoved = "node removed"
)

// zwaveDialer opens one connection generation to the Z-Wave JS server. The
// Adapter holds this seam so tests can substitute a scripted connection; the
// production implementation is websocketDialer.
type zwaveDialer interface {
	Dial(
		ctx context.Context,
		url string,
		schemaVersion int,
		userAgentComponents map[string]string,
	) (zwaveConnection, error)
}

// zwaveConnection is one established connection generation: an initialized
// schema-29 session with a complete snapshot, correlated requests, validated
// Events, and one terminal failure signal.
//
// StartListening performs the full handshake, so it succeeds at most once per
// generation. Close ends the generation deliberately; Lost reports the error
// that ended it.
//
// SetValue and GetNodeState are owned requests: ending their context before the
// result arrives ends the whole generation, because a late result could no
// longer be routed. PollValue is abandonable: ending its context releases its
// waiter, keeps the generation usable, and makes a late result recognizably
// ignorable.
type zwaveConnection interface {
	StartListening(ctx context.Context) (serverVersion, networkSnapshot, error)
	SetValue(ctx context.Context, nodeID int, id valueID, value json.RawMessage) (setValueStatus, error)
	PollValue(ctx context.Context, nodeID int, id valueID) (json.RawMessage, time.Time, error)
	GetNodeState(ctx context.Context, nodeID int) (nodeState, error)
	Events() <-chan receivedEvent
	Lost() <-chan error
	Close()
}

// receivedEvent is one validated upstream Event together with the Adapter-owned
// receive time. Z-Wave JS supplies no source timestamp for schema 29.
type receivedEvent struct {
	Event      serverEvent
	ReceivedAt time.Time
}

// serverVersion is the first frame the Z-Wave JS server sends. A compatible
// server reports a schema range that contains schema 29 and a Home ID. The
// schema bounds are pointers because an absent range must be reported as a
// malformed frame, not as a range of 0..0 that happens to exclude schema 29.
type serverVersion struct {
	Type             string  `json:"type"`
	DriverVersion    string  `json:"driverVersion"`
	ServerVersion    string  `json:"serverVersion"`
	HomeID           *uint32 `json:"homeId"`
	MinSchemaVersion *int    `json:"minSchemaVersion"`
	MaxSchemaVersion *int    `json:"maxSchemaVersion"`
}

// networkSnapshot is the complete protocol state returned by start_listening.
// It is the inventory authority for one connection generation.
type networkSnapshot struct {
	State struct {
		Controller controllerState `json:"controller"`
		Nodes      []nodeState     `json:"nodes"`
	} `json:"state"`
}

// controllerState is the consumed part of the controller state dump.
type controllerState struct {
	HomeID *uint32 `json:"homeId"`
}

// nodeState is one node of the network inventory. Only the fields v1 consumes
// are decoded; every other node property is ignored.
type nodeState struct {
	NodeID         int             `json:"nodeId"`
	Ready          bool            `json:"ready"`
	Status         int             `json:"status"`
	InterviewStage string          `json:"interviewStage"`
	IsController   bool            `json:"isControllerNode"`
	IsListening    bool            `json:"isListening"`
	Name           string          `json:"name"`
	Location       string          `json:"location"`
	Label          string          `json:"label"`
	ManufacturerID *int            `json:"manufacturerId"`
	ProductType    *int            `json:"productType"`
	ProductID      *int            `json:"productId"`
	Endpoints      []endpointState `json:"endpoints"`
	Values         []valueState    `json:"values"`
}

// endpointState is one endpoint of a node. Index 0 is the root endpoint.
type endpointState struct {
	Index         int    `json:"index"`
	EndpointLabel string `json:"endpointLabel"`
}

// valueID identifies one Z-Wave JS Value: a Command Class, an endpoint, and a
// property name. A root endpoint is encoded by omitting "endpoint", which
// decodes to endpoint 0.
type valueID struct {
	CommandClass int             `json:"commandClass"`
	Endpoint     int             `json:"endpoint,omitempty"`
	Property     valueProperty   `json:"property"`
	PropertyKey  json.RawMessage `json:"propertyKey,omitempty"`
}

// valueProperty is a Value ID property name. Z-Wave JS uses numeric property
// names for some Command Classes and can report an explicit JSON null, so
// decoding records those shapes instead of failing a whole node frame. Neither a
// numeric nor a null property produces a plan or is written upstream.
type valueProperty struct {
	// Name is the property name, empty when the upstream name was numeric or
	// null.
	Name string

	// Numeric records that the upstream property was a JSON number.
	Numeric bool

	// Invalid records that the upstream property was an explicit JSON null. A
	// null property names no Value, so it is never a plan candidate and never a
	// Command target.
	Invalid bool
}

// UnmarshalJSON accepts the documented string property name, records a numeric
// property name, and classifies an explicit JSON null as invalid, without
// failing the enclosing frame in any of those cases.
func (property *valueProperty) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	switch {
	case len(trimmed) == 0:
		return errors.New("zwavejs: value ID property is empty")
	case bytes.Equal(trimmed, []byte("null")):
		*property = valueProperty{Invalid: true}
		return nil
	case trimmed[0] == '"':
		var name string
		if err := json.Unmarshal(trimmed, &name); err != nil {
			return errors.New("zwavejs: value ID property is not a JSON string")
		}
		property.Name = name
		property.Numeric = false
		property.Invalid = false
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err != nil {
		return errors.New("zwavejs: value ID property is neither a string nor a number")
	}
	property.Name = ""
	property.Numeric = true
	property.Invalid = false
	return nil
}

// MarshalJSON writes the documented string property name. A numeric or invalid
// property has no upstream request encoding, so marshalling one fails loudly
// instead of sending a silently different Value ID.
func (property valueProperty) MarshalJSON() ([]byte, error) {
	if property.Numeric || property.Invalid || property.Name == "" {
		return nil, errors.New("zwavejs: value ID property is not a usable property name")
	}
	return json.Marshal(property.Name)
}

// valueMetadata is the consumed part of one Value's metadata. Readable and
// Writeable default to false, so incomplete metadata cannot produce a plan.
//
// Valid records whether every consumed field decoded from its documented type.
// It is set only by decoding, and it stays false for absent, null, non-object,
// or malformed metadata, so a metadata value that cannot be trusted never plans
// a capability.
type valueMetadata struct {
	Type      string   `json:"type"`
	Readable  bool     `json:"readable"`
	Writeable bool     `json:"writeable"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	Valid     bool
}

// UnmarshalJSON consumes one Value's metadata leniently. Every field this
// Adapter plans is decoded when it carries its documented JSON type; a field of
// any other type is recorded as invalid metadata instead of failing the
// enclosing snapshot. One malformed metadata value therefore isolates only its
// own capability, while syntactically invalid JSON, and every other malformed
// field of the snapshot, still fail decoding. Unknown metadata fields are
// ignored.
func (metadata *valueMetadata) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*metadata = valueMetadata{}
		return nil
	}
	var fields struct {
		Type      json.RawMessage `json:"type"`
		Readable  json.RawMessage `json:"readable"`
		Writeable json.RawMessage `json:"writeable"`
		Min       json.RawMessage `json:"min"`
		Max       json.RawMessage `json:"max"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		// Metadata that is not a JSON object is unusable, not fatal.
		*metadata = valueMetadata{}
		return nil //nolint:nilerr // Unusable metadata isolates its own capability; the snapshot still decodes.
	}
	*metadata = valueMetadata{Valid: true}
	if len(fields.Type) != 0 && json.Unmarshal(fields.Type, &metadata.Type) != nil {
		metadata.Valid = false
	}
	if len(fields.Readable) != 0 && json.Unmarshal(fields.Readable, &metadata.Readable) != nil {
		metadata.Valid = false
		metadata.Readable = false
	}
	if len(fields.Writeable) != 0 && json.Unmarshal(fields.Writeable, &metadata.Writeable) != nil {
		metadata.Valid = false
		metadata.Writeable = false
	}
	if len(fields.Min) != 0 && json.Unmarshal(fields.Min, &metadata.Min) != nil {
		metadata.Valid = false
		metadata.Min = nil
	}
	if len(fields.Max) != 0 && json.Unmarshal(fields.Max, &metadata.Max) != nil {
		metadata.Valid = false
		metadata.Max = nil
	}
	return nil
}

// valueState is one Value of a node: its Value ID, metadata, and current value.
type valueState struct {
	valueID

	Metadata valueMetadata   `json:"metadata"`
	Value    json.RawMessage `json:"value,omitempty"`
}

// resultEnvelope is one correlated result frame. Schema 29 reports failures
// with errorCode and, for Z-Wave errors, zwaveErrorCode with zwaveErrorMessage;
// the schema-32 zwaveErrorCodeName and schema-33 message fields are decoded when
// present and ignored otherwise.
type resultEnvelope struct {
	Type               string          `json:"type"`
	MessageID          string          `json:"messageId"`
	Success            bool            `json:"success"`
	Result             json.RawMessage `json:"result,omitempty"`
	ErrorCode          string          `json:"errorCode,omitempty"`
	ZWaveErrorCode     *int            `json:"zwaveErrorCode,omitempty"`
	ZWaveErrorCodeName string          `json:"zwaveErrorCodeName,omitempty"`
	ZWaveErrorMessage  string          `json:"zwaveErrorMessage,omitempty"`
	Message            string          `json:"message,omitempty"`

	// receivedAt is the Adapter-owned UTC receive time of this frame.
	receivedAt time.Time
}

// serverEvent is one upstream Event frame. Controller node added and node
// removed Events carry the node state under "node" and no "nodeId"; the reader
// fills NodeID from node.nodeId so consumers read one node ID field. State is
// the raw node state of a node ready Event, decoded from the "nodeState" key.
type serverEvent struct {
	Type  string `json:"type"`
	Event struct {
		Source string          `json:"source"`
		Event  string          `json:"event"`
		NodeID int             `json:"nodeId,omitempty"`
		Node   json.RawMessage `json:"node,omitempty"`
		State  json.RawMessage `json:"nodeState,omitempty"`
		Args   json.RawMessage `json:"args,omitempty"`
	} `json:"event"`
}

// requestEnvelope carries the correlation fields every request shares. Each
// concrete request embeds it, so requestFields makes them interchangeable.
type requestEnvelope struct {
	MessageID string `json:"messageId"`
	Command   string `json:"command"`
}

// requestFields exposes the correlation fields of an embedding request.
func (envelope *requestEnvelope) requestFields() *requestEnvelope { return envelope }

// correlatedRequest is one request whose result is awaited by message ID.
type correlatedRequest interface {
	requestFields() *requestEnvelope
}

// initializeRequest negotiates the schema version and identifies Hearth.
type initializeRequest struct {
	requestEnvelope

	SchemaVersion                 int               `json:"schemaVersion"`
	AdditionalUserAgentComponents map[string]string `json:"additionalUserAgentComponents"`
}

// startListeningRequest enables Events and returns the complete snapshot.
type startListeningRequest struct {
	requestEnvelope
}

// nodeSetValueRequest writes one planned Value and reports a typed status.
type nodeSetValueRequest struct {
	requestEnvelope

	NodeID  int             `json:"nodeId"`
	ValueID valueID         `json:"valueId"`
	Value   json.RawMessage `json:"value"`
}

// nodeSetValueResult is the schema-29 node.set_value result payload.
type nodeSetValueResult struct {
	Result struct {
		Status json.RawMessage `json:"status"`
	} `json:"result"`
}

// setValueStatus is the schema-29 encoding of node-zwave-js SetValueStatus.
// Only Working, SuccessUnsupervised, and Success may satisfy a Command; every
// other status is an upstream rejection.
type setValueStatus int

const (
	// setValueStatusUnrecognized reports a status the schema-29 encoding does
	// not document, or no status at all.
	setValueStatusUnrecognized setValueStatus = -1

	setValueStatusNoDeviceSupport  setValueStatus = 0
	setValueStatusWorking          setValueStatus = 1
	setValueStatusFail             setValueStatus = 2
	setValueStatusEndpointNotFound setValueStatus = 3
	setValueStatusNotImplemented   setValueStatus = 4
	setValueStatusInvalidValue     setValueStatus = 5

	setValueStatusSuccessUnsupervised setValueStatus = 254
	setValueStatusSuccess             setValueStatus = 255
)

// accepted reports whether this status may satisfy a Command. Working means the
// device accepted the command and is still executing it.
func (status setValueStatus) accepted() bool {
	return status == setValueStatusWorking ||
		status == setValueStatusSuccess ||
		status == setValueStatusSuccessUnsupervised
}

// nodePollValueRequest reads one fresh Value for Command evidence.
type nodePollValueRequest struct {
	requestEnvelope

	NodeID  int     `json:"nodeId"`
	ValueID valueID `json:"valueId"`
}

// nodePollValueResult is the node.poll_value result payload. A node that does
// not report the Value yields no value at all.
type nodePollValueResult struct {
	Value json.RawMessage `json:"value,omitempty"`
}

// nodeGetStateRequest refreshes one complete node plan.
type nodeGetStateRequest struct {
	requestEnvelope

	NodeID int `json:"nodeId"`
}

// nodeGetStateResult is the node.get_state result payload.
type nodeGetStateResult struct {
	State nodeState `json:"state"`
}

// websocketDialer is the production zwaveDialer.
type websocketDialer struct{}

// Dial opens one WebSocket connection generation, sets the 16 MiB read limit,
// and starts the single frame reader.
func (websocketDialer) Dial(
	ctx context.Context,
	url string,
	schemaVersion int,
	userAgentComponents map[string]string,
) (zwaveConnection, error) {
	socket, response, err := websocket.Dial(ctx, url, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, &dialFailureError{Cause: err}
	}
	socket.SetReadLimit(maximumFrameBytes)
	return newWebsocketConnection(ctx, socket, schemaVersion, userAgentComponents), nil
}

// defaultUserAgentComponents is the initialize identity used when the dialer
// supplies no component.
func defaultUserAgentComponents() map[string]string {
	return map[string]string{hearthUserAgentComponent: hearthAdapterVersion}
}

// websocketConnection is one connection generation. Exactly one reader
// goroutine reads frames, routes each result to its awaiting request, and
// forwards validated Events to the runtime coordinator.
type websocketConnection struct {
	connection          *websocket.Conn
	schemaVersion       int
	userAgentComponents map[string]string
	readContext         context.Context

	// writeGate is the single request-writer slot. Exactly one request holds it
	// while its frame is written, so message IDs increase in send order. It is a
	// one-slot channel rather than a mutex because acquiring a mutex cannot
	// observe a request's own write context or the generation's terminal signal:
	// a request waiting behind a write to a peer that stopped reading would then
	// outlive every deadline. Waiting on the gate selects on both, so a strict
	// deadline still bounds the whole request and a lost generation releases
	// every waiter.
	writeGate chan struct{}

	// mutex guards the fields below.
	mutex sync.Mutex
	// nextMessageID is the last decimal message ID allocated.
	nextMessageID uint64
	// pending holds the one-shot waiter of every request awaiting a result.
	pending map[uint64]chan resultEnvelope
	// abandoned remembers message IDs whose owner stopped waiting before the
	// result arrived. Their late result is recognized and ignored once instead of
	// ending the generation, and it is bounded by maximumAbandonedRequests.
	abandoned map[uint64]struct{}
	// cause is the error that ended the generation.
	cause error
	// handshakeStarted records that StartListening already ran.
	handshakeStarted bool

	// versionFrames carries the validated first frame to the handshake.
	versionFrames chan serverVersion
	// events carries validated Events to the runtime coordinator.
	events chan receivedEvent
	// lost carries the terminal error once and is then closed.
	lost chan error
	// done is closed when the generation ends.
	done chan struct{}
	// terminateOnce makes the first terminal cause authoritative.
	terminateOnce sync.Once

	// versionSeen is owned by the reader goroutine.
	versionSeen bool
}

// newWebsocketConnection starts the reader for an established socket.
func newWebsocketConnection(
	ctx context.Context,
	socket *websocket.Conn,
	schemaVersion int,
	userAgentComponents map[string]string,
) *websocketConnection {
	connection := &websocketConnection{
		connection:          socket,
		schemaVersion:       schemaVersion,
		userAgentComponents: maps.Clone(userAgentComponents),
		readContext:         context.WithoutCancel(ctx),
		pending:             make(map[uint64]chan resultEnvelope),
		abandoned:           make(map[uint64]struct{}),
		writeGate:           make(chan struct{}, 1),
		versionFrames:       make(chan serverVersion, 1),
		events:              make(chan receivedEvent, maximumQueuedEvents),
		lost:                make(chan error, 1),
		done:                make(chan struct{}),
	}
	go connection.readFrames()
	return connection
}

// Events delivers validated Events in receive order and is never closed. The
// runtime coordinator must keep draining them: a full queue ends the
// generation, and Lost reports that end.
func (connection *websocketConnection) Events() <-chan receivedEvent {
	return connection.events
}

// Lost delivers the error that ended this generation exactly once and is then
// closed. A nil error means Close was called deliberately.
func (connection *websocketConnection) Lost() <-chan error {
	return connection.lost
}

// Close ends the generation deliberately. It is safe to call more than once.
func (connection *websocketConnection) Close() {
	connection.terminate(nil)
}

// StartListening negotiates schema 29, waits for the version frame, initializes
// the session, and returns the complete start-listening snapshot. It may be
// called once per generation.
func (connection *websocketConnection) StartListening(
	ctx context.Context,
) (serverVersion, networkSnapshot, error) {
	if err := connection.markHandshakeStarted(); err != nil {
		return serverVersion{}, networkSnapshot{}, err
	}
	version, err := connection.awaitVersionFrame(ctx)
	if err != nil {
		return serverVersion{}, networkSnapshot{}, err
	}
	if err = connection.initializeSession(ctx); err != nil {
		return serverVersion{}, networkSnapshot{}, err
	}
	snapshot, err := connection.awaitSnapshot(ctx, version)
	if err != nil {
		return serverVersion{}, networkSnapshot{}, err
	}
	return version, snapshot, nil
}

// SetValue writes one planned Value and returns its exact schema-29 status. An
// unrecognized or unsuccessful status is a typed upstream rejection, not a
// transport failure.
func (connection *websocketConnection) SetValue(
	ctx context.Context,
	nodeID int,
	id valueID,
	value json.RawMessage,
) (setValueStatus, error) {
	if nodeID <= 0 {
		return setValueStatusUnrecognized, &invalidRequestError{
			Reason: "node.set_value requires a positive node ID",
		}
	}
	if err := validatePlannedValueID(id); err != nil {
		return setValueStatusUnrecognized, err
	}
	if !json.Valid(value) {
		return setValueStatusUnrecognized, &invalidRequestError{
			Reason: "node.set_value requires a valid JSON value payload",
		}
	}
	result, err := connection.requestSuccess(ctx, &nodeSetValueRequest{
		Command: commandSetValue,
		NodeID:  nodeID,
		ValueID: id,
		Value:   value,
	})
	if err != nil {
		return setValueStatusUnrecognized, err
	}
	var payload nodeSetValueResult
	if err = json.Unmarshal(result.Result, &payload); err != nil {
		return setValueStatusUnrecognized, &setValueRefusedError{
			Status: setValueStatusUnrecognized,
		}
	}
	status, ok := decodeSetValueStatus(payload.Result.Status)
	if !ok || !status.accepted() {
		return setValueStatusUnrecognized, &setValueRefusedError{Status: status}
	}
	return status, nil
}

// PollValue reads one fresh Value and its receive time. It is the only upstream
// report eligible for Command-linked evidence.
//
// A poll is abandonable: ending ctx before the result arrives releases this
// waiter and returns ctx's error without ending the generation, and the late
// result of an abandoned poll is recognized and ignored once. A Command deadline
// must never leave an unanswered poll awaiting the connection's lifetime, and it
// must never close a healthy generation either.
func (connection *websocketConnection) PollValue(
	ctx context.Context,
	nodeID int,
	id valueID,
) (json.RawMessage, time.Time, error) {
	if nodeID <= 0 {
		return nil, time.Time{}, &invalidRequestError{
			Reason: "node.poll_value requires a positive node ID",
		}
	}
	if err := validatePlannedValueID(id); err != nil {
		return nil, time.Time{}, err
	}
	result, err := connection.requestSuccessAbandonable(ctx, &nodePollValueRequest{
		Command: commandPollValue,
		NodeID:  nodeID,
		ValueID: id,
	})
	if err != nil {
		return nil, time.Time{}, err
	}
	var payload nodePollValueResult
	if err = json.Unmarshal(result.Result, &payload); err != nil {
		return nil, time.Time{}, connection.fatalResult(&malformedResultError{
			Command: commandPollValue,
			Reason:  "the result carried no decodable poll payload",
		})
	}
	return payload.Value, result.receivedAt, nil
}

// GetNodeState refreshes one complete node plan. It refreshes inventory, not a
// physical value, and never satisfies a Command.
func (connection *websocketConnection) GetNodeState(ctx context.Context, nodeID int) (nodeState, error) {
	if nodeID <= 0 {
		return nodeState{}, &invalidRequestError{
			Reason: "node.get_state requires a positive node ID",
		}
	}
	result, err := connection.requestSuccess(ctx, &nodeGetStateRequest{
		Command: commandGetState,
		NodeID:  nodeID,
	})
	if err != nil {
		return nodeState{}, err
	}
	var payload nodeGetStateResult
	if err = json.Unmarshal(result.Result, &payload); err != nil {
		return nodeState{}, connection.fatalResult(&malformedResultError{
			Command: commandGetState,
			Reason:  "the result carried no decodable node state",
		})
	}
	if payload.State.NodeID != nodeID {
		return nodeState{}, connection.fatalResult(&malformedResultError{
			Command: commandGetState,
			Reason:  "the result carried a different node",
		})
	}
	return payload.State, nil
}

// validatePlannedValueID refuses a Value ID this client cannot write. A local
// caller bug must not end the generation through a failed JSON encode. v1 never
// plans an invalid (null) property or a propertyKey, so both are refused
// defensively: an explicit JSON null key counts as absent, exactly as it does
// during planning.
func validatePlannedValueID(id valueID) error {
	switch {
	case id.CommandClass <= 0:
		return &invalidRequestError{Reason: "the Value ID needs a Command Class"}
	case id.Endpoint < 0:
		return &invalidRequestError{Reason: "the Value ID endpoint cannot be negative"}
	case id.Property.Numeric || id.Property.Invalid || id.Property.Name == "":
		return &invalidRequestError{Reason: "the Value ID needs a string property name"}
	case valueIDHasPropertyKey(id):
		return &invalidRequestError{Reason: "the Value ID cannot carry a property key"}
	default:
		return nil
	}
}

// markHandshakeStarted allows one handshake per generation.
func (connection *websocketConnection) markHandshakeStarted() error {
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	if connection.handshakeStarted {
		return &invalidRequestError{Reason: "StartListening was already called"}
	}
	connection.handshakeStarted = true
	return nil
}

// awaitVersionFrame waits for the validated first frame.
func (connection *websocketConnection) awaitVersionFrame(ctx context.Context) (serverVersion, error) {
	select {
	case version := <-connection.versionFrames:
		return version, nil
	case <-connection.done:
		return serverVersion{}, connection.failure()
	case <-ctx.Done():
		return serverVersion{}, connection.terminateHandshake(ctx, operationVersionFrame)
	}
}

// initializeSession sends initialize and requires a successful result.
func (connection *websocketConnection) initializeSession(ctx context.Context) error {
	components := connection.userAgentComponents
	if len(components) == 0 {
		components = defaultUserAgentComponents()
	}
	_, err := connection.requestSuccess(ctx, &initializeRequest{
		Command:                       commandInitialize,
		SchemaVersion:                 connection.schemaVersion,
		AdditionalUserAgentComponents: components,
	})
	if err != nil {
		// A generation that cannot initialize has no usable protocol state.
		connection.terminate(err)
		return err
	}
	return nil
}

// awaitSnapshot sends start_listening and requires a complete snapshot from the
// version frame's Home ID.
func (connection *websocketConnection) awaitSnapshot(
	ctx context.Context,
	version serverVersion,
) (networkSnapshot, error) {
	result, err := connection.requestSuccess(ctx, &startListeningRequest{
		Command: commandStartListening,
	})
	if err != nil {
		connection.terminate(err)
		return networkSnapshot{}, err
	}
	var snapshot networkSnapshot
	if err = json.Unmarshal(result.Result, &snapshot); err != nil {
		failure := &malformedSnapshotError{Reason: "the start_listening result did not decode"}
		connection.terminate(failure)
		return networkSnapshot{}, failure
	}
	homeID := snapshot.State.Controller.HomeID
	if homeID == nil {
		failure := &malformedSnapshotError{Reason: "the snapshot carried no controller Home ID"}
		connection.terminate(failure)
		return networkSnapshot{}, failure
	}
	if version.HomeID != nil && *version.HomeID != *homeID {
		failure := &homeIDMismatchError{
			VersionHomeID:  *version.HomeID,
			SnapshotHomeID: *homeID,
		}
		connection.terminate(failure)
		return networkSnapshot{}, failure
	}
	return snapshot, nil
}

// terminateHandshake ends a generation whose handshake context ended early.
func (connection *websocketConnection) terminateHandshake(ctx context.Context, operation string) error {
	timeout := &requestTimeoutError{Operation: operation, Cause: ctx.Err()}
	connection.terminate(timeout)
	return timeout
}

// requestSuccess writes one request and requires a successful result envelope.
func (connection *websocketConnection) requestSuccess(
	ctx context.Context,
	request correlatedRequest,
) (resultEnvelope, error) {
	result, err := connection.requestResult(ctx, request)
	if err != nil {
		return resultEnvelope{}, err
	}
	if !result.Success {
		return resultEnvelope{}, result.rejection()
	}
	return result, nil
}

// requestResult writes one correlated request and waits for its result.
// Cancelling ctx before the result arrives ends the generation, because the
// late result could no longer be routed to an owner.
func (connection *websocketConnection) requestResult(
	ctx context.Context,
	request correlatedRequest,
) (resultEnvelope, error) {
	if err := ctx.Err(); err != nil {
		return resultEnvelope{}, err
	}
	id, answer, err := connection.registerRequest(ctx, request)
	if err != nil {
		return resultEnvelope{}, err
	}
	result, arrived := connection.awaitResult(ctx, answer)
	if arrived {
		return result, nil
	}
	connection.removeRequest(id)
	if connection.terminated() {
		return resultEnvelope{}, connection.failure()
	}
	return resultEnvelope{}, connection.terminateHandshake(ctx, request.requestFields().Command)
}

// requestAbandonable writes one correlated request whose owner may stop waiting.
// Cancelling ctx releases the waiter, remembers the message ID as abandoned, and
// returns ctx's error while the generation stays usable; the late result of an
// abandoned correlation is then recognized and ignored once. A terminal failure
// is reported exactly as requestResult reports it, so an abandoned request never
// hides a lost connection. The frame itself is written under
// abandonableWriteContext, so a Command deadline ending after the write cannot
// close a healthy generation while a blocked write stays bounded. Only
// node.poll_value uses this path.
func (connection *websocketConnection) requestAbandonable(
	ctx context.Context,
	request correlatedRequest,
) (resultEnvelope, error) {
	if err := ctx.Err(); err != nil {
		return resultEnvelope{}, err
	}
	writeContext, cancelWrite := abandonableWriteContext(ctx)
	id, answer, err := connection.registerRequest(writeContext, request)
	cancelWrite()
	if err != nil {
		return resultEnvelope{}, err
	}
	result, arrived := connection.awaitResult(ctx, answer)
	if arrived {
		return result, nil
	}
	if connection.terminated() {
		connection.removeRequest(id)
		return resultEnvelope{}, connection.failure()
	}
	if err = ctx.Err(); err == nil {
		// awaitResult reports false only for an ended context or a lost
		// connection, so this is unreachable, but it must never mis-route a
		// result: end the generation instead of leaving the waiter dangling.
		connection.removeRequest(id)
		return resultEnvelope{}, connection.terminateHandshake(
			ctx,
			request.requestFields().Command,
		)
	}
	// A result that arrived with the deadline is consumed before the
	// correlation is abandoned, so a completed read is never discarded in
	// favour of a tombstone.
	select {
	case late := <-answer:
		return late, nil
	default:
	}
	if abandonErr := connection.abandonRequest(id); abandonErr != nil {
		connection.terminate(abandonErr)
		return resultEnvelope{}, abandonErr
	}
	return resultEnvelope{}, err
}

// requestSuccessAbandonable is requestSuccess over the abandonable correlation
// path, so a refused poll is the same typed upstream rejection it is on the
// owned path.
func (connection *websocketConnection) requestSuccessAbandonable(
	ctx context.Context,
	request correlatedRequest,
) (resultEnvelope, error) {
	result, err := connection.requestAbandonable(ctx, request)
	if err != nil {
		return resultEnvelope{}, err
	}
	if !result.Success {
		return resultEnvelope{}, result.rejection()
	}
	return result, nil
}

// abandonableWriteContext derives the bounded write context of one abandonable
// request. It keeps the caller's values but drops its cancellation and deadline,
// so a Command deadline that ends after the frame was written cannot make the
// WebSocket library close a healthy generation. The explicit pollWriteTimeout
// still bounds a write to a peer that stopped reading, so that write fails and
// ends the generation instead of blocking the caller forever.
func abandonableWriteContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), pollWriteTimeout)
}

// registerRequest reserves the next message ID and waiter, then writes the frame
// under writeContext. Message IDs are decimal and strictly increasing per
// generation. The write context is explicit because the two request kinds
// differ: an owned request is written under its caller's context, so a write
// blocked by a non-reading peer is bounded by the caller's deadline and its
// failure ends the generation, while an abandonable poll is written under
// abandonableWriteContext so a later caller cancellation cannot close the
// socket.
//
// The single writer slot is acquired first, and that acquisition is as
// context-bounded as the write itself: a request whose write context ends while
// another frame is being written fails then instead of waiting for the slot, so
// a strict deadline bounds the whole request and the bounded poll write context
// still bounds a poll.
func (connection *websocketConnection) registerRequest(
	writeContext context.Context,
	request correlatedRequest,
) (uint64, chan resultEnvelope, error) {
	if err := connection.acquireWriteGate(writeContext, request.requestFields().Command); err != nil {
		return 0, nil, err
	}
	defer connection.releaseWriteGate()
	if connection.terminated() {
		return 0, nil, connection.failure()
	}
	answer := make(chan resultEnvelope, 1)
	id, err := connection.addRequest(answer)
	if err != nil {
		return 0, nil, err
	}
	request.requestFields().MessageID = strconv.FormatUint(id, 10)
	if err = wsjson.Write(writeContext, connection.connection, request); err != nil {
		connection.removeRequest(id)
		failure := &writeFailureError{Command: request.requestFields().Command, Cause: err}
		connection.terminate(failure)
		return 0, nil, failure
	}
	return id, answer, nil
}

// acquireWriteGate waits for the single request-writer slot. Waiting observes
// the request's write context and the generation's terminal signal, so a request
// is never queued behind an unresponsive peer past its own deadline, and a
// generation that ends while a request waits releases it with the generation's
// failure.
//
// A write context that ends while the slot is held ends the generation exactly
// as a blocked frame write does: the caller can no longer be told whether its
// request would have reached the server had it been written, so the generation
// is not reused.
func (connection *websocketConnection) acquireWriteGate(ctx context.Context, command string) error {
	select {
	case connection.writeGate <- struct{}{}:
		return nil
	default:
	}
	select {
	case connection.writeGate <- struct{}{}:
		return nil
	case <-connection.done:
		return connection.failure()
	case <-ctx.Done():
		failure := &writeFailureError{Command: command, Cause: ctx.Err()}
		connection.terminate(failure)
		return failure
	}
}

// releaseWriteGate frees the single request-writer slot. Only the holder
// releases, so the slot is always occupied here.
func (connection *websocketConnection) releaseWriteGate() {
	<-connection.writeGate
}

// addRequest reserves one bounded waiter slot and the next message ID.
func (connection *websocketConnection) addRequest(answer chan resultEnvelope) (uint64, error) {
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	if len(connection.pending) >= maximumInFlightRequests {
		return 0, &inFlightRequestLimitError{Limit: maximumInFlightRequests}
	}
	connection.nextMessageID++
	id := connection.nextMessageID
	connection.pending[id] = answer
	return id, nil
}

// removeRequest releases a waiter that will not receive its result. Every caller
// terminates the generation right after, so no tombstone is kept: a result for
// the released ID is at or below nextMessageID, and deliverResult already
// classifies that as a duplicate.
func (connection *websocketConnection) removeRequest(id uint64) {
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	delete(connection.pending, id)
}

// abandonRequest releases one waiter whose result will no longer be consumed.
// The message ID becomes an abandoned correlation, so its late result is
// recognized and ignored once instead of ending the generation. The abandoned
// set is bounded: when it is full this reports an error so the generation ends
// rather than letting an unbounded correlation set decide routing.
func (connection *websocketConnection) abandonRequest(id uint64) error {
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	if _, pending := connection.pending[id]; !pending {
		return nil
	}
	delete(connection.pending, id)
	if len(connection.abandoned) >= maximumAbandonedRequests {
		return &abandonedRequestLimitError{Limit: maximumAbandonedRequests}
	}
	connection.abandoned[id] = struct{}{}
	return nil
}

// awaitResult waits for one correlated result. A result that already arrived
// wins over a concurrent connection failure.
func (connection *websocketConnection) awaitResult(
	ctx context.Context,
	answer <-chan resultEnvelope,
) (resultEnvelope, bool) {
	select {
	case result := <-answer:
		return result, true
	default:
	}
	select {
	case result := <-answer:
		return result, true
	case <-connection.done:
		return resultEnvelope{}, false
	case <-ctx.Done():
		return resultEnvelope{}, false
	}
}

// readFrames is the single reader goroutine. Any condition that makes routing
// unreliable ends the generation.
func (connection *websocketConnection) readFrames() {
	for {
		messageType, payload, err := connection.connection.Read(connection.readContext)
		if err != nil {
			connection.terminate(classifyReadFailure(err))
			return
		}
		if err = connection.routeFrame(messageType, payload); err != nil {
			connection.terminate(err)
			return
		}
		if connection.terminated() {
			return
		}
	}
}

// routeFrame classifies and routes one frame.
func (connection *websocketConnection) routeFrame(messageType websocket.MessageType, payload []byte) error {
	if messageType != websocket.MessageText {
		return &binaryFrameError{MessageType: int(messageType)}
	}
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &header); err != nil {
		return &malformedFrameError{Reason: "the frame is not a JSON object"}
	}
	if !connection.versionSeen && header.Type != frameTypeVersion {
		return &malformedFrameError{Reason: "the first frame was not a version frame"}
	}
	switch header.Type {
	case frameTypeVersion:
		return connection.routeVersionFrame(payload)
	case frameTypeResult:
		return connection.routeResultFrame(payload)
	case frameTypeEvent:
		return connection.routeEventFrame(payload)
	default:
		return &malformedFrameError{Reason: "the frame type is not a schema-29 frame type"}
	}
}

// routeVersionFrame validates the first frame and publishes it to the handshake.
func (connection *websocketConnection) routeVersionFrame(payload []byte) error {
	if connection.versionSeen {
		return &malformedFrameError{Reason: "the server sent a second version frame"}
	}
	connection.versionSeen = true
	var version serverVersion
	if err := json.Unmarshal(payload, &version); err != nil {
		return &malformedVersionFrameError{Reason: "the version frame did not decode"}
	}
	if version.HomeID == nil {
		return &malformedVersionFrameError{Reason: "the version frame carried no Home ID"}
	}
	if version.MinSchemaVersion == nil || version.MaxSchemaVersion == nil {
		return &malformedVersionFrameError{Reason: "the version frame carried no schema range"}
	}
	if *version.MinSchemaVersion > schemaVersion29 || *version.MaxSchemaVersion < schemaVersion29 {
		return &incompatibleSchemaVersionError{
			Minimum: *version.MinSchemaVersion,
			Maximum: *version.MaxSchemaVersion,
		}
	}
	connection.versionFrames <- version
	return nil
}

// routeResultFrame validates one correlated result and delivers it.
func (connection *websocketConnection) routeResultFrame(payload []byte) error {
	var result resultEnvelope
	if err := json.Unmarshal(payload, &result); err != nil {
		return &malformedFrameError{Reason: "the result frame did not decode"}
	}
	id, err := parseMessageID(result.MessageID)
	if err != nil {
		return err
	}
	result.receivedAt = time.Now().UTC()
	return connection.deliverResult(id, result)
}

// deliverResult routes one result to its waiter. A result for an abandoned
// correlation is recognized and ignored once; an unknown or repeated message ID
// ends the generation instead of being discarded.
func (connection *websocketConnection) deliverResult(id uint64, result resultEnvelope) error {
	connection.mutex.Lock()
	if waiter, found := connection.pending[id]; found {
		delete(connection.pending, id)
		connection.mutex.Unlock()
		waiter <- result
		return nil
	}
	if _, abandoned := connection.abandoned[id]; abandoned {
		delete(connection.abandoned, id)
		connection.mutex.Unlock()
		return nil
	}
	// Message IDs are allocated densely and strictly increasing from 1, so every
	// issued ID is at least 1 and at most nextMessageID. An issued ID that is
	// neither pending nor abandoned must already have had its result: releasing a
	// waiter always ends the generation, so no live waiter can be missing here. A
	// repeated result is therefore a duplicate, and both ID 0 and an ID above
	// nextMessageID are unknown.
	duplicate := id >= 1 && id <= connection.nextMessageID
	connection.mutex.Unlock()
	if duplicate {
		return &duplicateResultMessageIDError{MessageID: id}
	}
	return &unknownResultMessageIDError{MessageID: id}
}

// routeEventFrame validates one Event and queues it for the runtime coordinator.
func (connection *websocketConnection) routeEventFrame(payload []byte) error {
	var event serverEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return &malformedFrameError{Reason: "the event frame did not decode"}
	}
	if err := validateServerEvent(&event); err != nil {
		return err
	}
	select {
	case connection.events <- receivedEvent{Event: event, ReceivedAt: time.Now().UTC()}:
		return nil
	default:
		return &eventQueueOverflowError{Capacity: maximumQueuedEvents}
	}
}

// fatalResult reports a correlated result that cannot be consumed and ends the
// generation, because routing state is no longer trustworthy.
func (connection *websocketConnection) fatalResult(err error) error {
	connection.terminate(err)
	return err
}

// terminate ends the generation, records its cause, and releases every waiter.
// The first cause wins; later calls are no-ops.
func (connection *websocketConnection) terminate(cause error) {
	connection.terminateOnce.Do(func() {
		connection.mutex.Lock()
		connection.cause = cause
		connection.mutex.Unlock()
		_ = connection.connection.CloseNow()
		close(connection.done)
		connection.lost <- cause
		close(connection.lost)
	})
}

// terminated reports whether the generation has ended.
func (connection *websocketConnection) terminated() bool {
	select {
	case <-connection.done:
		return true
	default:
		return false
	}
}

// failure reports the cause that ended the generation.
func (connection *websocketConnection) failure() error {
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	if connection.cause == nil {
		return &connectionClosedError{}
	}
	return connection.cause
}

// validateServerEvent validates the consumed fields of one Event envelope.
// Unknown event names stay ignorable, but a structurally unusable envelope ends
// the generation because it cannot be routed.
func validateServerEvent(event *serverEvent) error {
	switch event.Event.Source {
	case eventSourceController, eventSourceNode, eventSourceDriver, eventSourceZniffer:
	default:
		return &malformedFrameError{Reason: "the event source is not a documented source"}
	}
	if event.Event.Event == "" {
		return &malformedFrameError{Reason: "the event name is empty"}
	}
	if event.Event.Source != eventSourceController {
		return nil
	}
	switch event.Event.Event {
	case eventNodeAdded, eventNodeRemoved:
		return deriveControllerEventNodeID(event)
	default:
		return nil
	}
}

// deriveControllerEventNodeID fills the node ID of a controller node added or
// node removed Event from node.nodeId, because schema 29 omits event.nodeId.
func deriveControllerEventNodeID(event *serverEvent) error {
	if len(event.Event.Node) == 0 {
		return &malformedFrameError{Reason: "the controller node event carried no node state"}
	}
	var node struct {
		NodeID int `json:"nodeId"`
	}
	if err := json.Unmarshal(event.Event.Node, &node); err != nil {
		return &malformedFrameError{Reason: "the controller node event node state did not decode"}
	}
	if node.NodeID <= 0 {
		return &malformedFrameError{Reason: "the controller node event carried no node ID"}
	}
	if event.Event.NodeID != 0 && event.Event.NodeID != node.NodeID {
		return &malformedFrameError{Reason: "the controller node event disagreed about its node ID"}
	}
	event.Event.NodeID = node.NodeID
	return nil
}

// rejection converts a failed result envelope into an upstream rejection.
func (result resultEnvelope) rejection() error {
	return &upstreamRejectionError{
		ErrorCode:          result.ErrorCode,
		ZWaveErrorCode:     result.ZWaveErrorCode,
		ZWaveErrorCodeName: result.ZWaveErrorCodeName,
		ZWaveErrorMessage:  result.ZWaveErrorMessage,
		Message:            result.Message,
	}
}

// decodeSetValueStatus accepts only the schema-29 numeric SetValueStatus
// encoding. A string status is not the documented representation and is never
// treated as success.
func decodeSetValueStatus(raw json.RawMessage) (setValueStatus, bool) {
	if len(raw) == 0 || raw[0] == '"' {
		return setValueStatusUnrecognized, false
	}
	var status setValueStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return setValueStatusUnrecognized, false
	}
	switch status {
	case setValueStatusNoDeviceSupport,
		setValueStatusWorking,
		setValueStatusFail,
		setValueStatusEndpointNotFound,
		setValueStatusNotImplemented,
		setValueStatusInvalidValue,
		setValueStatusSuccessUnsupervised,
		setValueStatusSuccess:
		return status, true
	case setValueStatusUnrecognized:
		// The sentinel is never a documented upstream encoding.
		return setValueStatusUnrecognized, false
	default:
		return setValueStatusUnrecognized, false
	}
}

// parseMessageID requires a canonical decimal message ID: digits only, no sign,
// no leading zeros, and at least 1, because this generation allocates its
// message IDs from 1 upward. Zero is canonical decimal text but can never name a
// request this generation sent, so it is reported as an unknown message ID
// rather than being mistaken for an already-completed correlation.
func parseMessageID(raw string) (uint64, error) {
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || strconv.FormatUint(id, 10) != raw {
		return 0, &malformedFrameError{Reason: "the result message ID is not a canonical decimal"}
	}
	if id == 0 {
		return 0, &unknownResultMessageIDError{MessageID: id}
	}
	return id, nil
}

// classifyReadFailure reports why frame reads stopped.
func classifyReadFailure(cause error) error {
	if errors.Is(cause, websocket.ErrMessageTooBig) {
		return &oversizedFrameError{LimitBytes: maximumFrameBytes}
	}
	if closeError, found := errors.AsType[websocket.CloseError](cause); found {
		return &readFailureError{Cause: cause, CloseCode: int(closeError.Code)}
	}
	return &readFailureError{Cause: cause}
}

// dialFailureError reports a failed WebSocket dial.
type dialFailureError struct{ Cause error }

func (err *dialFailureError) Error() string { return "zwavejs: dial failed: " + err.Cause.Error() }

// Unwrap exposes the dial cause.
func (err *dialFailureError) Unwrap() error { return err.Cause }

// readFailureError reports that frame reads stopped. CloseCode is the WebSocket
// close code, or zero when the peer sent none.
type readFailureError struct {
	Cause     error
	CloseCode int
}

func (err *readFailureError) Error() string {
	return "zwavejs: frame read failed: " + err.Cause.Error()
}

// Unwrap exposes the read cause.
func (err *readFailureError) Unwrap() error { return err.Cause }

// writeFailureError reports that a request could not be written.
type writeFailureError struct {
	Command string
	Cause   error
}

func (err *writeFailureError) Error() string {
	return "zwavejs: " + err.Command + " write failed: " + err.Cause.Error()
}

// Unwrap exposes the write cause.
func (err *writeFailureError) Unwrap() error { return err.Cause }

// oversizedFrameError reports a frame above the 16 MiB read limit.
type oversizedFrameError struct{ LimitBytes int }

func (err *oversizedFrameError) Error() string {
	return "zwavejs: frame exceeded the " +
		strconv.Itoa(err.LimitBytes) + " byte read limit"
}

// binaryFrameError reports a non-text frame, which schema 29 never sends.
type binaryFrameError struct{ MessageType int }

func (err *binaryFrameError) Error() string {
	return "zwavejs: received a non-text frame of type " + strconv.Itoa(err.MessageType)
}

// malformedFrameError reports a frame that cannot be routed.
type malformedFrameError struct{ Reason string }

func (err *malformedFrameError) Error() string { return "zwavejs: malformed frame: " + err.Reason }

// malformedVersionFrameError reports a version frame the client cannot consume.
// The Adapter reports this as an invalid snapshot.
type malformedVersionFrameError struct{ Reason string }

func (err *malformedVersionFrameError) Error() string {
	return "zwavejs: malformed version frame: " + err.Reason
}

// incompatibleSchemaVersionError reports a server whose schema range excludes
// schema 29.
type incompatibleSchemaVersionError struct{ Minimum, Maximum int }

func (err *incompatibleSchemaVersionError) Error() string {
	return "zwavejs: server schema range " + strconv.Itoa(err.Minimum) + ".." +
		strconv.Itoa(err.Maximum) + " excludes schema " + strconv.Itoa(schemaVersion29)
}

// malformedSnapshotError reports a start_listening snapshot the client cannot
// consume.
type malformedSnapshotError struct{ Reason string }

func (err *malformedSnapshotError) Error() string {
	return "zwavejs: malformed snapshot: " + err.Reason
}

// homeIDMismatchError reports a version frame and snapshot that describe
// different Z-Wave networks.
type homeIDMismatchError struct{ VersionHomeID, SnapshotHomeID uint32 }

func (err *homeIDMismatchError) Error() string {
	return "zwavejs: version frame Home ID " + strconv.FormatUint(uint64(err.VersionHomeID), 16) +
		" does not match snapshot Home ID " + strconv.FormatUint(uint64(err.SnapshotHomeID), 16)
}

// unknownResultMessageIDError reports a result for a request this generation
// never sent.
type unknownResultMessageIDError struct{ MessageID uint64 }

func (err *unknownResultMessageIDError) Error() string {
	return "zwavejs: result for unknown message ID " + strconv.FormatUint(err.MessageID, 10)
}

// duplicateResultMessageIDError reports a second result for one message ID.
type duplicateResultMessageIDError struct{ MessageID uint64 }

func (err *duplicateResultMessageIDError) Error() string {
	return "zwavejs: duplicate result for message ID " + strconv.FormatUint(err.MessageID, 10)
}

// eventQueueOverflowError reports that validated Events outran the runtime
// coordinator.
type eventQueueOverflowError struct{ Capacity int }

func (err *eventQueueOverflowError) Error() string {
	return "zwavejs: event queue overflowed its " + strconv.Itoa(err.Capacity) + " frame capacity"
}

// inFlightRequestLimitError reports too many concurrently awaited results.
type inFlightRequestLimitError struct{ Limit int }

func (err *inFlightRequestLimitError) Error() string {
	return "zwavejs: more than " + strconv.Itoa(err.Limit) + " requests are awaiting a result"
}

// abandonedRequestLimitError reports too many correlations abandoned before
// their results arrived. The generation ends so the next one resynchronizes
// instead of an unknown late result being mistaken for a routing fault.
type abandonedRequestLimitError struct{ Limit int }

func (err *abandonedRequestLimitError) Error() string {
	return "zwavejs: more than " + strconv.Itoa(err.Limit) +
		" requests were abandoned before their results arrived"
}

// requestTimeoutError reports a request whose result did not arrive before its
// context ended. The generation ends because a late result would be unroutable.
type requestTimeoutError struct {
	Operation string
	Cause     error
}

func (err *requestTimeoutError) Error() string {
	return "zwavejs: " + err.Operation + " did not answer before its context ended: " + err.Cause.Error()
}

// Unwrap exposes the context cause.
func (err *requestTimeoutError) Unwrap() error { return err.Cause }

// upstreamRejectionError reports a failed result envelope. Schema 29 reports
// Z-Wave failures as errorCode zwave_error with a zwaveErrorCode and
// zwaveErrorMessage.
type upstreamRejectionError struct {
	ErrorCode          string
	ZWaveErrorCode     *int
	ZWaveErrorCodeName string
	ZWaveErrorMessage  string
	Message            string
}

func (err *upstreamRejectionError) Error() string {
	// Schema 29 reports Z-Wave failures with zwaveErrorMessage, which is more
	// specific than the generic message field. Prefer it, and fall back to the
	// generic message so a rejection without a Z-Wave error still reports what
	// detail the server sent.
	detail := err.ZWaveErrorMessage
	if detail == "" {
		detail = err.Message
	}
	if detail == "" {
		detail = "the server reported no detail"
	}
	code := err.ErrorCode
	if err.ZWaveErrorCodeName != "" {
		code += "/" + err.ZWaveErrorCodeName
	}
	return "zwavejs: upstream rejected the request (" + code + "): " + detail
}

// setValueRefusedError reports a node.set_value result that is not one of the
// accepted statuses. Status is setValueStatusUnrecognized when the server sent
// no documented schema-29 status.
type setValueRefusedError struct{ Status setValueStatus }

func (err *setValueRefusedError) Error() string {
	if err.Status == setValueStatusUnrecognized {
		return "zwavejs: node.set_value returned no documented schema-29 status"
	}
	return "zwavejs: node.set_value was refused with status " + strconv.Itoa(int(err.Status))
}

// malformedResultError reports a correlated result whose payload cannot be
// consumed or that describes a different resource than the request named.
type malformedResultError struct {
	Command string
	Reason  string
}

func (err *malformedResultError) Error() string {
	return "zwavejs: malformed " + err.Command + " result: " + err.Reason
}

// invalidRequestError reports a request this client refuses to write.
type invalidRequestError struct{ Reason string }

func (err *invalidRequestError) Error() string { return "zwavejs: invalid request: " + err.Reason }

// connectionClosedError reports a request on a deliberately closed generation.
type connectionClosedError struct{}

func (*connectionClosedError) Error() string { return "zwavejs: connection generation is closed" }
