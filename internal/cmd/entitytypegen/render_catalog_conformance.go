package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

//nolint:gocognit // Generated conformance cases are assembled in one deterministic pass.
func renderCatalogConformanceTest(models []entityTypeModel, moduleRoot string) (output, error) {
	var source strings.Builder
	generatedHeader(&source)
	source.WriteString("package devices\n\n")
	source.WriteString("import (\n\t\"testing\"\n\t\"time\"\n)\n\n")
	source.WriteString("func TestGeneratedBuiltinCatalogConformance(t *testing.T) {\n")
	source.WriteString("\tcatalog, err := NewBuiltinTypeCatalog()\n\tif err != nil { t.Fatal(err) }\n")
	for _, model := range models {
		for _, example := range model.Examples.Cases {
			name := model.TypeID + "/" + example.Name
			fmt.Fprintf(&source, "\tt.Run(%s, func(t *testing.T) {\n", strconv.Quote(name))
			fmt.Fprintf(
				&source,
				"\t\tentity := Entity{ID: EntityID(%s), TypeID: EntityType%s, Support: EntitySupport(%s)}\n",
				strconv.Quote("generated_"+model.Package),
				entityTypeGoName(model),
				rawQuote(example.Support),
			)
			source.WriteString(
				"\t\tif _, err := catalog.NormalizeSupport(entity.TypeID, entity.Support); err != nil { t.Fatalf(\"support: %v\", err) }\n",
			)
			for index, state := range example.States {
				fmt.Fprintf(
					&source,
					"\t\tif _, err := catalog.NormalizeState(entity, Value(%s)); (err == nil) != %t { t.Errorf(%s, err) }\n",
					rawQuote(state.Value),
					state.Valid,
					strconv.Quote(fmt.Sprintf("State example %d error = %%v", index+1)),
				)
			}
			for _, operation := range model.Operations {
				values, supported := example.Operations[operation.Name]
				if !supported {
					continue
				}
				for index, parameters := range values.Parameters {
					fmt.Fprintf(
						&source,
						"\t\tif resolved, err := catalog.ResolveCommand(entity, OperationName(%s), CommandParameters(%s)); (err == nil) != %t { t.Errorf(%s, err) } else if err == nil && resolved.Deadline != %d*time.Millisecond { t.Errorf(%s, resolved.Deadline) }\n",
						strconv.Quote(operation.Name),
						rawQuote(parameters.Value),
						parameters.Valid,
						strconv.Quote(
							fmt.Sprintf("%s parameter example %d error = %%v", operation.Name, index+1),
						),
						operation.DeadlineMS,
						strconv.Quote(fmt.Sprintf("%s parameter example %d deadline = %%v", operation.Name, index+1)),
					)
				}
				for index, outcome := range values.Outcomes {
					fmt.Fprintf(
						&source,
						"\t\tif satisfied, err := catalog.Satisfies(entity, CommandRecord{OperationName: OperationName(%s), Parameters: CommandParameters(%s)}, Value(%s)); err != nil || satisfied != %t { t.Errorf(%s, satisfied, err) }\n",
						strconv.Quote(
							operation.Name,
						),
						rawQuote(outcome.Parameters),
						rawQuote(outcome.State),
						outcome.Satisfied,
						strconv.Quote(
							fmt.Sprintf("%s outcome %d: satisfied = %%v, error = %%v", operation.Name, index+1),
						),
					)
				}
			}
			source.WriteString("\t})\n")
		}
	}
	source.WriteString("}\n")
	formatted, err := formatGenerated(source.String())
	if err != nil {
		return output{}, err
	}
	return output{
		path:    filepath.Join(moduleRoot, "internal", "modules", "devices", "zz_generated_entitytypes_test.go"),
		content: formatted,
	}, nil
}
