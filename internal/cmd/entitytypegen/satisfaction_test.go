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

func boundedString(minLength, maxLength *int) schemaNode {
	return schemaNode{Type: string(kindString), MinLength: minLength, MaxLength: maxLength}
}

func stringArray(items schemaNode, minItems *int, uniqueItems *bool) schemaNode {
	return schemaNode{
		Type: schemaTypeArray, Items: &items, MinItems: minItems, UniqueItems: uniqueItems,
	}
}

func membershipRoots(
	parameters, support map[string]schemaNode,
	parametersRequired, supportRequired []string,
) map[string]referenceRoot {
	return map[string]referenceRoot{
		referenceRootParameters: {
			Schema: schemaNode{
				Type: schemaTypeObject, Required: parametersRequired, Properties: parameters,
			},
			GoExpression: referenceRootParameters,
		},
		referenceRootSupport: {
			Schema: schemaNode{
				Type: schemaTypeObject, Required: supportRequired, Properties: support,
			},
			GoExpression: referenceRootSupport,
		},
	}
}

func choicesSupport(minItems *int) map[string]schemaNode {
	return map[string]schemaNode{
		"choices": stringArray(
			boundedString(new(1), new(128)),
			minItems,
			new(true),
		),
	}
}

func TestInCompilesMembershipLoop(t *testing.T) {
	t.Parallel()
	roots := membershipRoots(
		map[string]schemaNode{"value": boundedString(nil, nil)},
		choicesSupport(new(1)),
		[]string{"value"},
		[]string{"choices"},
	)
	compiled, err := compileRule(ruleManifest{
		Op:    ruleOperatorIn,
		Left:  referenceManifest{Root: "parameters", Path: "/value"},
		Right: &(referenceManifest{Root: "support", Path: "/choices"}),
	}, roots)
	if err != nil {
		t.Fatal(err)
	}
	expected := "(func() bool { for _, candidate := range support.Choices " +
		"{ if string(parameters.Value) == string(candidate) { return true } }; return false }())"
	if condition := ruleCondition(compiled); condition != expected {
		t.Fatalf("in condition = %s", condition)
	}
	if description := ruleDescription(compiled); description !=
		"parameters/value must be one of support/choices" {
		t.Fatalf("in description = %s", description)
	}

	for name, rule := range map[string]ruleManifest{
		"non-string left": {
			Op:    ruleOperatorIn,
			Left:  referenceManifest{Root: "support", Path: "/maximum"},
			Right: &(referenceManifest{Root: "support", Path: "/choices"}),
		},
		"missing right": {
			Op:   ruleOperatorIn,
			Left: referenceManifest{Root: "parameters", Path: "/value"},
		},
		"non-array right": {
			Op:    ruleOperatorIn,
			Left:  referenceManifest{Root: "parameters", Path: "/value"},
			Right: &(referenceManifest{Root: "support", Path: "/maximum"}),
		},
		"tolerance present": {
			Op:        ruleOperatorIn,
			Left:      referenceManifest{Root: "parameters", Path: "/value"},
			Right:     &(referenceManifest{Root: "support", Path: "/choices"}),
			Tolerance: jsonNumber("1"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			badRoots := membershipRoots(
				map[string]schemaNode{"value": boundedString(nil, nil)},
				map[string]schemaNode{
					"choices": stringArray(
						boundedString(nil, nil),
						new(1),
						new(true),
					),
					"maximum": boundedInteger("0", "100"),
				},
				[]string{"value"},
				[]string{"choices", "maximum"},
			)
			if _, compileErr := compileRule(rule, badRoots); compileErr == nil {
				t.Fatal("invalid in rule unexpectedly accepted")
			}
		})
	}
}

func TestInRejectsEmptyOrNonUniqueChoices(t *testing.T) {
	t.Parallel()
	for name, choices := range map[string]schemaNode{
		"empty allowed": stringArray(boundedString(nil, nil), nil, new(true)),
		"zero minimum":  stringArray(boundedString(nil, nil), new(0), new(true)),
		"non-unique":    stringArray(boundedString(nil, nil), new(1), new(false)),
		"unique unset":  stringArray(boundedString(nil, nil), new(1), nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			roots := membershipRoots(
				map[string]schemaNode{"value": boundedString(nil, nil)},
				map[string]schemaNode{"choices": choices},
				[]string{"value"},
				[]string{"choices"},
			)
			rule := ruleManifest{
				Op:    ruleOperatorIn,
				Left:  referenceManifest{Root: "parameters", Path: "/value"},
				Right: &(referenceManifest{Root: "support", Path: "/choices"}),
			}
			if _, err := compileRule(rule, roots); err == nil {
				t.Fatal("invalid in choices unexpectedly accepted")
			}
		})
	}
}

func TestInIfPresentPassesWhenAbsent(t *testing.T) {
	t.Parallel()
	roots := membershipRoots(
		map[string]schemaNode{"choice": boundedString(new(1), new(128))},
		choicesSupport(nil),
		[]string{},
		[]string{"choices"},
	)
	compiled, err := compileRule(ruleManifest{
		Op:    ruleOperatorInIfPresent,
		Left:  referenceManifest{Root: "parameters", Path: "/choice"},
		Right: &(referenceManifest{Root: "support", Path: "/choices"}),
	}, roots)
	if err != nil {
		t.Fatal(err)
	}
	expected := "(parameters.Choice == nil || (func() bool { for _, candidate := range support.Choices " +
		"{ if string(*parameters.Choice) == string(candidate) { return true } }; return false }()))"
	if condition := ruleCondition(compiled); condition != expected {
		t.Fatalf("in_if_present condition = %s", condition)
	}

	for name, test := range map[string]struct {
		parameters map[string]schemaNode
		required   []string
		support    map[string]schemaNode
	}{
		"required left": {
			parameters: map[string]schemaNode{"choice": boundedString(new(1), new(128))},
			required:   []string{"choice"},
			support:    choicesSupport(nil),
		},
		"mismatched item bounds": {
			parameters: map[string]schemaNode{"choice": boundedString(new(1), new(128))},
			required:   []string{},
			support: map[string]schemaNode{"choices": stringArray(
				boundedString(new(1), new(64)),
				nil,
				new(true),
			)},
		},
		"non-unique choices": {
			parameters: map[string]schemaNode{"choice": boundedString(new(1), new(128))},
			required:   []string{},
			support: map[string]schemaNode{"choices": stringArray(
				boundedString(new(1), new(128)),
				nil,
				new(false),
			)},
		},
		"non-string left": {
			parameters: map[string]schemaNode{"choice": boundedInteger("0", "100")},
			required:   []string{},
			support:    choicesSupport(nil),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rule := ruleManifest{
				Op:    ruleOperatorInIfPresent,
				Left:  referenceManifest{Root: "parameters", Path: "/choice"},
				Right: &(referenceManifest{Root: "support", Path: "/choices"}),
			}
			badRoots := membershipRoots(test.parameters, test.support, test.required, []string{"choices"})
			if _, compileErr := compileRule(rule, badRoots); compileErr == nil {
				t.Fatal("invalid in_if_present rule unexpectedly accepted")
			}
		})
	}
}

func TestInIfPresentRejectsOptionalIntermediates(t *testing.T) {
	t.Parallel()
	roots := map[string]referenceRoot{
		referenceRootParameters: {
			Schema: schemaNode{
				Type:     schemaTypeObject,
				Required: []string{},
				Properties: map[string]schemaNode{"nested": {
					Type:     schemaTypeObject,
					Required: []string{"choice"},
					Properties: map[string]schemaNode{
						"choice": boundedString(new(1), new(128)),
					},
				}},
			},
			GoExpression: referenceRootParameters,
		},
		referenceRootSupport: {
			Schema: schemaNode{
				Type:       schemaTypeObject,
				Required:   []string{"choices"},
				Properties: choicesSupport(nil),
			},
			GoExpression: referenceRootSupport,
		},
	}
	rule := ruleManifest{
		Op:    ruleOperatorInIfPresent,
		Left:  referenceManifest{Root: "parameters", Path: "/nested/choice"},
		Right: &(referenceManifest{Root: "support", Path: "/choices"}),
	}
	if _, err := compileRule(rule, roots); err == nil {
		t.Fatal("optional intermediate unexpectedly accepted")
	}
}

func TestEqOptionalTruthTable(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		parametersRequired []string
		stateRequired      []string
		expected           string
	}{
		"absent absent": {
			expected: "((parameters.A == nil && state.B == nil) || " +
				"(parameters.A != nil && state.B != nil && " +
				"string(*parameters.A) == string(*state.B)))",
		},
		"left absent only": {
			stateRequired: []string{"b"},
			expected: "(parameters.A != nil && " +
				"string(*parameters.A) == string(state.B))",
		},
		"right absent only": {
			parametersRequired: []string{"a"},
			expected: "(state.B != nil && " +
				"string(parameters.A) == string(*state.B))",
		},
		"both present": {
			parametersRequired: []string{"a"},
			stateRequired:      []string{"b"},
			expected:           "string(parameters.A) == string(state.B)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			roots := satisfactionRoots(
				map[string]schemaNode{"a": boundedString(new(1), new(128))},
				map[string]schemaNode{"b": boundedString(new(1), new(128))},
				test.parametersRequired,
				test.stateRequired,
			)
			compiled, err := compileRule(ruleManifest{
				Op:    ruleOperatorEqOptional,
				Left:  referenceManifest{Root: "parameters", Path: "/a"},
				Right: &(referenceManifest{Root: "state", Path: "/b"}),
			}, roots)
			if err != nil {
				t.Fatal(err)
			}
			if condition := ruleCondition(compiled); condition != test.expected {
				t.Fatalf("eq_optional condition = %s", condition)
			}
		})
	}
}

func TestEqOptionalComparesOptionalNumbers(t *testing.T) {
	t.Parallel()
	roots := satisfactionRoots(
		map[string]schemaNode{"value": {Type: string(kindNumber)}},
		map[string]schemaNode{"value": {Type: string(kindNumber)}},
		[]string{},
		[]string{},
	)
	compiled, err := compileRule(ruleManifest{
		Op:    ruleOperatorEqOptional,
		Left:  referenceManifest{Root: "parameters", Path: "/value"},
		Right: &(referenceManifest{Root: "state", Path: "/value"}),
	}, roots)
	if err != nil {
		t.Fatal(err)
	}
	expected := "((parameters.Value == nil && state.Value == nil) || " +
		"(parameters.Value != nil && state.Value != nil && " +
		"float64(*parameters.Value) == float64(*state.Value)))"
	if condition := ruleCondition(compiled); condition != expected {
		t.Fatalf("eq_optional number condition = %s", condition)
	}

	mismatched := satisfactionRoots(
		map[string]schemaNode{"value": {Type: string(kindNumber)}},
		map[string]schemaNode{"value": boundedInteger("0", "100")},
		[]string{},
		[]string{},
	)
	if _, compileErr := compileRule(ruleManifest{
		Op:    ruleOperatorEqOptional,
		Left:  referenceManifest{Root: "parameters", Path: "/value"},
		Right: &(referenceManifest{Root: "state", Path: "/value"}),
	}, mismatched); compileErr == nil {
		t.Fatal("mismatched eq_optional operands unexpectedly accepted")
	}
}

func TestIfPresentOrderedPassesWhenAbsent(t *testing.T) {
	t.Parallel()
	for operator, symbol := range map[string]string{
		ruleOperatorGTEIfPresent: ">=",
		ruleOperatorLTEIfPresent: "<=",
	} {
		t.Run(operator, func(t *testing.T) {
			t.Parallel()
			roots := membershipRoots(
				map[string]schemaNode{"value": {Type: string(kindNumber)}},
				map[string]schemaNode{"minimum": {Type: string(kindNumber)}},
				[]string{},
				[]string{"minimum"},
			)
			compiled, err := compileRule(ruleManifest{
				Op:    operator,
				Left:  referenceManifest{Root: "parameters", Path: "/value"},
				Right: &(referenceManifest{Root: "support", Path: "/minimum"}),
			}, roots)
			if err != nil {
				t.Fatal(err)
			}
			expected := "(parameters.Value == nil || float64(*parameters.Value) " +
				symbol + " float64(support.Minimum))"
			if condition := ruleCondition(compiled); condition != expected {
				t.Fatalf("%s condition = %s", operator, condition)
			}
		})
	}

	for name, test := range map[string]struct {
		parameters map[string]schemaNode
		required   []string
		support    map[string]schemaNode
	}{
		"required left": {
			parameters: map[string]schemaNode{"value": {Type: string(kindNumber)}},
			required:   []string{"value"},
			support:    map[string]schemaNode{"minimum": {Type: string(kindNumber)}},
		},
		"non-numeric left": {
			parameters: map[string]schemaNode{"value": boundedString(nil, nil)},
			required:   []string{},
			support:    map[string]schemaNode{"minimum": {Type: string(kindNumber)}},
		},
		"mismatched kinds": {
			parameters: map[string]schemaNode{"value": {Type: string(kindNumber)}},
			required:   []string{},
			support:    map[string]schemaNode{"minimum": boundedInteger("0", "100")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rule := ruleManifest{
				Op:    ruleOperatorGTEIfPresent,
				Left:  referenceManifest{Root: "parameters", Path: "/value"},
				Right: &(referenceManifest{Root: "support", Path: "/minimum"}),
			}
			badRoots := membershipRoots(test.parameters, test.support, test.required, []string{"minimum"})
			if _, compileErr := compileRule(rule, badRoots); compileErr == nil {
				t.Fatal("invalid gte_if_present rule unexpectedly accepted")
			}
		})
	}
}

func TestSupportValidationRejectsNonSupportRoots(t *testing.T) {
	t.Parallel()
	supportOnly := map[string]referenceRoot{
		referenceRootSupport: {
			Schema:       schemaNode{Type: string(kindInteger)},
			GoExpression: referenceRootSupport,
		},
	}
	_, err := compileRules([]ruleManifest{{
		Op:    "gte",
		Left:  referenceManifest{Root: "state", Path: ""},
		Right: &(referenceManifest{Root: "support", Path: ""}),
	}}, supportOnly, "support validation")
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("support validation root error = %v", err)
	}

	compiled, err := compileRules([]ruleManifest{{
		Op:    "gte",
		Left:  referenceManifest{Root: "support", Path: "/maximum"},
		Right: &(referenceManifest{Root: "support", Path: "/minimum"}),
	}}, map[string]referenceRoot{
		referenceRootSupport: {
			Schema: schemaNode{
				Type:     schemaTypeObject,
				Required: []string{"maximum", "minimum"},
				Properties: map[string]schemaNode{
					"maximum": boundedInteger("0", "100"),
					"minimum": boundedInteger("0", "100"),
				},
			},
			GoExpression: referenceRootSupport,
		},
	}, "support validation")
	if err != nil {
		t.Fatal(err)
	}
	if condition := ruleCondition(compiled[0]); condition !=
		"int64(support.Maximum) >= int64(support.Minimum)" {
		t.Fatalf("support validation condition = %s", condition)
	}
}
