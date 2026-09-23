package zwavejs //nolint:testpackage // Tests exercise private planning, translation, and identity.

// testhelp_test.go holds the fixtures shared by the planning, translation, and
// identity tests: snapshot builders, sanitized transcript loading, and a
// recording Session that exercises the public seam without a broker.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	// fixtureHomeIDText is the normalized network identity every transcript
	// fixture and builder shares. It is a synthetic, sanitized Home ID shared
	// with client_test.go, not a real household Home ID.
	fixtureHomeIDText = "1a2b3c4d"

	// fixtureStatusAlive is the schema-29 node status of an alive or awake node.
	fixtureStatusAlive = 4
)

// snapshotValueFixture builds one snapshot Value. The value is raw text so a
// test can express null, fractions, numeric strings, and 255 exactly.
func snapshotValueFixture(
	commandClass, endpoint int,
	property string,
	metadata valueMetadata,
	value string,
) valueState {
	return valueState{
		valueID:  testValueID(commandClass, endpoint, property),
		Metadata: metadata,
		Value:    json.RawMessage(value),
	}
}

// numericPropertyValueFixture builds one snapshot Value whose property name is a
// JSON number, which real Z-Wave JS networks report for some Command Classes.
// The numeric name is deliberately discarded by decoding, so the fixture names
// no property.
func numericPropertyValueFixture(commandClass, endpoint int, value string) valueState {
	return valueState{
		CommandClass: commandClass,
		Endpoint:     endpoint,
		Property:     valueProperty{Numeric: true},
		Metadata:     valueMetadata{Type: metadataTypeNumber, Readable: true, Valid: true},
		Value:        json.RawMessage(value),
	}
}

// propertyKeyValueFixture builds one snapshot Value that carries a propertyKey,
// which is a different Value than the unkeyed one.
func propertyKeyValueFixture(
	commandClass, endpoint int,
	property string,
	propertyKey int,
	metadata valueMetadata,
	value string,
) valueState {
	fixture := snapshotValueFixture(commandClass, endpoint, property, metadata, value)
	fixture.PropertyKey = json.RawMessage(strconv.Itoa(propertyKey))
	return fixture
}

// boolMetadata is the metadata a Binary Switch current or target Value reports.
// Fixture metadata is marked valid exactly as decoding marks it, because a
// planning validator requires metadata that decoded from its documented types.
func boolMetadata(readable, writeable bool) valueMetadata {
	return valueMetadata{
		Type: metadataTypeBoolean, Readable: readable, Writeable: writeable, Valid: true,
	}
}

// levelMetadata is the metadata a native Multilevel Switch currentValue reports:
// a number with bounds exactly 0..99 and no write support.
func levelMetadata(readable bool) valueMetadata {
	minimum := 0.0
	maximum := float64(zWaveLevelMaximum)
	return valueMetadata{
		Type:     metadataTypeNumber,
		Readable: readable,
		Min:      &minimum,
		Max:      &maximum,
		Valid:    true,
	}
}

// boundedLevelMetadata is level metadata with explicit bounds, used for a device
// that scales its levels to a range Hearth cannot mirror.
func boundedLevelMetadata(minimum, maximum float64) valueMetadata {
	return valueMetadata{
		Type:     metadataTypeNumber,
		Readable: true,
		Min:      &minimum,
		Max:      &maximum,
		Valid:    true,
	}
}

// numberMetadata is number metadata without bounds, the shape a Multilevel
// Switch targetValue reports when no current value supplies a range.
func numberMetadata(readable, writeable bool) valueMetadata {
	return valueMetadata{
		Type: metadataTypeNumber, Readable: readable, Writeable: writeable, Valid: true,
	}
}

// binaryPairFixture is one endpoint's valid Binary Switch current and target
// Value pair.
func binaryPairFixture(endpoint int) []valueState {
	return []valueState{
		snapshotValueFixture(
			testCommandClassBinarySwitch, endpoint, valuePropertyCurrentValue,
			boolMetadata(true, false), "true",
		),
		snapshotValueFixture(
			testCommandClassBinarySwitch, endpoint, valuePropertyTargetValue,
			boolMetadata(false, true), "true",
		),
	}
}

// levelPairFixture is one endpoint's valid Multilevel Switch current and target
// Value pair.
func levelPairFixture(endpoint int) []valueState {
	return []valueState{
		snapshotValueFixture(
			testCommandClassMultilevelSwitch, endpoint, valuePropertyCurrentValue,
			levelMetadata(true), "15",
		),
		snapshotValueFixture(
			testCommandClassMultilevelSwitch, endpoint, valuePropertyTargetValue,
			numberMetadata(false, true), "80",
		),
	}
}

// nodeFixture builds one ready, always-listening, non-controller node with a
// complete interview. Tests override single fields to express one broken
// eligibility rule.
func nodeFixture(nodeID int, endpoints []endpointState, values []valueState) nodeState {
	return nodeState{
		NodeID:         nodeID,
		Ready:          true,
		Status:         fixtureStatusAlive,
		InterviewStage: interviewStageComplete,
		IsListening:    true,
		Name:           "Fixture Node",
		Label:          "Fixture",
		Endpoints:      endpoints,
		Values:         values,
	}
}

// rootEndpointFixture is the root endpoint entry every node inventory reports.
func rootEndpointFixture() endpointState {
	return endpointState{Index: 0}
}

// snapshotFixture builds one start-listening snapshot with explicit nodes.
func snapshotFixture(homeID uint32, nodes ...nodeState) networkSnapshot {
	var snapshot networkSnapshot
	snapshot.State.Controller = controllerState{HomeID: &homeID}
	snapshot.State.Nodes = nodes
	return snapshot
}

// loadTranscriptSnapshot reads one synthetic JSONL server transcript from
// testdata and returns its version frame and its start_listening snapshot. The
// fixture is source-derived and sanitized: it mirrors the frame shapes a
// Z-Wave JS schema-29 server emits for a made-up network, not a household
// capture, so planning tests run against realistic wire shapes instead of
// hand-built Go values.
func loadTranscriptSnapshot(t *testing.T, name string) (serverVersion, networkSnapshot) {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read transcript %s: %v", name, err)
	}
	var (
		version       serverVersion
		snapshot      networkSnapshot
		foundVersion  bool
		foundSnapshot bool
	)
	for index, line := range strings.Split(string(payload), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		var header struct {
			Type string `json:"type"`
		}
		if err = json.Unmarshal([]byte(trimmed), &header); err != nil {
			t.Fatalf("testdata/%s line %d is not a JSON object: %v", name, index+1, err)
		}
		switch header.Type {
		case frameTypeVersion:
			if err = json.Unmarshal([]byte(trimmed), &version); err != nil {
				t.Fatalf("testdata/%s line %d is not a version frame: %v", name, index+1, err)
			}
			foundVersion = true
		case frameTypeResult:
			lineVersion, lineSnapshot, ok := decodeTranscriptResult(t, name, index+1, trimmed)
			if !ok {
				continue
			}
			snapshot = lineSnapshot
			foundSnapshot = true
			if lineVersion != nil {
				version.HomeID = lineVersion
			}
		default:
		}
	}
	if !foundVersion || !foundSnapshot {
		t.Fatalf("testdata/%s has version=%t snapshot=%t, want both", name, foundVersion, foundSnapshot)
	}
	return version, snapshot
}

// decodeTranscriptResult extracts the start_listening snapshot from one result
// line, and reports false for a result that is not the snapshot.
func decodeTranscriptResult(
	t *testing.T,
	name string,
	lineNumber int,
	line string,
) (*uint32, networkSnapshot, bool) {
	t.Helper()
	var result resultEnvelope
	if err := json.Unmarshal([]byte(line), &result); err != nil {
		t.Fatalf("testdata/%s line %d is not a result frame: %v", name, lineNumber, err)
	}
	if !result.Success {
		return nil, networkSnapshot{}, false
	}
	var probe struct {
		State struct {
			Controller controllerState `json:"controller"`
		} `json:"state"`
	}
	if err := json.Unmarshal(result.Result, &probe); err != nil {
		return nil, networkSnapshot{}, false
	}
	if probe.State.Controller.HomeID == nil {
		return nil, networkSnapshot{}, false
	}
	var snapshot networkSnapshot
	if err := json.Unmarshal(result.Result, &snapshot); err != nil {
		t.Fatalf("testdata/%s line %d snapshot did not decode: %v", name, lineNumber, err)
	}
	return probe.State.Controller.HomeID, snapshot, true
}

// canonicalEntityID is the deterministic canonical Entity ID one test Session
// answers for one Entity key.
func canonicalEntityID(key string) string {
	return "ent_" + key
}

// recordingSession is a Session seam stub. It records every registration,
// Observation, availability batch, owned mapping page, and health report so a
// test can assert the boundary without a broker.
type recordingSession struct {
	mutex         sync.Mutex
	ownedMappings []adapter.OwnedMapping
	registrations []adapter.Registration
	bindings      []adapter.Binding
	observations  []adapter.Observation
	availability  [][]adapter.EntityAvailabilityReport
	health        []adapter.HealthReport
}

// ListOwnedMappings returns the owned mappings this stub was seeded with.
func (session *recordingSession) ListOwnedMappings(
	_ context.Context,
	_ adapter.OwnedMappingPageRequest,
) (adapter.OwnedMappingPage, error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return adapter.OwnedMappingPage{Items: slices.Clone(session.ownedMappings)}, nil
}

// Register records one registration and answers deterministic canonical Entity
// IDs, mirroring Hearth's canonical-binding response.
func (session *recordingSession) Register(
	_ context.Context,
	registration adapter.Registration,
) (adapter.Binding, error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	session.registrations = append(session.registrations, registration)
	binding := adapter.Binding{
		BindingKey: registration.BindingKey,
		DeviceID:   "dev_" + registration.BindingKey,
	}
	for _, entity := range registration.Entities {
		binding.Entities = append(binding.Entities, adapter.EntityBinding{
			Key:      entity.Key,
			EntityID: canonicalEntityID(entity.Key),
			Enabled:  true,
		})
	}
	session.bindings = append(session.bindings, binding)
	return binding, nil
}

// SetHealth records one health report.
func (session *recordingSession) SetHealth(_ context.Context, report adapter.HealthReport) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	session.health = append(session.health, report)
	return nil
}

// ReportEntityAvailability records one availability batch.
func (session *recordingSession) ReportEntityAvailability(
	_ context.Context,
	reports []adapter.EntityAvailabilityReport,
) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	session.availability = append(session.availability, slices.Clone(reports))
	return nil
}

// PublishObservation records one Observation and answers a deterministic ID.
func (session *recordingSession) PublishObservation(
	_ context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	session.observations = append(session.observations, observation)
	return adapter.ObservationID(fmt.Sprintf("obs-%d", len(session.observations))), nil
}

// registeredBindings returns a copy of every Binding this stub answered.
func (session *recordingSession) registeredBindings() []adapter.Binding {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return slices.Clone(session.bindings)
}

const (
	// harnessTimeout bounds one runtime test's eventual wait.
	harnessTimeout = 5 * time.Second

	// harnessPollHintInterval is the injected poll-coalescing interval. Tests
	// that assert the production floor use defaultPollHintInterval directly.
	harnessPollHintInterval = 5 * time.Millisecond
)

// runtimeRecorder is an ordered, concurrency-safe log of boundary events.
type runtimeRecorder struct {
	mutex  sync.Mutex
	events []string
}

func (recorder *runtimeRecorder) add(event string) {
	recorder.mutex.Lock()
	recorder.events = append(recorder.events, event)
	recorder.mutex.Unlock()
}

func (recorder *runtimeRecorder) snapshot() []string {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]string(nil), recorder.events...)
}

func (recorder *runtimeRecorder) has(event string) bool {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return slices.Contains(recorder.events, event)
}

func (recorder *runtimeRecorder) count(prefix string) int {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	total := 0
	for _, event := range recorder.events {
		if strings.HasPrefix(event, prefix) {
			total++
		}
	}
	return total
}

// firstIndex is the index of the first recorded event with one prefix, or -1.
func (recorder *runtimeRecorder) firstIndex(prefix string) int {
	for index, event := range recorder.snapshot() {
		if strings.HasPrefix(event, prefix) {
			return index
		}
	}
	return -1
}

// newRuntimeSession builds one recording Session stub.
func newRuntimeSession(recorder *runtimeRecorder) *runtimeSession {
	return &runtimeSession{recorder: recorder, logs: &logRecorder{}}
}

// runtimeSession is a Session seam stub. It records every boundary call in one
// ordered log and answers canonical Entity IDs that mirror Hearth's canonical
// binding response.
type runtimeSession struct {
	recorder *runtimeRecorder
	mutex    sync.Mutex

	mappings     []adapter.OwnedMapping
	mappingError error

	registers    []adapter.Registration
	registerHook func(context.Context, adapter.Registration) (adapter.Binding, error)

	health      []adapter.HealthReport
	healthError error
	healthHook  func(context.Context, adapter.HealthReport) error

	batches           [][]adapter.EntityAvailabilityReport
	availability      []adapter.EntityAvailabilityReport
	availabilityError error

	observations []adapter.Observation
	linked       []adapter.Observation
	publishError error

	// publishHook and linkedHook intercept one Observation before it is
	// recorded, so a test can block a publication and prove its ordering or its
	// cancellation. A hook that reports an error suppresses the record exactly as
	// a failed Session call would.
	publishHook func(context.Context, adapter.Observation) error
	linkedHook  func(context.Context, adapter.Observation) error

	// logs records every diagnostic event name so a test can observe a
	// lifecycle milestone that has no Session boundary.
	logs *logRecorder
}

// ListOwnedMappings answers one page with the seeded mappings.
func (session *runtimeSession) ListOwnedMappings(
	_ context.Context,
	_ adapter.OwnedMappingPageRequest,
) (adapter.OwnedMappingPage, error) {
	session.recorder.add("list")
	if session.mappingError != nil {
		return adapter.OwnedMappingPage{}, session.mappingError
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return adapter.OwnedMappingPage{Items: slices.Clone(session.mappings)}, nil
}

// Register answers the canonical Entity IDs of one registration, reusing a
// seeded mapping's ID for a key Core already owns.
func (session *runtimeSession) Register(
	ctx context.Context,
	registration adapter.Registration,
) (adapter.Binding, error) {
	session.recorder.add("register:" + registration.BindingKey)
	session.mutex.Lock()
	session.registers = append(session.registers, registration)
	hook := session.registerHook
	existing := make(map[string]string, len(session.mappings))
	for _, mapping := range session.mappings {
		existing[mapping.BindingKey+"/"+mapping.EntityKey] = mapping.EntityID
	}
	session.mutex.Unlock()
	if hook != nil {
		return hook(ctx, registration)
	}
	if err := ctx.Err(); err != nil {
		return adapter.Binding{}, err
	}
	binding := adapter.Binding{
		BindingKey: registration.BindingKey,
		DeviceID:   "dev-" + registration.BindingKey,
	}
	for _, descriptor := range registration.Entities {
		entityID, found := existing[registration.BindingKey+"/"+descriptor.Key]
		if !found {
			entityID = registration.BindingKey + "/" + descriptor.Key
		}
		binding.Entities = append(binding.Entities, adapter.EntityBinding{
			Key:      descriptor.Key,
			EntityID: entityID,
			Enabled:  true,
		})
	}
	return binding, nil
}

// SetHealth records one health report.
func (session *runtimeSession) SetHealth(ctx context.Context, report adapter.HealthReport) error {
	event := "health:" + string(report.Status)
	if report.ReasonCode != "" {
		event += ":" + report.ReasonCode
	}
	session.recorder.add(event)
	session.mutex.Lock()
	session.health = append(session.health, report)
	err := session.healthError
	hook := session.healthHook
	session.mutex.Unlock()
	if hook != nil {
		return hook(ctx, report)
	}
	return err
}

// ReportEntityAvailability records one availability batch.
func (session *runtimeSession) ReportEntityAvailability(
	_ context.Context,
	reports []adapter.EntityAvailabilityReport,
) error {
	session.recorder.add("availability")
	session.mutex.Lock()
	session.batches = append(session.batches, slices.Clone(reports))
	session.availability = append(session.availability, reports...)
	err := session.availabilityError
	session.mutex.Unlock()
	return err
}

// PublishObservation records one ordinary Observation.
func (session *runtimeSession) PublishObservation(
	ctx context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	session.mutex.Lock()
	hook := session.publishHook
	session.mutex.Unlock()
	if hook != nil {
		if err := hook(ctx, observation); err != nil {
			return "", err
		}
	}
	session.recorder.add("observation:" + observation.EntityID)
	session.mutex.Lock()
	session.observations = append(session.observations, observation)
	err := session.publishError
	session.mutex.Unlock()
	return adapter.ObservationID("obs"), err
}

// setPublishHook installs one ordinary-publication interceptor.
func (session *runtimeSession) setPublishHook(
	hook func(context.Context, adapter.Observation) error,
) {
	session.mutex.Lock()
	session.publishHook = hook
	session.mutex.Unlock()
}

// publishLinked records one command-linked Observation.
func (session *runtimeSession) publishLinked(
	ctx context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	session.mutex.Lock()
	hook := session.linkedHook
	session.mutex.Unlock()
	if hook != nil {
		if err := hook(ctx, observation); err != nil {
			return "", err
		}
	}
	session.recorder.add("linked:" + observation.EntityID)
	session.mutex.Lock()
	session.linked = append(session.linked, observation)
	err := session.publishError
	session.mutex.Unlock()
	return adapter.ObservationID("obs-linked"), err
}

// setLinkedPublishHook installs one command-linked publication interceptor.
func (session *runtimeSession) setLinkedPublishHook(
	hook func(context.Context, adapter.Observation) error,
) {
	session.mutex.Lock()
	session.linkedHook = hook
	session.mutex.Unlock()
}

// recordedBatches returns a copy of every availability batch.
func (session *runtimeSession) recordedBatches() [][]adapter.EntityAvailabilityReport {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	batches := make([][]adapter.EntityAvailabilityReport, 0, len(session.batches))
	for _, batch := range session.batches {
		batches = append(batches, slices.Clone(batch))
	}
	return batches
}

// recordedObservations returns a copy of every ordinary Observation.
func (session *runtimeSession) recordedObservations() []adapter.Observation {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return slices.Clone(session.observations)
}

// recordedLinked returns a copy of every command-linked Observation.
func (session *runtimeSession) recordedLinked() []adapter.Observation {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return slices.Clone(session.linked)
}

// recordedAvailability returns a copy of every availability report.
func (session *runtimeSession) recordedAvailability() []adapter.EntityAvailabilityReport {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return slices.Clone(session.availability)
}

// recordedRegistrations returns a copy of every registration.
func (session *runtimeSession) recordedRegistrations() []adapter.Registration {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return slices.Clone(session.registers)
}

// setMappings replaces the seeded owned mappings.
func (session *runtimeSession) setMappings(mappings ...adapter.OwnedMapping) {
	session.mutex.Lock()
	session.mappings = mappings
	session.mutex.Unlock()
}

// setHealthHook installs one health interceptor.
func (session *runtimeSession) setHealthHook(
	hook func(context.Context, adapter.HealthReport) error,
) {
	session.mutex.Lock()
	session.healthHook = hook
	session.mutex.Unlock()
}

// lastAvailability reports the most recent availability report for one Entity.
func (session *runtimeSession) lastAvailability(
	entityID string,
) (adapter.EntityAvailabilityReport, bool) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	for _, report := range slices.Backward(session.availability) {
		if report.EntityID == entityID {
			return report, true
		}
	}
	return adapter.EntityAvailabilityReport{}, false
}

// linkedEvidence is the CommandEvidence one attempt retains.
type linkedEvidence struct{ session *runtimeSession }

func (evidence linkedEvidence) PublishObservation(
	ctx context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	return evidence.session.publishLinked(ctx, observation)
}

// fakeResponder is a Responder that refuses a second response, so a test can
// assert exactly one Hearth response per Command.
type fakeResponder struct {
	recorder  *runtimeRecorder
	mutex     sync.Mutex
	evidence  adapter.CommandEvidence
	accepted  int
	rejected  int
	responded int
}

func newFakeResponder(recorder *runtimeRecorder, session *runtimeSession) *fakeResponder {
	return &fakeResponder{recorder: recorder, evidence: linkedEvidence{session: session}}
}

func (responder *fakeResponder) Accept() (adapter.CommandEvidence, error) {
	responder.mutex.Lock()
	defer responder.mutex.Unlock()
	if responder.responded > 0 {
		return nil, adapter.ErrAlreadyResponded
	}
	responder.responded++
	responder.accepted++
	responder.recorder.add("response:accepted")
	return responder.evidence, nil
}

func (responder *fakeResponder) Reject(string) error {
	responder.mutex.Lock()
	defer responder.mutex.Unlock()
	if responder.responded > 0 {
		return adapter.ErrAlreadyResponded
	}
	responder.responded++
	responder.rejected++
	responder.recorder.add("response:rejected")
	return nil
}

func (responder *fakeResponder) RejectUnavailable(string) error {
	responder.mutex.Lock()
	defer responder.mutex.Unlock()
	if responder.responded > 0 {
		return adapter.ErrAlreadyResponded
	}
	responder.responded++
	responder.rejected++
	responder.recorder.add("response:rejected")
	return nil
}

// responderCounts reports how many accept responses, reject responses, and total
// responses one responder produced. A Command must produce exactly one response.
func responderCounts(responder *fakeResponder) (int, int, int) {
	responder.mutex.Lock()
	defer responder.mutex.Unlock()
	return responder.accepted, responder.rejected, responder.responded
}

// setValueCall records one correlated node.set_value request.
type setValueCall struct {
	NodeID  int
	ValueID valueID
	Value   string
}

// fakeConnection is one scripted connection generation.
type fakeConnection struct {
	recorder *runtimeRecorder
	mutex    sync.Mutex

	version  serverVersion
	snapshot networkSnapshot
	startErr error

	nodeStates map[int]nodeState

	setValueHook  func(context.Context, int, valueID, json.RawMessage) (setValueStatus, error)
	pollValueHook func(context.Context, int, valueID) (json.RawMessage, time.Time, error)

	setCalls  []setValueCall
	pollCalls []valueID
	pollTimes []time.Time

	events    chan receivedEvent
	lost      chan error
	closed    chan struct{}
	closeOnce sync.Once
	lostOnce  sync.Once
}

// newFakeConnection builds one scripted generation over a version frame and a
// start-listening snapshot.
func newFakeConnection(
	recorder *runtimeRecorder,
	version serverVersion,
	snapshot networkSnapshot,
) *fakeConnection {
	states := make(map[int]nodeState, len(snapshot.State.Nodes))
	for _, node := range snapshot.State.Nodes {
		states[node.NodeID] = node
	}
	return &fakeConnection{
		recorder:   recorder,
		version:    version,
		snapshot:   snapshot,
		nodeStates: states,
		events:     make(chan receivedEvent, 64),
		lost:       make(chan error, 1),
		closed:     make(chan struct{}),
	}
}

// setPolledNodeState changes the State returned by scripted PollValue calls.
func (connection *fakeConnection) setPolledNodeState(node nodeState) {
	connection.mutex.Lock()
	connection.nodeStates[node.NodeID] = node
	connection.mutex.Unlock()
}

// StartListening answers the scripted handshake.
func (connection *fakeConnection) StartListening(context.Context) (serverVersion, networkSnapshot, error) {
	connection.recorder.add("start_listening")
	if connection.startErr != nil {
		return serverVersion{}, networkSnapshot{}, connection.startErr
	}
	return connection.version, connection.snapshot, nil
}

// SetValue records and answers one correlated write.
func (connection *fakeConnection) SetValue(
	ctx context.Context,
	nodeID int,
	id valueID,
	value json.RawMessage,
) (setValueStatus, error) {
	connection.recorder.add("set_value")
	connection.mutex.Lock()
	connection.setCalls = append(connection.setCalls, setValueCall{
		NodeID: nodeID, ValueID: id, Value: string(value),
	})
	hook := connection.setValueHook
	connection.mutex.Unlock()
	if hook != nil {
		return hook(ctx, nodeID, id, value)
	}
	if err := ctx.Err(); err != nil {
		return setValueStatusUnrecognized, err
	}
	return setValueStatusSuccess, nil
}

// PollValue records and answers one correlated fresh read.
func (connection *fakeConnection) PollValue(
	ctx context.Context,
	nodeID int,
	id valueID,
) (json.RawMessage, time.Time, error) {
	// The poll start time is stamped before any bookkeeping so a test can assert
	// the coalescing floor from the request itself.
	startedAt := time.Now()
	connection.recorder.add("poll_value")
	connection.mutex.Lock()
	connection.pollCalls = append(connection.pollCalls, id)
	connection.pollTimes = append(connection.pollTimes, startedAt)
	hook := connection.pollValueHook
	state, known := connection.nodeStates[nodeID]
	connection.mutex.Unlock()
	if hook != nil {
		return hook(ctx, nodeID, id)
	}
	if err := ctx.Err(); err != nil {
		return nil, time.Time{}, err
	}
	if !known {
		return nil, time.Time{}, errors.New("zwavejs: no such node")
	}
	value, found := resolveCurrentValue(state.Values, id)
	if !found {
		return nil, time.Time{}, errors.New("zwavejs: node does not report that Value")
	}
	return value, time.Now().UTC(), nil
}

// Events delivers validated Events to the runtime pump.
func (connection *fakeConnection) Events() <-chan receivedEvent { return connection.events }

// Lost delivers the terminal error of this generation.
func (connection *fakeConnection) Lost() <-chan error { return connection.lost }

// Close ends the generation deliberately.
func (connection *fakeConnection) Close() {
	connection.signalLost(nil)
	connection.closeOnce.Do(func() { close(connection.closed) })
}

// fail ends the generation with a terminal error, as a lost socket would.
func (connection *fakeConnection) fail(err error) {
	connection.signalLost(err)
}

func (connection *fakeConnection) signalLost(err error) {
	connection.lostOnce.Do(func() {
		connection.lost <- err
		close(connection.lost)
	})
}

// emit delivers one upstream Event with an Adapter-owned receive time.
func (connection *fakeConnection) emit(event serverEvent) {
	connection.recorder.add("emit:" + event.Event.Source + "/" + event.Event.Event)
	connection.events <- receivedEvent{Event: event, ReceivedAt: time.Now().UTC()}
}

// recordedSetCalls returns a copy of every recorded write.
func (connection *fakeConnection) recordedSetCalls() []setValueCall {
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	return slices.Clone(connection.setCalls)
}

// recordedPollCalls returns a copy of every recorded poll.
func (connection *fakeConnection) recordedPollCalls() []valueID {
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	return slices.Clone(connection.pollCalls)
}

// recordedPollTimes returns a copy of every recorded poll start time.
func (connection *fakeConnection) recordedPollTimes() []time.Time {
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	return slices.Clone(connection.pollTimes)
}

// fakeDialer answers each dial with the next scripted connection generation.
type fakeDialer struct {
	mutex       sync.Mutex
	dials       int
	connections []*fakeConnection
}

func (dialer *fakeDialer) Dial(
	ctx context.Context,
	_ string,
	_ int,
	_ map[string]string,
) (zwaveConnection, error) {
	dialer.mutex.Lock()
	if dialer.dials >= len(dialer.connections) {
		dialer.dials++
		dialer.mutex.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	connection := dialer.connections[dialer.dials]
	dialer.dials++
	dialer.mutex.Unlock()
	return connection, nil
}

// dialCount reports how many generations the dialer answered.
func (dialer *fakeDialer) dialCount() int {
	dialer.mutex.Lock()
	defer dialer.mutex.Unlock()
	return dialer.dials
}

// newRuntimeAdapter builds one Adapter over an injected connection seam. A
// recording Session also supplies a recording logger, so route activation is
// observable without a Session boundary.
func newRuntimeAdapter(t *testing.T, session Session, dialer zwaveDialer) *Adapter {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	if recording, ok := session.(*runtimeSession); ok {
		logger = slog.New(recording.logs)
	}
	return newRuntimeAdapterWithLogger(t, session, dialer, logger)
}

// newRuntimeAdapterWithLogger builds one Adapter with an injected logger so a
// test can assert a fixed diagnostic event name.
func newRuntimeAdapterWithLogger(
	t *testing.T,
	session Session,
	dialer zwaveDialer,
	logger *slog.Logger,
) *Adapter {
	t.Helper()
	zwave, err := newAdapter(session, Config{URL: "ws://127.0.0.1:3000"}, logger, dialer)
	if err != nil {
		t.Fatal(err)
	}
	zwave.retryDelay = func(time.Duration) time.Duration { return time.Millisecond }
	zwave.pollHintInterval = harnessPollHintInterval
	return zwave
}

// logRecorder records the stable event name of every diagnostic record.
type logRecorder struct {
	mutex  sync.Mutex
	events []string
}

func (recorder *logRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (recorder *logRecorder) Handle(_ context.Context, record slog.Record) error {
	name := ""
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == eventKey {
			name = attr.Value.String()
			return false
		}
		return true
	})
	if name == "" {
		return nil
	}
	recorder.mutex.Lock()
	recorder.events = append(recorder.events, name)
	recorder.mutex.Unlock()
	return nil
}

func (recorder *logRecorder) WithAttrs([]slog.Attr) slog.Handler { return recorder }
func (recorder *logRecorder) WithGroup(string) slog.Handler      { return recorder }

func (recorder *logRecorder) has(name string) bool {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return slices.Contains(recorder.events, name)
}

// startRuntime runs one Adapter and stops it at test cleanup.
func startRuntime(t *testing.T, zwave *Adapter) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- zwave.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Adapter.Run returned %v", err)
			}
		case <-time.After(harnessTimeout):
			t.Error("Adapter.Run did not stop")
		}
	})
}

// waitForRoutesActivated blocks until one generation's routes are dispatchable.
// It is the only correct point to submit a Command after a fresh reconcile: the
// healthy report is acknowledged before availability and snapshot State, and
// routes install only after every preceding call succeeded.
func waitForRoutesActivated(t *testing.T, session *runtimeSession) {
	t.Helper()
	waitFor(t, "route activation", func() bool {
		return session.logs.has("adapter.reconcile_completed")
	})
}

// waitFor blocks until one observable condition holds. It is the only bounded
// eventual wait in the runtime tests.
func waitFor(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(harnessTimeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(time.Millisecond)
	}
}

// assertOrdered asserts that one set of events appears in order, ignoring
// unrelated events between them.
func assertOrdered(t *testing.T, events []string, want ...string) {
	t.Helper()
	index := 0
	for _, event := range events {
		if index < len(want) && event == want[index] {
			index++
		}
	}
	if index != len(want) {
		t.Fatalf("events = %v, missing ordered suffix %v", events, want[index:])
	}
}

// assertNotBefore asserts that the first event with one prefix is not recorded
// before the first event with another prefix.
func assertNotBefore(t *testing.T, recorder *runtimeRecorder, later, earlier string) {
	t.Helper()
	laterIndex := recorder.firstIndex(later)
	earlierIndex := recorder.firstIndex(earlier)
	if laterIndex < 0 || earlierIndex < 0 {
		t.Fatalf("missing %q (%d) or %q (%d)", later, laterIndex, earlier, earlierIndex)
	}
	if laterIndex < earlierIndex {
		t.Fatalf("events = %v, want %q after %q", recorder.snapshot(), later, earlier)
	}
}

// versionFixture builds a compatible version frame for the fixture network.
func versionFixture() serverVersion {
	homeID := uint32(testHomeID)
	minimum := 0
	maximum := 50
	return serverVersion{
		Type:             frameTypeVersion,
		DriverVersion:    "15.0.0",
		ServerVersion:    "3.10.1",
		HomeID:           &homeID,
		MinSchemaVersion: &minimum,
		MaxSchemaVersion: &maximum,
	}
}

// switchNodeFixture builds one ready Binary Switch node.
func switchNodeFixture(nodeID int, name string) nodeState {
	node := nodeFixture(nodeID, []endpointState{rootEndpointFixture()}, binaryPairFixture(0))
	node.Name = name
	return node
}

// dimmerNodeFixture builds one ready Multilevel Switch node with root power and
// brightness.
func dimmerNodeFixture(nodeID int, name string) nodeState {
	node := dimmerNodeAtLevel(nodeID, "15")
	node.Name = name
	return node
}

// dimmerNodeAtLevel builds one ready Multilevel Switch node whose root current
// value is an exact level.
func dimmerNodeAtLevel(nodeID int, level string) nodeState {
	return nodeFixture(nodeID, []endpointState{rootEndpointFixture()}, []valueState{
		snapshotValueFixture(
			testCommandClassMultilevelSwitch, 0, valuePropertyCurrentValue,
			levelMetadata(true), level,
		),
		snapshotValueFixture(
			testCommandClassMultilevelSwitch, 0, valuePropertyTargetValue,
			numberMetadata(false, true), "80",
		),
	})
}

// valueUpdatedEvent builds one node value update Event for the fixture node.
func valueUpdatedEvent(id valueID, newValue string) serverEvent {
	return valueUpdatedEventForNode(testNodeID, id, newValue)
}

// valueUpdatedEventForNode builds one node value update Event for one node, so a
// test can deliver the same Value ID from two different nodes.
func valueUpdatedEventForNode(nodeID int, id valueID, newValue string) serverEvent {
	args, err := json.Marshal(map[string]any{
		"commandClass": id.CommandClass,
		"endpoint":     id.Endpoint,
		"property":     id.Property.Name,
		"newValue":     json.RawMessage(newValue),
	})
	if err != nil {
		panic(err)
	}
	var event serverEvent
	event.Type = frameTypeEvent
	event.Event.Source = eventSourceNode
	event.Event.Event = eventValueUpdated
	event.Event.NodeID = nodeID
	event.Event.Args = args
	return event
}

// keyedValueUpdatedEvent builds one node value update Event whose Value ID
// carries a propertyKey, which is a different Value than the unkeyed one.
func keyedValueUpdatedEvent(id valueID, propertyKey int, newValue string) serverEvent {
	args, err := json.Marshal(map[string]any{
		"commandClass": id.CommandClass,
		"endpoint":     id.Endpoint,
		"property":     id.Property.Name,
		"propertyKey":  propertyKey,
		"newValue":     json.RawMessage(newValue),
	})
	if err != nil {
		panic(err)
	}
	var event serverEvent
	event.Type = frameTypeEvent
	event.Event.Source = eventSourceNode
	event.Event.Event = eventValueUpdated
	event.Event.NodeID = testNodeID
	event.Event.Args = args
	return event
}

// canonicalBinding mirrors the deterministic Binding one test Session answers
// for a Registration, so a test that intercepts Register can still answer the
// binding shape the runtime expects.
func canonicalBinding(registration adapter.Registration) adapter.Binding {
	binding := adapter.Binding{
		BindingKey: registration.BindingKey,
		DeviceID:   "dev-" + registration.BindingKey,
	}
	for _, descriptor := range registration.Entities {
		binding.Entities = append(binding.Entities, adapter.EntityBinding{
			Key:      descriptor.Key,
			EntityID: registration.BindingKey + "/" + descriptor.Key,
			Enabled:  true,
		})
	}
	return binding
}

// observationForEntity finds the most recent ordinary Observation of one Entity.
func observationForEntity(
	session *runtimeSession,
	entityID string,
) (adapter.Observation, bool) {
	for _, observation := range slices.Backward(session.recordedObservations()) {
		if observation.EntityID == entityID {
			return observation, true
		}
	}
	return adapter.Observation{}, false
}

// nodeEvent builds one node-sourced Event for the fixture node.
func nodeEvent(name string) serverEvent {
	var event serverEvent
	event.Type = frameTypeEvent
	event.Event.Source = eventSourceNode
	event.Event.Event = name
	event.Event.NodeID = testNodeID
	return event
}

// controllerNodeEvent builds one controller node Event carrying node state.
func controllerNodeEvent(name string, node nodeState) serverEvent {
	raw, err := json.Marshal(map[string]any{"nodeId": node.NodeID})
	if err != nil {
		panic(err)
	}
	var event serverEvent
	event.Type = frameTypeEvent
	event.Event.Source = eventSourceController
	event.Event.Event = name
	event.Event.Node = raw
	// The schema-29 reader derives the node ID of a controller node Event from
	// node.nodeId, so this fixture carries the normalized shape.
	event.Event.NodeID = node.NodeID
	return event
}

// commandFixture builds one set Command with an absolute deadline.
func commandFixture(entityID, parameters string, deadline time.Time) adapter.Command {
	return adapter.Command{
		ID:            "cmd-test",
		CorrelationID: "cor-test",
		EntityID:      entityID,
		OperationName: operationSet,
		Parameters:    json.RawMessage(parameters),
		Deadline:      deadline.UTC().Format(time.RFC3339Nano),
	}
}

// submitCommand runs one Command through the public Adapter seam and reports the
// handler result.
func submitCommand(
	t *testing.T,
	zwave *Adapter,
	command adapter.Command,
	responder adapter.Responder,
) chan error {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- zwave.HandleCommand(t.Context(), command, responder) }()
	return result
}

// routeEntityID is the canonical Entity ID one reconciled node's Entity key
// receives from the runtime Session stub.
func routeEntityID(nodeID int, entityKey string) string {
	return nodeBindingKey(fixtureHomeIDText, nodeID) + "/" + entityKey
}

// ownedMappingFixture is one Adapter-owned mapping of the fixture network.
func ownedMappingFixture(nodeID int, entityKey, entityID string) adapter.OwnedMapping {
	return adapter.OwnedMapping{
		BindingKey: nodeBindingKey(fixtureHomeIDText, nodeID),
		DeviceID:   "dev-" + nodeBindingKey(fixtureHomeIDText, nodeID),
		EntityKey:  entityKey,
		EntityID:   entityID,
	}
}

// observationValues renders the typed State of every recorded ordinary
// Observation in order.
func observationValues(observations []adapter.Observation) []string {
	values := make([]string, 0, len(observations))
	for _, observation := range observations {
		values = append(values, string(observation.Value))
	}
	return values
}
