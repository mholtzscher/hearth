package natswire //nolint:testpackage // Tests exercise package-private subject validation alongside public routing.

import "testing"

const (
	testRuntimeID       = "run_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testSecondRuntimeID = "run_01890f47-7a6b-7c4d-8e9f-0123456789ac"
	testEntityID        = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

//nolint:cyclop,gocognit,gocyclo // The protocol route matrix is easier to audit in one place.
func TestRuntimeScopedSubjectsRoundTrip(t *testing.T) {
	t.Parallel()
	wildcards := map[string]string{
		"claim":        AdapterClaimWildcard(),
		"heartbeat":    AdapterHeartbeatWildcard(),
		"release":      AdapterReleaseWildcard(),
		"registration": RegistrationWildcard(),
		"availability": EntityAvailabilityWildcard(),
		"observation":  ObservationWildcard(),
		"enablement":   EntityEnablementWildcard(),
		"all commands": AllCommandsWildcard(),
	}
	wantWildcards := map[string]string{
		"claim":        "hearth.v1.adapter.*.claim",
		"heartbeat":    "hearth.v1.adapter.*.runtime.*.heartbeat",
		"release":      "hearth.v1.adapter.*.runtime.*.release",
		"registration": "hearth.v1.adapter.*.runtime.*.register",
		"availability": "hearth.v1.adapter.*.runtime.*.availability",
		"observation":  "hearth.v1.adapter.*.runtime.*.observation.*",
		"enablement":   "hearth.v1.adapter.*.runtime.*.enablement.*",
		"all commands": "hearth.v1.adapter.*.runtime.*.command.*.*",
	}
	for name, want := range wantWildcards {
		if got := wildcards[name]; got != want {
			t.Fatalf("%s wildcard = %q, want %q", name, got, want)
		}
	}

	claim, err := AdapterClaimSubject("simulator")
	if err != nil || claim != "hearth.v1.adapter.simulator.claim" {
		t.Fatalf("claim subject = %q, err = %v", claim, err)
	}
	claimRoute, err := ParseAdapterClaimSubject(claim)
	if err != nil || claimRoute.AdapterID != "simulator" {
		t.Fatalf("claim route = %#v, err = %v", claimRoute, err)
	}

	heartbeat, err := AdapterHeartbeatSubject("simulator", testRuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	heartbeatRoute, err := ParseAdapterHeartbeatSubject(heartbeat)
	if err != nil || heartbeatRoute.AdapterID != "simulator" || heartbeatRoute.RuntimeID != testRuntimeID {
		t.Fatalf("heartbeat route = %#v, err = %v", heartbeatRoute, err)
	}

	release, err := AdapterReleaseSubject("simulator", testRuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	releaseRoute, err := ParseAdapterReleaseSubject(release)
	if err != nil || releaseRoute.AdapterID != "simulator" || releaseRoute.RuntimeID != testRuntimeID {
		t.Fatalf("release route = %#v, err = %v", releaseRoute, err)
	}

	registration, err := RegistrationSubject("simulator", testRuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	registrationRoute, err := ParseRegistrationSubject(registration)
	if err != nil || registrationRoute.AdapterID != "simulator" || registrationRoute.RuntimeID != testRuntimeID {
		t.Fatalf("registration route = %#v, err = %v", registrationRoute, err)
	}

	availability, err := EntityAvailabilitySubject("simulator", testRuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	availabilityRoute, err := ParseEntityAvailabilitySubject(availability)
	if err != nil || availabilityRoute.AdapterID != "simulator" || availabilityRoute.RuntimeID != testRuntimeID {
		t.Fatalf("availability route = %#v, err = %v", availabilityRoute, err)
	}

	observation, err := ObservationSubject("simulator", testRuntimeID, testEntityID)
	if err != nil {
		t.Fatal(err)
	}
	observationRoute, err := ParseObservationSubject(observation)
	if err != nil || observationRoute.AdapterID != "simulator" || observationRoute.RuntimeID != testRuntimeID ||
		observationRoute.EntityID != testEntityID {
		t.Fatalf("observation route = %#v, err = %v", observationRoute, err)
	}

	enablement, err := EntityEnablementSubject("simulator", testRuntimeID, testEntityID)
	if err != nil {
		t.Fatal(err)
	}
	enablementRoute, err := ParseEntityEnablementSubject(enablement)
	if err != nil || enablementRoute.AdapterID != "simulator" || enablementRoute.RuntimeID != testRuntimeID ||
		enablementRoute.EntityID != testEntityID {
		t.Fatalf("Entity enablement route = %#v, err = %v", enablementRoute, err)
	}

	command, err := CommandSubject("simulator", testRuntimeID, testEntityID, "set")
	if err != nil {
		t.Fatal(err)
	}
	commandRoute, err := ParseCommandSubject(command)
	if err != nil || commandRoute.AdapterID != "simulator" || commandRoute.RuntimeID != testRuntimeID ||
		commandRoute.EntityID != testEntityID || commandRoute.OperationName != "set" {
		t.Fatalf("command route = %#v, err = %v", commandRoute, err)
	}
	wildcard, err := CommandWildcard("simulator", testRuntimeID)
	if err != nil || wildcard != "hearth.v1.adapter.simulator.runtime."+testRuntimeID+".command.*.*" {
		t.Fatalf("command wildcard = %q, err = %v", wildcard, err)
	}
}

func TestSubjectsRejectMissingOrMalformedRuntimeIDs(t *testing.T) {
	t.Parallel()
	invalidRuntimeIDs := []string{
		"",
		"run_not-a-uuid",
		"run_01890f47-7a6b-4c4d-8e9f-0123456789ab",
		"run_01890F47-7A6B-7C4D-8E9F-0123456789AB",
	}
	for _, runtimeID := range invalidRuntimeIDs {
		if _, err := AdapterHeartbeatSubject("simulator", runtimeID); err == nil {
			t.Fatalf("runtime ID %q unexpectedly accepted", runtimeID)
		}
	}
	if _, err := RegistrationSubject("simulator", ""); err == nil {
		t.Fatal("registration without a runtime ID unexpectedly accepted")
	}
	if _, err := ObservationSubject("simulator", "", testEntityID); err == nil {
		t.Fatal("observation without a runtime ID unexpectedly accepted")
	}
	if _, err := CommandWildcard("simulator", ""); err == nil {
		t.Fatal("command wildcard without a runtime ID unexpectedly accepted")
	}
}

func TestStrictParsersRejectStablePreCutoverSubjects(t *testing.T) {
	t.Parallel()
	oldSubjects := []struct {
		subject string
		parse   func(string) error
	}{
		{
			"hearth.v1.adapter.simulator.register",
			func(value string) error { _, err := ParseRegistrationSubject(value); return err },
		},
		{
			"hearth.v1.adapter.simulator.observation." + testEntityID,
			func(value string) error { _, err := ParseObservationSubject(value); return err },
		},
		{
			"hearth.v1.adapter.simulator.command." + testEntityID + ".set",
			func(value string) error { _, err := ParseCommandSubject(value); return err },
		},
		{
			"hearth.v1.adapter.simulator.enablement." + testEntityID,
			func(value string) error { _, err := ParseEntityEnablementSubject(value); return err },
		},
	}
	for _, test := range oldSubjects {
		if err := test.parse(test.subject); err == nil {
			t.Fatalf("pre-cutover subject %q unexpectedly accepted", test.subject)
		}
	}
}

func TestRuntimeRoutesPreserveIsolation(t *testing.T) {
	t.Parallel()
	first, err := CommandSubject("simulator", testRuntimeID, testEntityID, "set")
	if err != nil {
		t.Fatal(err)
	}
	second, err := CommandSubject("simulator", testSecondRuntimeID, testEntityID, "set")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("different runtimes produced the same command subject")
	}
	parsed, err := ParseCommandSubject(second)
	if err != nil || parsed.RuntimeID != testSecondRuntimeID {
		t.Fatalf("second runtime route = %#v, err = %v", parsed, err)
	}
}

func TestSubjectsRejectUnsafeTokens(t *testing.T) {
	t.Parallel()
	if _, err := AdapterClaimSubject("bad.adapter"); err == nil {
		t.Fatal("adapter ID containing a period unexpectedly accepted")
	}
	if _, err := ObservationSubject("simulator", testRuntimeID, "ent_not-a-uuid"); err == nil {
		t.Fatal("invalid entity ID unexpectedly accepted")
	}
	if _, err := CommandSubject("simulator", testRuntimeID, testEntityID, "bad.operation"); err == nil {
		t.Fatal("operation containing a period unexpectedly accepted")
	}
	if _, err := ParseCommandSubject(
		"hearth.v1.adapter.simulator.runtime." + testRuntimeID + ".command.extra." + testEntityID + ".set",
	); err == nil {
		t.Fatal("malformed command subject unexpectedly accepted")
	}
}
