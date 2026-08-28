package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type typeEmitter struct {
	declarations map[string]string
	order        []string
}

func renderTypes(model entityTypeModel) ([]byte, error) {
	emitter := &typeEmitter{declarations: make(map[string]string)}
	if err := emitter.define("State", model.StateSchema); err != nil {
		return nil, fmt.Errorf("state: %w", err)
	}
	if err := emitter.define("StateSupport", model.StateSupport); err != nil {
		return nil, fmt.Errorf("StateSupport: %w", err)
	}
	operationsSchema := model.SupportSchema.Properties["operations"]
	for _, operation := range model.Operations {
		if err := emitter.define(operation.GoName+"Support", operationsSchema.Properties[operation.Name]); err != nil {
			return nil, fmt.Errorf("%s support: %w", operation.Name, err)
		}
	}

	var operationFields strings.Builder
	for _, operation := range model.Operations {
		fieldType := operation.GoName + "Support"
		tag := operation.Name
		if !operation.Required {
			fieldType = "*" + fieldType
			tag += ",omitempty"
		}
		fmt.Fprintf(&operationFields, "\t%s %s `json:%s`\n", operation.GoName, fieldType, strconv.Quote(tag))
	}
	if err := emitter.add(
		"OperationSupport",
		"type OperationSupport struct {\n"+operationFields.String()+"}\n",
	); err != nil {
		return nil, err
	}
	if err := emitter.add(
		"Support",
		"type Support struct {\n\tState StateSupport `json:\"state\"`\n\tOperations OperationSupport `json:\"operations\"`\n}\n",
	); err != nil {
		return nil, err
	}
	for _, operation := range model.Operations {
		if err := emitter.define(operation.GoName+"Parameters", operation.ParametersSchema); err != nil {
			return nil, fmt.Errorf("%s parameters: %w", operation.Name, err)
		}
	}

	var source strings.Builder
	generatedHeader(&source)
	fmt.Fprintf(
		&source,
		"// Package %s provides schemas, behavior, and Go bindings for %s.\n",
		model.Package,
		model.TypeID,
	)
	fmt.Fprintf(&source, "package %s\n\n", model.Package)
	fmt.Fprintf(&source, "const TypeID = %s\n\n", strconv.Quote(model.TypeID))
	if len(model.Operations) > 0 {
		source.WriteString("const (\n")
		for _, operation := range model.Operations {
			fmt.Fprintf(&source, "\tOperation%s = %s\n", operation.GoName, strconv.Quote(operation.Name))
		}
		source.WriteString(")\n\n")
	}
	for _, name := range emitter.order {
		source.WriteString(emitter.declarations[name])
		source.WriteByte('\n')
	}
	return formatGenerated(source.String())
}

func (emitter *typeEmitter) define(name string, schema schemaNode) error {
	if _, exists := emitter.declarations[name]; exists {
		return fmt.Errorf("duplicate generated type %q", name)
	}
	var declaration string
	switch schema.Type {
	case "boolean":
		declaration = "type " + name + " bool\n"
	case "string":
		declaration = "type " + name + " string\n"
	case "integer":
		declaration = "type " + name + " int64\n"
	case "number":
		return errors.New("number schemas require a lossless binding and are not supported")
	case "array":
		if schema.Items == nil {
			return errors.New("array schema requires items")
		}
		itemType, err := emitter.propertyType(name+"Item", *schema.Items)
		if err != nil {
			return err
		}
		declaration = fmt.Sprintf("type %s []%s\n", name, itemType)
	case "object":
		if err := requireClosedObject(schema); err != nil {
			return err
		}
		properties := sortedProperties(schema.Properties)
		var fields strings.Builder
		seenFields := make(map[string]string, len(properties))
		for _, property := range properties {
			fieldName, err := exportedName(property)
			if err != nil {
				return err
			}
			if previous, collision := seenFields[fieldName]; collision {
				return fmt.Errorf("properties %q and %q both map to field %q", previous, property, fieldName)
			}
			seenFields[fieldName] = property
			fieldType, err := emitter.propertyType(name+fieldName, schema.Properties[property])
			if err != nil {
				return fmt.Errorf("property %q: %w", property, err)
			}
			tag := property
			if !required(schema, property) {
				fieldType = "*" + fieldType
				tag += ",omitempty"
			}
			fmt.Fprintf(&fields, "\t%s %s `json:%s`\n", fieldName, fieldType, strconv.Quote(tag))
		}
		declaration = "type " + name + " struct {\n" + fields.String() + "}\n"
	default:
		return fmt.Errorf("unsupported schema type %q", schema.Type)
	}
	return emitter.add(name, declaration)
}

func (emitter *typeEmitter) propertyType(name string, schema schemaNode) (string, error) {
	switch schema.Type {
	case "boolean":
		return "bool", nil
	case "string":
		return "string", nil
	case "integer":
		return "int64", nil
	case "number":
		return "", errors.New("number schemas require a lossless binding and are not supported")
	case "object", "array":
		if err := emitter.define(name, schema); err != nil {
			return "", err
		}
		return name, nil
	default:
		return "", fmt.Errorf("unsupported schema type %q", schema.Type)
	}
}

func (emitter *typeEmitter) add(name, declaration string) error {
	if _, exists := emitter.declarations[name]; exists {
		return fmt.Errorf("duplicate generated type %q", name)
	}
	emitter.declarations[name] = declaration
	emitter.order = append(emitter.order, name)
	return nil
}

func requireClosedObject(schema schemaNode) error {
	if schema.MaxProperties != nil && *schema.MaxProperties == 0 {
		return nil
	}
	if len(schema.AdditionalProperties) == 0 {
		return errors.New("object schema must set additionalProperties to false")
	}
	var additional bool
	if err := json.Unmarshal(schema.AdditionalProperties, &additional); err != nil || additional {
		return errors.New("typed object schema must set additionalProperties to false")
	}
	return nil
}

func renderCodecs(model entityTypeModel) ([]byte, error) {
	var source strings.Builder
	generatedHeader(&source)
	fmt.Fprintf(&source, "package %s\n\n", model.Package)
	source.WriteString(
		"import (\n\t\"embed\"\n\t\"encoding/json\"\n\t\"fmt\"\n\n\t\"github.com/mholtzscher/hearth/entitytypes\"\n)\n\n",
	)
	source.WriteString("const (\n")
	fmt.Fprintf(&source, "\tStateSchemaID = %s\n", strconv.Quote(model.StateSchema.ID))
	fmt.Fprintf(&source, "\tSupportSchemaID = %s\n", strconv.Quote(model.SupportSchema.ID))
	for _, operation := range model.Operations {
		fmt.Fprintf(
			&source,
			"\t%sParametersSchemaID = %s\n",
			operation.GoName,
			strconv.Quote(operation.ParametersSchema.ID),
		)
	}
	source.WriteString(")\n\n")
	schemaFiles := []string{model.StateFile, model.SupportFile}
	for _, operation := range model.Operations {
		schemaFiles = append(schemaFiles, operation.ParametersFile)
	}
	sort.Strings(schemaFiles)
	source.WriteString("// FS contains the authoritative Entity-type JSON Schemas.\n//\n//go:embed")
	previous := ""
	for _, path := range schemaFiles {
		if path == previous {
			continue
		}
		fmt.Fprintf(&source, " %s", strconv.Quote(path))
		previous = path
	}
	source.WriteString("\nvar FS embed.FS\n\n")
	source.WriteString("func SchemaFiles() map[string]string {\n\treturn map[string]string{\n")
	fmt.Fprintf(&source, "\t\tStateSchemaID: %s,\n", strconv.Quote(model.StateFile))
	fmt.Fprintf(&source, "\t\tSupportSchemaID: %s,\n", strconv.Quote(model.SupportFile))
	for _, operation := range model.Operations {
		fmt.Fprintf(
			&source,
			"\t\t%sParametersSchemaID: %s,\n",
			operation.GoName,
			strconv.Quote(operation.ParametersFile),
		)
	}
	source.WriteString("\t}\n}\n\n")
	source.WriteString(
		"type Codecs struct {\n\tState *entitytypes.JSONCodec[State]\n\tSupport *entitytypes.JSONCodec[Support]\n",
	)
	for _, operation := range model.Operations {
		fmt.Fprintf(
			&source,
			"\t%sParameters *entitytypes.JSONCodec[%sParameters]\n",
			operation.GoName,
			operation.GoName,
		)
	}
	source.WriteString("}\n\n")
	source.WriteString("func Compile() (*Codecs, error) {\n")
	source.WriteString(
		"\tstate, err := compileCodec[State](StateSchemaID, SchemaFiles()[StateSchemaID])\n\tif err != nil { return nil, err }\n",
	)
	source.WriteString(
		"\tsupport, err := compileCodec[Support](SupportSchemaID, SchemaFiles()[SupportSchemaID])\n\tif err != nil { return nil, err }\n",
	)
	for _, operation := range model.Operations {
		variable := lowerFirst(operation.GoName) + "Parameters"
		fmt.Fprintf(
			&source,
			"\t%s, err := compileCodec[%sParameters](%sParametersSchemaID, SchemaFiles()[%sParametersSchemaID])\n",
			variable,
			operation.GoName,
			operation.GoName,
			operation.GoName,
		)
		source.WriteString("\tif err != nil { return nil, err }\n")
	}
	source.WriteString("\treturn &Codecs{State: state, Support: support")
	for _, operation := range model.Operations {
		fmt.Fprintf(&source, ", %sParameters: %sParameters", operation.GoName, lowerFirst(operation.GoName))
	}
	source.WriteString("}, nil\n}\n\n")
	source.WriteString(
		"func compileCodec[T any](schemaID, path string) (*entitytypes.JSONCodec[T], error) {\n\traw, err := FS.ReadFile(path)\n\tif err != nil { return nil, fmt.Errorf(\"read %s: %w\", path, err) }\n\treturn entitytypes.CompileJSONCodec[T](schemaID, json.RawMessage(raw), nil)\n}\n",
	)
	return formatGenerated(source.String())
}
