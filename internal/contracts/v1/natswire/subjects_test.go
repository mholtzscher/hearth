package natswire //nolint:testpackage // Tests exercise package-private subject validation alongside public routing.

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
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
	rapid.Check(t, func(t *rapid.T) {
		adapterID := subjectSlugGenerator().Draw(t, "adapter ID")
		runtimeID := subjectResourceIDGenerator("run").Draw(t, "runtime ID")
		entityID := subjectResourceIDGenerator("ent").Draw(t, "entity ID")
		operationName := subjectSlugGenerator().Draw(t, "operation name")
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
				t.Fatalf("%s subject = %q, want %q, err = %v", route.name, got, route.subject, err)
			}
			if route.parse != nil {
				parsed, parseErr := route.parse(got)
				if parseErr != nil || parsed != route.wantRoute {
					t.Fatalf("%s route = %#v, want %#v, err = %v", route.name, parsed, route.wantRoute, parseErr)
				}
			}
			if !route.usesRuntime {
				continue
			}
			for _, invalidRuntimeID := range invalidRuntimeIDs {
				if _, constructErr := route.construct(invalidRuntimeID); constructErr == nil {
					t.Fatalf("%s constructor accepted runtime ID %q", route.name, invalidRuntimeID)
				}
				if route.parse != nil {
					invalidSubject := strings.Replace(route.subject, runtimeID, invalidRuntimeID, 1)
					if _, parseErr := route.parse(invalidSubject); parseErr == nil {
						t.Fatalf("%s parser accepted runtime ID %q", route.name, invalidRuntimeID)
					}
				}
			}
		}
	})
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
	if _, err := CommandSubject("simulator", testRuntimeID, testEntityID, "bad.operation"); err == nil {
		t.Fatal("operation containing a period unexpectedly accepted")
	}
	if _, err := ParseCommandSubject(
		"hearth.v1.adapter.simulator.runtime." + testRuntimeID + ".command.extra." + testEntityID + ".set",
	); err == nil {
		t.Fatal("malformed command subject unexpectedly accepted")
	}
}

func subjectSlugGenerator() *rapid.Generator[string] {
	firstCharacters := []rune("abcdefghijklmnopqrstuvwxyz0123456789")
	characters := append(append([]rune{}, firstCharacters...), '_', '-')
	return rapid.Custom(func(t *rapid.T) string {
		first := rapid.SampledFrom(firstCharacters).Draw(t, "first character")
		rest := rapid.StringOfN(rapid.SampledFrom(characters), 0, 62, -1).Draw(t, "remaining characters")
		return string(first) + rest
	})
}

func subjectResourceIDGenerator(prefix string) *rapid.Generator[string] {
	hexDigit := rapid.SampledFrom([]rune("0123456789abcdef"))
	variant := rapid.SampledFrom([]rune("89ab"))
	return rapid.Custom(func(t *rapid.T) string {
		digits := rapid.StringOfN(hexDigit, 30, 30, -1).Draw(t, "hex digits")
		variantDigit := variant.Draw(t, "variant")
		return prefix + "_" + digits[:8] + "-" + digits[8:12] + "-7" + digits[12:15] +
			"-" + string(variantDigit) + digits[15:18] + "-" + digits[18:]
	})
}

func erasedSubjectParser[T any](parse func(string) (T, error)) func(string) (any, error) {
	return func(subject string) (any, error) {
		return parse(subject)
	}
}
