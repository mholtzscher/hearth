package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

func renderBehavior(model entityTypeModel) output {
	var source strings.Builder
	generatedHeader(&source)
	fmt.Fprintf(&source, "package %s\n\n", model.Package)

	hasValidation := len(model.StateValidation) > 0 || len(model.SupportValidation) > 0
	for _, operation := range model.Operations {
		hasValidation = hasValidation || len(operation.ParameterValidation) > 0
	}
	isComparable := schemaComparable(model.StateSchema)
	var imports []string
	if hasValidation {
		imports = append(imports, "errors")
	}
	if model.EventSource {
		imports = append(imports, "fmt", "regexp")
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
	source.WriteString("func ValidateSupport(support Support) error {\n")
	writeValidationRules(&source, model.SupportValidation, "\t")
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
	if model.EventSource {
		writeEntityEventNameBehavior(&source)
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
		// Dispatched operations declare no outcome predicate, so no
		// <Operation>Satisfied matcher is generated. The catalog
		// installs a nil matcher and Satisfies rejects dispatched calls.
		if operation.Outcome == outcomeDispatched {
			continue
		}
		fmt.Fprintf(
			&source,
			"func %sSatisfied(parameters %sParameters, state State) bool {\n",
			operation.GoName,
			operation.GoName,
		)
		fmt.Fprintf(&source, "\treturn %s\n}\n\n", satisfactionCondition(operation.SatisfiedWhen))
	}
	return output{
		path:    filepath.Join(model.Directory, "zz_generated_behavior.go"),
		content: []byte(source.String()),
	}
}

// writeEntityEventNameBehavior emits the typed name validation and accessor the
// catalog selector and SDK facade build on. Names are the only Entity Event
// support, so the generated shape is a closed slug list rather than a command
// or state surface.
func writeEntityEventNameBehavior(source *strings.Builder) {
	fmt.Fprintf(
		source,
		"var entityEventNamePattern = regexp.MustCompile(%s)\n\n",
		strconv.Quote(entityEventNamePattern),
	)
	source.WriteString("// EntityEventNames returns an owned copy of the support's supported Entity Event names.\n")
	source.WriteString("func EntityEventNames(support Support) []string {\n")
	source.WriteString("\tnames := make([]string, 0, len(support.Events.Names))\n")
	source.WriteString("\treturn append(names, support.Events.Names...)\n")
	source.WriteString("}\n\n")
	source.WriteString("// ValidateEntityEventName reports whether name is a canonical Entity Event name\n")
	source.WriteString("// that the Entity's current support accepts.\n")
	source.WriteString("func ValidateEntityEventName(support Support, name string) error {\n")
	source.WriteString("\tif !entityEventNamePattern.MatchString(name) {\n")
	source.WriteString("\t\treturn fmt.Errorf(\"Entity Event name %q is not a canonical name slug\", name)\n")
	source.WriteString("\t}\n")
	source.WriteString("\tfor _, supported := range support.Events.Names {\n")
	source.WriteString("\t\tif supported == name {\n\t\t\treturn nil\n\t\t}\n")
	source.WriteString("\t}\n")
	source.WriteString("\treturn fmt.Errorf(\"Entity Event name %q is not supported\", name)\n")
	source.WriteString("}\n\n")
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
