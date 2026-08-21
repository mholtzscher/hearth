package config

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

func LoadFile(path string, destination any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open config %q: %w", path, err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode config %q: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode config %q: multiple YAML documents are not allowed", path)
		}
		return fmt.Errorf("decode config %q: %w", path, err)
	}
	return nil
}
