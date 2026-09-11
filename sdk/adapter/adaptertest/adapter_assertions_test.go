package adaptertest //nolint:testpackage // Table cases exercise the unexported classification and parsing helpers directly.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// TestIsValidationErrorClassifiesErrors protects rejection classification:
// only adapter validation errors, including wrapped ones, count as validation
// failures, so a plain or unrelated error cannot pass a generated rejection
// check.
func TestIsValidationErrorClassifiesErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "plain", err: errors.New("boom"), want: false},
		{
			name: "unrelated typed",
			err:  &os.PathError{Op: "open", Path: "state.json", Err: errors.New("missing")},
			want: false,
		},
		{name: "direct", err: &adapter.ValidationError{Err: errors.New("bad support")}, want: true},
		{
			name: "wrapped",
			err:  fmt.Errorf("observation: %w", &adapter.ValidationError{Err: errors.New("bad support")}),
			want: true,
		},
	}
	for _, example := range cases {
		t.Run(example.name, func(t *testing.T) {
			t.Parallel()
			if got := isValidationError(example.err); got != example.want {
				t.Errorf("isValidationError(%v) = %v, want %v", example.err, got, example.want)
			}
		})
	}
}

// TestParseAdapterTimeRequiresRFC3339NanoUTC protects the UTC timestamp
// contract: valid RFC3339Nano text with a UTC offset parses to the instant,
// including fractional seconds, while malformed text or any non-UTC offset is
// rejected with the field named.
func TestParseAdapterTimeRequiresRFC3339NanoUTC(t *testing.T) {
	t.Parallel()
	valid := []struct {
		name string
		raw  string
		want time.Time
	}{
		{
			name: "utc",
			raw:  "2026-03-10T14:30:00Z",
			want: time.Date(2026, time.March, 10, 14, 30, 0, 0, time.UTC),
		},
		{
			name: "nanoseconds",
			raw:  "2026-03-10T14:30:00.123456789Z",
			want: time.Date(2026, time.March, 10, 14, 30, 0, 123456789, time.UTC),
		},
		{
			name: "explicit zero offset",
			raw:  "2026-03-10T14:30:00+00:00",
			want: time.Date(2026, time.March, 10, 14, 30, 0, 0, time.UTC),
		},
	}
	for _, example := range valid {
		t.Run(example.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseAdapterTime("adapter received time", example.raw)
			if err != nil {
				t.Fatalf("parseAdapterTime(%q) error = %v", example.raw, err)
			}
			if !got.Equal(example.want) {
				t.Errorf("parseAdapterTime(%q) = %s, want %s", example.raw, got, example.want)
			}
		})
	}
	invalid := []struct {
		name   string
		raw    string
		reason string
	}{
		{name: "malformed", raw: "2026-03-10 14:30:00", reason: "is not RFC3339Nano"},
		{name: "positive offset", raw: "2026-03-10T14:30:00+01:00", reason: "is not formatted as UTC"},
		{name: "negative offset", raw: "2026-03-09T08:15:30-05:00", reason: "is not formatted as UTC"},
	}
	for _, example := range invalid {
		t.Run(example.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseAdapterTime("source updated time", example.raw)
			if err == nil {
				t.Fatalf("parseAdapterTime(%q) accepted an invalid timestamp", example.raw)
			}
			if !strings.Contains(err.Error(), example.reason) {
				t.Errorf("error = %v, want it to contain %q", err, example.reason)
			}
			if !strings.Contains(err.Error(), "source updated time") {
				t.Errorf("error = %v, want it to name the field", err)
			}
		})
	}
}

// TestAssertionsAcceptMetContracts protects that the fatal wrappers stay silent
// on the contracts they are meant to accept, returning the parsed instant.
func TestAssertionsAcceptMetContracts(t *testing.T) {
	t.Parallel()
	RequireValidationError(t, &adapter.ValidationError{Err: errors.New("bad support")}, "reject invalid Entity support")
	received := ParseAdapterTime(t, "adapter received time", "2026-03-10T14:30:00Z")
	if want := time.Date(2026, time.March, 10, 14, 30, 0, 0, time.UTC); !received.Equal(want) {
		t.Errorf("ParseAdapterTime = %s, want %s", received, want)
	}
}

const assertionScenarioEnv = "HEARTH_ADAPTERTEST_SCENARIO"

// TestAssertionsFailOnUnmetContracts protects the fatal wrappers: both
// RequireValidationError and ParseAdapterTime must fail the test, naming the action or
// field, when their contract is unmet. Rejection runs in a subprocess because
// the wrappers fail with t.Fatalf.
func TestAssertionsFailOnUnmetContracts(t *testing.T) {
	t.Parallel()
	if scenario := os.Getenv(assertionScenarioEnv); scenario != "" {
		switch scenario {
		case "validation-error":
			RequireValidationError(t, errors.New("plain failure"), "reject support-incompatible State")
		case "time-utc":
			ParseAdapterTime(t, "adapter received time", "2026-03-10T14:30:00+01:00")
		default:
			t.Fatalf("unknown scenario %q", scenario)
		}
		t.Fatalf("scenario %s unexpectedly passed", scenario)
		return
	}
	scenarios := []struct{ name, want string }{
		{
			name: "validation-error",
			want: "reject support-incompatible State: expected adapter validation error",
		},
		{
			name: "time-utc",
			want: `adapter received time "2026-03-10T14:30:00+01:00" is not formatted as UTC`,
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			command := exec.Command(os.Args[0], "-test.run", "^TestAssertionsFailOnUnmetContracts$", "-test.count=1")
			command.Env = append(os.Environ(), assertionScenarioEnv+"="+scenario.name)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("scenario %s passed, want failure:\n%s", scenario.name, output)
			}
			if !strings.Contains(string(output), scenario.want) {
				t.Fatalf("scenario %s output does not contain %q:\n%s", scenario.name, scenario.want, output)
			}
		})
	}
}
