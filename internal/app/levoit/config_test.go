package levoit //nolint:testpackage // Tests the private secret-file boundary.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigDefaultsAndValidation(t *testing.T) {
	t.Parallel()
	config := Config{AdapterID: "levoit-home", NATSURL: "nats://127.0.0.1:4222", CredentialsFile: "private.json"}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	runtime, err := config.runtimeConfig()
	if err != nil || runtime.PollInterval != time.Minute || runtime.TimeZone != "UTC" {
		t.Fatalf("runtime = %+v, error = %v", runtime, err)
	}
	for _, interval := range []time.Duration{0, -time.Second, 29 * time.Second, time.Hour + time.Second} {
		config.PollInterval = &interval
		if err = config.Validate(); err == nil {
			t.Fatalf("accepted interval %s", interval)
		}
	}
	config.PollInterval = nil
	config.TimeZone = "not/a-zone"
	if err = config.Validate(); err == nil {
		t.Fatal("accepted unknown timezone")
	}
}

func TestCredentialsRemainPrivateAndPreservePassword(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"email":"private@example.com","password":"  secret value  "}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	credentials, err := readCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.Email != "private@example.com" || credentials.Password != "  secret value  " {
		t.Fatal("credentials changed")
	}
	for _, data := range []string{
		`{"email":"private@example.com","password":`,
		`{"email":"private@example.com","password":42}`,
		`{"email":"private@example.com","password":""}`,
		`null`,
		strings.Repeat("private@example.com", 16384),
	} {
		if err = os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = readCredentials(path)
		if err == nil || strings.Contains(err.Error(), "private@example.com") {
			t.Fatal("invalid or unsafe credential error")
		}
	}
	if _, err = readCredentials(t.TempDir()); err == nil {
		t.Fatal("accepted directory")
	}
	if _, err = readCredentials(path + "missing"); err == nil {
		t.Fatal("accepted missing file")
	}
}
