package natswire

import "strings"

type FunctionRegistrationRoute struct{ AdapterID, RuntimeID string }
type FunctionInvokeRoute struct{ AdapterID, RuntimeID string }

func FunctionRegistrationWildcard() string {
	return runtimeWildcard("function-register")
}

func FunctionRegistrationSubject(adapterID, runtimeID string) (string, error) {
	return runtimeSubject(adapterID, runtimeID, "function-register")
}

func FunctionInvokeSubject(adapterID, runtimeID string) (string, error) {
	return runtimeSubject(adapterID, runtimeID, "function-invoke")
}

func ParseFunctionRegistrationSubject(subject string) (FunctionRegistrationRoute, error) {
	a, r, err := parseRuntimeSubjectParts(strings.Split(subject, "."), "function-register", 0)
	return FunctionRegistrationRoute{AdapterID: a, RuntimeID: r}, err
}

func ParseFunctionInvokeSubject(subject string) (FunctionInvokeRoute, error) {
	a, r, err := parseRuntimeSubjectParts(strings.Split(subject, "."), "function-invoke", 0)
	return FunctionInvokeRoute{AdapterID: a, RuntimeID: r}, err
}
