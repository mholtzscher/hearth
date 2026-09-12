package devices_test

import (
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestTypedIDsGenerateCanonicalUUIDv7(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		prefix string
		newID  func() (string, error)
		parse  func(string) error
	}{
		{
			"device",
			"dev_",
			func() (string, error) { value, err := devices.NewDeviceID(); return string(value), err },
			func(value string) error { _, err := devices.ParseDeviceID(value); return err },
		},
		{
			"entity",
			"ent_",
			func() (string, error) { value, err := devices.NewEntityID(); return string(value), err },
			func(value string) error { _, err := devices.ParseEntityID(value); return err },
		},
		{
			"observation",
			"obs_",
			func() (string, error) { value, err := devices.NewObservationID(); return string(value), err },
			func(value string) error { _, err := devices.ParseObservationID(value); return err },
		},
		{
			"command",
			"cmd_",
			func() (string, error) { value, err := devices.NewCommandID(); return string(value), err },
			func(value string) error { _, err := devices.ParseCommandID(value); return err },
		},
		{
			"correlation",
			"cor_",
			func() (string, error) { value, err := devices.NewCorrelationID(); return string(value), err },
			func(value string) error { _, err := devices.ParseCorrelationID(value); return err },
		},
		{
			"device fact",
			"fct_",
			func() (string, error) { value, err := devices.NewDeviceFactID(); return string(value), err },
			func(value string) error { _, err := devices.ParseDeviceFactID(value); return err },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value, generationErr := test.newID()
			if generationErr != nil {
				t.Fatal(generationErr)
			}
			if !strings.HasPrefix(value, test.prefix) {
				t.Fatalf("ID %q does not have prefix %q", value, test.prefix)
			}
			if err := test.parse(value); err != nil {
				t.Fatalf("parse generated ID: %v", err)
			}
		})
	}
}

func TestParseRuntimeIDRejectsNonCanonicalOrWrongVersion(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"run_01890f47-7a6b-4c4d-8e9f-0123456789ab",
		"run_01890F47-7A6B-7C4D-8E9F-0123456789AB",
		"run_not-a-uuid",
	} {
		if _, err := devices.ParseRuntimeID(value); err == nil {
			t.Fatalf("devices.ParseRuntimeID(%q) unexpectedly succeeded", value)
		}
	}
}

func TestParseEntityIDRejectsNonCanonicalOrWrongVersion(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"dev_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"ent_01890f47-7a6b-4c4d-8e9f-0123456789ab",
		"ent_01890F47-7A6B-7C4D-8E9F-0123456789AB",
		"ent_not-a-uuid",
	} {
		if _, err := devices.ParseEntityID(value); err == nil {
			t.Fatalf("devices.ParseEntityID(%q) unexpectedly succeeded", value)
		}
	}
}

// TestParseDeviceFactIDRejectsNonCanonicalOrWrongVersion keeps the durable fact
// envelope identity as strict as every durable source identity.
func TestParseDeviceFactIDRejectsNonCanonicalOrWrongVersion(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"",
		"obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"fct_01890f47-7a6b-4c4d-8e9f-0123456789ab",
		"fct_01890f47-7a6b-7c4d-7e9f-0123456789ab",
		"fct_01890F47-7A6B-7C4D-8E9F-0123456789AB",
		"fct_not-a-uuid",
		"fct_",
	} {
		if _, err := devices.ParseDeviceFactID(value); err == nil {
			t.Fatalf("devices.ParseDeviceFactID(%q) unexpectedly succeeded", value)
		}
	}
}
