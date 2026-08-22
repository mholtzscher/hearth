package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

func renderFacadeConformanceTest(model entityTypeModel) (output, error) {
	var source strings.Builder
	generatedHeader(&source)
	fmt.Fprintf(&source, "package %s\n\n", model.Package)
	source.WriteString("import (\n\t\"encoding/json\"\n\t\"testing\"\n\t\"time\"\n)\n\n")
	source.WriteString("func TestGeneratedObservationConformance(t *testing.T) {\n")
	source.WriteString("\tcodecs, err := codecs()\n\tif err != nil { t.Fatal(err) }\n")
	for _, example := range model.Examples.Cases {
		fmt.Fprintf(&source, "\tt.Run(%s, func(t *testing.T) {\n", strconv.Quote(example.Name))
		fmt.Fprintf(&source, "\t\tsupport, _, err := codecs.Support.Decode(json.RawMessage(%s))\n", rawQuote(example.Support))
		source.WriteString("\t\tif err != nil { t.Fatalf(\"support: %v\", err) }\n")
		for index, state := range example.States {
			source.WriteString("\t\t{\n")
			fmt.Fprintf(&source, "\t\t\tstate, _, decodeErr := codecs.State.Decode(json.RawMessage(%s))\n", rawQuote(state.Value))
			source.WriteString("\t\t\tvalid := false\n")
			source.WriteString("\t\t\tvar observationErr error\n")
			source.WriteString("\t\t\tif decodeErr == nil {\n")
			source.WriteString("\t\t\t\t_, observationErr = NewObservation(ObservationInput{EntityID: \"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab\", Support: support, State: state, AdapterReceivedAt: time.Unix(0, 0).UTC()})\n")
			source.WriteString("\t\t\t\tvalid = observationErr == nil\n\t\t\t}\n")
			fmt.Fprintf(&source, "\t\t\tif valid != %t { t.Errorf(%s, valid, decodeErr, observationErr) }\n", state.Valid, strconv.Quote(fmt.Sprintf("State example %d: valid = %%v, decode error = %%v, Observation error = %%v", index+1)))
			source.WriteString("\t\t}\n")
		}
		source.WriteString("\t})\n")
	}
	source.WriteString("}\n")
	formatted, err := formatGenerated(source.String())
	if err != nil {
		return output{}, err
	}
	return output{
		path:    filepath.Join(model.ModuleRoot, "sdk", "adapter", model.Package, "zz_generated_facade_test.go"),
		content: formatted,
	}, nil
}
