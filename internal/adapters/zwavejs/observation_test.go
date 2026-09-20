package zwavejs //nolint:testpackage // Tests exercise private planning, translation, and identity.

import (
	"testing"
	"time"
)

// fixtureReceivedAtText is the Adapter-owned receive time of every translation
// test. It is not midnight UTC so a local-time bug cannot pass.
const fixtureReceivedAtText = "2026-03-10T14:30:00Z"

// fixtureReceivedAt is the parsed form of fixtureReceivedAtText.
func fixtureReceivedAt() time.Time {
	return time.Date(2026, 3, 10, 14, 30, 0, 0, time.UTC)
}

// fixtureBinaryPowerPlan plans one Binary Switch power Entity in isolation.
func fixtureBinaryPowerPlan(t *testing.T, values []valueState) entityPlan {
	t.Helper()
	node := nodeFixture(fixtureSwitchNodeID, []endpointState{rootEndpointFixture()}, values)
	plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
	return requirePlan(t, requireOnlyNode(t, plan), "power")
}

// fixtureBrightnessPlan plans one Multilevel Switch brightness Entity in
// isolation.
func fixtureBrightnessPlan(t *testing.T, values []valueState) entityPlan {
	t.Helper()
	node := nodeFixture(fixtureDimmerNodeID, []endpointState{rootEndpointFixture()}, values)
	plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
	return requirePlan(t, requireOnlyNode(t, plan), "brightness")
}

// This test protects A4 Binary Switch State decoding, and fails if a non-boolean
// JSON value is accepted as power.
func TestBinaryPowerStateAcceptsOnlyJSONBooleans(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value string
		want  bool
		ok    bool
	}{
		{value: "true", want: true, ok: true},
		{value: " false ", ok: true},
		{value: "TRUE"},
		{value: `"true"`},
		{value: "1"},
		{value: "0"},
		{value: "null"},
		{value: "{}"},
		{value: ""},
	} {
		got, err := decodeBinaryPowerState([]byte(test.value))
		if test.ok && err != nil {
			t.Fatalf("decodeBinaryPowerState(%q) = %v, want %t", test.value, err, test.want)
		}
		if !test.ok {
			if err == nil {
				t.Fatalf("decodeBinaryPowerState(%q) = %t, want an error", test.value, got)
			}
			continue
		}
		if got != test.want {
			t.Fatalf("decodeBinaryPowerState(%q) = %t, want %t", test.value, got, test.want)
		}
	}
}

// This test protects A4 native level boundaries, and fails if 0 and 99 are
// rejected, or 100, 255, fractions, numeric strings, null, and non-finite values
// are accepted as brightness.
func TestBrightnessStateAcceptsOnlyNativeLevels(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value string
		want  int64
		ok    bool
	}{
		{value: "0", want: 0, ok: true},
		{value: "99", want: 99, ok: true},
		{value: "99.0", want: 99, ok: true},
		{value: "15", want: 15, ok: true},
		{value: "-1"},
		{value: "100"},
		{value: "255"},
		{value: "15.5"},
		{value: `"15"`},
		{value: "null"},
		{value: "true"},
		{value: "1e999"},
		{value: "1e2"},
		{value: ""},
	} {
		got, err := decodeZwaveLevel([]byte(test.value))
		if test.ok && err != nil {
			t.Fatalf("decodeZwaveLevel(%q) = %v, want %d", test.value, err, test.want)
		}
		if !test.ok {
			if err == nil {
				t.Fatalf("decodeZwaveLevel(%q) = %d, want an error", test.value, got)
			}
			continue
		}
		if got != test.want {
			t.Fatalf("decodeZwaveLevel(%q) = %d, want %d", test.value, got, test.want)
		}
	}
}

// This test protects A4 derived power, and fails if zero and non-zero levels are
// not mapped to off and on, or if restore-previous level and out-of-range values
// become State.
func TestDerivedMultilevelPowerMapsZeroAndNonZero(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value string
		want  bool
		ok    bool
	}{
		{value: "0", want: false, ok: true},
		{value: "1", want: true, ok: true},
		{value: "99", want: true, ok: true},
		{value: "255"},
		{value: "100"},
		{value: "-1"},
		{value: "0.5"},
		{value: `"1"`},
		{value: "null"},
	} {
		got, err := decodeMultilevelPowerState([]byte(test.value))
		if test.ok && err != nil {
			t.Fatalf("decodeMultilevelPowerState(%q) = %v, want %t", test.value, err, test.want)
		}
		if !test.ok {
			if err == nil {
				t.Fatalf("decodeMultilevelPowerState(%q) = %t, want an error", test.value, got)
			}
			continue
		}
		if got != test.want {
			t.Fatalf("decodeMultilevelPowerState(%q) = %t, want %t", test.value, got, test.want)
		}
	}
}

// This test protects the typed Observation boundary, and fails if an Entity ID is
// dropped, the receive time is not UTC-formatted, or a source timestamp appears.
func TestObserveProducesTypedObservations(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		plan    entityPlan
		current string
		want    string
	}{
		{
			name:    "binary_on",
			plan:    fixtureBinaryPowerPlan(t, binaryPairFixture(0)),
			current: "true", want: "true",
		},
		{
			name:    "binary_off",
			plan:    fixtureBinaryPowerPlan(t, binaryPairFixture(0)),
			current: "false", want: "false",
		},
		{
			name:    "brightness",
			plan:    fixtureBrightnessPlan(t, levelPairFixture(0)),
			current: "42", want: "42",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			observation, err := test.plan.observe(
				canonicalEntityID(test.plan.Key),
				fixtureReceivedAt(),
				[]byte(test.current),
			)
			if err != nil {
				t.Fatal(err)
			}
			if observation.EntityID != canonicalEntityID(test.plan.Key) {
				t.Fatalf("observation entity = %q", observation.EntityID)
			}
			if string(observation.Value) != test.want {
				t.Fatalf("observation value = %s, want %s", observation.Value, test.want)
			}
			if observation.AdapterReceivedAt != fixtureReceivedAtText {
				t.Fatalf("observation receive time = %q", observation.AdapterReceivedAt)
			}
			if observation.SourceUpdatedAt != nil {
				t.Fatalf("observation source time = %v, want nil", observation.SourceUpdatedAt)
			}
			if _, err = test.plan.DecodeState([]byte(test.current)); err != nil {
				t.Fatalf("DecodeState(%q) = %v", test.current, err)
			}
		})
	}
}

// This test protects A4 ordering, and fails if one Multilevel Switch frame that
// maps to both power and brightness publishes brightness first.
func TestTranslateNodeValuesPublishesPowerBeforeBrightness(t *testing.T) {
	t.Parallel()
	plan := planNetwork(testHomeID, snapshotFixture(testHomeID, nodeFixture(
		fixtureDimmerNodeID,
		[]endpointState{rootEndpointFixture()},
		levelPairFixture(0),
	)))
	node := requireOnlyNode(t, plan)
	routes := bindFixtureRoutes(t, node)

	observations, issues := translateNodeValues(routes, fixtureReceivedAt(), []valueState{
		snapshotValueFixture(
			testCommandClassMultilevelSwitch, 0, valuePropertyCurrentValue,
			levelMetadata(true), "7",
		),
		snapshotValueFixture(
			testCommandClassMultilevelSwitch, 0, valuePropertyTargetValue,
			numberMetadata(false, true), "7",
		),
	})
	if len(issues) != 0 {
		t.Fatalf("issues = %#v, want none", issues)
	}
	if len(observations) != 2 {
		t.Fatalf("observations = %d, want 2", len(observations))
	}
	if observations[0].Key != "power" || string(observations[0].Observation.Value) != "true" {
		t.Fatalf("first observation = %#v, want derived power true", observations[0])
	}
	if observations[1].Key != "brightness" || string(observations[1].Observation.Value) != "7" {
		t.Fatalf("second observation = %#v, want brightness 7", observations[1])
	}
}

// This test protects A4 sibling isolation, and fails if one malformed current
// Value suppresses a valid sibling Entity, or if an absent Value becomes a State
// report.
func TestTranslateNodeValuesSkipsMalformedValuesPerEntity(t *testing.T) {
	t.Parallel()
	values := []valueState{
		snapshotValueFixture(
			testCommandClassBinarySwitch, 0, valuePropertyCurrentValue,
			boolMetadata(true, false), `"true"`,
		),
		snapshotValueFixture(
			testCommandClassBinarySwitch, 0, valuePropertyTargetValue,
			boolMetadata(false, true), "true",
		),
		snapshotValueFixture(
			testCommandClassMultilevelSwitch, 0, valuePropertyCurrentValue,
			levelMetadata(true), "5",
		),
		snapshotValueFixture(
			testCommandClassMultilevelSwitch, 0, valuePropertyTargetValue,
			numberMetadata(false, true), "80",
		),
	}
	plan := planNetwork(testHomeID, snapshotFixture(testHomeID, nodeFixture(
		fixtureDimmerNodeID,
		[]endpointState{rootEndpointFixture()},
		values,
	)))
	node := requireOnlyNode(t, plan)
	requirePlanKeys(t, node, []string{"power", "brightness"})
	routes := bindFixtureRoutes(t, node)

	observations, issues := translateNodeValues(routes, fixtureReceivedAt(), values)
	if len(observations) != 1 || observations[0].Key != "brightness" ||
		string(observations[0].Observation.Value) != "5" {
		t.Fatalf("observations = %#v, want only the multilevel brightness sibling", observations)
	}
	if len(issues) != 1 || issues[0].Key != "power" {
		t.Fatalf("issues = %#v, want only the Binary Switch power Entity", issues)
	}

	// An absent current Value is not a State report and not a diagnostic, and a
	// frame that reports only a target Value is never State.
	absent, absentIssues := translateNodeValues(routes, fixtureReceivedAt(), nil)
	if len(absent) != 0 || len(absentIssues) != 0 {
		t.Fatalf("absent values produced observations=%#v issues=%#v", absent, absentIssues)
	}
	targetOnly, targetOnlyIssues := translateNodeValues(routes, fixtureReceivedAt(), []valueState{
		snapshotValueFixture(
			testCommandClassBinarySwitch, 0, valuePropertyTargetValue,
			boolMetadata(false, true), "false",
		),
		snapshotValueFixture(
			testCommandClassMultilevelSwitch, 0, valuePropertyTargetValue,
			numberMetadata(false, true), "7",
		),
	})
	if len(targetOnly) != 0 || len(targetOnlyIssues) != 0 {
		t.Fatalf("target values produced observations=%#v issues=%#v", targetOnly, targetOnlyIssues)
	}
}

// This test protects Command parameter translation, and fails if the exact
// upstream Value of a set Command drifts.
func TestEncodeSetWritesExactUpstreamValues(t *testing.T) {
	t.Parallel()
	derivedPower := func(t *testing.T) entityPlan {
		t.Helper()
		node := nodeFixture(fixtureDimmerNodeID, []endpointState{rootEndpointFixture()}, levelPairFixture(0))
		plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
		return requirePlan(t, requireOnlyNode(t, plan), "power")
	}
	for _, test := range []struct {
		name       string
		plan       entityPlan
		parameters string
		want       string
		ok         bool
	}{
		{name: "binary_on", plan: fixtureBinaryPowerPlan(t, binaryPairFixture(0)),
			parameters: `{"value":true}`, want: "true", ok: true},
		{name: "binary_off", plan: fixtureBinaryPowerPlan(t, binaryPairFixture(0)),
			parameters: `{"value":false}`, want: "false", ok: true},
		{name: "brightness_mid", plan: fixtureBrightnessPlan(t, levelPairFixture(0)),
			parameters: `{"value":42}`, want: "42", ok: true},
		{name: "brightness_zero", plan: fixtureBrightnessPlan(t, levelPairFixture(0)),
			parameters: `{"value":0}`, want: "0", ok: true},
		{name: "brightness_ninety_nine", plan: fixtureBrightnessPlan(t, levelPairFixture(0)),
			parameters: `{"value":99}`, want: "99", ok: true},
		{name: "derived_power_on", plan: derivedPower(t), parameters: `{"value":true}`, want: "255", ok: true},
		{name: "derived_power_off", plan: derivedPower(t), parameters: `{"value":false}`, want: "0", ok: true},
		{name: "brightness_over_maximum", plan: fixtureBrightnessPlan(t, levelPairFixture(0)),
			parameters: `{"value":100}`},
		{name: "brightness_wrong_type", plan: fixtureBrightnessPlan(t, levelPairFixture(0)),
			parameters: `{"value":"42"}`},
		{name: "power_wrong_type", plan: fixtureBinaryPowerPlan(t, binaryPairFixture(0)),
			parameters: `{"value":1}`},
		{name: "malformed_parameters", plan: fixtureBinaryPowerPlan(t, binaryPairFixture(0)),
			parameters: `{`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := probeSetCommand([]byte(test.parameters))
			command.EntityID = canonicalEntityID(test.plan.Key)
			got, err := test.plan.EncodeSet(command)
			if !test.ok {
				if err == nil {
					t.Fatalf("EncodeSet(%s) = %s, want an error", test.parameters, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("EncodeSet(%s) = %v", test.parameters, err)
			}
			if string(got) != test.want {
				t.Fatalf("EncodeSet(%s) = %s, want %s", test.parameters, got, test.want)
			}
		})
	}
}

// This test protects Command outcome matching, and fails if typed parameters and
// State are compared loosely or a decode error counts as a match.
func TestMatchesComparesTypedParametersAndState(t *testing.T) {
	t.Parallel()
	binary := fixtureBinaryPowerPlan(t, binaryPairFixture(0))
	brightness := fixtureBrightnessPlan(t, levelPairFixture(0))
	for _, test := range []struct {
		name       string
		plan       entityPlan
		parameters string
		state      string
		want       bool
	}{
		{name: "power_match", plan: binary, parameters: `{"value":true}`, state: "true", want: true},
		{name: "power_mismatch", plan: binary, parameters: `{"value":true}`, state: "false"},
		{name: "power_bad_state", plan: binary, parameters: `{"value":true}`, state: "1"},
		{name: "power_bad_parameters", plan: binary, parameters: `{"value":1}`, state: "true"},
		{name: "brightness_match", plan: brightness, parameters: `{"value":42}`, state: "42", want: true},
		{name: "brightness_mismatch", plan: brightness, parameters: `{"value":42}`, state: "41"},
		{name: "brightness_restore_previous", plan: brightness, parameters: `{"value":42}`, state: "255"},
		{name: "brightness_bad_parameters", plan: brightness, parameters: `{"value":100}`, state: "99"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := test.plan.Matches([]byte(test.parameters), []byte(test.state))
			if got != test.want {
				t.Fatalf("Matches(%s, %s) = %t, want %t", test.parameters, test.state, got, test.want)
			}
		})
	}
}

// This test protects the round trip between Command encoding and State decoding,
// and fails if an accepted set Command can never be satisfied by its own read.
func TestEncodedSetMatchesItsOwnDecodedState(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		plan       entityPlan
		parameters string
		upstream   string
	}{
		{
			name: "binary_on", plan: fixtureBinaryPowerPlan(t, binaryPairFixture(0)),
			parameters: `{"value":true}`, upstream: "true",
		},
		{
			name: "brightness", plan: fixtureBrightnessPlan(t, levelPairFixture(0)),
			parameters: `{"value":42}`, upstream: "42",
		},
		{
			name: "derived_power_on",
			plan: func() entityPlan {
				node := nodeFixture(
					fixtureDimmerNodeID, []endpointState{rootEndpointFixture()}, levelPairFixture(0),
				)
				plan := planNetwork(testHomeID, snapshotFixture(testHomeID, node))
				return requirePlan(t, requireOnlyNode(t, plan), "power")
			}(),
			parameters: `{"value":true}`, upstream: "99",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			state, err := test.plan.DecodeState([]byte(test.upstream))
			if err != nil {
				t.Fatal(err)
			}
			if !test.plan.Matches([]byte(test.parameters), state) {
				t.Fatalf("encoded %s did not match decoded state %s", test.parameters, state)
			}
		})
	}
}

// bindFixtureRoutes registers one planned node through a recording Session and
// binds its canonical Entity IDs.
func bindFixtureRoutes(t *testing.T, node discoveredNode) []entityRoute {
	t.Helper()
	session := &recordingSession{}
	binding, err := session.Register(t.Context(), node.Registration)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := bindEntityRoutes(binding, node)
	if err != nil {
		t.Fatal(err)
	}
	return routes
}
