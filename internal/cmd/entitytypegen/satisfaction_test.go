package main

import (
	"strings"
	"testing"
)

func boundedInteger(minimum, maximum string) schemaNode {
	minimumNumber := jsonNumber(minimum)
	maximumNumber := jsonNumber(maximum)
	return schemaNode{
		Type:    string(kindInteger),
		Minimum: minimumNumber,
		Maximum: maximumNumber,
	}
}

func satisfactionRoots(
	parameters, state map[string]schemaNode,
	parametersRequired, stateRequired []string,
) map[string]referenceRoot {
	return map[string]referenceRoot{
		referenceRootParameters: {
			Schema: schemaNode{
				Type:       schemaTypeObject,
				Required:   parametersRequired,
				Properties: parameters,
			},
			GoExpression: referenceRootParameters,
		},
		referenceRootState: {
			Schema: schemaNode{
				Type:       schemaTypeObject,
				Required:   stateRequired,
				Properties: state,
			},
			GoExpression: referenceRootState,
		},
	}
}

func TestIsTrueRequiresBoolean(t *testing.T) {
	t.Parallel()
	roots := satisfactionRoots(
		map[string]schemaNode{"value": {Type: string(kindBoolean)}},
		map[string]schemaNode{"active": {Type: string(kindBoolean)}},
		[]string{"value"},
		[]string{"active"},
	)
	compiled, err := compileRule(ruleManifest{
		Op:   ruleOperatorIsTrue,
		Left: referenceManifest{Root: "state", Path: "/active"},
	}, roots)
	if err != nil {
		t.Fatal(err)
	}
	if condition := ruleCondition(compiled); condition != "bool(state.Active)" {
		t.Fatalf("is_true condition = %s", condition)
	}

	for name, rule := range map[string]ruleManifest{
		"non-boolean": {
			Op:   ruleOperatorIsTrue,
			Left: referenceManifest{Root: "parameters", Path: "/value"},
		},
		"right present": {
			Op:    ruleOperatorIsTrue,
			Left:  referenceManifest{Root: "state", Path: "/active"},
			Right: &(referenceManifest{Root: "state", Path: "/active"}),
		},
		"tolerance present": {
			Op:        ruleOperatorIsTrue,
			Left:      referenceManifest{Root: "state", Path: "/active"},
			Tolerance: jsonNumber("1"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			badRoots := satisfactionRoots(
				map[string]schemaNode{"value": boundedInteger("0", "100")},
				map[string]schemaNode{"active": {Type: string(kindBoolean)}},
				[]string{"value"},
				[]string{"active"},
			)
			if _, compileErr := compileRule(rule, badRoots); compileErr == nil {
				t.Fatal("invalid is_true rule unexpectedly accepted")
			}
		})
	}
}

func TestNearValidatesBoundsAndConstants(t *testing.T) {
	t.Parallel()
	roots := satisfactionRoots(
		map[string]schemaNode{"x": boundedInteger("0", "10000")},
		map[string]schemaNode{"x": boundedInteger("0", "10000")},
		[]string{"x"},
		[]string{"x"},
	)
	compiled, err := compileRule(ruleManifest{
		Op:        ruleOperatorNear,
		Left:      referenceManifest{Root: "parameters", Path: "/x"},
		Right:     &(referenceManifest{Root: "state", Path: "/x"}),
		Tolerance: jsonNumber("1"),
	}, roots)
	if err != nil {
		t.Fatal(err)
	}
	condition := ruleCondition(compiled)
	if !strings.Contains(condition, "int64(parameters.X) >= int64(state.X)") ||
		!strings.Contains(condition, "<= 1") {
		t.Fatalf("near condition = %s", condition)
	}

	for name, rule := range map[string]ruleManifest{
		"missing tolerance": {
			Op:    ruleOperatorNear,
			Left:  referenceManifest{Root: "parameters", Path: "/x"},
			Right: &(referenceManifest{Root: "state", Path: "/x"}),
		},
		"negative tolerance": {
			Op:        ruleOperatorNear,
			Left:      referenceManifest{Root: "parameters", Path: "/x"},
			Right:     &(referenceManifest{Root: "state", Path: "/x"}),
			Tolerance: jsonNumber("-1"),
		},
		"fractional tolerance": {
			Op:        ruleOperatorNear,
			Left:      referenceManifest{Root: "parameters", Path: "/x"},
			Right:     &(referenceManifest{Root: "state", Path: "/x"}),
			Tolerance: jsonNumber("1.5"),
		},
		"modulus present": {
			Op:        ruleOperatorNear,
			Left:      referenceManifest{Root: "parameters", Path: "/x"},
			Right:     &(referenceManifest{Root: "state", Path: "/x"}),
			Tolerance: jsonNumber("1"),
			Modulus:   jsonNumber("360"),
		},
		"missing right": {
			Op:        ruleOperatorNear,
			Left:      referenceManifest{Root: "parameters", Path: "/x"},
			Tolerance: jsonNumber("1"),
		},
		"unknown operator": {
			Op:        "almost",
			Left:      referenceManifest{Root: "parameters", Path: "/x"},
			Right:     &(referenceManifest{Root: "state", Path: "/x"}),
			Tolerance: jsonNumber("1"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, compileErr := compileRule(rule, roots); compileErr == nil {
				t.Fatal("invalid near rule unexpectedly accepted")
			}
		})
	}
}

func TestNearRejectsUnboundedNegativeAndOptionalOperands(t *testing.T) {
	t.Parallel()
	for name, schemas := range map[string]struct {
		parameters map[string]schemaNode
		state      map[string]schemaNode
	}{
		"negative minimum": {
			parameters: map[string]schemaNode{"x": boundedInteger("-1", "100")},
			state:      map[string]schemaNode{"x": boundedInteger("0", "100")},
		},
		"unbounded": {
			parameters: map[string]schemaNode{"x": {Type: string(kindInteger)}},
			state:      map[string]schemaNode{"x": boundedInteger("0", "100")},
		},
		"non-integer": {
			parameters: map[string]schemaNode{"x": {Type: string(kindNumber)}},
			state:      map[string]schemaNode{"x": boundedInteger("0", "100")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			roots := satisfactionRoots(schemas.parameters, schemas.state, []string{"x"}, []string{"x"})
			rule := ruleManifest{
				Op:        ruleOperatorNear,
				Left:      referenceManifest{Root: "parameters", Path: "/x"},
				Right:     &(referenceManifest{Root: "state", Path: "/x"}),
				Tolerance: jsonNumber("1"),
			}
			if _, err := compileRule(rule, roots); err == nil {
				t.Fatal("invalid near operand unexpectedly accepted")
			}
		})
	}

	optionalRoots := map[string]referenceRoot{
		referenceRootParameters: {
			Schema: schemaNode{
				Type:       schemaTypeObject,
				Required:   []string{},
				Properties: map[string]schemaNode{"x": boundedInteger("0", "100")},
			},
			GoExpression: referenceRootParameters,
		},
		referenceRootState: {
			Schema: schemaNode{
				Type:       schemaTypeObject,
				Required:   []string{"x"},
				Properties: map[string]schemaNode{"x": boundedInteger("0", "100")},
			},
			GoExpression: referenceRootState,
		},
	}
	if _, err := compileRule(ruleManifest{
		Op:        ruleOperatorNear,
		Left:      referenceManifest{Root: "parameters", Path: "/x"},
		Right:     &(referenceManifest{Root: "state", Path: "/x"}),
		Tolerance: jsonNumber("1"),
	}, optionalRoots); err == nil {
		t.Fatal("optional near operand unexpectedly accepted")
	}
}

func TestCircularNearValidatesDomain(t *testing.T) {
	t.Parallel()
	roots := satisfactionRoots(
		map[string]schemaNode{"hue": boundedInteger("0", "359")},
		map[string]schemaNode{"hue": boundedInteger("0", "359")},
		[]string{"hue"},
		[]string{"hue"},
	)
	compiled, err := compileRule(ruleManifest{
		Op:        ruleOperatorCircularNear,
		Left:      referenceManifest{Root: "parameters", Path: "/hue"},
		Right:     &(referenceManifest{Root: "state", Path: "/hue"}),
		Tolerance: jsonNumber("2"),
		Modulus:   jsonNumber("360"),
	}, roots)
	if err != nil {
		t.Fatal(err)
	}
	condition := ruleCondition(compiled)
	if !strings.Contains(condition, "360-d <= 2") || !strings.Contains(condition, "d <= 2") {
		t.Fatalf("circular_near condition = %s", condition)
	}

	for name, rule := range map[string]ruleManifest{
		"tolerance too large": {
			Op:        ruleOperatorCircularNear,
			Left:      referenceManifest{Root: "parameters", Path: "/hue"},
			Right:     &(referenceManifest{Root: "state", Path: "/hue"}),
			Tolerance: jsonNumber("180"),
			Modulus:   jsonNumber("360"),
		},
		"tolerance equal half": {
			Op:        ruleOperatorCircularNear,
			Left:      referenceManifest{Root: "parameters", Path: "/hue"},
			Right:     &(referenceManifest{Root: "state", Path: "/hue"}),
			Tolerance: jsonNumber("180"),
			Modulus:   jsonNumber("360"),
		},
		"zero modulus": {
			Op:        ruleOperatorCircularNear,
			Left:      referenceManifest{Root: "parameters", Path: "/hue"},
			Right:     &(referenceManifest{Root: "state", Path: "/hue"}),
			Tolerance: jsonNumber("2"),
			Modulus:   jsonNumber("0"),
		},
		"fractional modulus": {
			Op:        ruleOperatorCircularNear,
			Left:      referenceManifest{Root: "parameters", Path: "/hue"},
			Right:     &(referenceManifest{Root: "state", Path: "/hue"}),
			Tolerance: jsonNumber("2"),
			Modulus:   jsonNumber("360.5"),
		},
		"missing modulus": {
			Op:        ruleOperatorCircularNear,
			Left:      referenceManifest{Root: "parameters", Path: "/hue"},
			Right:     &(referenceManifest{Root: "state", Path: "/hue"}),
			Tolerance: jsonNumber("2"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, compileErr := compileRule(rule, roots); compileErr == nil {
				t.Fatal("invalid circular_near rule unexpectedly accepted")
			}
		})
	}
}

func TestCircularNearRejectsOperandsAtOrAboveModulus(t *testing.T) {
	t.Parallel()
	roots := satisfactionRoots(
		map[string]schemaNode{"hue": boundedInteger("0", "360")},
		map[string]schemaNode{"hue": boundedInteger("0", "359")},
		[]string{"hue"},
		[]string{"hue"},
	)
	rule := ruleManifest{
		Op:        ruleOperatorCircularNear,
		Left:      referenceManifest{Root: "parameters", Path: "/hue"},
		Right:     &(referenceManifest{Root: "state", Path: "/hue"}),
		Tolerance: jsonNumber("2"),
		Modulus:   jsonNumber("360"),
	}
	if _, err := compileRule(rule, roots); err == nil {
		t.Fatal("operand at modulus unexpectedly accepted")
	}
}

func TestCircularNearToleranceCheckIsOverflowSafe(t *testing.T) {
	t.Parallel()
	roots := satisfactionRoots(
		map[string]schemaNode{"hue": boundedInteger("0", "359")},
		map[string]schemaNode{"hue": boundedInteger("0", "359")},
		[]string{"hue"},
		[]string{"hue"},
	)
	// tolerance*2 would overflow int64 here (2^62*2 wraps negative) and a
	// naive check could accept the domain. The safe comparison must still
	// reject 0 <= tolerance < modulus/2.
	rule := ruleManifest{
		Op:        ruleOperatorCircularNear,
		Left:      referenceManifest{Root: "parameters", Path: "/hue"},
		Right:     &(referenceManifest{Root: "state", Path: "/hue"}),
		Tolerance: jsonNumber("4611686018427387904"),
		Modulus:   jsonNumber("360"),
	}
	if _, err := compileRule(rule, roots); err == nil {
		t.Fatal("overflowing tolerance unexpectedly accepted")
	}
}

func TestSatisfactionRendersConjunction(t *testing.T) {
	t.Parallel()
	rules := []ruleModel{
		{
			Op:   ruleOperatorIsTrue,
			Left: referenceModel{Kind: kindBoolean, GoExpression: "state.Active"},
		},
		{
			Op:       "eq",
			Left:     referenceModel{Kind: kindInteger, GoExpression: "parameters.Value"},
			Right:    referenceModel{Kind: kindInteger, GoExpression: "state.Value"},
			HasRight: true,
		},
	}
	condition := satisfactionCondition(rules)
	if condition != "(bool(state.Active)) && (int64(parameters.Value) == int64(state.Value))" {
		t.Fatalf("conjunction = %s", condition)
	}
}
