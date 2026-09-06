package main

import (
	"fmt"
	"strconv"
	"strings"
)

func renderBehavior(model entityTypeModel) ([]byte, error) {
	var source strings.Builder
	generatedHeader(&source)
	fmt.Fprintf(&source, "package %s\n\n", model.Package)

	hasValidation := len(model.StateValidation) > 0
	for _, operation := range model.Operations {
		hasValidation = hasValidation || len(operation.ParameterValidation) > 0
	}
	isComparable := schemaComparable(model.StateSchema)
	var imports []string
	if hasValidation {
		imports = append(imports, "errors")
	}
	if !isComparable {
		imports = append(imports, "reflect")
	}
	if len(model.Operations) > 0 {
		imports = append(imports, "time")
	}
	if len(imports) > 0 {
		source.WriteString("import (\n")
		for _, name := range imports {
			fmt.Fprintf(&source, "\t%q\n", name)
		}
		source.WriteString(")\n\n")
	}

	source.WriteString("func ValidateState(support Support, state State) error {\n")
	writeValidationRules(&source, model.StateValidation, "\t")
	source.WriteString("\treturn nil\n}\n\n")
	if isComparable {
		source.WriteString("func EqualState(left, right State) bool { return left == right }\n\n")
	} else {
		source.WriteString("func EqualState(left, right State) bool { return reflect.DeepEqual(left, right) }\n\n")
	}

	if len(model.Operations) > 0 {
		source.WriteString("const (\n")
		for _, operation := range model.Operations {
			fmt.Fprintf(
				&source,
				"\t%sDeadline time.Duration = %d * time.Millisecond\n",
				operation.GoName,
				operation.DeadlineMS,
			)
		}
		source.WriteString(")\n\n")
	}
	for _, operation := range model.Operations {
		fmt.Fprintf(
			&source,
			"func Validate%sParameters(support Support, operationSupport %sSupport, parameters %sParameters) error {\n",
			operation.GoName,
			operation.GoName,
			operation.GoName,
		)
		writeValidationRules(&source, operation.ParameterValidation, "\t")
		source.WriteString("\treturn nil\n}\n\n")
		fmt.Fprintf(
			&source,
			"func %sSatisfied(parameters %sParameters, state State) bool {\n",
			operation.GoName,
			operation.GoName,
		)
		fmt.Fprintf(&source, "\treturn %s\n}\n\n", satisfactionCondition(operation.SatisfiedWhen))
	}
	return formatGenerated(source.String())
}

func writeValidationRules(source *strings.Builder, rules []ruleModel, indent string) {
	for _, rule := range rules {
		fmt.Fprintf(source, "%sif !(%s) {\n", indent, ruleCondition(rule))
		fmt.Fprintf(source, "%s\treturn errors.New(%s)\n", indent, strconv.Quote(ruleDescription(rule)))
		fmt.Fprintf(source, "%s}\n", indent)
	}
}

func schemaComparable(schema schemaNode) bool {
	switch schema.Type {
	case string(kindBoolean), string(kindString), string(kindInteger), string(kindNumber):
		return true
	case schemaTypeArray:
		return false
	case schemaTypeObject:
		for name, property := range schema.Properties {
			if !required(schema, name) || !schemaComparable(property) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

//nolint:funlen // Keeping the generated test template together makes its emitted structure reviewable.
func renderConformanceTest(model entityTypeModel) ([]byte, error) {
	var source strings.Builder
	generatedHeader(&source)
	fmt.Fprintf(&source, "package %s\n\n", model.Package)
	source.WriteString("import (\n\t\"encoding/json\"\n\t\"testing\"\n)\n\n")
	source.WriteString("func TestGeneratedConformance(t *testing.T) {\n")
	source.WriteString("\tcodecs, err := Compile()\n\tif err != nil { t.Fatal(err) }\n")
	for _, example := range model.Examples.Cases {
		fmt.Fprintf(&source, "\tt.Run(%s, func(t *testing.T) {\n", strconv.Quote(example.Name))
		fmt.Fprintf(
			&source,
			"\t\tsupport, _, err := codecs.Support.Decode(json.RawMessage(%s))\n",
			rawQuote(example.Support),
		)
		source.WriteString("\t\tif err != nil { t.Fatalf(\"support: %v\", err) }\n")
		for index, state := range example.States {
			source.WriteString("\t\t{\n")
			fmt.Fprintf(
				&source,
				"\t\t\tvalue, _, decodeErr := codecs.State.Decode(json.RawMessage(%s))\n",
				rawQuote(state.Value),
			)
			source.WriteString("\t\t\tvalid := decodeErr == nil\n")
			source.WriteString("\t\t\tif decodeErr == nil { valid = ValidateState(support, value) == nil }\n")
			fmt.Fprintf(
				&source,
				"\t\t\tif valid != %t { t.Errorf(\"state example %d: valid = %%v, decode error = %%v\", valid, decodeErr) }\n",
				state.Valid,
				index+1,
			)
			source.WriteString("\t\t}\n")
		}
		for _, operation := range model.Operations {
			values, supported := example.Operations[operation.Name]
			if !supported {
				continue
			}
			if operation.Required {
				fmt.Fprintf(
					&source,
					"\t\t%sSupport := support.Operations.%s\n",
					lowerFirst(operation.GoName),
					operation.GoName,
				)
			} else {
				fmt.Fprintf(
					&source,
					"\t\tif support.Operations.%s == nil { t.Fatal(%s) }\n",
					operation.GoName,
					strconv.Quote(operation.Name+" support is required by this conformance case"),
				)
				fmt.Fprintf(
					&source,
					"\t\t%sSupport := *support.Operations.%s\n",
					lowerFirst(operation.GoName),
					operation.GoName,
				)
			}
			for index, parameters := range values.Parameters {
				source.WriteString("\t\t{\n")
				fmt.Fprintf(
					&source,
					"\t\t\tvalue, _, decodeErr := codecs.%sParameters.Decode(json.RawMessage(%s))\n",
					operation.GoName,
					rawQuote(parameters.Value),
				)
				source.WriteString("\t\t\tvalid := decodeErr == nil\n")
				fmt.Fprintf(
					&source,
					"\t\t\tif decodeErr == nil { valid = Validate%sParameters(support, %sSupport, value) == nil }\n",
					operation.GoName,
					lowerFirst(operation.GoName),
				)
				fmt.Fprintf(
					&source,
					"\t\t\tif valid != %t { t.Errorf(%s, valid, decodeErr) }\n",
					parameters.Valid,
					strconv.Quote(
						fmt.Sprintf(
							"%s parameter example %d: valid = %%v, decode error = %%v",
							operation.Name,
							index+1,
						),
					),
				)
				source.WriteString("\t\t}\n")
			}
			for index, outcome := range values.Outcomes {
				source.WriteString("\t\t{\n")
				fmt.Fprintf(
					&source,
					"\t\t\tparameters, _, err := codecs.%sParameters.Decode(json.RawMessage(%s))\n",
					operation.GoName,
					rawQuote(outcome.Parameters),
				)
				fmt.Fprintf(
					&source,
					"\t\t\tif err != nil { t.Fatalf(%s, err) }\n",
					strconv.Quote(fmt.Sprintf("%s outcome %d parameters: %%v", operation.Name, index+1)),
				)
				fmt.Fprintf(
					&source,
					"\t\t\tstate, _, err := codecs.State.Decode(json.RawMessage(%s))\n",
					rawQuote(outcome.State),
				)
				fmt.Fprintf(
					&source,
					"\t\t\tif err != nil { t.Fatalf(%s, err) }\n",
					strconv.Quote(fmt.Sprintf("%s outcome %d State: %%v", operation.Name, index+1)),
				)
				fmt.Fprintf(
					&source,
					"\t\t\tif satisfied := %sSatisfied(parameters, state); satisfied != %t { t.Errorf(%s, satisfied) }\n",
					operation.GoName,
					outcome.Satisfied,
					strconv.Quote(fmt.Sprintf("%s outcome %d: satisfied = %%v", operation.Name, index+1)),
				)
				source.WriteString("\t\t}\n")
			}
		}
		source.WriteString("\t})\n")
	}
	source.WriteString("}\n")
	return formatGenerated(source.String())
}

func rawQuote(raw []byte) string { return strconv.Quote(string(raw)) }
