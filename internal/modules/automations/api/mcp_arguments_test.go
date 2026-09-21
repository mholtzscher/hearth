package api //nolint:testpackage // Tests exercise package-private argument decoders.

import (
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// TestMCPArgumentDecodersPrefixEveryFailureCode proves each custom scalar decoder
// rejects malformed input with the stable failure code the Huma handler would
// report.
//
// The code has to ride the message because the SDK renders a decoding failure as
// the tool result text, and a schema-invalid value is rejected before the decoder
// runs. The non-string cases are asserted here for exactly that reason: the
// prefix is the decoder's contract, so a value that reaches it directly — or a
// schema that stops constraining the JSON type — cannot change the code an agent
// branches on.
func TestMCPArgumentDecodersPrefixEveryFailureCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		data      string
		unmarshal func([]byte) error
		code      string
	}{
		{
			"automation ID that is not a JSON string",
			`7`,
			func(data []byte) error { var value mcpAutomationID; return value.UnmarshalJSON(data) },
			"invalid_automation_id",
		},
		{
			"automation ID that is not canonical",
			`"not-an-id"`,
			func(data []byte) error { var value mcpAutomationID; return value.UnmarshalJSON(data) },
			"invalid_automation_id",
		},
		{
			"entry ID that is not a JSON string",
			`7`,
			func(data []byte) error { var value mcpEntryID; return value.UnmarshalJSON(data) },
			"invalid_entry_id",
		},
		{
			"entry ID that is not canonical",
			`"not-an-id"`,
			func(data []byte) error { var value mcpEntryID; return value.UnmarshalJSON(data) },
			"invalid_entry_id",
		},
		{
			"limit that is not an integer",
			`"lots"`,
			func(data []byte) error { var value mcpPageLimit; return value.UnmarshalJSON(data) },
			"invalid_limit",
		},
		{
			"limit that is fractional",
			`1.5`,
			func(data []byte) error { var value mcpPageLimit; return value.UnmarshalJSON(data) },
			"invalid_limit",
		},
		{
			"limit below the accepted range",
			`0`,
			func(data []byte) error { var value mcpPageLimit; return value.UnmarshalJSON(data) },
			"invalid_limit",
		},
		{
			"limit above the accepted range",
			`201`,
			func(data []byte) error { var value mcpPageLimit; return value.UnmarshalJSON(data) },
			"invalid_limit",
		},
		{
			"revision that is not an integer",
			`"one"`,
			func(data []byte) error { var value mcpExpectedRevision; return value.UnmarshalJSON(data) },
			"invalid_revision",
		},
		{
			"revision below the accepted range",
			`-1`,
			func(data []byte) error { var value mcpExpectedRevision; return value.UnmarshalJSON(data) },
			"invalid_revision",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.unmarshal([]byte(test.data))
			if err == nil {
				t.Fatalf("decoding %s succeeded, want a rejection", test.data)
			}
			message := err.Error()
			if !strings.HasPrefix(message, test.code+": ") {
				t.Fatalf("decoding %s error = %q, want the %q prefix", test.data, message, test.code)
			}
			if detail := strings.TrimPrefix(message, test.code+": "); detail == "" {
				t.Fatalf("decoding %s error = %q, want a reason after the %q prefix", test.data, message, test.code)
			}
		})
	}
}

// TestMCPArgumentDecodersAcceptEveryValidScalar proves the range and identity
// checks reject only what the Huma route would reject: the accepted boundary
// values still decode, and an omitted limit keeps the default page size.
func TestMCPArgumentDecodersAcceptEveryValidScalar(t *testing.T) {
	t.Parallel()
	automation, err := automations.NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}
	run, err := automations.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	skip, err := automations.NewSkipID()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		data      string
		unmarshal func([]byte) error
	}{
		{
			"automation ID",
			`"` + string(automation) + `"`,
			func(data []byte) error { var value mcpAutomationID; return value.UnmarshalJSON(data) },
		},
		{
			"Run entry ID",
			`"` + string(run) + `"`,
			func(data []byte) error { var value mcpEntryID; return value.UnmarshalJSON(data) },
		},
		{
			"Skip entry ID",
			`"` + string(skip) + `"`,
			func(data []byte) error { var value mcpEntryID; return value.UnmarshalJSON(data) },
		},
		{
			"minimum limit",
			`1`,
			func(data []byte) error { var value mcpPageLimit; return value.UnmarshalJSON(data) },
		},
		{
			"maximum limit",
			`200`,
			func(data []byte) error { var value mcpPageLimit; return value.UnmarshalJSON(data) },
		},
		{
			"minimum revision",
			`1`,
			func(data []byte) error { var value mcpExpectedRevision; return value.UnmarshalJSON(data) },
		},
		{
			"revision above the float64 integer range",
			`9007199254740993`,
			func(data []byte) error { var value mcpExpectedRevision; return value.UnmarshalJSON(data) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if unmarshalErr := test.unmarshal([]byte(test.data)); unmarshalErr != nil {
				t.Fatalf("decoding %s: %v", test.data, unmarshalErr)
			}
		})
	}
	var omitted mcpPageLimit
	if size := omitted.pageSize(); size != mcpPageDefaultLimit {
		t.Fatalf("omitted limit page size = %d, want %d", size, mcpPageDefaultLimit)
	}
}
