//nolint:testpackage // Exercises the worker's private Step-scoped input resolver.
package automations

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func nestedInputScope(t *testing.T, step Step) (stepInputs, Step) {
	t.Helper()
	definition := flowDefinition(t, nil, []Step{{ID: "route", Body: IfStep{
		Conditions: Condition{ID: "yes", Body: ExpressionCondition{Value: flowExpression("true")}},
		Then:       []Step{step},
	}}})
	values, err := newRunValues(flowRun(definition))
	require.NoError(t, err)
	prepared := definition.Steps[0].Body.(IfStep).Then[0]
	return values.inputsFor(definition, prepared.ID), prepared
}

func TestStepPayloadResolutionKeepsLiteralJSONOpaqueAndOwned(t *testing.T) {
	t.Parallel()
	const raw = ` {"expr":"vars.not_code","large":9007199254740993,"huge":9223372036854775808,"tiny":1e-999} `
	resolver, step := nestedInputScope(t, Step{ID: "command", Body: CommandStep{
		EntityID: "ent_01980000-0000-7000-8000-000000000001", OperationName: "set",
		Parameters: flowLiteral(raw),
	}})
	binding := step.Body.(CommandStep).Parameters
	resolved, err := resolver.payload(t.Context(), "parameters", binding)
	require.NoError(t, err)
	require.True(t, bytes.Equal([]byte(raw), resolved), "opaque source tokens changed")
	resolved[0] = '['
	require.True(t, bytes.Equal([]byte(raw), binding.Body.(LiteralValue).Value), "result aliases the definition")
	require.Empty(t, resolver.values.env.Vars)
	require.Empty(t, resolver.values.env.Steps)
	for _, size := range []int{automationValueMaxBytes, automationValueMaxBytes + 1} {
		literal := flowLiteral(`"` + strings.Repeat("x", size-2) + `"`)
		resolved, err = resolver.payload(t.Context(), "parameters", literal)
		if size == automationValueMaxBytes {
			require.NoError(t, err)
			require.Len(t, resolved, size)
			continue
		}
		require.Nil(t, resolved)
		failure := valueFailure(resolver.path, err, nil)
		require.Equal(t, "/steps/0/then/0/parameters", failure.Path)
		require.Equal(t, "expression_limit", failure.FailureCode)
	}
}

func TestStepPayloadResolutionLocatesExpressionFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		source string
		want   string
		code   string
	}{
		{`{value: 9007199254740993}`, `{"value":9007199254740993}`, ""},
		{`vars.private_value.nested`, "", "expression_evaluation"},
		{`1..129`, "", "expression_limit"},
	} {
		t.Run(test.source, func(t *testing.T) {
			t.Parallel()
			resolver, step := nestedInputScope(t, Step{ID: "function", Body: FunctionStep{
				AdapterID: "weather", FunctionKey: "forecast",
				ContractFingerprint: "sha256-v1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Arguments:           flowExpression(test.source),
			}})
			resolved, err := resolver.payload(t.Context(), "arguments", step.Body.(FunctionStep).Arguments)
			if test.code == "" {
				require.NoError(t, err)
				require.True(t, bytes.Equal([]byte(test.want), resolved), "resolved number changed")
				return
			}
			require.Error(t, err)
			require.Nil(t, resolved)
			failure := valueFailure(resolver.path, err, nil)
			require.Equal(t, "/steps/0/then/0/arguments_expr", failure.Path)
			require.Equal(t, test.code, failure.FailureCode)
			require.NotContains(t, err.Error(), "private_value")
			if test.code == "expression_evaluation" {
				require.NotNil(t, failure.SourceStart)
				require.NotNil(t, failure.SourceEnd)
				require.Greater(t, *failure.SourceEnd, *failure.SourceStart)
				require.LessOrEqual(t, *failure.SourceEnd, len(test.source))
			}
		})
	}
}

func TestStepTextResolutionRequiresStringsAndLocatesFields(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		binding ValueBinding
		want    string
		code    string
	}{
		{flowLiteral(`"Hello\nworld"`), "Hello\nworld", ""},
		{flowExpression(`"Target " + string(9007199254740993)`), "Target 9007199254740993", ""},
		{flowExpression(`42`), "", failureResolvedInputInvalid},
		{flowExpression(`nil`), "", failureResolvedInputInvalid},
		{flowExpression(`{value: "not a string"}`), "", failureResolvedInputInvalid},
	} {
		for _, field := range []string{"title", "message"} {
			body := NotificationStep{DestinationID: "ndst_01980000-0000-7000-8000-000000000001",
				Title: flowLiteral(`"Title"`), Message: flowLiteral(`"Message"`)}
			if field == "title" {
				body.Title = test.binding
			} else {
				body.Message = test.binding
			}
			resolver, step := nestedInputScope(t, Step{ID: "notify", Body: body})
			prepared := step.Body.(NotificationStep)
			binding := prepared.Title
			if field == "message" {
				binding = prepared.Message
			}
			resolved, err := resolver.text(t.Context(), field, binding)
			if test.code == "" {
				require.NoError(t, err)
				require.Equal(t, test.want, resolved.value)
				var encoded string
				require.NoError(t, json.Unmarshal(resolved.raw, &encoded))
				require.Equal(t, resolved.value, encoded)
				continue
			}
			require.Error(t, err)
			require.Empty(t, resolved.value)
			require.Nil(t, resolved.raw)
			failure := valueFailure(resolver.path, err, nil)
			require.Equal(t, "/steps/0/then/0/"+field+"_expr", failure.Path)
			require.Equal(t, test.code, failure.FailureCode)
		}
	}
}

func TestStepInputResolutionHonorsCancellationForLiteralsAndExpressions(t *testing.T) {
	t.Parallel()
	for _, lifetime := range []string{"call", "evaluation", "stop"} {
		t.Run(lifetime, func(t *testing.T) {
			t.Parallel()
			resolver, step := nestedInputScope(t, Step{ID: "notify", Body: NotificationStep{
				DestinationID: "ndst_01980000-0000-7000-8000-000000000001",
				Title:         flowLiteral(`"Literal"`), Message: flowExpression(`"Expression"`),
			}})
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			ctx := t.Context()
			switch lifetime {
			case "call":
				ctx = canceled
			case "evaluation":
				resolver.values.evaluationContext = canceled
			case "stop":
				resolver.values.stopContext = canceled
			}
			body := step.Body.(NotificationStep)
			for _, binding := range []ValueBinding{body.Title, body.Message} {
				raw, err := resolver.payload(ctx, "arguments", binding)
				require.ErrorIs(t, err, context.Canceled)
				require.Nil(t, raw)
				text, err := resolver.text(ctx, "title", binding)
				require.ErrorIs(t, err, context.Canceled)
				require.Empty(t, text.value)
				require.Nil(t, text.raw)
			}
		})
	}
}
