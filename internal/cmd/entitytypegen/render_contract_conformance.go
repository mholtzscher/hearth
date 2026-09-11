package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// renderConformanceTest generates compact contract test wiring: the
// manifest's exact examples file plus one typed callback per operation.
// Expected-result comparison stays in the shared handwritten runner, so the
// generated file carries no per-example assertion blocks; only authored
// invalid supports assert directly against the generated support validator.
func renderConformanceTest(model entityTypeModel, modulePath string) output {
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
	if model.EventSource {
		writeEntityEventNameContractChecks(&source, model)
	}
	return output{
		path:    filepath.Join(model.Directory, "zz_generated_conformance_test.go"),
		content: []byte(source.String()),
	}
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

// writeEntityEventNameContractChecks emits the event-source name probe: the
// generated accessor returns an owned name slice and the generated validator
// separates a canonical supported name from an unsupported name and from a
// non-canonical one.
func writeEntityEventNameContractChecks(source *strings.Builder, model entityTypeModel) {
	names, err := entityEventNamesFromSupport(model.Examples.Cases[0].Support)
	if err != nil {
		panic("event-source examples lost their names: " + err.Error())
	}
	unsupported := unsupportedEntityEventName(names)
	source.WriteString("\nfunc TestGeneratedEntityEventNames(t *testing.T) {\n")
	source.WriteString("\tcodecs, err := Compile()\n\tif err != nil { t.Fatal(err) }\n")
	fmt.Fprintf(
		source,
		"\tsupport, _, err := codecs.Support.Decode(json.RawMessage(%s))\n",
		rawQuote(model.Examples.Cases[0].Support),
	)
	source.WriteString("\tif err != nil { t.Fatalf(\"support: %v\", err) }\n")
	source.WriteString("\tnames := EntityEventNames(support)\n")
	fmt.Fprintf(source, "\tif len(names) != %d { t.Fatalf(\"Entity Event names = %%v\", names) }\n", len(names))
	for index, name := range names {
		fmt.Fprintf(
			source,
			"\tif names[%d] != %s { t.Errorf(\"Entity Event name = %%q, want %%q\", names[%d], %s) }\n",
			index,
			strconv.Quote(name),
			index,
			strconv.Quote(name),
		)
	}
	fmt.Fprintf(
		source,
		"\tif err := ValidateEntityEventName(support, %s); err != nil { t.Errorf(\"supported Entity Event name rejected: %%v\", err) }\n",
		strconv.Quote(names[0]),
	)
	fmt.Fprintf(
		source,
		"\tif err := ValidateEntityEventName(support, %s); err == nil { t.Error(\"unsupported Entity Event name was accepted\") }\n",
		strconv.Quote(unsupported),
	)
	source.WriteString(
		"\tif err := ValidateEntityEventName(support, \"not a name\"); err == nil { t.Error(\"non-canonical Entity Event name was accepted\") }\n",
	)
	source.WriteString("\tnames[0] = \"mutated\"\n")
	fmt.Fprintf(
		source,
		"\tif got := EntityEventNames(support); got[0] != %s { t.Errorf(\"EntityEventNames returned a shared slice: %%v\", got) }\n",
		strconv.Quote(names[0]),
	)
	source.WriteString("}\n")
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
// mutable current support. Dispatched operations declare no outcome
// predicate, so they set Dispatched and omit Satisfies; the runner replays
// no outcome examples for them.
func writeContractOperationProbe(source *strings.Builder, operation operationModel) {
	variable := lowerFirst(operation.GoName) + "Support"
	fmt.Fprintf(source, "\t\t\t%s: {\n", strconv.Quote(operation.Name))
	if operation.Outcome == outcomeDispatched {
		source.WriteString("\t\t\t\tDispatched: true,\n")
	}
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
	if operation.Outcome == outcomeDispatched {
		source.WriteString("\t\t\t},\n")
		return
	}
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
