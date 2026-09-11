package natswire_test

import (
	"testing"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

const (
	fuzzRuntimeID = "run_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	fuzzEntityID  = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

// FuzzSubjectParsersCanonicalRoundTrip protects strict, canonical subject parsing.
// Any accepted subject must be exactly reproducible by its public constructor and parse stably.
func FuzzSubjectParsersCanonicalRoundTrip(f *testing.F) {
	validRuntimePrefix := "hearth.v1.adapter.simulator.runtime." + fuzzRuntimeID
	seeds := []string{
		// Every valid parsed route family.
		"hearth.v1.adapter.simulator.claim",
		validRuntimePrefix + ".heartbeat",
		validRuntimePrefix + ".release",
		validRuntimePrefix + ".register",
		validRuntimePrefix + ".mappings",
		validRuntimePrefix + ".availability",
		validRuntimePrefix + ".observation." + fuzzEntityID,
		validRuntimePrefix + ".entity-event." + fuzzEntityID,
		validRuntimePrefix + ".enablement." + fuzzEntityID,
		validRuntimePrefix + ".command." + fuzzEntityID + ".set",

		// Malformed, truncated, extra-token, and unsafe-token subjects.
		"",
		"not-a-subject",
		"hearth.v1.adapter.simulator.runtime." + fuzzRuntimeID,
		validRuntimePrefix + ".command." + fuzzEntityID,
		"hearth.v1.adapter.simulator.claim.extra",
		validRuntimePrefix + ".command." + fuzzEntityID + ".set.extra",
		"hearth.v1.adapter.bad*adapter.claim",
		validRuntimePrefix + ".command." + fuzzEntityID + ".bad>operation",

		// Non-canonical case, protocol version, UUID version, and UUID variant.
		"HEARTH.v1.adapter.simulator.claim",
		"hearth.v2.adapter.simulator.claim",
		"hearth.v1.adapter.simulator.runtime.run_01890f47-7a6b-4c4d-8e9f-0123456789ab.heartbeat",
		"hearth.v1.adapter.simulator.runtime.run_01890f47-7a6b-7c4d-7e9f-0123456789ab.heartbeat",
		"hearth.v1.adapter.simulator.runtime.RUN_01890F47-7A6B-7C4D-8E9F-0123456789AB.heartbeat",

		// Stable pre-runtime cutover forms.
		"hearth.v1.adapter.simulator.register",
		"hearth.v1.adapter.simulator.mappings",
		"hearth.v1.adapter.simulator.observation." + fuzzEntityID,
		"hearth.v1.adapter.simulator.enablement." + fuzzEntityID,
		"hearth.v1.adapter.simulator.command." + fuzzEntityID + ".set",
	}
	for _, subject := range seeds {
		f.Add(subject)
	}

	f.Fuzz(func(t *testing.T, subject string) {
		if len(subject) > 512 {
			return
		}

		checkAcceptedSubject(
			t, subject, natswire.ParseAdapterClaimSubject,
			func(route natswire.AdapterClaimRoute) (string, error) {
				return natswire.AdapterClaimSubject(route.AdapterID)
			},
		)
		checkAcceptedSubject(
			t, subject, natswire.ParseAdapterHeartbeatSubject,
			func(route natswire.AdapterHeartbeatRoute) (string, error) {
				return natswire.AdapterHeartbeatSubject(route.AdapterID, route.RuntimeID)
			},
		)
		checkAcceptedSubject(
			t, subject, natswire.ParseAdapterReleaseSubject,
			func(route natswire.AdapterReleaseRoute) (string, error) {
				return natswire.AdapterReleaseSubject(route.AdapterID, route.RuntimeID)
			},
		)
		checkAcceptedSubject(
			t, subject, natswire.ParseRegistrationSubject,
			func(route natswire.RegistrationRoute) (string, error) {
				return natswire.RegistrationSubject(route.AdapterID, route.RuntimeID)
			},
		)
		checkAcceptedSubject(
			t, subject, natswire.ParseOwnedMappingsSubject,
			func(route natswire.OwnedMappingsRoute) (string, error) {
				return natswire.OwnedMappingsSubject(route.AdapterID, route.RuntimeID)
			},
		)
		checkAcceptedSubject(
			t, subject, natswire.ParseEntityAvailabilitySubject,
			func(route natswire.EntityAvailabilityRoute) (string, error) {
				return natswire.EntityAvailabilitySubject(route.AdapterID, route.RuntimeID)
			},
		)
		checkAcceptedSubject(
			t, subject, natswire.ParseObservationSubject,
			func(route natswire.ObservationRoute) (string, error) {
				return natswire.ObservationSubject(route.AdapterID, route.RuntimeID, route.EntityID)
			},
		)
		checkAcceptedSubject(
			t, subject, natswire.ParseEntityEventSubject,
			func(route natswire.EntityEventRoute) (string, error) {
				return natswire.EntityEventSubject(route.AdapterID, route.RuntimeID, route.EntityID)
			},
		)
		checkAcceptedSubject(
			t, subject, natswire.ParseEntityEnablementSubject,
			func(route natswire.EntityEnablementRoute) (string, error) {
				return natswire.EntityEnablementSubject(route.AdapterID, route.RuntimeID, route.EntityID)
			},
		)
		checkAcceptedSubject(
			t, subject, natswire.ParseCommandSubject,
			func(route natswire.CommandRoute) (string, error) {
				return natswire.CommandSubject(route.AdapterID, route.RuntimeID, route.EntityID, route.OperationName)
			},
		)
	})
}

func checkAcceptedSubject[T comparable](
	t *testing.T,
	subject string,
	parse func(string) (T, error),
	construct func(T) (string, error),
) {
	t.Helper()
	route, err := parse(subject)
	if err != nil {
		return
	}
	canonical, err := construct(route)
	if err != nil {
		t.Fatalf("parser accepted %q as %#v, but its constructor rejected the route: %v", subject, route, err)
	}
	if canonical != subject {
		t.Fatalf("parser accepted non-canonical subject %q as %#v; constructor produced %q", subject, route, canonical)
	}
	reparsed, err := parse(canonical)
	if err != nil {
		t.Fatalf("parser did not accept reconstructed subject %q: %v", canonical, err)
	}
	if reparsed != route {
		t.Fatalf(
			"route changed after reconstructing and reparsing %q: first %#v, second %#v",
			canonical,
			route,
			reparsed,
		)
	}
}
