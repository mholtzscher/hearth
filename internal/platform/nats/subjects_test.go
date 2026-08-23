package nats

import "testing"

const testEntityID = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"

func TestSubjectsRoundTrip(t *testing.T) {
	if got := RegistrationWildcard(); got != "hearth.v1.adapter.*.register" {
		t.Fatalf("registration wildcard = %q", got)
	}
	if got := ObservationWildcard(); got != "hearth.v1.adapter.*.observation.>" {
		t.Fatalf("observation wildcard = %q", got)
	}

	registration, err := RegistrationSubject("simulator")
	if err != nil || registration != "hearth.v1.adapter.simulator.register" {
		t.Fatalf("registration subject = %q, err = %v", registration, err)
	}
	registrationRoute, err := ParseRegistrationSubject(registration)
	if err != nil || registrationRoute.AdapterID != "simulator" {
		t.Fatalf("registration route = %#v, err = %v", registrationRoute, err)
	}

	observation, err := ObservationSubject("simulator", testEntityID)
	if err != nil {
		t.Fatal(err)
	}
	observationRoute, err := ParseObservationSubject(observation)
	if err != nil {
		t.Fatal(err)
	}
	if observationRoute.AdapterID != "simulator" || observationRoute.EntityID != testEntityID {
		t.Fatalf("observation route = %#v", observationRoute)
	}

	command, err := CommandSubject("simulator", testEntityID, "set")
	if err != nil {
		t.Fatal(err)
	}
	commandRoute, err := ParseCommandSubject(command)
	if err != nil {
		t.Fatal(err)
	}
	if commandRoute.AdapterID != "simulator" || commandRoute.EntityID != testEntityID || commandRoute.OperationName != "set" {
		t.Fatalf("command route = %#v", commandRoute)
	}
}

func TestSubjectsRejectUnsafeTokens(t *testing.T) {
	if _, err := RegistrationSubject("bad.adapter"); err == nil {
		t.Fatal("adapter ID containing a period unexpectedly accepted")
	}
	if _, err := ObservationSubject("simulator", "ent_not-a-uuid"); err == nil {
		t.Fatal("invalid entity ID unexpectedly accepted")
	}
	if _, err := CommandSubject("simulator", testEntityID, "bad.operation"); err == nil {
		t.Fatal("operation containing a period unexpectedly accepted")
	}
	if _, err := ParseCommandSubject("hearth.v1.adapter.simulator.command.extra." + testEntityID + ".set"); err == nil {
		t.Fatal("malformed command subject unexpectedly accepted")
	}
}
