package config

import (
	"errors"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadYAML decodes one YAML document. A missing implicit default is
// optional; an explicitly selected path must exist. Errors never expose paths
// or configuration values.
func LoadYAML[T any](path string, explicit bool) (T, error) {
	var destination T
	var zero T
	file, err := os.Open(path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return destination, nil
		}
		return zero, errors.New("configuration file could not be read")
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	if decodeErr := decoder.Decode(&destination); decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
		return zero, errors.New("configuration file contains invalid YAML")
	}
	var extra any
	if decodeErr := decoder.Decode(&extra); !errors.Is(decodeErr, io.EOF) {
		return zero, errors.New("configuration file must contain a single YAML document")
	}
	return destination, nil
}
