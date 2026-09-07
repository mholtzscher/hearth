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

func rawQuote(raw []byte) string { return strconv.Quote(string(raw)) }
