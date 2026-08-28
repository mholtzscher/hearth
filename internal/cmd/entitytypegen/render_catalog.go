package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func renderCatalog(models []entityTypeModel, modulePath string, moduleRoot string) (output, error) {
	ordered := append([]entityTypeModel(nil), models...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].TypeID < ordered[right].TypeID })

	var source strings.Builder
	generatedHeader(&source)
	source.WriteString("package devices\n\n")
	source.WriteString("import (\n\t\"fmt\"\n\n")
	for _, model := range ordered {
		fmt.Fprintf(
			&source,
			"\tcontract%s %s\n",
			model.Package,
			strconv.Quote(modulePath+"/entitytypes/"+model.Package),
		)
	}
	source.WriteString(")\n\n")
	source.WriteString("const (\n")
	for _, model := range ordered {
		fmt.Fprintf(&source, "\tEntityType%s EntityTypeID = %s\n", entityTypeGoName(model), strconv.Quote(model.TypeID))
	}
	source.WriteString(")\n\n")

	source.WriteString("func NewBuiltinTypeCatalog() (*TypeCatalog, error) {\n")
	source.WriteString("\tdefinitions := make([]EntityTypeDefinition, 0, ")
	fmt.Fprintf(&source, "%d)\n", len(ordered))
	for _, model := range ordered {
		variable := lowerFirst(entityTypeGoName(model))
		fmt.Fprintf(
			&source,
			"\t%s, err := new%sTypeDefinition(EntityType%s)\n",
			variable,
			entityTypeGoName(model),
			entityTypeGoName(model),
		)
		fmt.Fprintf(&source, "\tif err != nil { return nil, err }\n\tdefinitions = append(definitions, %s)\n", variable)
	}
	source.WriteString("\treturn NewTypeCatalog(definitions)\n}\n\n")

	for _, model := range ordered {
		goName := entityTypeGoName(model)
		fmt.Fprintf(&source, "func new%sTypeDefinition(id EntityTypeID) (EntityTypeDefinition, error) {\n", goName)
		fmt.Fprintf(&source, "\tcodecs, err := contract%s.Compile()\n", model.Package)
		fmt.Fprintf(
			&source,
			"\tif err != nil { return EntityTypeDefinition{}, fmt.Errorf(%s, err) }\n",
			strconv.Quote("compile "+model.TypeID+" codecs: %w"),
		)
		for _, operation := range model.Operations {
			variable := lowerFirst(operation.GoName)
			fmt.Fprintf(&source, "\t%s := DefineOperation(\n", variable)
			fmt.Fprintf(&source, "\t\tOperationName(contract%s.Operation%s),\n", model.Package, operation.GoName)
			fmt.Fprintf(&source, "\t\tcodecs.%sParameters,\n", operation.GoName)
			fmt.Fprintf(
				&source,
				"\t\tfunc(support contract%s.Support) (contract%s.%sSupport, bool) {\n",
				model.Package,
				model.Package,
				operation.GoName,
			)
			if operation.Required {
				fmt.Fprintf(&source, "\t\t\treturn support.Operations.%s, true\n", operation.GoName)
			} else {
				fmt.Fprintf(
					&source,
					"\t\t\tif support.Operations.%s == nil { return contract%s.%sSupport{}, false }\n",
					operation.GoName,
					model.Package,
					operation.GoName,
				)
				fmt.Fprintf(&source, "\t\t\treturn *support.Operations.%s, true\n", operation.GoName)
			}
			source.WriteString("\t\t},\n")
			fmt.Fprintf(&source, "\t\tcontract%s.Validate%sParameters,\n", model.Package, operation.GoName)
			fmt.Fprintf(&source, "\t\tcontract%s.%sDeadline,\n", model.Package, operation.GoName)
			fmt.Fprintf(&source, "\t\tcontract%s.%sSatisfied,\n", model.Package, operation.GoName)
			source.WriteString("\t)\n")
		}
		fmt.Fprintf(
			&source,
			"\tdefinition, err := DefineEntityType(id, codecs.State, codecs.Support, contract%s.ValidateState, contract%s.EqualState",
			model.Package,
			model.Package,
		)
		for _, operation := range model.Operations {
			fmt.Fprintf(&source, ", %s", lowerFirst(operation.GoName))
		}
		source.WriteString(")\n")
		source.WriteString("\tif err != nil { return EntityTypeDefinition{}, err }\n\treturn definition, nil\n}\n\n")
	}

	formatted, err := formatGenerated(source.String())
	if err != nil {
		return output{}, err
	}
	return output{
		path:    filepath.Join(moduleRoot, "internal", "modules", "devices", "zz_generated_entitytypes.go"),
		content: formatted,
	}, nil
}

func entityTypeGoName(model entityTypeModel) string {
	packageName := model.Package
	if index := strings.LastIndex(packageName, "v"); index > 0 && index < len(packageName)-1 {
		version := packageName[index+1:]
		if _, conversionErr := strconv.Atoi(version); conversionErr == nil {
			prefix, nameErr := exportedName(packageName[:index])
			if nameErr != nil {
				panic(nameErr)
			}
			return prefix + "V" + version
		}
	}
	name, err := exportedName(packageName)
	if err != nil {
		panic(err)
	}
	return name
}
