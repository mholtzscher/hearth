package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// renderConformanceTest generates compact contract test wiring: the
// manifest's exact examples file plus one typed callback per operation.
// Expected-result comparison stays in the shared handwritten runner, so the
// generated file carries no per-example assertion blocks; only authored
// invalid supports assert directly against the generated support validator.
func renderConformanceTest(model entityTypeModel, modulePath string) ([]byte, error) {
	var source strings.Builder
	generatedHeader(&source)
	fmt.Fprintf(&source, "package %s\n\n", model.Package)
	source.WriteString("import (\n")
	source.WriteString("\t_ \"embed\"\n")
	source.WriteString("\t\"encoding/json\"\n")
	if hasOptionalOperation(model) {
		source.WriteString("\t\"errors\"\n")
	}
	source.WriteString("\t\"testing\"\n\n")
	fmt.Fprintf(&source, "\t\"%s/internal/entitytypetest\"\n", modulePath)
	source.WriteString(")\n\n")
	fmt.Fprintf(&source, "//go:embed %s\nvar examplesJSON []byte\n\n", strconv.Quote(model.ExamplesFile))
	source.WriteString("func TestGeneratedConformance(t *testing.T) {\n")
	source.WriteString("\tcodecs, err := Compile()\n\tif err != nil { t.Fatal(err) }\n")
	source.WriteString("\tentitytypetest.RunContractExamples(t, examplesJSON, entitytypetest.ContractProbe{\n")
	source.WriteString("\t\tValidateSupport: func(support json.RawMessage) error {\n")
	source.WriteString("\t\t\tdecodedSupport, _, err := codecs.Support.Decode(support)\n")
	source.WriteString("\t\t\tif err != nil { return err }\n")
	source.WriteString("\t\t\treturn ValidateSupport(decodedSupport)\n")
	source.WriteString("\t\t},\n")
	source.WriteString("\t\tValidateState: func(support, state json.RawMessage) error {\n")
	source.WriteString("\t\t\tdecodedSupport, _, err := codecs.Support.Decode(support)\n")
	source.WriteString("\t\t\tif err != nil { return err }\n")
	source.WriteString("\t\t\tdecodedState, _, err := codecs.State.Decode(state)\n")
	source.WriteString("\t\t\tif err != nil { return err }\n")
	source.WriteString("\t\t\treturn ValidateState(decodedSupport, decodedState)\n")
	source.WriteString("\t\t},\n")
	if len(model.Operations) == 0 {
		source.WriteString("\t\tOperations: nil,\n")
	} else {
		source.WriteString("\t\tOperations: map[string]entitytypetest.OperationProbe{\n")
		for _, operation := range model.Operations {
			writeContractOperationProbe(&source, operation)
		}
		source.WriteString("\t\t},\n")
	}
	source.WriteString("\t})\n")
	writeInvalidSupportChecks(&source, model)
	source.WriteString("}\n")
	formatted, err := formatGenerated(source.String())
	if err != nil {
		return nil, err
	}
	return formatted, nil
}

// writeInvalidSupportChecks asserts the generated support validator
// rejects every authored invalid support. Entries schema-decode by
// load-time checks, so acceptance here proves support_validation rules
// rather than schema rejection.
func writeInvalidSupportChecks(source *strings.Builder, model entityTypeModel) {
	for index, raw := range model.Examples.InvalidSupports {
		var compacted bytes.Buffer
		if err := json.Compact(&compacted, raw); err != nil {
			panic("invalid support is not compact JSON")
		}
		fmt.Fprintf(
			source,
			"\tinvalidSupport%d, _, err := codecs.Support.Decode(json.RawMessage(%s))\n",
			index+1,
			strconv.Quote(compacted.String()),
		)
		fmt.Fprintf(
			source,
			"\tif err != nil { t.Fatalf(\"invalid support %d is not schema-decodable: %%v\", err) }\n",
			index+1,
		)
		fmt.Fprintf(
			source,
			"\tif err := ValidateSupport(invalidSupport%d); err == nil { t.Error(\"invalid support %d was accepted\") }\n",
			index+1,
			index+1,
		)
	}
}

func hasOptionalOperation(model entityTypeModel) bool {
	for _, operation := range model.Operations {
		if !operation.Required {
			return true
		}
	}
	return false
}

// writeContractOperationProbe emits one typed callback per operation. The
// callbacks decode and validate inputs and return actual errors or outcomes;
// only the handwritten runner compares expected results. Outcome callbacks
// schema-decode recorded parameters and State without revalidating against
// mutable current support.
func writeContractOperationProbe(source *strings.Builder, operation operationModel) {
	variable := lowerFirst(operation.GoName) + "Support"
	fmt.Fprintf(source, "\t\t\t%s: {\n", strconv.Quote(operation.Name))
	source.WriteString("\t\t\t\tValidateParameters: func(support, parameters json.RawMessage) error {\n")
	source.WriteString("\t\t\t\t\tdecodedSupport, _, err := codecs.Support.Decode(support)\n")
	source.WriteString("\t\t\t\t\tif err != nil { return err }\n")
	if operation.Required {
		fmt.Fprintf(source, "\t\t\t\t\t%s := decodedSupport.Operations.%s\n", variable, operation.GoName)
	} else {
		fmt.Fprintf(
			source,
			"\t\t\t\t\tif decodedSupport.Operations.%s == nil { return errors.New(%s) }\n",
			operation.GoName,
			strconv.Quote(operation.Name+" support is required by this conformance case"),
		)
		fmt.Fprintf(source, "\t\t\t\t\t%s := *decodedSupport.Operations.%s\n", variable, operation.GoName)
	}
	fmt.Fprintf(
		source,
		"\t\t\t\t\tdecodedParameters, _, err := codecs.%sParameters.Decode(parameters)\n",
		operation.GoName,
	)
	source.WriteString("\t\t\t\t\tif err != nil { return err }\n")
	fmt.Fprintf(
		source,
		"\t\t\t\t\treturn Validate%sParameters(decodedSupport, %s, decodedParameters)\n",
		operation.GoName,
		variable,
	)
	source.WriteString("\t\t\t\t},\n")
	source.WriteString("\t\t\t\tSatisfies: func(parameters, state json.RawMessage) (bool, error) {\n")
	fmt.Fprintf(
		source,
		"\t\t\t\t\tdecodedParameters, _, err := codecs.%sParameters.Decode(parameters)\n",
		operation.GoName,
	)
	source.WriteString("\t\t\t\t\tif err != nil { return false, err }\n")
	source.WriteString("\t\t\t\t\tdecodedState, _, err := codecs.State.Decode(state)\n")
	source.WriteString("\t\t\t\t\tif err != nil { return false, err }\n")
	fmt.Fprintf(
		source,
		"\t\t\t\t\treturn %sSatisfied(decodedParameters, decodedState), nil\n",
		operation.GoName,
	)
	source.WriteString("\t\t\t\t},\n")
	source.WriteString("\t\t\t},\n")
}
