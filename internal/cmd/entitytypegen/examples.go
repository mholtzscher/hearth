package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type examplesFile struct {
	Cases []exampleCase `json:"cases"`
}

type exampleCase struct {
	Name       string                       `json:"name"`
	Support    json.RawMessage              `json:"support"`
	States     []validityExample            `json:"states"`
	Operations map[string]operationExamples `json:"operations"`
}

type validityExample struct {
	Value json.RawMessage `json:"value"`
	Valid bool            `json:"valid"`
}

type operationExamples struct {
	Parameters []validityExample `json:"parameters"`
	Outcomes   []outcomeExample  `json:"outcomes"`
}

type outcomeExample struct {
	Parameters json.RawMessage `json:"parameters"`
	State      json.RawMessage `json:"state"`
	Satisfied  bool            `json:"satisfied"`
}

// normalizeExamplesPath returns the cleaned slash-separated path for the one
// exact examples file that generated conformance tests embed.
func normalizeExamplesPath(directory, relative string) (string, error) {
	if character := strings.IndexAny(relative, `*?[]\`); character >= 0 {
		return "", fmt.Errorf(
			"examples path %q contains unsupported Go embed pattern metacharacter %q",
			relative,
			relative[character:character+1],
		)
	}
	path, err := localPath(directory, relative)
	if err != nil {
		return "", err
	}
	normalized, err := filepath.Rel(directory, path)
	if err != nil {
		return "", fmt.Errorf("normalize examples path %q: %w", relative, err)
	}
	normalized = filepath.ToSlash(normalized)
	if strings.HasPrefix(normalized, "all:") {
		return "", fmt.Errorf("examples path %q uses unsupported Go embed pattern prefix %q", relative, "all:")
	}
	if validationErr := validateExamplesEmbedPath(directory, normalized); validationErr != nil {
		return "", fmt.Errorf("examples path %q: %w", relative, validationErr)
	}
	return normalized, nil
}

func validateExamplesEmbedPath(directory, normalized string) error {
	if !fs.ValidPath(normalized) {
		return errors.New("is not a valid Go embed path")
	}
	components := strings.Split(normalized, "/")
	for _, component := range components {
		if err := validateExamplesEmbedComponent(component); err != nil {
			return fmt.Errorf("component %q is not valid for Go embed: %w", component, err)
		}
	}

	path := directory
	for index, component := range components {
		path = filepath.Join(path, component)
		exists, err := validateExamplesEmbedFilesystemComponent(path, component, index == len(components)-1)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
	}
	return nil
}

func validateExamplesEmbedFilesystemComponent(path, component string, file bool) (bool, error) {
	info, statErr := os.Lstat(path)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return false, nil
		}
		return false, fmt.Errorf("stat component %q: %w", component, statErr)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return true, fmt.Errorf("component %q is a symlink", component)
	}
	if file {
		if !info.Mode().IsRegular() {
			return true, fmt.Errorf("component %q is not a regular file", component)
		}
		return true, nil
	}
	if !info.IsDir() {
		return true, fmt.Errorf("component %q is not a directory", component)
	}
	if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
		return true, fmt.Errorf("component %q is a nested Go module", component)
	}
	return true, nil
}

func validateExamplesEmbedComponent(component string) error {
	if !utf8.ValidString(component) || component == "" ||
		strings.Count(component, ".") == len(component) || strings.HasSuffix(component, ".") {
		return errors.New("invalid filename")
	}
	for _, character := range component {
		if !validExamplesEmbedFilenameCharacter(character) {
			return fmt.Errorf("invalid character %q", character)
		}
	}
	switch component {
	case ".bzr", ".git", ".hg", ".svn":
		return errors.New("reserved filename")
	}
	for _, reserved := range []string{"CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"} {
		short := component
		if dot := strings.IndexByte(short, '.'); dot >= 0 {
			short = short[:dot]
		}
		if strings.EqualFold(short, reserved) {
			return errors.New("reserved filename")
		}
	}
	return nil
}

func validExamplesEmbedFilenameCharacter(character rune) bool {
	if character < utf8.RuneSelf {
		if character >= '0' && character <= '9' ||
			character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z' {
			return true
		}
		return strings.ContainsRune("!#$%&()+,-.=@[]^_{}~ ", character)
	}
	return unicode.IsLetter(character)
}

//nolint:gocognit // Validation follows the nested examples document shape in one linear pass.
func loadExamples(directory, relative string, operations []operationModel) (examplesFile, error) {
	if relative == "" {
		return examplesFile{}, errors.New("examples is required")
	}
	path, err := localPath(directory, relative)
	if err != nil {
		return examplesFile{}, err
	}
	var examples examplesFile
	if decodeErr := decodeStrictFile(path, &examples); decodeErr != nil {
		return examplesFile{}, decodeErr
	}
	if len(examples.Cases) == 0 {
		return examplesFile{}, errors.New("at least one conformance case is required")
	}
	operationNames := make(map[string]struct{}, len(operations))
	for _, operation := range operations {
		operationNames[operation.Name] = struct{}{}
	}
	caseNames := make(map[string]struct{}, len(examples.Cases))
	for caseIndex, example := range examples.Cases {
		location := fmt.Sprintf("case %d", caseIndex+1)
		if example.Name == "" {
			return examplesFile{}, fmt.Errorf("%s has no name", location)
		}
		if _, duplicate := caseNames[example.Name]; duplicate {
			return examplesFile{}, fmt.Errorf("duplicate case name %q", example.Name)
		}
		caseNames[example.Name] = struct{}{}
		if len(example.Support) == 0 {
			return examplesFile{}, fmt.Errorf("case %q has no support", example.Name)
		}
		var supportShape struct {
			Operations map[string]json.RawMessage `json:"operations"`
		}
		if decodeErr := json.Unmarshal(example.Support, &supportShape); decodeErr != nil {
			return examplesFile{}, fmt.Errorf("case %q support: %w", example.Name, decodeErr)
		}
		if supportShape.Operations == nil {
			return examplesFile{}, fmt.Errorf("case %q support has no operations object", example.Name)
		}
		if coverageErr := requireValidityCoverage(example.States); coverageErr != nil {
			return examplesFile{}, fmt.Errorf("case %q states: %w", example.Name, coverageErr)
		}
		if len(example.Operations) != len(supportShape.Operations) {
			return examplesFile{}, fmt.Errorf(
				"case %q examples must exactly match its supported operations",
				example.Name,
			)
		}
		for name := range supportShape.Operations {
			if _, exists := example.Operations[name]; !exists {
				return examplesFile{}, fmt.Errorf(
					"case %q has no examples for supported operation %q",
					example.Name,
					name,
				)
			}
		}
		for name, values := range example.Operations {
			if _, exists := operationNames[name]; !exists {
				return examplesFile{}, fmt.Errorf("case %q has unknown operation %q", example.Name, name)
			}
			if _, supported := supportShape.Operations[name]; !supported {
				return examplesFile{}, fmt.Errorf(
					"case %q has examples for unsupported operation %q",
					example.Name,
					name,
				)
			}
			if coverageErr := requireValidityCoverage(values.Parameters); coverageErr != nil {
				return examplesFile{}, fmt.Errorf(
					"case %q operation %q parameters: %w", example.Name, name, coverageErr,
				)
			}
			if coverageErr := requireOutcomeCoverage(values.Outcomes); coverageErr != nil {
				return examplesFile{}, fmt.Errorf(
					"case %q operation %q outcomes: %w", example.Name, name, coverageErr,
				)
			}
		}
	}
	if coverageErr := requireOperationCoverage(examples.Cases, operations); coverageErr != nil {
		return examplesFile{}, coverageErr
	}
	return examples, nil
}

// requireOperationCoverage keeps optional operations honest: every
// manifest-declared operation must appear in at least one case's support and
// operation examples, otherwise the generated wiring would silently drop it.
func requireOperationCoverage(cases []exampleCase, operations []operationModel) error {
	covered := make(map[string]struct{}, len(cases))
	for _, example := range cases {
		for name := range example.Operations {
			covered[name] = struct{}{}
		}
	}
	for _, operation := range operations {
		if _, ok := covered[operation.Name]; !ok {
			return fmt.Errorf("operation %q has no examples in any case", operation.Name)
		}
	}
	return nil
}

func requireValidityCoverage(examples []validityExample) error {
	var valid, invalid bool
	for index, example := range examples {
		if len(example.Value) == 0 {
			return fmt.Errorf("example %d has no value", index+1)
		}
		if example.Valid {
			valid = true
		} else {
			invalid = true
		}
	}
	if !valid || !invalid {
		return errors.New("requires at least one valid and one invalid example")
	}
	return nil
}

func requireOutcomeCoverage(examples []outcomeExample) error {
	var satisfied, unsatisfied bool
	for index, example := range examples {
		if len(example.Parameters) == 0 || len(example.State) == 0 {
			return fmt.Errorf("example %d requires parameters and state", index+1)
		}
		if example.Satisfied {
			satisfied = true
		} else {
			unsatisfied = true
		}
	}
	if !satisfied || !unsatisfied {
		return errors.New("requires at least one satisfied and one unsatisfied example")
	}
	return nil
}
