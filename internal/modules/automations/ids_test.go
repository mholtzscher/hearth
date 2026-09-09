package automations //nolint:testpackage // Tests inspect parser masks and inject repository clocks/failures.

import (
	"strings"
	"testing"
)

func TestAutomationCanonicalIDs(t *testing.T) {
	t.Parallel()
	id, err := NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}
	if got, parseErr := ParseAutomationID(string(id)); parseErr != nil || got != id {
		t.Fatal(got, parseErr)
	}
	run, err := NewAutomationRunID()
	if err != nil {
		t.Fatal(err)
	}
	if got, parseErr := ParseAutomationRunID(string(run)); parseErr != nil || got != run {
		t.Fatal(got, parseErr)
	}
	for _, value := range []string{strings.ToUpper(string(id)), "aut_01900000000070008000000000000001", "aut_01900000-0000-4000-8000-000000000001", "aut_01900000-0000-7000-c000-000000000001", string(run), "aut_{01900000-0000-7000-8000-000000000001}"} {
		if _, err = ParseAutomationID(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	if _, err = ParseAutomationRunID("run_01900000-0000-7000-8000-000000000001"); err == nil {
		t.Fatal("accepted adapter runtime ID")
	}
	for _, key := range []string{"", strings.Repeat("a", 129), "space key", "tab\tkey", "line\nkey", "\x7f", "café"} {
		if err = ValidateAutomationIdempotencyKey(key); err == nil {
			t.Fatalf("accepted key %q", key)
		}
	}
	for _, key := range []string{"!~", strings.Repeat("a", 128)} {
		if err = ValidateAutomationIdempotencyKey(key); err != nil {
			t.Fatal(err)
		}
	}
}
