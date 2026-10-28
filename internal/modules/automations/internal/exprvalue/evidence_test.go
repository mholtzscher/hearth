package exprvalue_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
)

func TestNormalizeEvidenceDoesNotChangeIngressPolicy(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{
		`9223372036854776000`:  `9223372036854776000`,
		`9007199254740993`:     `9007199254740993`,
		`9223372036854775807`:  `9223372036854775807`,
		`-9223372036854775808`: `-9223372036854775808`,
		`{"n":1.0}`:            `{"n":1}`,
	} {
		got, err := exprvalue.NormalizeEvidence(json.RawMessage(raw))
		if err != nil || string(got) != want {
			t.Errorf("evidence %s = %s, %v; want %s", raw, got, err, want)
		}
	}
	_, err := exprvalue.DecodeValue(json.RawMessage(`9223372036854776000`))
	failureCode(t, err, "expression_value_invalid")
}

func TestNormalizeEvidenceRetainsValueGuards(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		raw   string
		valid bool
	}{
		{"array at", `[` + strings.Repeat(`0,`, 127) + `0]`, true},
		{"array beyond", `[` + strings.Repeat(`0,`, 128) + `0]`, false},
		{"depth at", strings.Repeat(`[`, 15) + `0` + strings.Repeat(`]`, 15), true},
		{"depth beyond", strings.Repeat(`[`, 16) + `0` + strings.Repeat(`]`, 16), false},
		{"bytes at", `"` + strings.Repeat(`x`, 65534) + `"`, true},
		{"bytes beyond", `"` + strings.Repeat(`x`, 65535) + `"`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertEvidenceBound(t, json.RawMessage(test.raw), test.valid)
		})
	}
	for _, size := range []int{128, 129} {
		object := map[string]any{}
		for i := range size {
			object[strconv.Itoa(i)] = int64(0)
		}
		raw, err := json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		assertEvidenceBound(t, raw, size == 128)
	}
	for _, leaves := range []int{8127, 8128} {
		groups := make([]any, 64)
		remaining := leaves
		for i := range groups {
			count := min(128, remaining)
			remaining -= count
			groups[i] = make([]any, count)
		}
		raw, err := json.Marshal(groups)
		if err != nil {
			t.Fatal(err)
		}
		assertEvidenceBound(t, raw, leaves == 8127)
	}
}

func assertEvidenceBound(t *testing.T, raw json.RawMessage, valid bool) {
	t.Helper()
	_, err := exprvalue.NormalizeEvidence(raw)
	if valid {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	failureCode(t, err, "expression_limit")
}

func TestNormalizeEvidenceRejectsMalformedValues(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`{"x":1,"x":2}`, `1 2`, `1e309`, `1e-400`, `1e-1000000000`, `NaN`, "\xff"} {
		_, err := exprvalue.NormalizeEvidence(json.RawMessage(raw))
		failureCode(t, err, "expression_value_invalid")
	}
}
