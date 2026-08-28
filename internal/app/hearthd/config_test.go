package hearthd_test

import (
	"path/filepath"
	"testing"

	"github.com/mholtzscher/hearth/internal/app/hearthd"
)

func TestLoadExampleConfig(t *testing.T) {
	t.Parallel()
	value, err := hearthd.LoadConfig(filepath.Join("..", "..", "..", "configs", "hearthd.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if value.HTTPAddr != "127.0.0.1:8080" {
		t.Fatalf("http_addr = %q", value.HTTPAddr)
	}
}

func TestConfigAcceptsNonLoopbackHTTP(t *testing.T) {
	t.Parallel()
	value := hearthd.Config{HTTPAddr: "0.0.0.0:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db"}
	if err := value.Validate(); err != nil {
		t.Fatalf("validate non-loopback HTTP address: %v", err)
	}
}
