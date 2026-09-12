package natswire_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

const (
	factEntityID      = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	otherFactEntityID = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ac"
)

func TestDeviceFactWildcardsMatchProtocol(t *testing.T) {
	t.Parallel()
	if got := natswire.DeviceFactWildcard(); got != "hearth.v1.core.fact.>" {
		t.Fatalf("DeviceFactWildcard() = %q, want %q", got, "hearth.v1.core.fact.>")
	}
	wantEntityWildcard := "hearth.v1.core.fact.entity." + factEntityID + ".>"
	gotEntityWildcard, err := natswire.EntityDeviceFactsWildcard(factEntityID)
	if err != nil || gotEntityWildcard != wantEntityWildcard {
		t.Fatalf("EntityDeviceFactsWildcard() = %q, %v, want %q", gotEntityWildcard, err, wantEntityWildcard)
	}
	families := map[natswire.DeviceFactFamily]string{
		natswire.DeviceFactFamilyObservation: "hearth.v1.core.fact.entity.*.observation.>",
		natswire.DeviceFactFamilyEntityEvent: "hearth.v1.core.fact.entity.*.entity-event.>",
		natswire.DeviceFactFamilyCommand:     "hearth.v1.core.fact.entity.*.command.>",
	}
	for family, want := range families {
		got, familyErr := natswire.DeviceFactFamilyWildcard(family)
		if familyErr != nil || got != want {
			t.Fatalf("DeviceFactFamilyWildcard(%q) = %q, %v, want %q", family, got, familyErr, want)
		}
	}
}

func TestDeviceFactSubjectsCoverEveryFamilyAndVariant(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		family  natswire.DeviceFactFamily
		variant string
		want    string
	}{
		{
			name:    "observation applied",
			family:  natswire.DeviceFactFamilyObservation,
			variant: natswire.ObservationFactApplied,
			want:    "hearth.v1.core.fact.entity." + factEntityID + ".observation.applied",
		},
		{
			name:    "observation unchanged",
			family:  natswire.DeviceFactFamilyObservation,
			variant: natswire.ObservationFactUnchanged,
			want:    "hearth.v1.core.fact.entity." + factEntityID + ".observation.unchanged",
		},
		{
			name:    "entity event",
			family:  natswire.DeviceFactFamilyEntityEvent,
			variant: "single_press",
			want:    "hearth.v1.core.fact.entity." + factEntityID + ".entity-event.single_press",
		},
		{
			name:    "command requested",
			family:  natswire.DeviceFactFamilyCommand,
			variant: natswire.CommandFactRequested,
			want:    "hearth.v1.core.fact.entity." + factEntityID + ".command.requested",
		},
		{
			name:    "command accepted",
			family:  natswire.DeviceFactFamilyCommand,
			variant: natswire.CommandFactAccepted,
			want:    "hearth.v1.core.fact.entity." + factEntityID + ".command.accepted",
		},
		{
			name:    "command satisfied",
			family:  natswire.DeviceFactFamilyCommand,
			variant: natswire.CommandFactSatisfied,
			want:    "hearth.v1.core.fact.entity." + factEntityID + ".command.satisfied",
		},
		{
			name:    "command dispatched",
			family:  natswire.DeviceFactFamilyCommand,
			variant: natswire.CommandFactDispatched,
			want:    "hearth.v1.core.fact.entity." + factEntityID + ".command.dispatched",
		},
		{
			name:    "command rejected",
			family:  natswire.DeviceFactFamilyCommand,
			variant: natswire.CommandFactRejected,
			want:    "hearth.v1.core.fact.entity." + factEntityID + ".command.rejected",
		},
		{
			name:    "command adapter unhealthy",
			family:  natswire.DeviceFactFamilyCommand,
			variant: natswire.CommandFactAdapterUnhealthy,
			want:    "hearth.v1.core.fact.entity." + factEntityID + ".command.adapter_unhealthy",
		},
		{
			name:    "command entity unavailable",
			family:  natswire.DeviceFactFamilyCommand,
			variant: natswire.CommandFactEntityUnavailable,
			want:    "hearth.v1.core.fact.entity." + factEntityID + ".command.entity_unavailable",
		},
		{
			name:    "command outcome timeout",
			family:  natswire.DeviceFactFamilyCommand,
			variant: natswire.CommandFactOutcomeTimeout,
			want:    "hearth.v1.core.fact.entity." + factEntityID + ".command.outcome_timeout",
		},
		{
			name:    "command entity disabled",
			family:  natswire.DeviceFactFamilyCommand,
			variant: natswire.CommandFactEntityDisabled,
			want:    "hearth.v1.core.fact.entity." + factEntityID + ".command.entity_disabled",
		},
		{
			name:    "command internal failure",
			family:  natswire.DeviceFactFamilyCommand,
			variant: natswire.CommandFactInternalFailure,
			want:    "hearth.v1.core.fact.entity." + factEntityID + ".command.internal_failure",
		},
		{
			name:    "command interrupted",
			family:  natswire.DeviceFactFamilyCommand,
			variant: natswire.CommandFactInterrupted,
			want:    "hearth.v1.core.fact.entity." + factEntityID + ".command.interrupted",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject, err := buildDeviceFactSubject(natswire.DeviceFactRoute{
				EntityID: factEntityID,
				Family:   test.family,
				Variant:  test.variant,
			})
			if err != nil || subject != test.want {
				t.Fatalf("subject = %q, %v, want %q", subject, err, test.want)
			}
			route, parseErr := natswire.ParseDeviceFactSubject(subject)
			if parseErr != nil {
				t.Fatalf("parse %q: %v", subject, parseErr)
			}
			want := natswire.DeviceFactRoute{
				EntityID: factEntityID, Family: test.family, Variant: test.variant,
			}
			if route != want {
				t.Fatalf("route = %#v, want %#v", route, want)
			}
		})
	}
}

// TestDeviceFactSubjectBuildersRejectUnsafeInput keeps the closed variant sets
// and the canonical Entity identity enforceable at every construction site.
func TestDeviceFactSubjectBuildersRejectUnsafeInput(t *testing.T) {
	t.Parallel()
	invalidVariants := map[natswire.DeviceFactFamily][]string{
		natswire.DeviceFactFamilyObservation: {
			"", "rejected", "duplicate", "Applied", "applied.extra", "applied>", "*",
		},
		natswire.DeviceFactFamilyEntityEvent: {
			"", "Single Press", "single.press", "single>press", "*", strings.Repeat("a", 64),
		},
		natswire.DeviceFactFamilyCommand: {
			"", "cancelled", "Satisfied", "satisfied.extra", "satisfied>", "*",
		},
	}
	for family, variants := range invalidVariants {
		for _, variant := range variants {
			_, err := buildDeviceFactSubject(natswire.DeviceFactRoute{
				EntityID: factEntityID, Family: family, Variant: variant,
			})
			if err == nil {
				t.Fatalf("%s builder accepted variant %q", family, variant)
			}
		}
	}
	invalidEntityIDs := []string{
		"",
		"ent_not-a-uuid",
		"dev_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"ent_01890F47-7A6B-7C4D-8E9F-0123456789AB",
		"ent_01890f47-7a6b-4c4d-8e9f-0123456789ab",
		"*",
	}
	for _, entityID := range invalidEntityIDs {
		for family, variant := range map[natswire.DeviceFactFamily]string{
			natswire.DeviceFactFamilyObservation: natswire.ObservationFactApplied,
			natswire.DeviceFactFamilyEntityEvent: "single_press",
			natswire.DeviceFactFamilyCommand:     natswire.CommandFactSatisfied,
		} {
			if _, err := buildDeviceFactSubject(natswire.DeviceFactRoute{
				EntityID: entityID, Family: family, Variant: variant,
			}); err == nil {
				t.Fatalf("%s builder accepted entity ID %q", family, entityID)
			}
		}
		if _, err := natswire.EntityDeviceFactsWildcard(entityID); err == nil {
			t.Fatalf("EntityDeviceFactsWildcard accepted entity ID %q", entityID)
		}
	}
	if _, err := natswire.DeviceFactFamilyWildcard(natswire.DeviceFactFamily("fact")); err == nil {
		t.Fatal("DeviceFactFamilyWildcard accepted an unknown family")
	}
	if _, err := natswire.DeviceFactFamilyWildcard(""); err == nil {
		t.Fatal("DeviceFactFamilyWildcard accepted an empty family")
	}
}

func TestParseDeviceFactSubjectRejectsNonCanonicalInput(t *testing.T) {
	t.Parallel()
	prefix := "hearth.v1.core.fact.entity." + factEntityID
	invalidSubjects := map[string]string{
		"empty":                     "",
		"not a subject":             "not-a-subject",
		"too few tokens":            "hearth.v1.core.fact.entity." + factEntityID + ".command",
		"too many tokens":           prefix + ".command.satisfied.extra",
		"trailing empty token":      prefix + ".command.satisfied.",
		"missing scope token":       "hearth.v1.core.fact." + factEntityID + ".command.satisfied",
		"unknown scope":             "hearth.v1.core.fact.adapter." + factEntityID + ".command.satisfied",
		"wrong namespace":           "hearth.v1.adapter.simulator.claim",
		"adapter fact namespace":    "hearth.v2.core.fact.entity." + factEntityID + ".command.satisfied",
		"unknown family":            prefix + ".fact.satisfied",
		"empty family":              prefix + "..satisfied",
		"noncanonical family case":  prefix + ".Command.satisfied",
		"entity wildcard":           "hearth.v1.core.fact.entity.*.command.satisfied",
		"family wildcard":           prefix + ".*.satisfied",
		"variant wildcard":          prefix + ".command.*",
		"variant wildcard suffix":   prefix + ".command.satisfied.*",
		"variant token wildcard":    prefix + ".entity-event.>",
		"operation is not a status": prefix + ".command.set",
		"illegal observation":       prefix + ".observation.rejected",
		"illegal command status":    prefix + ".command.cancelled",
		"illegal event name":        prefix + ".entity-event.Single Press",
		"noncanonical entity case":  "hearth.v1.core.fact.entity.ent_01890F47-7A6B-7C4D-8E9F-0123456789AB.observation.applied",
		"wrong UUID version":        "hearth.v1.core.fact.entity.ent_01890f47-7a6b-4c4d-8e9f-0123456789ab.observation.applied",
		"wrong entity prefix":       "hearth.v1.core.fact.entity.dev_01890f47-7a6b-7c4d-8e9f-0123456789ab.observation.applied",
		"empty entity":              "hearth.v1.core.fact.entity..observation.applied",
		"unsafe event token":        prefix + ".entity-event.bad>event",
		"uppercase disposition":     prefix + ".observation.Applied",
	}
	for name, subject := range invalidSubjects {
		if _, err := natswire.ParseDeviceFactSubject(subject); err == nil {
			t.Fatalf("%s: parser accepted %q", name, subject)
		}
	}
	// A canonical subject for another Entity must parse to that Entity, so the
	// parser cannot be short-circuiting on any fixed Entity or variant.
	route, err := natswire.ParseDeviceFactSubject(
		"hearth.v1.core.fact.entity." + otherFactEntityID + ".command.interrupted",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := natswire.DeviceFactRoute{
		EntityID: otherFactEntityID,
		Family:   natswire.DeviceFactFamilyCommand,
		Variant:  natswire.CommandFactInterrupted,
	}
	if route != want {
		t.Fatalf("route = %#v, want %#v", route, want)
	}
}

// FuzzParseDeviceFactSubjectCanonicalRoundTrip protects the strict, canonical
// fact subject grammar: any accepted subject must be reproduced exactly by its
// public builder and parse back to the same route.
func FuzzParseDeviceFactSubjectCanonicalRoundTrip(f *testing.F) {
	prefix := "hearth.v1.core.fact.entity." + fuzzEntityID
	seeds := []string{
		"hearth.v1.core.fact.>",
		prefix + ".observation.applied",
		prefix + ".observation.unchanged",
		prefix + ".entity-event.single_press",
		prefix + ".command.requested",
		prefix + ".command.satisfied",
		prefix + ".command.interrupted",
		"",
		"not-a-subject",
		"hearth.v1.core.fact.entity.*.command.satisfied",
		prefix + ".command.cancelled",
		prefix + ".observation.rejected",
		prefix + ".entity-event.Single Press",
		"hearth.v1.core.fact.entity.ent_01890F47-7A6B-7C4D-8E9F-0123456789AB.observation.applied",
		"hearth.v1.core.fact.entity.ent_01890f47-7a6b-4c4d-8e9f-0123456789ab.observation.applied",
		prefix + ".command.satisfied.extra",
	}
	for _, subject := range seeds {
		f.Add(subject)
	}

	f.Fuzz(func(t *testing.T, subject string) {
		if len(subject) > 512 {
			return
		}
		route, err := natswire.ParseDeviceFactSubject(subject)
		if err != nil {
			return
		}
		canonical, err := buildDeviceFactSubject(route)
		if err != nil {
			t.Fatalf("parser accepted %q as %#v, but its builder rejected the route: %v", subject, route, err)
		}
		if canonical != subject {
			t.Fatalf("parser accepted non-canonical subject %q as %#v; builder produced %q", subject, route, canonical)
		}
		reparsed, err := natswire.ParseDeviceFactSubject(canonical)
		if err != nil {
			t.Fatalf("parser did not accept reconstructed subject %q: %v", canonical, err)
		}
		if reparsed != route {
			t.Fatalf("route changed after rebuilding %q: first %#v, second %#v", canonical, route, reparsed)
		}
	})
}

func buildDeviceFactSubject(route natswire.DeviceFactRoute) (string, error) {
	switch route.Family {
	case natswire.DeviceFactFamilyObservation:
		return natswire.ObservationFactSubject(route.EntityID, route.Variant)
	case natswire.DeviceFactFamilyEntityEvent:
		return natswire.EntityEventFactSubject(route.EntityID, route.Variant)
	case natswire.DeviceFactFamilyCommand:
		return natswire.CommandFactSubject(route.EntityID, route.Variant)
	}
	return "", errors.New("unexpected Device Fact family")
}
