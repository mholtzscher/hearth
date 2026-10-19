package natswire_test

import (
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

func TestFunctionSubjectsUseExactRuntimeRoutes(t *testing.T) {
	t.Parallel()
	runtimeID := "run_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	registration := "hearth.v1.adapter.zigbee-main.runtime." + runtimeID + ".function-register"
	invoke := "hearth.v1.adapter.zigbee-main.runtime." + runtimeID + ".function-invoke"
	for _, tt := range []struct {
		name, expected string
		build          func(string, string) (string, error)
		parse          func(string) (string, string, error)
	}{
		{"registration", registration, natswire.FunctionRegistrationSubject, func(subject string) (string, string, error) {
			route, err := natswire.ParseFunctionRegistrationSubject(subject)
			return route.AdapterID, route.RuntimeID, err
		}},
		{"invoke", invoke, natswire.FunctionInvokeSubject, func(subject string) (string, string, error) {
			route, err := natswire.ParseFunctionInvokeSubject(subject)
			return route.AdapterID, route.RuntimeID, err
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.build("zigbee-main", runtimeID)
			if err != nil || got != tt.expected {
				t.Fatalf("subject=%q, err=%v; want %q", got, err, tt.expected)
			}
			adapter, runtime, err := tt.parse(tt.expected)
			if err != nil || adapter != "zigbee-main" || runtime != runtimeID {
				t.Fatalf("route=(%q,%q), err=%v", adapter, runtime, err)
			}
			for _, invalid := range []string{
				tt.expected + ".extra", tt.expected[:strings.LastIndex(tt.expected, ".")],
				strings.Replace(tt.expected, "hearth.v1", "hearth.v2", 1),
				strings.Replace(tt.expected, ".adapter.", ".core.", 1),
				strings.Replace(tt.expected, ".runtime.", ".wrong.", 1),
				strings.Replace(tt.expected, "zigbee-main", "bad.key", 1),
				strings.Replace(tt.expected, "zigbee-main", "*", 1),
				strings.Replace(tt.expected, runtimeID, ">", 1),
				strings.Replace(tt.expected, "-7c4d-", "-4c4d-", 1),
				strings.Replace(tt.expected, "-8e9f-", "-7e9f-", 1),
				strings.Replace(tt.expected, ".function-", ".function.", 1),
				strings.Replace(tt.expected, ".function-", ".notification-", 1),
			} {
				if _, _, parseErr := tt.parse(invalid); parseErr == nil {
					t.Errorf("invalid subject accepted: %q", invalid)
				}
			}
		})
	}
	if _, err := natswire.ParseFunctionRegistrationSubject(invoke); err == nil {
		t.Fatal("invoke route accepted as registration")
	}
	if _, err := natswire.ParseFunctionInvokeSubject(registration); err == nil {
		t.Fatal("registration route accepted as invoke")
	}
	wildcard := natswire.FunctionRegistrationWildcard()
	if wildcard != "hearth.v1.adapter.*.runtime.*.function-register" {
		t.Fatalf("registration subscription wildcard=%q", wildcard)
	}
}

func TestFunctionSubjectBuildersRejectInvalidIdentities(t *testing.T) {
	t.Parallel()
	runtimeID := "run_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	for _, build := range []func(string, string) (string, error){natswire.FunctionRegistrationSubject, natswire.FunctionInvokeSubject} {
		for _, invalid := range []struct{ adapter, runtime string }{
			{"", runtimeID}, {"*", runtimeID}, {"bad.key", runtimeID}, {strings.Repeat("a", 64), runtimeID},
			{"zigbee-main", ""}, {"zigbee-main", "*"}, {"zigbee-main", strings.Replace(runtimeID, "run_", "afi_", 1)},
		} {
			if _, err := build(invalid.adapter, invalid.runtime); err == nil {
				t.Errorf("invalid route accepted: %#v", invalid)
			}
		}
	}
}
