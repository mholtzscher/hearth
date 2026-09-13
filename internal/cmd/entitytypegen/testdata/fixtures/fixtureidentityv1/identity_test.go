package fixtureidentityv1

import "testing"

// TestGeneratedSameSupportIdentityIgnoresMutableFields proves the generated
// comparison only keys on the manifest-declared immutable support path: bounds
// and other mutable fields may change freely.
func TestGeneratedSameSupportIdentityIgnoresMutableFields(t *testing.T) {
	previous := Support{State: StateSupport{Kind: "temperature", Minimum: -273.15, Maximum: 1000}}
	next := Support{State: StateSupport{Kind: "temperature", Minimum: 0, Maximum: 40}}
	if !SameSupportIdentity(previous, next) {
		t.Error("changed mutable bounds reported a different support identity")
	}
	if !SameSupportIdentity(next, next) {
		t.Error("identical support reported a different support identity")
	}
}

// TestGeneratedSameSupportIdentityDetectsDeclaredFieldChange proves the
// generated comparison detects a change to /state/kind.
func TestGeneratedSameSupportIdentityDetectsDeclaredFieldChange(t *testing.T) {
	previous := Support{State: StateSupport{Kind: "temperature", Minimum: 0, Maximum: 100}}
	next := Support{State: StateSupport{Kind: "relative_humidity", Minimum: 0, Maximum: 100}}
	if SameSupportIdentity(previous, next) {
		t.Error("changed immutable kind reported the same support identity")
	}
}
