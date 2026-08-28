package main

import (
	"encoding/json"
	"errors"
	"fmt"
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

func loadExamples(directory, relative string, operations []operationModel) (examplesFile, error) {
	if relative == "" {
		return examplesFile{}, errors.New("examples is required")
	}
	path, err := localPath(directory, relative)
	if err != nil {
		return examplesFile{}, err
	}
	var examples examplesFile
	if err := decodeStrictFile(path, &examples); err != nil {
		return examplesFile{}, err
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
		if err := json.Unmarshal(example.Support, &supportShape); err != nil {
			return examplesFile{}, fmt.Errorf("case %q support: %w", example.Name, err)
		}
		if supportShape.Operations == nil {
			return examplesFile{}, fmt.Errorf("case %q support has no operations object", example.Name)
		}
		if err := requireValidityCoverage(example.States); err != nil {
			return examplesFile{}, fmt.Errorf("case %q states: %w", example.Name, err)
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
			if err := requireValidityCoverage(values.Parameters); err != nil {
				return examplesFile{}, fmt.Errorf("case %q operation %q parameters: %w", example.Name, name, err)
			}
			if err := requireOutcomeCoverage(values.Outcomes); err != nil {
				return examplesFile{}, fmt.Errorf("case %q operation %q outcomes: %w", example.Name, name, err)
			}
		}
	}
	return examples, nil
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
