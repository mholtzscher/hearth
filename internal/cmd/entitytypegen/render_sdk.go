package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

//nolint:funlen,gocognit // Keeping the generated facade template together makes its emitted structure reviewable.
func renderFacade(model entityTypeModel, modulePath string) output {
	var source strings.Builder
	generatedHeader(&source)
	fmt.Fprintf(&source, "package %s\n\n", model.Package)
	// Stateless entity types carry no State observations, so the facade
	// defines no ObservationInput or NewObservation. Event-source types also
	// carry no Operations, so the facade defines only name validation and a
	// typed Entity Event builder. Time is only needed for the observation
	// input timestamps.
	writesJSON := len(model.Operations) > 0
	writesErrors := len(model.Operations) > 0 || model.EventSource
	source.WriteString("import (\n")
	if writesJSON {
		source.WriteString("\t\"encoding/json\"\n")
	}
	if writesErrors {
		source.WriteString("\t\"errors\"\n")
	}
	source.WriteString("\t\"fmt\"\n\t\"sync\"\n")
	if !model.Stateless {
		source.WriteString("\t\"time\"\n")
	}
	source.WriteString("\n")
	fmt.Fprintf(
		&source,
		"\tcontract%s %s\n",
		model.Package,
		strconv.Quote(modulePath+"/entitytypes/"+model.Package),
	)
	source.WriteString(
		"\t\"github.com/mholtzscher/hearth/sdk/adapter\"\n\t\"github.com/mholtzscher/hearth/sdk/adapter/typed\"\n)\n\n",
	)
	fmt.Fprintf(
		&source,
		"type Support = contract%s.Support\ntype State = contract%s.State\n",
		model.Package,
		model.Package,
	)
	fmt.Fprintf(
		&source,
		"type StateSupport = contract%s.StateSupport\ntype OperationSupport = contract%s.OperationSupport\n",
		model.Package,
		model.Package,
	)
	if model.EventSource {
		fmt.Fprintf(&source, "type SupportEvents = contract%s.SupportEvents\n", model.Package)
	}
	for _, operation := range model.Operations {
		fmt.Fprintf(
			&source,
			"type %sSupport = contract%s.%sSupport\n",
			operation.GoName,
			model.Package,
			operation.GoName,
		)
		fmt.Fprintf(
			&source,
			"type %sParameters = contract%s.%sParameters\n",
			operation.GoName,
			model.Package,
			operation.GoName,
		)
		fmt.Fprintf(&source, "type %sCommand = typed.Command[%sParameters]\n", operation.GoName, operation.GoName)
	}
	source.WriteByte('\n')
	if len(model.Operations) > 0 {
		source.WriteString("type Handlers struct {\n")
		for _, operation := range model.Operations {
			fmt.Fprintf(&source, "\t%s typed.Handler[%sParameters]\n", operation.GoName, operation.GoName)
		}
		source.WriteString("}\n\n")
	}
	if model.Stateless {
		if model.EventSource {
			source.WriteString(
				"// Event-source entity types carry no State observations and no Operations;\n" +
					"// the facade defines only typed support and Entity Event name behavior.\n\n",
			)
		} else {
			source.WriteString(
				"// Stateless entity types carry no State observations; the facade\n" +
					"// intentionally defines no ObservationInput or NewObservation.\n\n",
			)
		}
	} else {
		source.WriteString(
			"type ObservationInput struct {\n\tEntityID string\n\tSupport Support\n\tState State\n\tAdapterReceivedAt time.Time\n\tSourceUpdatedAt *time.Time\n}\n\n",
		)
	}
	source.WriteString("var (\n\tcompileOnce sync.Once\n")
	fmt.Fprintf(&source, "\tsharedCodecs *contract%s.Codecs\n", model.Package)
	source.WriteString("\tcompileErr error\n)\n\n")
	source.WriteString(
		"func NewEntityDescriptor(metadata adapter.EntityMetadata, support Support) (adapter.EntityDescriptor, error) {\n\tcodecs, err := codecs()\n\tif err != nil { return adapter.EntityDescriptor{}, err }\n\tdescriptor, err := typed.NewTypedEntityDescriptor(metadata, ",
	)
	fmt.Fprintf(
		&source,
		"contract%s.TypeID, support, codecs.Support)\n",
		model.Package,
	)
	source.WriteString("\tif err != nil { return adapter.EntityDescriptor{}, err }\n")
	fmt.Fprintf(
		&source,
		"\tif err := contract%s.ValidateSupport(support); err != nil { return adapter.EntityDescriptor{}, &adapter.ValidationError{Err: fmt.Errorf(\"invalid Entity support: %%w\", err)} }\n\treturn descriptor, nil\n}\n\n",
		model.Package,
	)
	if model.EventSource {
		writeEntityEventBuilder(&source, model)
	}

	if len(model.Operations) > 0 {
		source.WriteString(
			"func NewCommandHandler(entityID string, support Support, handlers Handlers) (adapter.CommandHandler, error) {\n\tcodecs, err := codecs()\n\tif err != nil { return nil, err }\n\tif _, err := codecs.Support.Encode(support); err != nil { return nil, validationError(fmt.Errorf(\"invalid Entity support: %w\", err)) }\n",
		)
		fmt.Fprintf(
			&source,
			"\tif err := contract%s.ValidateSupport(support); err != nil { return nil, validationError(fmt.Errorf(\"invalid Entity support: %%w\", err)) }\n\tvar routes []typed.Route\n",
			model.Package,
		)
		for _, operation := range model.Operations {
			if operation.Required {
				fmt.Fprintf(
					&source,
					"\tif handlers.%s == nil { return nil, validationError(errors.New(%s)) }\n",
					operation.GoName,
					strconv.Quote(operation.Name+" handler is required"),
				)
				fmt.Fprintf(
					&source,
					"\t%sSupport := support.Operations.%s\n",
					lowerFirst(operation.GoName),
					operation.GoName,
				)
				writeRoute(&source, model, operation, "\t", lowerFirst(operation.GoName)+"Support")
			} else {
				fmt.Fprintf(&source, "\tif support.Operations.%s != nil {\n", operation.GoName)
				fmt.Fprintf(
					&source,
					"\t\tif handlers.%s == nil { return nil, validationError(errors.New(%s)) }\n",
					operation.GoName,
					strconv.Quote(operation.Name+" handler is required when the operation is supported"),
				)
				fmt.Fprintf(
					&source,
					"\t\t%sSupport := *support.Operations.%s\n",
					lowerFirst(operation.GoName),
					operation.GoName,
				)
				writeRoute(&source, model, operation, "\t\t", lowerFirst(operation.GoName)+"Support")
				fmt.Fprintf(
					&source,
					"\t} else if handlers.%s != nil { return nil, validationError(errors.New(%s)) }\n",
					operation.GoName,
					strconv.Quote(operation.Name+" handler was provided for an unsupported operation"),
				)
			}
		}
		source.WriteString(
			"\thandler, err := typed.NewCommandHandler(routes...)\n\tif err != nil { return nil, validationError(err) }\n\treturn handler, nil\n}\n\n",
		)
	}

	if !model.Stateless {
		source.WriteString(
			"func NewObservation(input ObservationInput) (adapter.Observation, error) {\n\tcodecs, err := codecs()\n\tif err != nil { return adapter.Observation{}, err }\n\treturn typed.NewTypedEntityObservation(typed.EntityObservationInput[State, Support]{EntityID: input.EntityID, Support: input.Support, State: input.State, AdapterReceivedAt: input.AdapterReceivedAt, SourceUpdatedAt: input.SourceUpdatedAt}, codecs.State, codecs.Support, ",
		)
		fmt.Fprintf(&source, "contract%s.ValidateState", model.Package)
		source.WriteString(")\n}\n\n")
	}
	fmt.Fprintf(
		&source,
		"func codecs() (*contract%s.Codecs, error) {\n\tcompileOnce.Do(func() { sharedCodecs, compileErr = contract%s.Compile() })\n\tif compileErr != nil { return nil, fmt.Errorf(\"compile Entity-type schemas: %%w\", compileErr) }\n\treturn sharedCodecs, nil\n}\n\n",
		model.Package,
		model.Package,
	)
	if len(model.Operations) > 0 || model.EventSource {
		source.WriteString("func validationError(err error) error { return &adapter.ValidationError{Err: err} }\n")
	}
	return output{
		path:    filepath.Join(model.ModuleRoot, "sdk", "adapter", model.Package, "zz_generated_facade.go"),
		content: []byte(source.String()),
	}
}

// writeEntityEventBuilder emits the typed Entity Event builder for an
// event-source facade. The builder validates local support and name shape so a
// report leaves the Adapter well-formed; Core still decides whether the name is
// currently supported when the report arrives.
func writeEntityEventBuilder(source *strings.Builder, model entityTypeModel) {
	source.WriteString("// EntityEventInput carries one Entity Event report for typed validation.\n")
	source.WriteString("type EntityEventInput struct {\n\tEntityID string\n\tSupport Support\n\tName string\n}\n\n")
	source.WriteString("// NewEntityEvent validates Entity identity, Entity support, and the reported\n")
	source.WriteString("// name, then returns the report a Session publishes.\n")
	source.WriteString("func NewEntityEvent(input EntityEventInput) (adapter.EntityEvent, error) {\n")
	source.WriteString("\tcodecs, err := codecs()\n\tif err != nil { return adapter.EntityEvent{}, err }\n")
	source.WriteString("\tif input.EntityID == \"\" {\n")
	source.WriteString(
		"\t\treturn adapter.EntityEvent{}, validationError(errors.New(\"Entity Event entity ID is required\"))\n",
	)
	source.WriteString("\t}\n")
	source.WriteString("\tif _, err := codecs.Support.Encode(input.Support); err != nil {\n")
	source.WriteString(
		"\t\treturn adapter.EntityEvent{}, validationError(fmt.Errorf(\"invalid Entity support: %w\", err))\n",
	)
	source.WriteString("\t}\n")
	// Schema encoding alone accepts relationally invalid supports, so the
	// builder must also run the generated semantic rules. This mirrors
	// NewEntityDescriptor and NewCommandHandler, which call ValidateSupport
	// after the schema codec.
	fmt.Fprintf(
		source,
		"\tif err := contract%s.ValidateSupport(input.Support); err != nil {\n",
		model.Package,
	)
	source.WriteString(
		"\t\treturn adapter.EntityEvent{}, validationError(fmt.Errorf(\"invalid Entity support: %w\", err))\n",
	)
	source.WriteString("\t}\n")
	fmt.Fprintf(
		source,
		"\tif err := contract%s.ValidateEntityEventName(input.Support, input.Name); err != nil {\n",
		model.Package,
	)
	source.WriteString("\t\treturn adapter.EntityEvent{}, validationError(err)\n\t}\n")
	source.WriteString("\treturn adapter.EntityEvent{EntityID: input.EntityID, Name: input.Name}, nil\n}\n\n")
}

func writeRoute(
	source *strings.Builder,
	model entityTypeModel,
	operation operationModel,
	indent, supportVariable string,
) {
	variable := lowerFirst(operation.GoName) + "Route"
	fmt.Fprintf(
		source,
		"%s%s, err := typed.Operation(entityID, contract%s.Operation%s, func(raw json.RawMessage) (%sParameters, error) {\n",
		indent,
		variable,
		model.Package,
		operation.GoName,
		operation.GoName,
	)
	fmt.Fprintf(source, "%s\tparameters, _, err := codecs.%sParameters.Decode(raw)\n", indent, operation.GoName)
	fmt.Fprintf(source, "%s\tif err != nil { return %sParameters{}, err }\n", indent, operation.GoName)
	fmt.Fprintf(
		source,
		"%s\tif err := contract%s.Validate%sParameters(support, %s, parameters); err != nil { return %sParameters{}, err }\n",
		indent,
		model.Package,
		operation.GoName,
		supportVariable,
		operation.GoName,
	)
	fmt.Fprintf(source, "%s\treturn parameters, nil\n%s}, handlers.%s)\n", indent, indent, operation.GoName)
	fmt.Fprintf(source, "%sif err != nil { return nil, validationError(err) }\n", indent)
	fmt.Fprintf(source, "%sroutes = append(routes, %s)\n", indent, variable)
}
