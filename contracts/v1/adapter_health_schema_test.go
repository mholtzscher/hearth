package v1_test

import (
	"strings"
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
)

// The schema is the language-neutral owner of required/null semantics and exact
// int64 revisions; these are not domain-validator or marshaler assertions.
func TestAdapterHealthSchema(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	valid := `{"id":"ahu_01890f47-7a6b-7c4d-8e9f-0123456789ab","schema":"urn:hearth:schema:adapter-health-update:v1","emitted_at":"2026-10-18T12:00:00Z","correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab","data":{"adapter_id":"zigbee-main","runtime_id":null,"revision":9223372036854775807,"status":"unknown","source":"core","reason_code":"hearth.starting","changed_at":"2026-10-18T12:00:00Z","evidence_at":"2026-10-18T12:00:00Z"}}`
	cases := []struct {
		name, payload string
		valid         bool
	}{
		{"nullable baseline and exact maximum", valid, true},
		{
			"healthy",
			strings.ReplaceAll(
				strings.ReplaceAll(
					strings.ReplaceAll(valid, `"status":"unknown"`, `"status":"healthy"`),
					`"runtime_id":null`,
					`"runtime_id":"run_01890f47-7a6b-7c4d-8e9f-0123456789ab"`,
				),
				`,"reason_code":"hearth.starting"`,
				"",
			),
			true,
		},
		{"overflow", strings.ReplaceAll(valid, "9223372036854775807", "9223372036854775808"), false},
		{"zero", strings.ReplaceAll(valid, "9223372036854775807", "0"), false},
		{"fractional", strings.ReplaceAll(valid, "9223372036854775807", "1.5"), false},
		{"missing runtime", strings.ReplaceAll(valid, `"runtime_id":null,`, ""), false},
		{"missing reason", strings.ReplaceAll(valid, `,"reason_code":"hearth.starting"`, ""), false},
		{
			"healthy null runtime",
			strings.ReplaceAll(
				strings.ReplaceAll(valid, `"status":"unknown"`, `"status":"healthy"`),
				`,"reason_code":"hearth.starting"`,
				"",
			),
			false,
		},
		{
			"healthy reason forbidden",
			strings.ReplaceAll(
				strings.ReplaceAll(valid, `"status":"unknown"`, `"status":"healthy"`),
				`"runtime_id":null`,
				`"runtime_id":"run_01890f47-7a6b-7c4d-8e9f-0123456789ab"`,
			),
			false,
		},
		{
			"null optional",
			strings.ReplaceAll(valid, `"source":"core"`, `"source":"core","source_observed_at":null`),
			false,
		},
		{"unknown data property", strings.ReplaceAll(valid, `"source":"core"`, `"source":"core","secret":"no"`), false},
		{"non UTC", strings.ReplaceAll(valid, "12:00:00Z", "12:00:00+00:00"), false},
		{"wrong identity", strings.ReplaceAll(valid, "ahu_", "hbt_"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			validationErr := validator.Validate(contractsv1.AdapterHealthUpdateSchemaID, []byte(tc.payload))
			if (validationErr == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, validationErr)
			}
		})
	}
}
