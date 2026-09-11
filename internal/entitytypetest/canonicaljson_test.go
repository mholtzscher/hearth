package entitytypetest_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/entitytypetest"
)

// TestCanonicalJSONComparesExactNumbers protects the numeric and ordering
// contract of the shared helpers with hand-authored JSON rather than the
// generator, so a helper regression cannot be masked by a matching generator
// omission. Numbers compare by exact rational value, so equivalent spellings
// agree and integers beyond 2^53 stay distinct from their neighbors; numbers
// never collide with their quoted spelling; object order and whitespace are
// insignificant while array order is significant.
func TestCanonicalJSONComparesExactNumbers(t *testing.T) {
	t.Parallel()
	equal := []struct{ name, left, right string }{
		{name: "integer and decimal", left: "1", right: "1.0"},
		{name: "integer and exponent", left: "1", right: "1e0"},
		{name: "negative zero and zero", left: "-0", right: "0"},
		{
			name:  "beyond 2^53 integer and exponent",
			left:  `{"value":9007199254740993}`,
			right: `{"value":9.007199254740993e15}`,
		},
		{name: "object key order", left: `{"a":1,"b":2}`, right: `{"b":2,"a":1}`},
		{name: "insignificant whitespace", left: "{ \"a\" : [ 1 , 2 ] }", right: `{"a":[1,2]}`},
	}
	for _, example := range equal {
		t.Run(example.name, func(t *testing.T) {
			t.Parallel()
			if !entitytypetest.EqualJSON(t, []byte(example.left), []byte(example.right)) {
				t.Errorf("EqualJSON(%s, %s) = false, want true", example.left, example.right)
			}
		})
	}
	unequal := []struct{ name, left, right string }{
		{name: "adjacent beyond 2^53", left: "9007199254740993", right: "9007199254740992"},
		{name: "number and string", left: "75", right: `"75"`},
		{name: "nested number and string", left: `{"value":1}`, right: `{"value":"1"}`},
		{name: "array order", left: "[1,2]", right: "[2,1]"},
		{name: "distinct object keys", left: `{"a":1}`, right: `{"b":1}`},
		{name: "malformed input", left: "{", right: "{}"},
	}
	for _, example := range unequal {
		t.Run(example.name, func(t *testing.T) {
			t.Parallel()
			if entitytypetest.EqualJSON(t, []byte(example.left), []byte(example.right)) {
				t.Errorf("EqualJSON(%s, %s) = true, want false", example.left, example.right)
			}
		})
	}
}

// TestCanonicalValueMatchesCanonicalJSON protects the two spellings of the same
// normalization: a Go value marshaled by encoding/json must compare equal to
// the equivalent raw JSON text, including an integer beyond 2^53.
func TestCanonicalValueMatchesCanonicalJSON(t *testing.T) {
	t.Parallel()
	type sample struct {
		Count  int64    `json:"count"`
		Labels []string `json:"labels"`
	}
	raw := json.RawMessage(`{"labels":["a","b"],"count":9007199254740993}`)
	value := sample{Count: 9007199254740993, Labels: []string{"a", "b"}}
	if got, want := entitytypetest.CanonicalValue(t, value), entitytypetest.CanonicalJSON(t, raw); got != want {
		t.Errorf("CanonicalValue = %s, CanonicalJSON = %s", got, want)
	}
}

const canonicalScenarioEnv = "HEARTH_ENTITYTYPETEST_CANONICAL_SCENARIO"

// TestCanonicalJSONRejectsMalformedInput protects the fatal branch generated
// tests rely on: CanonicalJSON fails instead of returning a normalized string
// when its input is not JSON. Rejection runs in a subprocess because the helper
// fails with t.Fatalf.
func TestCanonicalJSONRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	if os.Getenv(canonicalScenarioEnv) != "" {
		entitytypetest.CanonicalJSON(t, json.RawMessage("{"))
		t.Fatal("malformed JSON was accepted")
		return
	}
	command := exec.Command(os.Args[0], "-test.run", "^TestCanonicalJSONRejectsMalformedInput$", "-test.count=1")
	command.Env = append(os.Environ(), canonicalScenarioEnv+"=1")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("malformed JSON passed, want failure:\n%s", output)
	}
	if !strings.Contains(string(output), "decode JSON:") {
		t.Fatalf("output does not contain %q:\n%s", "decode JSON:", output)
	}
}
