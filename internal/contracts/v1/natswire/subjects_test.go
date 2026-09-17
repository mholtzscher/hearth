package natswire //nolint:testpackage // Tests exercise package-private subject validation alongside public routing.

import (
	"strings"
	"testing"
)

const (
	testRuntimeID = "run_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testEntityID  = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

func TestSubjectWildcardsMatchProtocol(t *testing.T) {
	t.Parallel()
	wildcards := map[string]string{
		"claim":          AdapterClaimWildcard(),
		"heartbeat":      AdapterHeartbeatWildcard(),
		"release":        AdapterReleaseWildcard(),
		"registration":   RegistrationWildcard(),
		"owned mappings": OwnedMappingsWildcard(),
		"availability":   EntityAvailabilityWildcard(),
		"observation":    ObservationWildcard(),
		"entity event":   EntityEventWildcard(),
		"enablement":     EntityEnablementWildcard(),
		"all commands":   AllCommandsWildcard(),
	}
	wantWildcards := map[string]string{
		"claim":          "hearth.v1.adapter.*.claim",
		"heartbeat":      "hearth.v1.adapter.*.runtime.*.heartbeat",
		"release":        "hearth.v1.adapter.*.runtime.*.release",
		"registration":   "hearth.v1.adapter.*.runtime.*.register",
		"owned mappings": "hearth.v1.adapter.*.runtime.*.mappings",
		"availability":   "hearth.v1.adapter.*.runtime.*.availability",
		"observation":    "hearth.v1.adapter.*.runtime.*.observation.*",
		"entity event":   "hearth.v1.adapter.*.runtime.*.entity-event.*",
		"enablement":     "hearth.v1.adapter.*.runtime.*.enablement.*",
		"all commands":   "hearth.v1.adapter.*.runtime.*.command.*.*",
	}
	for name, want := range wantWildcards {
		if got := wildcards[name]; got != want {
			t.Fatalf("%s wildcard = %q, want %q", name, got, want)
		}
	}
}

type subjectPropertyRoute struct {
	name        string
	subject     string
	wantRoute   any
	usesRuntime bool
	construct   func(string) (string, error)
	parse       func(string) (any, error)
}

//nolint:gocognit // The table keeps the complete public route matrix auditable.
func TestSubjectConstructorsAndParsersMatchProtocol(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		adapterID     string
		runtimeID     string
		entityID      string
		operationName string
	}{
		{
			name: "typical", adapterID: "simulator",
			runtimeID: "run_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			entityID:  "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab", operationName: "set",
		},
		{
			name: "minimal slugs", adapterID: "a",
			runtimeID: "run_00000000-0000-7000-8000-000000000000",
			entityID:  "ent_00000000-0000-7000-8000-000000000000", operationName: "0",
		},
		{
			name: "punctuated slugs and variant 9", adapterID: "adapter_1-x",
			runtimeID: "run_ffffffff-ffff-7fff-9fff-ffffffffffff",
			entityID:  "ent_ffffffff-ffff-7fff-9fff-ffffffffffff", operationName: "toggle-state_2",
		},
		{
			name: "max length slug and variant a", adapterID: "z" + strings.Repeat("a0_-b", 12) + "ab",
			runtimeID: "run_12345678-1234-7abc-afff-123456789abc",
			entityID:  "ent_12345678-1234-7abc-afff-123456789abc", operationName: "o" + strings.Repeat("p", 62),
		},
		{
			name: "numeric slugs and variant b", adapterID: "0abc",
			runtimeID: "run_abcdef01-2345-7def-b456-789abcdef012",
			entityID:  "ent_abcdef01-2345-7def-b456-789abcdef012", operationName: "9-start",
		},
	}
	for _, test := range cases {
		adapterID := test.adapterID
		runtimeID := test.runtimeID
		entityID := test.entityID
		operationName := test.operationName
		prefix := "hearth.v1.adapter." + adapterID
		runtimePrefix := prefix + ".runtime." + runtimeID
		routes := []subjectPropertyRoute{
			{
				name: "claim", subject: prefix + ".claim", wantRoute: AdapterClaimRoute{AdapterID: adapterID},
				construct: func(string) (string, error) { return AdapterClaimSubject(adapterID) },
				parse:     erasedSubjectParser(ParseAdapterClaimSubject),
			},
			{
				name: "heartbeat", subject: runtimePrefix + ".heartbeat", usesRuntime: true,
				wantRoute: AdapterHeartbeatRoute{AdapterID: adapterID, RuntimeID: runtimeID},
				construct: func(value string) (string, error) { return AdapterHeartbeatSubject(adapterID, value) },
				parse:     erasedSubjectParser(ParseAdapterHeartbeatSubject),
			},
			{
				name: "release", subject: runtimePrefix + ".release", usesRuntime: true,
				wantRoute: AdapterReleaseRoute{AdapterID: adapterID, RuntimeID: runtimeID},
				construct: func(value string) (string, error) { return AdapterReleaseSubject(adapterID, value) },
				parse:     erasedSubjectParser(ParseAdapterReleaseSubject),
			},
			{
				name: "registration", subject: runtimePrefix + ".register", usesRuntime: true,
				wantRoute: RegistrationRoute{AdapterID: adapterID, RuntimeID: runtimeID},
				construct: func(value string) (string, error) { return RegistrationSubject(adapterID, value) },
				parse:     erasedSubjectParser(ParseRegistrationSubject),
			},
			{
				name: "owned mappings", subject: runtimePrefix + ".mappings", usesRuntime: true,
				wantRoute: OwnedMappingsRoute{AdapterID: adapterID, RuntimeID: runtimeID},
				construct: func(value string) (string, error) { return OwnedMappingsSubject(adapterID, value) },
				parse:     erasedSubjectParser(ParseOwnedMappingsSubject),
			},
			{
				name: "availability", subject: runtimePrefix + ".availability", usesRuntime: true,
				wantRoute: EntityAvailabilityRoute{AdapterID: adapterID, RuntimeID: runtimeID},
				construct: func(value string) (string, error) { return EntityAvailabilitySubject(adapterID, value) },
				parse:     erasedSubjectParser(ParseEntityAvailabilitySubject),
			},
			{
				name: "observation", subject: runtimePrefix + ".observation." + entityID, usesRuntime: true,
				wantRoute: ObservationRoute{AdapterID: adapterID, RuntimeID: runtimeID, EntityID: entityID},
				construct: func(value string) (string, error) { return ObservationSubject(adapterID, value, entityID) },
				parse:     erasedSubjectParser(ParseObservationSubject),
			},
			{
				name: "entity event", subject: runtimePrefix + ".entity-event." + entityID, usesRuntime: true,
				wantRoute: EntityEventRoute{AdapterID: adapterID, RuntimeID: runtimeID, EntityID: entityID},
				construct: func(value string) (string, error) { return EntityEventSubject(adapterID, value, entityID) },
				parse:     erasedSubjectParser(ParseEntityEventSubject),
			},
			{
				name:        "enablement",
				subject:     runtimePrefix + ".enablement." + entityID,
				usesRuntime: true,
				wantRoute:   EntityEnablementRoute{AdapterID: adapterID, RuntimeID: runtimeID, EntityID: entityID},
				construct:   func(value string) (string, error) { return EntityEnablementSubject(adapterID, value, entityID) },
				parse:       erasedSubjectParser(ParseEntityEnablementSubject),
			},
			{
				name: "command", subject: runtimePrefix + ".command." + entityID + "." + operationName,
				usesRuntime: true,
				wantRoute: CommandRoute{
					AdapterID: adapterID, RuntimeID: runtimeID, EntityID: entityID, OperationName: operationName,
				},
				construct: func(value string) (string, error) {
					return CommandSubject(adapterID, value, entityID, operationName)
				},
				parse: erasedSubjectParser(ParseCommandSubject),
			},
			{
				name: "command wildcard", subject: runtimePrefix + ".command.*.*", usesRuntime: true,
				construct: func(value string) (string, error) { return CommandWildcard(adapterID, value) },
			},
		}
		invalidRuntimeIDs := []string{
			"",
			"run_not-a-uuid",
			runtimeID[:18] + "4" + runtimeID[19:],
			runtimeID[:23] + "7" + runtimeID[24:],
			strings.ToUpper(runtimeID),
		}

		for _, route := range routes {
			got, err := route.construct(runtimeID)
			if err != nil || got != route.subject {
				t.Fatalf("%s/%s subject = %q, want %q, err = %v", test.name, route.name, got, route.subject, err)
			}
			if route.parse != nil {
				parsed, parseErr := route.parse(got)
				if parseErr != nil || parsed != route.wantRoute {
					t.Fatalf(
						"%s/%s route = %#v, want %#v, err = %v",
						test.name,
						route.name,
						parsed,
						route.wantRoute,
						parseErr,
					)
				}
			}
			if !route.usesRuntime {
				continue
			}
			for _, invalidRuntimeID := range invalidRuntimeIDs {
				if _, constructErr := route.construct(invalidRuntimeID); constructErr == nil {
					t.Fatalf("%s/%s constructor accepted runtime ID %q", test.name, route.name, invalidRuntimeID)
				}
				if route.parse != nil {
					invalidSubject := strings.Replace(route.subject, runtimeID, invalidRuntimeID, 1)
					if _, parseErr := route.parse(invalidSubject); parseErr == nil {
						t.Fatalf("%s/%s parser accepted runtime ID %q", test.name, route.name, invalidRuntimeID)
					}
				}
			}
		}
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
			"hearth.v1.adapter.simulator.mappings",
			func(value string) error { _, err := ParseOwnedMappingsSubject(value); return err },
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

func TestSubjectsRejectUnsafeTokens(t *testing.T) {
	t.Parallel()
	if _, err := AdapterClaimSubject("bad.adapter"); err == nil {
		t.Fatal("adapter ID containing a period unexpectedly accepted")
	}
	if _, err := ObservationSubject("simulator", testRuntimeID, "ent_not-a-uuid"); err == nil {
		t.Fatal("invalid entity ID unexpectedly accepted")
	}
	if _, err := EntityEventSubject("simulator", testRuntimeID, "ent_not-a-uuid"); err == nil {
		t.Fatal("invalid entity event entity ID unexpectedly accepted")
	}
	if _, err := ParseEntityEventSubject(
		"hearth.v1.adapter.simulator.runtime." + testRuntimeID + ".entity-event." + testEntityID + ".extra",
	); err == nil {
		t.Fatal("malformed entity event subject unexpectedly accepted")
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

func erasedSubjectParser[T any](parse func(string) (T, error)) func(string) (any, error) {
	return func(subject string) (any, error) {
		return parse(subject)
	}
}
