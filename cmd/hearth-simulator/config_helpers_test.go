package main

import (
	"os"
	"path/filepath"
	"testing"
)

// validDevicesConfig is a minimal scripted Device list that satisfies the
// authoritative Entity-type schemas, so it only fails when it should.
const validDevicesConfig = "nats_url: \"nats://127.0.0.1:4222\"\n" +
	"adapters:\n" +
	"  - adapter_id: \"test-simulator\"\n" +
	"    devices:\n" +
	"      - binding_key: \"test-light\"\n" +
	"        name: \"Test light\"\n" +
	"        kind: \"light\"\n" +
	"        entities:\n" +
	"          - key: \"power\"\n" +
	"            name: \"Power\"\n" +
	"            type: \"hearth.power/v1\"\n" +
	"            support: {state: {}, operations: {set: {}}}\n" +
	"            initial: true\n"

// invalidDevicesConfig omits the colorhs set operation the type requires.
const invalidDevicesConfig = "nats_url: \"nats://127.0.0.1:4222\"\n" +
	"adapters:\n" +
	"  - adapter_id: \"test-simulator\"\n" +
	"    devices:\n" +
	"      - binding_key: \"test-light\"\n" +
	"        name: \"Test light\"\n" +
	"        kind: \"light\"\n" +
	"        entities:\n" +
	"          - key: \"color\"\n" +
	"            name: \"Color\"\n" +
	"            type: \"hearth.colorhs/v1\"\n" +
	"            support: {state: {}, operations: {}}\n" +
	"            initial: {active: true, hue: 120, saturation: 80}\n"

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "simulator.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
