package hearthd

import (
	"path/filepath"
	"testing"
)

func TestLoadExampleConfig(t *testing.T) {
	value, err := LoadConfig(filepath.Join("..", "..", "..", "configs", "hearthd.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if value.HTTPAddr != "127.0.0.1:8080" {
		t.Fatalf("http_addr = %q", value.HTTPAddr)
	}
}

func TestConfigRejectsNonLoopbackHTTP(t *testing.T) {
	value := Config{HTTPAddr: "0.0.0.0:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db"}
	if err := value.Validate(); err == nil {
		t.Fatal("non-loopback HTTP address unexpectedly accepted")
	}
}
