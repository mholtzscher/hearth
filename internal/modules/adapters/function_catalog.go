package adapters

import (
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/mholtzscher/hearth/contracts/functionschema"
)

var functionReasonPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,127}$`)

func ParseCatalogID(value string) (CatalogID, error) {
	if validateID(value, "afc") != nil {
		return "", ErrInvalidFunctionInput
	}
	return CatalogID(value), nil
}

func ParseInvocationID(value string) (InvocationID, error) {
	if validateID(value, "afi") != nil {
		return "", ErrInvalidFunctionInput
	}
	return InvocationID(value), nil
}

func NewInvocationID() (InvocationID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return InvocationID("afi_" + id.String()), nil
}

func ValidFunctionKey(key FunctionKey) bool { return registrationSlugPattern.MatchString(string(key)) }

// PrepareCatalog validates freely constructed descriptors and owns their bytes.
// Both registration entry points call this rule independently.
func PrepareCatalog(catalog Catalog) (Catalog, error) {
	if ValidateAdapterID(catalog.AdapterID) != nil || validateID(string(catalog.RuntimeID), "run") != nil ||
		validateID(
			string(catalog.ID),
			"afc",
		) != nil || len(catalog.Functions) < 1 || len(catalog.Functions) > MaximumFunctions {
		return Catalog{}, ErrInvalidFunctionInput
	}
	functions := make([]Descriptor, 0, len(catalog.Functions))
	for _, descriptor := range catalog.Functions {
		prepared, _, _, err := PrepareDescriptor(descriptor)
		if err != nil {
			return Catalog{}, err
		}
		functions = append(functions, prepared)
	}
	slices.SortFunc(functions, func(a, b Descriptor) int { return strings.Compare(string(a.Key), string(b.Key)) })
	for index := 1; index < len(functions); index++ {
		if functions[index-1].Key == functions[index].Key {
			return Catalog{}, ErrInvalidFunctionInput
		}
	}
	catalog.Functions = functions
	return catalog, nil
}

// PrepareDescriptor owns normalized descriptor bytes and immutable validators.
// Persistence and protocol consumers reuse these within their boundary.
func PrepareDescriptor(descriptor Descriptor) (Descriptor, *functionschema.Schema, *functionschema.Schema, error) {
	const maximumDescriptionLength = 2000
	if !ValidFunctionKey(descriptor.Key) || !utf8.ValidString(descriptor.Description) ||
		strings.TrimSpace(
			descriptor.Description,
		) == "" || utf8.RuneCountInString(descriptor.Description) > maximumDescriptionLength ||
		descriptor.TimeoutMS < MinimumFunctionTimeoutMS || descriptor.TimeoutMS > MaximumFunctionTimeoutMS {
		return Descriptor{}, nil, nil, ErrInvalidFunctionInput
	}
	input, err := functionschema.Compile(descriptor.InputSchema)
	if err != nil {
		return Descriptor{}, nil, nil, ErrInvalidFunctionInput
	}
	output, err := functionschema.Compile(descriptor.OutputSchema)
	if err != nil {
		return Descriptor{}, nil, nil, ErrInvalidFunctionInput
	}
	descriptor.InputSchema = input.Document()
	descriptor.OutputSchema = output.Document()
	return descriptor, input, output, nil
}

func copyDescriptor(descriptor Descriptor) Descriptor {
	descriptor.InputSchema = slices.Clone(descriptor.InputSchema)
	descriptor.OutputSchema = slices.Clone(descriptor.OutputSchema)
	return descriptor
}
