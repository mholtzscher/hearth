package adapters_test

import (
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestParseRuntimeIDRejectsNonCanonicalOrWrongVersion(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"run_01890f47-7a6b-4c4d-8e9f-0123456789ab",
		"run_01890F47-7A6B-7C4D-8E9F-0123456789AB",
		"run_not-a-uuid",
	} {
		if _, err := adapters.ParseRuntimeID(value); err == nil {
			t.Fatalf("adapters.ParseRuntimeID(%q) unexpectedly succeeded", value)
		}
	}
}
