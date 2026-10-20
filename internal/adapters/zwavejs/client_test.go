package zwavejs //nolint:testpackage // Tests exercise the private schema-29 protocol client.

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	// testHomeID is the sanitized Home ID of the scripted network. It is not a real household Home ID.
	testHomeID = 0x1a2b3c4d
	testNodeID = 23
	// testCommandClassBinarySwitch and testCommandClassMultilevelSwitch are the Z-Wave Command Class numbers the
	// Adapter plans.
	testCommandClassBinarySwitch     = 37
	testCommandClassMultilevelSwitch = 38
	// testTimeout bounds client calls in scripted tests.
	testTimeout = 10 * time.Second
	// testQuietPeriod is how long a test waits to prove nothing was written.
	testQuietPeriod = 250 * time.Millisecond
	// testRequestDeadline is the deadline of a scripted request that never answers.
	testRequestDeadline = 150 * time.Millisecond
	// testBlockedWriteDeadline bounds a scripted blocked write. It is long enough that the request reaches its frame
	// write before the deadline fires.
	testBlockedWriteDeadline = time.Second
	// testBlockingPayloadBytes is larger than the kernel socket buffers a loopback peer can hold, so a request frame
	// that carries it blocks once the scripted server stops reading.
	testBlockingPayloadBytes = 8 << 20
	// scriptedRequestBuffer bounds captured requests.
	scriptedRequestBuffer = 64
)

func testValueID(commandClass int, endpoint int, property string) valueID {
	return valueID{
		CommandClass: commandClass,
		Endpoint:     endpoint,
		Property:     valueProperty{Name: property},
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	t.Cleanup(cancel)
	return ctx
}

// requireErrorSameType compares concrete types. Pass a zero value for want.
func requireErrorSameType(t *testing.T, err error, want error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: no error, want %T", what, want)
	}
	if reflect.TypeOf(err) != reflect.TypeOf(want) {
		t.Fatalf("%s: error = %T (%v), want %T", what, err, err, want)
	}
}

// This test protects the message-ID parser directly and fails if it accepts zero or any non-canonical decimal text.
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

// A buffered answer must win when the connection closes immediately after routing it, even if the terminating select
// chooses the closed channel first.
func TestAwaitResultPrefersAnswerAfterTermination(t *testing.T) {
	t.Parallel()
	connection := &websocketConnection{done: make(chan struct{})}
	close(connection.done)
	for range 1000 {
		answer := make(chan resultEnvelope, 1)
		answer <- resultEnvelope{Success: true}
		result, arrived := connection.awaitResult(context.Background(), answer)
		if !arrived || !result.Success {
			t.Fatal("discarded the already-delivered answer")
		}
	}
}

// This test protects the root-endpoint wire rule and fails if a root Value ID is written with an explicit endpoint or
// an endpoint Value ID loses its index.
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

// Value Events must preserve their exact identity and payload, while unusable Command Class or property forms
// must be refused at the argument decoder rather than accidentally routed to another Entity.
func TestDecodeValueEventArgsPreservesIdentityAndRejectsMalformedForms(t *testing.T) {
	t.Parallel()
	for _, payload := range []string{
		`{"commandClass":37,"endpoint":2,"property":"currentValue","newValue":true}`,
		`{"commandClass":37,"endpoint":2,"property":"currentValue","propertyKey":null,"newValue":true}`,
	} {
		args, ok := decodeValueEventArgs(json.RawMessage(payload))
		if !ok || args.CommandClass != 37 || args.Endpoint != 2 || args.Property.Name != "currentValue" ||
			args.Property.Numeric || args.Property.Invalid || string(args.NewValue) != "true" {
			t.Fatalf("decodeValueEventArgs(%s) = %#v, %t; want CC 37 endpoint 2 currentValue true", payload, args, ok)
		}
	}
	for _, testCase := range []struct {
		name    string
		payload string
	}{
		{"missing Command Class", `{"property":"currentValue","newValue":true}`},
		{"zero Command Class", `{"commandClass":0,"property":"currentValue","newValue":true}`},
		{"negative Command Class", `{"commandClass":-37,"property":"currentValue","newValue":true}`},
		{"string Command Class", `{"commandClass":"37","property":"currentValue","newValue":true}`},
		{"fractional Command Class", `{"commandClass":37.5,"property":"currentValue","newValue":true}`},
		{"null property", `{"commandClass":37,"property":null,"newValue":true}`},
		{"empty property", `{"commandClass":37,"property":"","newValue":true}`},
		{"boolean property", `{"commandClass":37,"property":true,"newValue":true}`},
		{"object property", `{"commandClass":37,"property":{},"newValue":true}`},
		{"absent arguments", ``},
		{"null arguments", `null`},
		{"array arguments", `[]`},
		{"malformed JSON", `{"commandClass":37`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if args, ok := decodeValueEventArgs(json.RawMessage(testCase.payload)); ok {
				t.Fatalf("accepted malformed value arguments: %#v", args)
			}
		})
	}
}

// This test protects snapshot decoding against the numeric property names real Z-Wave JS networks report and fails if
// one such Value makes the whole snapshot undecodable. It also protects the propertyKey marker that isolates ambiguous
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

// This test protects value property decoding, and fails if an explicit JSON null property is classified as numeric, if
// a numeric property name stops being recorded for per-capability isolation, or if either shape makes the enclosing
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
	// A null property is invalid, not fatal: it is confined to its own Value instead of failing the enclosing snapshot
	// or its siblings.
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

// This test protects the local planned-Value-ID guard, and fails if a Value ID this Adapter never plans (a numeric
// property name, a null/empty property, or a propertyKey) is written upstream.
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
	// A caller-built null property, whether it records an explicit null or is an unfilled zero value, must still be
	// refused.
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
	// An explicit JSON null key counts as absent, exactly as it does during planning and Event decoding.
	nullKeyed := planned
	nullKeyed.PropertyKey = json.RawMessage("null")
	if err := validatePlannedValueID(nullKeyed); err != nil {
		t.Fatalf("an explicit null key was rejected: %v", err)
	}
}

// This test protects rejection diagnostics, and fails if a schema-29 Z-Wave error reports the generic message instead
// of its specific zwaveErrorMessage, or if a rejection without a Z-Wave detail loses its generic message.
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

// This test protects the abandonable write bound and fails if the poll write context is cancelled with its caller —
// which would let a Command deadline close a healthy generation — or is left unbounded, which would let a non-reading
// peer block a poll write forever.
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
