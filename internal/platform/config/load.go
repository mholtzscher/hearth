package config

import (
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

func LoadFile(path string, destination any) error {
	file, openErr := os.Open(path)
	if openErr != nil {
		return fmt.Errorf("open config %q: %w", path, openErr)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode config %q: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode config %q: multiple YAML documents are not allowed", path)
		}
		return fmt.Errorf("decode config %q: %w", path, err)
	}
	return nil
}
