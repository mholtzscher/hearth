package zwavejs //nolint:testpackage // Tests exercise private planning, translation, and identity.

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	// powerSupportJSON and brightnessSupportJSON are the exact typed support of
	// every planned Entity. They are hardcoded so a facade change that alters the
	// registered support cannot pass unnoticed.
	powerSupportJSON      = `{"state":{},"operations":{"set":{}}}`
	brightnessSupportJSON = `{"state":{"maximum":99},"operations":{"set":{"step":1}}}`

	powerTypeID      = "hearth.power/v1"
	brightnessTypeID = "hearth.brightness/v1"

	// commandClassBasic is the Z-Wave Basic Command Class, which v1 ignores: it
	// never becomes a plan even when it reports switch-shaped metadata.
	commandClassBasic = 32

	// fixtureSwitchNodeID, fixtureDimmerNodeID, and fixtureScaledNodeID are the
	// nodes of the synthetic, sanitized transcript fixtures.
	fixtureSwitchNodeID = 4
	fixtureDimmerNodeID = 23
	fixtureScaledNodeID = 30

	// fixtureEndpointCountLimit and fixtureEndpointCountOverLimit bracket Hearth's
	// 64 Entity bound with two capabilities per endpoint.
	fixtureEndpointCountLimit     = maximumPlannedEntitiesPerNode / 2
	fixtureEndpointCountOverLimit = fixtureEndpointCountLimit + 1

	// fixtureNameAtBound and fixtureNameOverBound are display names of exactly
	// 128 and 129 runes.
	fixtureNameAtBound   = maximumDescriptorRunes
	fixtureNameOverBound = maximumDescriptorRunes + 1
)

// entityExpectation is one independently written Entity descriptor expectation.
type entityExpectation struct {
	key        string
	externalID string
	name       string
	typeID     string
	support    string
}

// requireEntities asserts one registration's Entity descriptors exactly, in
// registration order.
func requireEntities(
	t *testing.T,
	descriptors []adapter.EntityDescriptor,
	want []entityExpectation,
) {
	t.Helper()
	if len(descriptors) != len(want) {
		t.Fatalf("registered Entities = %d, want %d", len(descriptors), len(want))
	}
	for index, expected := range want {
		descriptor := descriptors[index]
		switch {
		case descriptor.Key != expected.key:
			t.Fatalf("Entity %d key = %q, want %q", index, descriptor.Key, expected.key)
		case descriptor.ExternalID != expected.externalID:
			t.Fatalf("Entity %d external ID = %q, want %q", index, descriptor.ExternalID, expected.externalID)
		case descriptor.Name != expected.name:
			t.Fatalf("Entity %d name = %q, want %q", index, descriptor.Name, expected.name)
		case descriptor.Type != expected.typeID:
			t.Fatalf("Entity %d type = %q, want %q", index, descriptor.Type, expected.typeID)
		case string(descriptor.Support) != expected.support:
			t.Fatalf("Entity %d support = %s, want %s", index, descriptor.Support, expected.support)
		}
	}
}

// requireRegistrationIdentity asserts the stable identity of one registration.
func requireRegistrationIdentity(
	t *testing.T,
	registration adapter.Registration,
	homeID string,
	nodeID int,
	kind, name string,
) {
	t.Helper()
	if registration.BindingKey != "zwave-"+homeID+"-node-"+strconv.Itoa(nodeID) {
		t.Fatalf("Binding key = %q", registration.BindingKey)
	}
	device := registration.Device
	if device.ExternalID == nil || *device.ExternalID != homeID+"/node/"+strconv.Itoa(nodeID) {
		t.Fatalf("Device external ID = %v", device.ExternalID)
	}
	if device.Kind != kind || device.Name != name {
		t.Fatalf("Device = %#v, want kind %q name %q", device, kind, name)
	}
}

// requirePlan returns one planned Entity by key.
func requirePlan(t *testing.T, node discoveredNode, key string) entityPlan {
	t.Helper()
	for _, plan := range node.Plans {
		if plan.Key == key {
			return plan
		}
	}
	t.Fatalf("node %d has no planned Entity %q", node.NodeID, key)
	return entityPlan{}
}

// requirePlanKeys asserts the planned Entity keys of one node in plan order.
func requirePlanKeys(t *testing.T, node discoveredNode, want []string) {
	t.Helper()
	got := make([]string, 0, len(node.Plans))
	for _, plan := range node.Plans {
		got = append(got, plan.Key)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("node %d plans = %v, want %v", node.NodeID, got, want)
	}
}

// requireValueID asserts one planned Value ID exactly, which catches a Command
// Class, endpoint, or property swap.
func requireValueID(t *testing.T, id valueID, commandClass, endpoint int, property string) {
	t.Helper()
	if id.CommandClass != commandClass || id.Endpoint != endpoint || id.Property.Name != property {
		t.Fatalf(
			"Value ID = {cc:%d ep:%d property:%q}, want {cc:%d ep:%d property:%q}",
			id.CommandClass, id.Endpoint, id.Property.Name, commandClass, endpoint, property,
		)
	}
	if id.Property.Numeric || id.Property.Name == "" {
		t.Fatalf("Value ID property = %#v, want a string property name", id.Property)
	}
}

// requireOnlyNode returns the single planned node.
func requireOnlyNode(t *testing.T, plan networkPlan) discoveredNode {
	t.Helper()
	if len(plan.Nodes) != 1 {
		t.Fatalf("planned nodes = %d, want 1 (rejections %#v)", len(plan.Nodes), plan.Rejections)
	}
	return plan.Nodes[0]
}

// requireOnlyRejection returns the single rejection code of one node.
func requireOnlyRejection(t *testing.T, plan networkPlan, nodeID int) nodeRejectionCode {
	t.Helper()
	if len(plan.Nodes) != 0 {
		t.Fatalf("planned nodes = %d, want none", len(plan.Nodes))
	}
	for _, rejection := range plan.Rejections {
		if rejection.NodeID == nodeID {
			return rejection.Code
		}
	}
	t.Fatalf("no rejection for node %d (%#v)", nodeID, plan.Rejections)
	return ""
}

// This test protects the network identity every Hearth key and external ID
// carries, and fails if a Home ID loses its padding or hexadecimal case.
func TestNormalizedHomeIDIsEightLowercaseHexDigits(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		homeID uint32
		want   string
	}{
		{homeID: 0, want: "00000000"},
		{homeID: 0xabc, want: "00000abc"},
		{homeID: testHomeID, want: fixtureHomeIDText},
		{homeID: 0xffffffff, want: "ffffffff"},
	} {
		if got := normalizedHomeID(test.homeID); got != test.want {
			t.Fatalf("normalizedHomeID(%#x) = %q, want %q", test.homeID, got, test.want)
		}
	}
}

// This test protects A5 restart and rename identity, and fails if mutable node
// metadata leaks into a Binding key, Device external ID, or Entity external ID.
func TestEntityIdentityIgnoresMutableNodeMetadata(t *testing.T) {
	t.Parallel()
	original := nodeFixture(fixtureSwitchNodeID, []endpointState{rootEndpointFixture()}, binaryPairFixture(0))
	original.Name = "Kitchen Switch"
	original.Label = "Switch"
	original.Location = "Kitchen"
	manufacturer, product := 134, 2
	original.ManufacturerID = &manufacturer
	original.ProductID = &product

	renamed := original
	renamed.Name = "Renamed Kitchen Switch"
	renamed.Label = "Relay"
	renamed.Location = "Garage"
	otherManufacturer := 271
	renamed.ManufacturerID = &otherManufacturer

	first := planNetwork(testHomeID, snapshotFixture(testHomeID, original))
	second := planNetwork(testHomeID, snapshotFixture(testHomeID, renamed))
	firstNode := requireOnlyNode(t, first)
	secondNode := requireOnlyNode(t, second)

	if firstNode.Registration.BindingKey != secondNode.Registration.BindingKey {
		t.Fatalf("rename changed the Binding key %q -> %q",
			firstNode.Registration.BindingKey, secondNode.Registration.BindingKey)
	}
	if *firstNode.Registration.Device.ExternalID != *secondNode.Registration.Device.ExternalID {
		t.Fatalf("rename changed the Device external ID")
	}
	if len(firstNode.Registration.Entities) != len(secondNode.Registration.Entities) {
		t.Fatalf("rename changed the Entity count")
	}
	for index := range firstNode.Registration.Entities {
		before := firstNode.Registration.Entities[index]
		after := secondNode.Registration.Entities[index]
		if before.Key != after.Key || before.ExternalID != after.ExternalID {
			t.Fatalf("rename changed Entity identity %#v -> %#v", before, after)
		}
	}
	if firstNode.Registration.Device.Name != "Kitchen Switch" {
		t.Fatalf("Device name = %q, want the trimmed node name", firstNode.Registration.Device.Name)
	}
	if secondNode.Registration.Device.Name != "Renamed Kitchen Switch" {
		t.Fatalf("renamed Device name = %q", secondNode.Registration.Device.Name)
	}
}

// This test protects A5 network and slot identity, and fails if a different Home
// ID or node ID reuses node-number-shaped canonical identity.
func TestEntityIdentityChangesWithHomeIDAndNodeID(t *testing.T) {
	t.Parallel()
	first := planNetwork(testHomeID, snapshotFixture(
		testHomeID,
		nodeFixture(fixtureSwitchNodeID, []endpointState{rootEndpointFixture()}, binaryPairFixture(0)),
	))
	otherHome := planNetwork(0x2b2b2b2b, snapshotFixture(
		0x2b2b2b2b,
		nodeFixture(fixtureSwitchNodeID, []endpointState{rootEndpointFixture()}, binaryPairFixture(0)),
	))
	otherNode := planNetwork(testHomeID, snapshotFixture(
		testHomeID,
		nodeFixture(fixtureSwitchNodeID+1, []endpointState{rootEndpointFixture()}, binaryPairFixture(0)),
	))

	firstNode := requireOnlyNode(t, first)
	if got := requireOnlyNode(t, otherHome).Registration.BindingKey; got == firstNode.Registration.BindingKey {
		t.Fatalf("a different Home ID reused Binding key %q", got)
	}
	if got := requireOnlyNode(t, otherNode).Registration.BindingKey; got == firstNode.Registration.BindingKey {
		t.Fatalf("a different node ID reused Binding key %q", got)
	}
	if got := requireOnlyNode(t, otherHome).Registration.Entities[0].ExternalID; got ==
		firstNode.Registration.Entities[0].ExternalID {
		t.Fatalf("a different Home ID reused Entity external ID %q", got)
	}
}

// This test protects A3 for a synthetic, source-derived Binary Switch
// transcript, and fails if endpoint ordering, names, keys, external IDs, or
// Device kind drift.
func TestPlanSwitchTranscriptRegistration(t *testing.T) {
	t.Parallel()
	version, snapshot := loadTranscriptSnapshot(t, "switch-session.jsonl")
	plan := planNetwork(*version.HomeID, snapshot)
	if plan.HomeID != fixtureHomeIDText {
		t.Fatalf("network identity = %q, want %q", plan.HomeID, fixtureHomeIDText)
	}
	node := requireOnlyNode(t, plan)
	if node.NodeID != fixtureSwitchNodeID {
		t.Fatalf("planned node = %d, want %d", node.NodeID, fixtureSwitchNodeID)
	}
	requireRegistrationIdentity(
		t, node.Registration, fixtureHomeIDText, fixtureSwitchNodeID, "relay", "Kitchen Switch",
	)
	requirePlanKeys(t, node, []string{"power", "power-ep1", "power-ep2"})
	requireEntities(t, node.Registration.Entities, []entityExpectation{
		{
			key: "power", externalID: "1a2b3c4d/node/4/ep/0/power", name: "Power",
			typeID: powerTypeID, support: powerSupportJSON,
		},
		{
			key: "power-ep1", externalID: "1a2b3c4d/node/4/ep/1/power", name: "Outlet Power",
			typeID: powerTypeID, support: powerSupportJSON,
		},
		{
			key: "power-ep2", externalID: "1a2b3c4d/node/4/ep/2/power", name: "Endpoint 2 Power",
			typeID: powerTypeID, support: powerSupportJSON,
		},
	})
	for endpoint, key := range map[int]string{0: "power", 1: "power-ep1", 2: "power-ep2"} {
		capability := requirePlan(t, node, key)
		if capability.PowerFromMultilevel {
			t.Fatalf("Entity %q derived power from multilevel state", key)
		}
		requireValueID(t, capability.CurrentValueID, commandClassBinarySwitch, endpoint, valuePropertyCurrentValue)
		requireValueID(t, capability.TargetValueID, commandClassBinarySwitch, endpoint, valuePropertyTargetValue)
	}
	if len(plan.Rejections) != 5 {
		t.Fatalf("switch transcript rejections = %#v, want 5", plan.Rejections)
	}
}

// This test protects A3 for a synthetic, source-derived dimmer transcript,
// including derived power, native bounds rejection, and labelled endpoints.
func TestPlanDimmerTranscriptRegistration(t *testing.T) {
	t.Parallel()
	version, snapshot := loadTranscriptSnapshot(t, "dimmer-session.jsonl")
	plan := planNetwork(*version.HomeID, snapshot)
	node := requireOnlyNode(t, plan)
	if node.NodeID != fixtureDimmerNodeID {
		t.Fatalf("planned node = %d, want %d", node.NodeID, fixtureDimmerNodeID)
	}
	requireRegistrationIdentity(
		t, node.Registration, fixtureHomeIDText, fixtureDimmerNodeID, "light", "Hallway Dimmer",
	)
	requirePlanKeys(t, node, []string{"power", "brightness", "power-ep1", "brightness-ep1"})
	requireEntities(t, node.Registration.Entities, []entityExpectation{
		{
			key: "power", externalID: "1a2b3c4d/node/23/ep/0/power", name: "Power",
			typeID: powerTypeID, support: powerSupportJSON,
		},
		{
			key: "brightness", externalID: "1a2b3c4d/node/23/ep/0/brightness", name: "Brightness",
			typeID: brightnessTypeID, support: brightnessSupportJSON,
		},
		{
			key: "power-ep1", externalID: "1a2b3c4d/node/23/ep/1/power", name: "Second Power",
			typeID: powerTypeID, support: powerSupportJSON,
		},
		{
			key: "brightness-ep1", externalID: "1a2b3c4d/node/23/ep/1/brightness",
			name: "Second Brightness", typeID: brightnessTypeID, support: brightnessSupportJSON,
		},
	})

	rootPower := requirePlan(t, node, "power")
	if !rootPower.PowerFromMultilevel {
		t.Fatal("root power did not derive from Multilevel Switch state")
	}
	requireValueID(t, rootPower.CurrentValueID, commandClassMultilevelSwitch, 0, valuePropertyCurrentValue)
	requireValueID(t, rootPower.TargetValueID, commandClassMultilevelSwitch, 0, valuePropertyTargetValue)
	requireValueID(
		t, requirePlan(t, node, "brightness").CurrentValueID,
		commandClassMultilevelSwitch, 0, valuePropertyCurrentValue,
	)

	endpointPower := requirePlan(t, node, "power-ep1")
	if endpointPower.PowerFromMultilevel {
		t.Fatal("Binary Switch did not own endpoint 1 power")
	}
	requireValueID(t, endpointPower.CurrentValueID, commandClassBinarySwitch, 1, valuePropertyCurrentValue)
	requireValueID(t, endpointPower.TargetValueID, commandClassBinarySwitch, 1, valuePropertyTargetValue)
	requireValueID(
		t, requirePlan(t, node, "brightness-ep1").CurrentValueID,
		commandClassMultilevelSwitch, 1, valuePropertyCurrentValue,
	)

	if len(plan.Rejections) != 2 {
		t.Fatalf("dimmer transcript rejections = %#v, want 2", plan.Rejections)
	}
	codes := map[int]nodeRejectionCode{}
	for _, rejection := range plan.Rejections {
		codes[rejection.NodeID] = rejection.Code
	}
	if codes[1] != rejectionNodeController || codes[fixtureScaledNodeID] != rejectionNodeNoCapability {
		t.Fatalf("dimmer transcript rejection codes = %#v", codes)
	}
}

// This test protects A3 eligibility, and fails if a controller, sleeping, not
// ready, incompletely interviewed, or capability-free node is planned.
func TestPlanNetworkRejectsIneligibleNodes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(*nodeState)
		code nodeRejectionCode
	}{
		{name: "controller", edit: func(node *nodeState) { node.IsController = true }, code: rejectionNodeController},
		{name: "sleeping", edit: func(node *nodeState) { node.IsListening = false }, code: rejectionNodeSleeping},
		{name: "not_ready", edit: func(node *nodeState) { node.Ready = false }, code: rejectionNodeNotReady},
		{
			name: "interview",
			edit: func(node *nodeState) { node.InterviewStage = "NeighborDiscovery" },
			code: rejectionNodeInterview,
		},
		{name: "no_node_id", edit: func(node *nodeState) { node.NodeID = 0 }, code: rejectionNodeMalformed},
		{name: "no_capability", edit: func(node *nodeState) { node.Values = nil }, code: rejectionNodeNoCapability},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			node := nodeFixture(fixtureSwitchNodeID, []endpointState{rootEndpointFixture()}, binaryPairFixture(0))
			test.edit(&node)
			plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
			if got := requireOnlyRejection(t, plan, node.NodeID); got != test.code {
				t.Fatalf("rejection code = %q, want %q", got, test.code)
			}
		})
	}
}

// This test protects A4 sibling isolation, and fails if one malformed capability
// suppresses a valid sibling endpoint or the sibling capability on its own
// endpoint.
func TestPlanNetworkIsolatesInvalidCandidatesWithoutSuppressingSiblings(t *testing.T) {
	t.Parallel()
	brokenBinary := []valueState{
		snapshotValueFixture(
			testCommandClassBinarySwitch, 0, valuePropertyCurrentValue,
			valueMetadata{Type: "string", Readable: true, Valid: true}, `"true"`,
		),
		snapshotValueFixture(
			testCommandClassBinarySwitch, 0, valuePropertyTargetValue,
			boolMetadata(false, true), "true",
		),
	}
	unreadableBinary := []valueState{
		snapshotValueFixture(
			testCommandClassBinarySwitch, 2, valuePropertyCurrentValue,
			boolMetadata(false, false), "true",
		),
		snapshotValueFixture(
			testCommandClassBinarySwitch, 2, valuePropertyTargetValue,
			boolMetadata(false, true), "true",
		),
	}
	values := slices.Concat(brokenBinary, levelPairFixture(0), binaryPairFixture(1), unreadableBinary)
	node := nodeFixture(fixtureSwitchNodeID, []endpointState{
		rootEndpointFixture(),
		{Index: 1},
		{Index: 2},
	}, values)

	plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
	planned := requireOnlyNode(t, plan)
	requirePlanKeys(t, planned, []string{"power", "brightness", "power-ep1"})

	// Endpoint 0 keeps its multilevel brightness and derives power from it.
	if power := requirePlan(t, planned, "power"); !power.PowerFromMultilevel {
		t.Fatal("endpoint 0 power did not fall back to Multilevel Switch state")
	}
	requireValueID(
		t, requirePlan(t, planned, "power").CurrentValueID,
		commandClassMultilevelSwitch, 0, valuePropertyCurrentValue,
	)
	requireValueID(
		t, requirePlan(t, planned, "brightness").TargetValueID,
		commandClassMultilevelSwitch, 0, valuePropertyTargetValue,
	)
	// Endpoint 1 remains a valid Binary Switch.
	if power := requirePlan(t, planned, "power-ep1"); power.PowerFromMultilevel {
		t.Fatal("endpoint 1 power did not come from the Binary Switch pair")
	}
}

// This test protects snapshot metadata decoding and fails if one Value with
// malformed metadata makes the whole snapshot undecodable, if malformed metadata
// is planned as capability, or if unknown metadata fields invalidate usable
// capability. Malformed metadata isolates only its own capability.
func TestSnapshotDecodesMalformedMetadataWithoutIsolatingSiblings(t *testing.T) {
	t.Parallel()
	const (
		// The metadata under test belongs to endpoint 0's current Value. Endpoint
		// 0's target and both endpoint 1 Values carry documented metadata, so a
		// sibling capability must survive.
		metadataNode = `{"nodeId":23,"ready":true,"status":4,"interviewStage":"Complete",` +
			`"isListening":true,"endpoints":[{"index":0},{"index":1}],"values":[`
		validTarget = `{"commandClass":37,"property":"targetValue",` +
			`"metadata":{"type":"boolean","writeable":true},"value":true}`
		validSibling = `{"commandClass":37,"endpoint":1,"property":"currentValue",` +
			`"metadata":{"type":"boolean","readable":true},"value":false},` +
			`{"commandClass":37,"endpoint":1,"property":"targetValue",` +
			`"metadata":{"type":"boolean","writeable":true},"value":false}`
	)
	snapshotFrame := func(metadata string) string {
		return `{"state":{"controller":{"homeId":439041101},"nodes":[` + metadataNode +
			`{"commandClass":37,"property":"currentValue","metadata":` + metadata +
			`,"value":true},` + validTarget + `,` + validSibling + `]}]}}`
	}
	cases := []struct {
		name        string
		metadata    string
		wantPlanned []string
	}{
		{
			name: "documented types with unknown fields",
			metadata: `{"type":"boolean","readable":true,"writeable":false,` +
				`"unknownMetadataField":{"nested":1}}`,
			wantPlanned: []string{"power", "power-ep1"},
		},
		{name: "readable is a string", metadata: `{"type":"boolean","readable":"true"}`},
		{name: "writeable is a number", metadata: `{"type":"boolean","writeable":1}`},
		{name: "type is a number", metadata: `{"type":37,"readable":true}`},
		{
			name:     "bound is a string",
			metadata: `{"type":"number","readable":true,"min":"0","max":99}`,
		},
		{
			name:     "bound overflows a float",
			metadata: `{"type":"number","readable":true,"min":1e999,"max":99}`,
		},
		{name: "metadata is not an object", metadata: `"boolean"`},
		{name: "metadata is null", metadata: `null`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var snapshot networkSnapshot
			if err := json.Unmarshal([]byte(snapshotFrame(testCase.metadata)), &snapshot); err != nil {
				t.Fatalf("snapshot with %s metadata did not decode: %v", testCase.name, err)
			}
			plan := planNetwork(testHomeID, snapshot)
			planned := requireOnlyNode(t, plan)
			want := testCase.wantPlanned
			if want == nil {
				// Malformed metadata isolates exactly endpoint 0's power
				// capability, so only the sibling endpoint remains planned.
				want = []string{"power-ep1"}
			}
			requirePlanKeys(t, planned, want)
		})
	}
}

// This test protects the snapshot decode contract and fails if lenient metadata
// also accepts syntactically invalid JSON or another malformed field.
func TestSnapshotStillRejectsInvalidJSONAndMalformedFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		frame string
	}{
		{
			name:  "truncated JSON",
			frame: `{"state":{"controller":{"homeId":439041101},"nodes":[{"nodeId":23`,
		},
		{
			name:  "malformed node id",
			frame: `{"state":{"controller":{"homeId":439041101},"nodes":[{"nodeId":"23"}]}}`,
		},
		{
			name:  "malformed values",
			frame: `{"state":{"controller":{"homeId":439041101},"nodes":[{"nodeId":23,"values":3}]}}`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var snapshot networkSnapshot
			if err := json.Unmarshal([]byte(testCase.frame), &snapshot); err == nil {
				t.Fatalf("snapshot %q decoded, want a decode failure", testCase.frame)
			}
		})
	}
}

// This test protects A3 metadata validation, and fails if a Binary Switch pair is
// planned from metadata that is not a readable boolean current and writeable
// boolean target.
func TestBinarySwitchPairRequiresBooleanMetadata(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		values []valueState
	}{
		{
			name: "current_not_readable",
			values: []valueState{
				snapshotValueFixture(
					testCommandClassBinarySwitch, 0, valuePropertyCurrentValue,
					boolMetadata(false, false), "true",
				),
				snapshotValueFixture(
					testCommandClassBinarySwitch, 0, valuePropertyTargetValue,
					boolMetadata(false, true), "true",
				),
			},
		},
		{
			name: "target_not_writeable",
			values: []valueState{
				snapshotValueFixture(
					testCommandClassBinarySwitch, 0, valuePropertyCurrentValue,
					boolMetadata(true, false), "true",
				),
				snapshotValueFixture(
					testCommandClassBinarySwitch, 0, valuePropertyTargetValue,
					boolMetadata(true, false), "true",
				),
			},
		},
		{
			name: "current_number_metadata",
			values: []valueState{
				snapshotValueFixture(
					testCommandClassBinarySwitch, 0, valuePropertyCurrentValue,
					numberMetadata(true, false), "1",
				),
				snapshotValueFixture(
					testCommandClassBinarySwitch, 0, valuePropertyTargetValue,
					boolMetadata(false, true), "true",
				),
			},
		},
		{
			name: "target_boolean_type_missing",
			values: []valueState{
				snapshotValueFixture(
					testCommandClassBinarySwitch, 0, valuePropertyCurrentValue,
					boolMetadata(true, false), "true",
				),
				snapshotValueFixture(
					testCommandClassBinarySwitch, 0, valuePropertyTargetValue,
					valueMetadata{Writeable: true, Valid: true}, "true",
				),
			},
		},
		{
			name: "target_missing",
			values: []valueState{
				snapshotValueFixture(
					testCommandClassBinarySwitch, 0, valuePropertyCurrentValue,
					boolMetadata(true, false), "true",
				),
			},
		},
		{
			name: "pair_split_across_endpoints",
			values: []valueState{
				snapshotValueFixture(
					testCommandClassBinarySwitch, 0, valuePropertyCurrentValue,
					boolMetadata(true, false), "true",
				),
				snapshotValueFixture(
					testCommandClassBinarySwitch, 1, valuePropertyTargetValue,
					boolMetadata(false, true), "true",
				),
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			node := nodeFixture(fixtureSwitchNodeID, []endpointState{
				rootEndpointFixture(), {Index: 1},
			}, test.values)
			plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
			if got := requireOnlyRejection(t, plan, fixtureSwitchNodeID); got != rejectionNodeNoCapability {
				t.Fatalf("rejection code = %q, want %q", got, rejectionNodeNoCapability)
			}
		})
	}
}

// This test protects A3 native brightness support, and fails if a Multilevel
// Switch is planned without number metadata, readable state, writeable target,
// or bounds exactly 0..99.
func TestMultilevelSwitchPairRequiresNativeBounds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		current     valueMetadata
		target      valueMetadata
		targetValue string
	}{
		{
			name:        "scaled_to_percent",
			current:     boundedLevelMetadata(0, 100),
			target:      numberMetadata(false, true),
			targetValue: "50",
		},
		{
			name:        "missing_minimum",
			current:     valueMetadata{Type: metadataTypeNumber, Readable: true, Max: new(99.0), Valid: true},
			target:      numberMetadata(false, true),
			targetValue: "50",
		},
		{
			name:        "wrong_maximum",
			current:     boundedLevelMetadata(0, 98),
			target:      numberMetadata(false, true),
			targetValue: "50",
		},
		{
			name:        "current_string_metadata",
			current:     valueMetadata{Type: "string", Readable: true, Valid: true},
			target:      numberMetadata(false, true),
			targetValue: "50",
		},
		{
			name:        "current_not_readable",
			current:     levelMetadata(false),
			target:      numberMetadata(false, true),
			targetValue: "50",
		},
		{
			name:        "target_not_writeable",
			current:     levelMetadata(true),
			target:      numberMetadata(false, false),
			targetValue: "50",
		},
		{
			name:        "target_boolean_metadata",
			current:     levelMetadata(true),
			target:      boolMetadata(false, true),
			targetValue: "true",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			values := []valueState{
				snapshotValueFixture(
					testCommandClassMultilevelSwitch, 0, valuePropertyCurrentValue,
					test.current, "15",
				),
				snapshotValueFixture(
					testCommandClassMultilevelSwitch, 0, valuePropertyTargetValue,
					test.target, test.targetValue,
				),
			}
			node := nodeFixture(fixtureDimmerNodeID, []endpointState{rootEndpointFixture()}, values)
			plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
			if got := requireOnlyRejection(t, plan, fixtureDimmerNodeID); got != rejectionNodeNoCapability {
				t.Fatalf("rejection code = %q, want %q", got, rejectionNodeNoCapability)
			}
		})
	}
}

// This test protects A3 target-type validation, and fails if a target without a
// current value is rejected or a target without number metadata is accepted.
func TestMultilevelTargetTypeComesFromMetadata(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		target      valueMetadata
		targetValue string
		planned     bool
	}{
		{
			name:        "null_target_value",
			target:      numberMetadata(false, true),
			targetValue: "null",
			planned:     true,
		},
		{
			name:        "absent_target_value",
			target:      numberMetadata(false, true),
			targetValue: "",
			planned:     true,
		},
		{
			name:        "untyped_target",
			target:      valueMetadata{Writeable: true, Valid: true},
			targetValue: "50",
			planned:     false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			values := []valueState{
				snapshotValueFixture(
					testCommandClassMultilevelSwitch, 0, valuePropertyCurrentValue,
					levelMetadata(true), "15",
				),
				snapshotValueFixture(
					testCommandClassMultilevelSwitch, 0, valuePropertyTargetValue,
					test.target, test.targetValue,
				),
			}
			node := nodeFixture(fixtureDimmerNodeID, []endpointState{rootEndpointFixture()}, values)
			plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
			if test.planned {
				requireOnlyNode(t, plan)
				return
			}
			if got := requireOnlyRejection(t, plan, fixtureDimmerNodeID); got != rejectionNodeNoCapability {
				t.Fatalf("rejection code = %q, want %q", got, rejectionNodeNoCapability)
			}
		})
	}
}

// This test protects candidate filtering, and fails if a numeric property name,
// a propertyKey-bearing Value, or an unrelated property becomes a plan or
// isolates a sibling endpoint.
func TestPlanNetworkIgnoresNonCandidateValues(t *testing.T) {
	t.Parallel()
	noise := []valueState{
		numericPropertyValueFixture(112, 0, "30"),
		propertyKeyValueFixture(
			testCommandClassBinarySwitch, 0, valuePropertyCurrentValue, 2,
			boolMetadata(true, false), "true",
		),
		snapshotValueFixture(
			testCommandClassBinarySwitch, 0, "duration",
			valueMetadata{Type: "string", Readable: true, Valid: true}, `"1s"`,
		),
		snapshotValueFixture(
			commandClassBasic, 0, valuePropertyCurrentValue,
			boolMetadata(true, false), "true",
		),
		snapshotValueFixture(
			commandClassBasic, 0, valuePropertyTargetValue,
			boolMetadata(false, true), "true",
		),
	}
	for _, test := range []struct {
		name    string
		values  []valueState
		planned bool
	}{
		{name: "noise_only", values: noise, planned: false},
		{name: "noise_with_pair", values: slices.Concat(noise, binaryPairFixture(0)), planned: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			node := nodeFixture(fixtureSwitchNodeID, []endpointState{rootEndpointFixture()}, test.values)
			plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
			if !test.planned {
				if got := requireOnlyRejection(t, plan, fixtureSwitchNodeID); got != rejectionNodeNoCapability {
					t.Fatalf("rejection code = %q, want %q", got, rejectionNodeNoCapability)
				}
				return
			}
			requirePlanKeys(t, requireOnlyNode(t, plan), []string{"power"})
		})
	}
}

// This test protects Value ID endpoint validity, and fails if a negative
// endpoint index plans or registers an Entity whose Commands the client refuses
// to address, or if it discards a valid sibling endpoint.
func TestPlanNetworkIsolatesNegativeEndpointValues(t *testing.T) {
	t.Parallel()
	negativePair := func(endpoint int) []valueState {
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
	t.Run("negative_only", func(t *testing.T) {
		t.Parallel()
		node := nodeFixture(
			fixtureSwitchNodeID,
			[]endpointState{rootEndpointFixture(), {Index: -1}},
			negativePair(-1),
		)
		plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
		if got := requireOnlyRejection(t, plan, fixtureSwitchNodeID); got != rejectionNodeNoCapability {
			t.Fatalf("rejection code = %q, want %q", got, rejectionNodeNoCapability)
		}
	})
	t.Run("valid_sibling_remains", func(t *testing.T) {
		t.Parallel()
		values := slices.Concat(binaryPairFixture(0), negativePair(-1))
		node := nodeFixture(
			fixtureSwitchNodeID,
			[]endpointState{rootEndpointFixture(), {Index: -1}},
			values,
		)
		plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
		requirePlanKeys(t, requireOnlyNode(t, plan), []string{"power"})
	})
}

// This test protects structural endpoint isolation, and fails if a repeated
// endpoint index is planned. A repeated Value ID no longer isolates a whole
// endpoint, so its duplicate_value_id case only asserts that the duplicated slot
// stops planning; TestPlanNetworkDuplicateValueIsolatesOnlyAffectedCapability is
// the fault detector for sibling-capability preservation.
func TestPlanNetworkIsolatesAmbiguousEndpoints(t *testing.T) {
	t.Parallel()
	// duplicate_value_id confirms a duplicated Value ID is not planned against
	// its ambiguous slot. It cannot distinguish clearing that slot from isolating
	// the whole endpoint, because both drop the affected power Entity, so it does
	// not detect a regression to whole-endpoint isolation; the dedicated
	// sibling-capability test below is the oracle for that distinction.
	t.Run("duplicate_value_id", func(t *testing.T) {
		t.Parallel()
		duplicate := snapshotValueFixture(
			testCommandClassBinarySwitch, 1, valuePropertyCurrentValue,
			boolMetadata(true, false), "false",
		)
		values := slices.Concat(
			binaryPairFixture(0),
			binaryPairFixture(1),
			[]valueState{duplicate},
		)
		node := nodeFixture(fixtureSwitchNodeID, []endpointState{
			rootEndpointFixture(), {Index: 1},
		}, values)
		plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
		requirePlanKeys(t, requireOnlyNode(t, plan), []string{"power"})
	})
	t.Run("duplicate_endpoint_index", func(t *testing.T) {
		t.Parallel()
		values := slices.Concat(binaryPairFixture(0), binaryPairFixture(1))
		node := nodeFixture(fixtureSwitchNodeID, []endpointState{
			rootEndpointFixture(),
			{Index: 1, EndpointLabel: "Left"},
			{Index: 1, EndpointLabel: "Right"},
		}, values)
		plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
		requirePlanKeys(t, requireOnlyNode(t, plan), []string{"power"})
	})
}

// This test protects the adapter spec rule that a duplicate Value ID isolates
// only the affected endpoint capability, and fails if a repeated Value ID for one
// Command Class suppresses a valid sibling capability on the same endpoint.
func TestPlanNetworkDuplicateValueIsolatesOnlyAffectedCapability(t *testing.T) {
	t.Parallel()

	// A duplicate Binary Switch pair invalidates Binary power only. The endpoint's
	// valid Multilevel Switch pair still plans derived power and brightness.
	t.Run("duplicate_binary_keeps_multilevel", func(t *testing.T) {
		t.Parallel()
		values := slices.Concat(binaryPairFixture(0), binaryPairFixture(0), levelPairFixture(0))
		node := nodeFixture(fixtureDimmerNodeID, []endpointState{rootEndpointFixture()}, values)
		plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
		planned := requireOnlyNode(t, plan)
		requirePlanKeys(t, planned, []string{"power", "brightness"})
		power := requirePlan(t, planned, "power")
		if !power.PowerFromMultilevel {
			t.Fatal("power did not derive from the valid Multilevel Switch pair")
		}
		requireValueID(
			t, power.CurrentValueID,
			commandClassMultilevelSwitch, 0, valuePropertyCurrentValue,
		)
	})

	// A duplicate Multilevel Switch pair invalidates brightness and derived power
	// only. The endpoint's valid Binary Switch pair still owns power.
	t.Run("duplicate_multilevel_keeps_binary", func(t *testing.T) {
		t.Parallel()
		values := slices.Concat(binaryPairFixture(0), levelPairFixture(0), levelPairFixture(0))
		node := nodeFixture(fixtureDimmerNodeID, []endpointState{rootEndpointFixture()}, values)
		plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
		planned := requireOnlyNode(t, plan)
		requirePlanKeys(t, planned, []string{"power"})
		power := requirePlan(t, planned, "power")
		if power.PowerFromMultilevel {
			t.Fatal("power derived from an invalidated Multilevel Switch pair")
		}
		requireValueID(
			t, power.CurrentValueID,
			commandClassBinarySwitch, 0, valuePropertyCurrentValue,
		)
	})
}

// This test protects per-capability isolation for an explicit JSON null
// property Value, and fails if one such Value makes the enclosing snapshot or
// node unplannable, or suppresses a valid sibling capability or endpoint.
func TestPlanNetworkIsolatesNullPropertyValue(t *testing.T) {
	t.Parallel()
	// The null-property Value shares its endpoint and Command Class with a valid
	// Binary Switch pair, and a second endpoint carries a valid pair of its own.
	frame := `{"state":{"controller":{"homeId":439041101},"nodes":[{"nodeId":23,` +
		`"ready":true,"status":4,"interviewStage":"Complete","isListening":true,` +
		`"name":"Null Property Switch","endpoints":[{"index":0},{"index":1}],` +
		`"values":[` +
		`{"commandClass":37,"endpoint":0,"property":null,` +
		`"metadata":{"type":"boolean","readable":true},"value":true},` +
		`{"commandClass":37,"endpoint":0,"property":"currentValue",` +
		`"metadata":{"type":"boolean","readable":true},"value":true},` +
		`{"commandClass":37,"endpoint":0,"property":"targetValue",` +
		`"metadata":{"type":"boolean","writeable":true},"value":true},` +
		`{"commandClass":37,"endpoint":1,"property":"currentValue",` +
		`"metadata":{"type":"boolean","readable":true},"value":false},` +
		`{"commandClass":37,"endpoint":1,"property":"targetValue",` +
		`"metadata":{"type":"boolean","writeable":true},"value":false}` +
		`]}]}}`
	var snapshot networkSnapshot
	if err := json.Unmarshal([]byte(frame), &snapshot); err != nil {
		t.Fatalf("snapshot with a null property did not decode: %v", err)
	}
	if !snapshot.State.Nodes[0].Values[0].Property.Invalid {
		t.Fatalf("null property = %#v, want an invalid property", snapshot.State.Nodes[0].Values[0].Property)
	}
	planned := requireOnlyNode(t, planNetwork(testHomeID, snapshot))
	requirePlanKeys(t, planned, []string{"power", "power-ep1"})
	requireValueID(
		t, requirePlan(t, planned, "power").CurrentValueID,
		commandClassBinarySwitch, 0, valuePropertyCurrentValue,
	)
	requireValueID(
		t, requirePlan(t, planned, "power-ep1").CurrentValueID,
		commandClassBinarySwitch, 1, valuePropertyCurrentValue,
	)
}

// This test protects A3 ordering, and fails if endpoints are planned out of
// ascending order or brightness precedes power on one endpoint.
func TestPlanNetworkOrdersEndpointsAscendingWithPowerFirst(t *testing.T) {
	t.Parallel()
	values := slices.Concat(
		levelPairFixture(3),
		binaryPairFixture(1),
		binaryPairFixture(0),
		levelPairFixture(0),
	)
	node := nodeFixture(fixtureDimmerNodeID, []endpointState{
		rootEndpointFixture(),
		{Index: 1},
		{Index: 3},
	}, values)
	plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
	requirePlanKeys(t, requireOnlyNode(t, plan), []string{
		"power", "brightness", "power-ep1", "power-ep3", "brightness-ep3",
	})
}

// This test protects Hearth's Entity bound, and fails if 64 planned Entities are
// rejected or 65 are split across Devices.
func TestPlanNetworkEnforcesEntityLimit(t *testing.T) {
	t.Parallel()
	boundedNode := func(endpoints int) nodeState {
		values := make([]valueState, 0, endpoints*4)
		state := make([]endpointState, 0, endpoints)
		for endpoint := range endpoints {
			state = append(state, endpointState{Index: endpoint})
			values = append(values, binaryPairFixture(endpoint)...)
			values = append(values, levelPairFixture(endpoint)...)
		}
		return nodeFixture(fixtureDimmerNodeID, state, values)
	}

	atLimit := planNetwork(testHomeID, snapshotFixture(testHomeID, boundedNode(fixtureEndpointCountLimit)))
	planned := requireOnlyNode(t, atLimit)
	if len(planned.Registration.Entities) != maximumPlannedEntitiesPerNode {
		t.Fatalf("registered Entities = %d, want %d",
			len(planned.Registration.Entities), maximumPlannedEntitiesPerNode)
	}

	overLimit := planNetwork(
		testHomeID,
		snapshotFixture(testHomeID, boundedNode(fixtureEndpointCountOverLimit)),
	)
	if got := requireOnlyRejection(t, overLimit, fixtureDimmerNodeID); got != rejectionNodeTooManyEntities {
		t.Fatalf("rejection code = %q, want %q", got, rejectionNodeTooManyEntities)
	}
}

// This test protects descriptor name bounds, and fails if a long Device name is
// truncated or accepted.
func TestPlanNetworkRejectsOverlongDeviceName(t *testing.T) {
	t.Parallel()
	atBound := nodeFixture(
		fixtureSwitchNodeID,
		[]endpointState{rootEndpointFixture()},
		binaryPairFixture(0),
	)
	atBound.Name = strings.Repeat("a", fixtureNameAtBound)
	plan := planNetwork(testHomeID, snapshotFixture(testHomeID, atBound))
	if name := requireOnlyNode(t, plan).Registration.Device.Name; name != atBound.Name {
		t.Fatalf("Device name length = %d, want %d", len(name), fixtureNameAtBound)
	}

	overBound := atBound
	overBound.Name = strings.Repeat("a", fixtureNameOverBound)
	overPlan := planNetwork(testHomeID, snapshotFixture(testHomeID, overBound))
	if got := requireOnlyRejection(t, overPlan, fixtureSwitchNodeID); got != rejectionNodeInvalidDescriptor {
		t.Fatalf("rejection code = %q, want %q", got, rejectionNodeInvalidDescriptor)
	}
}

// This test protects deterministic Device and Entity display names, and fails if
// a fallback or an endpoint prefix drifts.
func TestPlanNetworkFallsBackToDeterministicNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		nodeName   string
		nodeLabel  string
		endpoint   string
		wantDevice string
		wantEntity string
	}{
		{
			name: "node_name", nodeName: "  Hallway Dimmer  ", nodeLabel: "Dimmer",
			endpoint: "Left", wantDevice: "Hallway Dimmer", wantEntity: "Left Power",
		},
		{
			name: "node_label", nodeName: "   ", nodeLabel: "  Dimmer  ",
			endpoint: "Left", wantDevice: "Dimmer", wantEntity: "Left Power",
		},
		{
			name: "node_id", nodeName: "", nodeLabel: "",
			endpoint: "Left", wantDevice: "Z-Wave Node 23", wantEntity: "Left Power",
		},
		{
			name: "numeric_label", nodeName: "Dimmer", nodeLabel: "",
			endpoint: "3", wantDevice: "Dimmer", wantEntity: "Endpoint 1 Power",
		},
		{
			name: "empty_label", nodeName: "Dimmer", nodeLabel: "",
			endpoint: "", wantDevice: "Dimmer", wantEntity: "Endpoint 1 Power",
		},
		{
			name: "overlong_label", nodeName: "Dimmer", nodeLabel: "",
			endpoint: strings.Repeat("b", fixtureNameAtBound), wantDevice: "Dimmer",
			wantEntity: "Endpoint 1 Power",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			node := nodeFixture(fixtureDimmerNodeID, []endpointState{
				rootEndpointFixture(),
				{Index: 1, EndpointLabel: test.endpoint},
			}, slices.Concat(levelPairFixture(0), levelPairFixture(1)))
			node.Name = test.nodeName
			node.Label = test.nodeLabel
			plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
			planned := requireOnlyNode(t, plan)
			if planned.Registration.Device.Name != test.wantDevice {
				t.Fatalf("Device name = %q, want %q", planned.Registration.Device.Name, test.wantDevice)
			}
			if name := requirePlan(t, planned, "power-ep1").Name; name != test.wantEntity {
				t.Fatalf("endpoint Entity name = %q, want %q", name, test.wantEntity)
			}
			if name := requirePlan(t, planned, "power").Name; name != "Power" {
				t.Fatalf("root Entity name = %q, want Power", name)
			}
		})
	}
}

// This test protects snapshot node isolation, and fails if a repeated node ID
// plans one node or drops an unrelated sibling.
func TestPlanNetworkIsolatesDuplicateNodeIDs(t *testing.T) {
	t.Parallel()
	first := nodeFixture(fixtureSwitchNodeID, []endpointState{rootEndpointFixture()}, binaryPairFixture(0))
	duplicate := nodeFixture(fixtureSwitchNodeID, []endpointState{rootEndpointFixture()}, binaryPairFixture(0))
	sibling := nodeFixture(fixtureDimmerNodeID, []endpointState{rootEndpointFixture()}, levelPairFixture(0))
	plan := planNetwork(testHomeID, snapshotFixture(testHomeID, first, duplicate, sibling))
	if len(plan.Nodes) != 1 || plan.Nodes[0].NodeID != fixtureDimmerNodeID {
		t.Fatalf("planned nodes = %#v, want only node %d", plan.Nodes, fixtureDimmerNodeID)
	}
	if len(plan.Rejections) != 2 {
		t.Fatalf("rejections = %#v, want two duplicate node rejections", plan.Rejections)
	}
	for _, rejection := range plan.Rejections {
		if rejection.Code != rejectionNodeDuplicateNodeID {
			t.Fatalf("rejection code = %q, want %q", rejection.Code, rejectionNodeDuplicateNodeID)
		}
	}
}

// This test protects A5 network identity validation, and fails if one Adapter ID
// silently reuses node-number-shaped identity on a different network.
func TestVerifyNetworkIdentityComparesOwnedMappings(t *testing.T) {
	t.Parallel()
	mapping := func(bindingKey string) adapter.OwnedMapping {
		return adapter.OwnedMapping{BindingKey: bindingKey, EntityKey: "power", EntityID: "ent_power"}
	}
	for _, test := range []struct {
		name      string
		mappings  []adapter.OwnedMapping
		connected uint32
		wantOwned []string
		mismatch  bool
	}{
		{name: "no_mappings", connected: testHomeID},
		{
			name:      "matching_network",
			mappings:  []adapter.OwnedMapping{mapping("zwave-1a2b3c4d-node-23")},
			connected: testHomeID,
		},
		{
			name: "different_network",
			mappings: []adapter.OwnedMapping{
				mapping("zwave-1a2b3c4d-node-23"),
				mapping("zwave-1a2b3c4d-node-24"),
			},
			connected: 0x2b2b2b2b,
			wantOwned: []string{"1a2b3c4d"},
			mismatch:  true,
		},
		{
			name: "conflicting_identities",
			mappings: []adapter.OwnedMapping{
				mapping("zwave-1a2b3c4d-node-23"),
				mapping("zwave-2b2b2b2b-node-23"),
			},
			connected: 0x1a2b3c4d,
			wantOwned: []string{"1a2b3c4d", "2b2b2b2b"},
			mismatch:  true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := verifyNetworkIdentity(test.mappings, test.connected)
			if !test.mismatch {
				if err != nil {
					t.Fatalf("verifyNetworkIdentity = %v, want nil", err)
				}
				return
			}
			var mismatch *networkIdentityMismatchError
			if !errors.As(err, &mismatch) {
				t.Fatalf("verifyNetworkIdentity = %v, want a network identity mismatch", err)
			}
			if !slices.Equal(mismatch.OwnedHomeIDs, test.wantOwned) {
				t.Fatalf("owned identities = %v, want %v", mismatch.OwnedHomeIDs, test.wantOwned)
			}
			if mismatch.ConnectedHomeID != normalizedHomeID(test.connected) {
				t.Fatalf("connected identity = %q", mismatch.ConnectedHomeID)
			}
			if !strings.Contains(err.Error(), "network identity") {
				t.Fatalf("mismatch error %q is not operator safe", err)
			}
		})
	}
}

// This test protects the owned-mapping identity parser, and fails if a foreign
// Binding key is accepted as this Adapter's own.
func TestVerifyNetworkIdentityRejectsForeignBindingKeys(t *testing.T) {
	t.Parallel()
	for _, bindingKey := range []string{
		"z2m-00124b0024abcdef",
		"zwavejs-1a2b3c4d-node-23",
		"zwave-1A2B3C4D-node-23",
		"zwave-1a2b3c4d-node",
		"zwave-1a2b3c4-node-23",
		"zwave-1a2b3c4dnode-23",
		"zwave-1a2b3c4d-node-abc",
	} {
		err := verifyNetworkIdentity(
			[]adapter.OwnedMapping{{BindingKey: bindingKey}},
			testHomeID,
		)
		var foreign *ownedMappingIdentityError
		if !errors.As(err, &foreign) {
			t.Fatalf("verifyNetworkIdentity(%q) = %v, want a foreign Binding error", bindingKey, err)
		}
		if foreign.BindingKey != bindingKey {
			t.Fatalf("foreign Binding key = %q, want %q", foreign.BindingKey, bindingKey)
		}
		if !strings.Contains(err.Error(), bindingKey) {
			t.Fatalf("foreign Binding error %q omits the Binding key", err)
		}
	}
}

// This test protects canonical binding, and fails if a registration that answers
// the wrong Binding, omits a plan, or repeats an Entity ID is bound.
func TestBindEntityRoutesBindsCanonicalEntityIDs(t *testing.T) {
	t.Parallel()
	session := &recordingSession{}
	plan := planNetwork(testHomeID, snapshotFixture(testHomeID, nodeFixture(
		fixtureDimmerNodeID,
		[]endpointState{rootEndpointFixture()},
		levelPairFixture(0),
	)))
	node := requireOnlyNode(t, plan)
	binding, err := session.Register(t.Context(), node.Registration)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := bindEntityRoutes(binding, node)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("bound routes = %d, want 2", len(routes))
	}
	if routes[0].Plan.Key != "power" || routes[0].EntityID != canonicalEntityID("power") {
		t.Fatalf("first route = %#v", routes[0])
	}
	if routes[1].Plan.Key != "brightness" || routes[1].EntityID != canonicalEntityID("brightness") {
		t.Fatalf("second route = %#v", routes[1])
	}

	for _, test := range []struct {
		name string
		edit func(*adapter.Binding)
	}{
		{name: "wrong_binding", edit: func(binding *adapter.Binding) { binding.BindingKey = "zwave-other" }},
		{name: "missing_entity", edit: func(binding *adapter.Binding) {
			binding.Entities = binding.Entities[:1]
		}},
		{name: "renamed_entity", edit: func(binding *adapter.Binding) { binding.Entities[0].Key = "other" }},
		{name: "repeated_entity_id", edit: func(binding *adapter.Binding) {
			binding.Entities[1].EntityID = binding.Entities[0].EntityID
		}},
		{name: "empty_entity_id", edit: func(binding *adapter.Binding) { binding.Entities[0].EntityID = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			edited := binding
			edited.Entities = slices.Clone(binding.Entities)
			test.edit(&edited)
			if _, bindErr := bindEntityRoutes(edited, node); bindErr == nil {
				t.Fatal("bindEntityRoutes accepted an unusable registration")
			}
		})
	}
}

// This test protects route installation, and fails if one Multilevel Switch Value
// does not resolve to power before brightness, or if a duplicate route installs.
func TestNewRouteSnapshotIndexesValuesInPlanOrder(t *testing.T) {
	t.Parallel()
	session := &recordingSession{}
	plan := planNetwork(testHomeID, snapshotFixture(testHomeID, nodeFixture(
		fixtureDimmerNodeID,
		[]endpointState{rootEndpointFixture()},
		levelPairFixture(0),
	)))
	node := requireOnlyNode(t, plan)
	binding, err := session.Register(t.Context(), node.Registration)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := bindEntityRoutes(binding, node)
	if err != nil {
		t.Fatal(err)
	}
	snapshotRoutes, err := newRouteSnapshot(7, 3, routes)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotRoutes.Generation != 7 || snapshotRoutes.Revision != 3 {
		t.Fatalf("route snapshot = %#v", snapshotRoutes)
	}
	shared := snapshotRoutes.routesForValue(fixtureDimmerNodeID, testValueID(
		testCommandClassMultilevelSwitch, 0, valuePropertyCurrentValue,
	))
	if len(shared) != 2 {
		t.Fatalf("routes for the multilevel current Value = %d, want 2", len(shared))
	}
	if shared[0].Plan.Kind != entityKindPower || shared[1].Plan.Kind != entityKindBrightness {
		t.Fatalf("shared routes = %#v, want power then brightness", shared)
	}
	if targetRoutes := snapshotRoutes.routesForValue(fixtureDimmerNodeID, testValueID(
		testCommandClassMultilevelSwitch, 0, valuePropertyTargetValue,
	)); len(targetRoutes) != 0 {
		t.Fatalf("target Value resolved to %d routes, want none", len(targetRoutes))
	}

	if _, err = newRouteSnapshot(1, 1, []entityRoute{routes[0], routes[0]}); err == nil {
		t.Fatal("newRouteSnapshot accepted a duplicate canonical Entity ID")
	}
	if _, err = newRouteSnapshot(1, 1, []entityRoute{{Plan: routes[0].Plan}}); err == nil {
		t.Fatal("newRouteSnapshot accepted a route without an Entity ID")
	}
	empty, err := newRouteSnapshot(1, 1, nil)
	if err != nil {
		t.Fatalf("empty route snapshot = %v, want a healthy empty network", err)
	}
	if len(empty.ByEntityID) != 0 {
		t.Fatalf("empty route snapshot = %#v", empty)
	}
}

// This test protects the node-scoped route index, and fails if two planned nodes
// that report an identical Value ID resolve to each other's routes instead of
// their own.
func TestNewRouteSnapshotScopesValuesToTheirNode(t *testing.T) {
	t.Parallel()
	const garageNodeID = testNodeID + 1
	plan := planNetwork(testHomeID, snapshotFixture(
		testHomeID,
		switchNodeFixture(testNodeID, "Kitchen Switch"),
		switchNodeFixture(garageNodeID, "Garage Switch"),
	))
	if len(plan.Nodes) != 2 {
		t.Fatalf("planned nodes = %d, want 2", len(plan.Nodes))
	}
	session := newRuntimeSession(&runtimeRecorder{})
	var routes []entityRoute
	for _, node := range plan.Nodes {
		binding, err := session.Register(t.Context(), node.Registration)
		if err != nil {
			t.Fatal(err)
		}
		bound, err := bindEntityRoutes(binding, node)
		if err != nil {
			t.Fatal(err)
		}
		routes = append(routes, bound...)
	}
	snapshotRoutes, err := newRouteSnapshot(1, 1, routes)
	if err != nil {
		t.Fatal(err)
	}
	id := testValueID(testCommandClassBinarySwitch, 0, valuePropertyCurrentValue)
	first := snapshotRoutes.routesForValue(testNodeID, id)
	if len(first) != 1 || first[0].Plan.NodeID != testNodeID {
		t.Fatalf("routes for node %d = %#v, want only that node's route", testNodeID, first)
	}
	second := snapshotRoutes.routesForValue(garageNodeID, id)
	if len(second) != 1 || second[0].Plan.NodeID != garageNodeID {
		t.Fatalf("routes for node %d = %#v, want only that node's route", garageNodeID, second)
	}
}

// This test protects the registered planning boundary end to end, and fails if a
// Session registration cannot be bound and translated into typed Observations.
func TestPlannedFixtureNetworkTranslatesRegisteredObservations(t *testing.T) {
	t.Parallel()
	version, snapshot := loadTranscriptSnapshot(t, "switch-session.jsonl")
	plan := planNetwork(*version.HomeID, snapshot)
	session := &recordingSession{}
	receivedAt := time.Date(2026, 3, 10, 14, 30, 0, 0, time.UTC)
	observations := make([]entityObservation, 0, len(plan.Nodes))
	for _, node := range plan.Nodes {
		binding, err := session.Register(t.Context(), node.Registration)
		if err != nil {
			t.Fatal(err)
		}
		routes, err := bindEntityRoutes(binding, node)
		if err != nil {
			t.Fatal(err)
		}
		nodeObservations, issues := translateNodeValues(routes, receivedAt, nodeStateValues(t,
			snapshot, node.NodeID))
		if len(issues) != 0 {
			t.Fatalf("translation issues = %#v, want none", issues)
		}
		observations = append(observations, nodeObservations...)
	}
	if len(observations) != 3 {
		t.Fatalf("observations = %d, want 3", len(observations))
	}
	if len(session.registeredBindings()) != len(plan.Nodes) {
		t.Fatalf("session recordings = %d, want %d registrations",
			len(session.registeredBindings()), len(plan.Nodes))
	}
	wantEntityIDs := []string{"ent_power", "ent_power-ep1", "ent_power-ep2"}
	wantValues := []string{"true", "false", "true"}
	for index, observation := range observations {
		if observation.EntityID != wantEntityIDs[index] {
			t.Fatalf("observation %d entity = %q, want %q",
				index, observation.EntityID, wantEntityIDs[index])
		}
		if string(observation.Observation.Value) != wantValues[index] {
			t.Fatalf("observation %d value = %s, want %s",
				index, observation.Observation.Value, wantValues[index])
		}
		if observation.Observation.AdapterReceivedAt != "2026-03-10T14:30:00Z" {
			t.Fatalf("observation %d receive time = %q",
				index, observation.Observation.AdapterReceivedAt)
		}
		if observation.Observation.SourceUpdatedAt != nil {
			t.Fatalf("observation %d source time = %v, want nil",
				index, observation.Observation.SourceUpdatedAt)
		}
	}
}

// This test protects the pre-registration plan invariants, and fails if a plan
// whose descriptor disagrees with its identity, whose translator is missing, or
// whose key is duplicated could be registered.
func TestValidateEntityPlansRejectsDivergentPlans(t *testing.T) {
	t.Parallel()
	validPlans := func(t *testing.T) []entityPlan {
		t.Helper()
		node := nodeFixture(fixtureDimmerNodeID, []endpointState{rootEndpointFixture()}, levelPairFixture(0))
		plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
		plans := requireOnlyNode(t, plan).Plans
		if err := validateEntityPlans(plans); err != nil {
			t.Fatalf("validateEntityPlans(planned) = %v, want nil", err)
		}
		return plans
	}
	for _, test := range []struct {
		name string
		edit func(plans *[]entityPlan)
	}{
		{name: "unset_kind", edit: func(plans *[]entityPlan) { (*plans)[0].Kind = 0 }},
		{name: "invalid_key", edit: func(plans *[]entityPlan) { (*plans)[0].Key = "Power" }},
		{name: "overlong_key", edit: func(plans *[]entityPlan) {
			(*plans)[0].Key = strings.Repeat("a", maximumEntityKeyBytes+1)
		}},
		{name: "empty_name", edit: func(plans *[]entityPlan) { (*plans)[0].Name = "" }},
		{name: "overlong_name", edit: func(plans *[]entityPlan) {
			(*plans)[0].Name = strings.Repeat("a", fixtureNameOverBound)
		}},
		{name: "empty_external_id", edit: func(plans *[]entityPlan) { (*plans)[0].ExternalID = "" }},
		{name: "descriptor_key_diverges", edit: func(plans *[]entityPlan) {
			(*plans)[0].Descriptor.Key = "other"
		}},
		{name: "descriptor_external_id_diverges", edit: func(plans *[]entityPlan) {
			(*plans)[0].Descriptor.ExternalID = "other"
		}},
		{name: "descriptor_name_diverges", edit: func(plans *[]entityPlan) {
			(*plans)[0].Descriptor.Name = "Other"
		}},
		{name: "descriptor_type_missing", edit: func(plans *[]entityPlan) {
			(*plans)[0].Descriptor.Type = ""
		}},
		{name: "descriptor_support_missing", edit: func(plans *[]entityPlan) {
			(*plans)[0].Descriptor.Support = nil
		}},
		{name: "same_read_and_write_value", edit: func(plans *[]entityPlan) {
			(*plans)[0].TargetValueID = (*plans)[0].CurrentValueID
		}},
		{name: "missing_state_decoder", edit: func(plans *[]entityPlan) { (*plans)[0].DecodeState = nil }},
		{name: "missing_set_encoder", edit: func(plans *[]entityPlan) { (*plans)[0].EncodeSet = nil }},
		{name: "missing_matcher", edit: func(plans *[]entityPlan) { (*plans)[0].Matches = nil }},
		{name: "duplicate_key", edit: func(plans *[]entityPlan) {
			duplicate := (*plans)[0]
			duplicate.ExternalID += "-copy"
			duplicate.Descriptor.ExternalID = duplicate.ExternalID
			*plans = append(*plans, duplicate)
		}},
		{name: "duplicate_external_id", edit: func(plans *[]entityPlan) {
			duplicate := (*plans)[0]
			duplicate.Key = "brightness"
			duplicate.Descriptor.Key = duplicate.Key
			*plans = append(*plans, duplicate)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plans := validPlans(t)
			test.edit(&plans)
			if err := validateEntityPlans(plans); err == nil {
				t.Fatal("validateEntityPlans accepted a divergent plan")
			}
		})
	}
}

// This test protects Hearth's subject-safe Entity key rule, and fails if a key
// with an uppercase byte, a leading separator, or an out-of-bounds length is
// accepted.
func TestValidEntityKeyMatchesHearthSlugRule(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		key   string
		valid bool
	}{
		{key: "power", valid: true},
		{key: "brightness-ep12", valid: true},
		{key: "power_ep1", valid: true},
		{key: strings.Repeat("a", maximumEntityKeyBytes), valid: true},
		{key: ""},
		{key: "Power"},
		{key: "-power"},
		{key: "power.ep1"},
		{key: "power ep1"},
		{key: strings.Repeat("a", maximumEntityKeyBytes+1)},
	} {
		if got := validEntityKey(test.key); got != test.valid {
			t.Fatalf("validEntityKey(%q) = %t, want %t", test.key, got, test.valid)
		}
	}
}

// This test protects the single-node reconciliation seam, and fails if a
// refreshed node plans differently than the same node inside a snapshot, or if
// an ineligible refresh silently keeps planning.
func TestPlanNodeStateAgreesWithSnapshotPlanning(t *testing.T) {
	t.Parallel()
	node := nodeFixture(fixtureDimmerNodeID, []endpointState{rootEndpointFixture()}, levelPairFixture(0))
	refreshed, rejection := planNodeState(testHomeID, node)
	if rejection != nil {
		t.Fatalf("planNodeState rejected an eligible node: %#v", rejection)
	}
	fromSnapshot := requireOnlyNode(t, planNetwork(testHomeID, snapshotFixture(testHomeID, node)))
	if refreshed.Registration.BindingKey != fromSnapshot.Registration.BindingKey {
		t.Fatalf("refreshed Binding key = %q, want %q",
			refreshed.Registration.BindingKey, fromSnapshot.Registration.BindingKey)
	}
	requirePlanKeys(t, refreshed, []string{"power", "brightness"})
	requirePlanKeys(t, fromSnapshot, []string{"power", "brightness"})

	sleeping := node
	sleeping.IsListening = false
	slept, code := planNodeState(testHomeID, sleeping)
	if code == nil || code.Code != rejectionNodeSleeping {
		t.Fatalf("planNodeState(sleeping) rejection = %#v, want %q", code, rejectionNodeSleeping)
	}
	if len(slept.Plans) != 0 || slept.Registration.BindingKey != "" {
		t.Fatalf("a rejected refresh still produced a plan: %#v", slept)
	}
}

// nodeStateValues returns the snapshot Values of one node of a planned snapshot.
func nodeStateValues(t *testing.T, snapshot networkSnapshot, nodeID int) []valueState {
	t.Helper()
	for _, node := range snapshot.State.Nodes {
		if node.NodeID == nodeID {
			return node.Values
		}
	}
	t.Fatalf("snapshot has no node %d", nodeID)
	return nil
}
