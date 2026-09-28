package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The daemon readiness URL must follow the runtime Adapter ID, not the ID in
// the example. An environment override has the same precedence as the CLI.
func TestReadyUsesEffectiveAdapterID(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name, config, override, want string
	}{
		{"YAML", "adapter_id: other-network\n", "", "other-network"},
		{"quoted YAML", "adapter_id: 'quoted-id'\n", "", "quoted-id"},
		{"environment", "adapter_id: other-network\n", "override-id", "override-id"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			checkReadyID(t, testCase.config, testCase.override, testCase.want)
		})
	}
}

func checkReadyID(t *testing.T, config, override, want string) {
	t.Helper()
	root := t.TempDir()
	configDir := filepath.Join(root, ".data", "simulator-stack")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "zwavejs.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "curl"),
		[]byte("#!/bin/sh\nprintf '%s\\n' \"$2\" > \"$READY_URL_FILE\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	urlFile := filepath.Join(root, "url")
	command := exec.CommandContext(t.Context(), "bash", filepath.Join(mustGetwd(t), "ready.sh"))
	command.Dir = root
	command.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"),
		"SIM_CORE_PORT=8080", "HEARTH_ADAPTER_ZWAVEJS_ADAPTER_ID="+override, "READY_URL_FILE="+urlFile)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("ready probe: %v: %s", err, output)
	}
	url, err := os.ReadFile(urlFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(url), "/v1/adapters/"+want) {
		t.Fatalf("probe URL = %q, want Adapter ID %q", url, want)
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
