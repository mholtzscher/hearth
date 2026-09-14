package hearthd_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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
	value := hearthd.Config{
		HouseholdTimezone: "UTC",
		HTTPAddr:          "0.0.0.0:8080",
		NATSURL:           "nats://127.0.0.1:4222",
		SQLitePath:        "hearth.db",
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("validate non-loopback HTTP address: %v", err)
	}
}

func TestLoadConfigDefaultsObservationRetention(t *testing.T) {
	t.Parallel()
	value := loadRetentionConfig(t, "")
	if value.ObservationRetention != hearthd.DefaultObservationRetention {
		t.Fatalf(
			"observation_retention = %s, want default %s",
			value.ObservationRetention,
			hearthd.DefaultObservationRetention,
		)
	}
	if hearthd.DefaultObservationRetention != 30*24*time.Hour {
		t.Fatalf("default observation retention = %s, want 720h", hearthd.DefaultObservationRetention)
	}
}

func TestLoadConfigParsesExplicitObservationRetention(t *testing.T) {
	t.Parallel()
	// Eight days is the minimum: it stays above the seven-day JetStream retention.
	value := loadRetentionConfig(t, "observation_retention: 192h\n")
	if value.ObservationRetention != 192*time.Hour {
		t.Fatalf("observation_retention = %s, want 192h", value.ObservationRetention)
	}
}

func TestLoadConfigExplicitZeroObservationRetentionSelectsDefault(t *testing.T) {
	t.Parallel()
	// Zero keeps its documented default meaning: an explicit zero is
	// indistinguishable from unset and selects the default.
	value := loadRetentionConfig(t, "observation_retention: 0s\n")
	if value.ObservationRetention != hearthd.DefaultObservationRetention {
		t.Fatalf(
			"observation_retention = %s, want default %s",
			value.ObservationRetention,
			hearthd.DefaultObservationRetention,
		)
	}
}

func TestObservationRetentionRejectsNegativeAndJustBelowMinimum(t *testing.T) {
	t.Parallel()
	if hearthd.MinimumObservationRetention != 8*24*time.Hour {
		t.Fatalf(
			"minimum observation retention = %s, want 192h",
			hearthd.MinimumObservationRetention,
		)
	}
	for _, retention := range []time.Duration{
		-time.Hour,
		hearthd.MinimumObservationRetention - time.Nanosecond,
	} {
		value := hearthd.Config{HouseholdTimezone: "UTC",
			HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
			ObservationRetention: retention,
		}
		if err := value.Validate(); err == nil {
			t.Fatalf("observation retention %s unexpectedly accepted", retention)
		}
	}
}

func TestLoadConfigRejectsObservationRetentionBelowMinimum(t *testing.T) {
	t.Parallel()
	short := hearthd.Config{HouseholdTimezone: "UTC",
		HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
		ObservationRetention: 7 * 24 * time.Hour,
	}
	if err := short.Validate(); err == nil {
		t.Fatal("seven-day observation retention unexpectedly accepted")
	}
	path := writeRetentionConfig(t, "observation_retention: 168h\n")
	if _, err := hearthd.LoadConfig(path); err == nil {
		t.Fatal("seven-day observation retention file unexpectedly accepted")
	}
}

func TestLoadConfigDefaultsAutomationHistoryRetention(t *testing.T) {
	t.Parallel()
	value := loadRetentionConfig(t, "")
	if value.AutomationHistoryRetention != hearthd.DefaultAutomationHistoryRetention {
		t.Fatalf(
			"automation_history_retention = %s, want default %s",
			value.AutomationHistoryRetention,
			hearthd.DefaultAutomationHistoryRetention,
		)
	}
	if hearthd.DefaultAutomationHistoryRetention != 30*24*time.Hour {
		t.Fatalf("default automation history retention = %s, want 720h", hearthd.DefaultAutomationHistoryRetention)
	}
	if hearthd.MinimumAutomationHistoryRetention != 8*24*time.Hour {
		t.Fatalf("minimum automation history retention = %s, want 192h", hearthd.MinimumAutomationHistoryRetention)
	}
	if hearthd.AutomationFactMaximumAge != 30*time.Second {
		t.Fatalf("automation fact maximum age = %s, want 30s", hearthd.AutomationFactMaximumAge)
	}
}

func TestLoadConfigParsesExplicitAutomationHistoryRetention(t *testing.T) {
	t.Parallel()
	value := loadRetentionConfig(t, "automation_history_retention: 192h\n")
	if value.AutomationHistoryRetention != 192*time.Hour {
		t.Fatalf("automation_history_retention = %s, want 192h", value.AutomationHistoryRetention)
	}
	if got := value.EffectiveAutomationHistoryRetention(); got != 192*time.Hour {
		t.Fatalf("effective automation history retention = %s, want 192h", got)
	}
}

func TestAutomationHistoryRetentionRejectsBelowMinimum(t *testing.T) {
	t.Parallel()
	for _, retention := range []time.Duration{
		-time.Hour,
		hearthd.MinimumAutomationHistoryRetention - time.Nanosecond,
	} {
		value := hearthd.Config{HouseholdTimezone: "UTC",
			HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
			AutomationHistoryRetention: retention,
		}
		if err := value.Validate(); err == nil {
			t.Fatalf("automation history retention %s unexpectedly accepted", retention)
		}
	}
	var unset hearthd.Config
	if got := unset.EffectiveAutomationHistoryRetention(); got != hearthd.DefaultAutomationHistoryRetention {
		t.Fatalf("effective unset retention = %s, want default", got)
	}
}

func TestLoadExampleConfigDocumentsAutomationHistoryRetention(t *testing.T) {
	t.Parallel()
	value, err := hearthd.LoadConfig(filepath.Join("..", "..", "..", "configs", "hearthd.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if value.AutomationHistoryRetention != 720*time.Hour {
		t.Fatalf("example automation_history_retention = %s, want 720h", value.AutomationHistoryRetention)
	}
}

func TestEffectiveObservationRetentionFallsBackToDefault(t *testing.T) {
	t.Parallel()
	var unset hearthd.Config
	if got := unset.EffectiveObservationRetention(); got != hearthd.DefaultObservationRetention {
		t.Fatalf("effective retention = %s, want default %s", got, hearthd.DefaultObservationRetention)
	}
	set := hearthd.Config{HouseholdTimezone: "UTC",
		HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
		ObservationRetention: 8 * 24 * time.Hour,
	}
	if got := set.EffectiveObservationRetention(); got != 8*24*time.Hour {
		t.Fatalf("effective retention = %s, want 192h", got)
	}
	if err := set.Validate(); err != nil {
		t.Fatalf("minimum observation retention rejected: %v", err)
	}
}

func loadRetentionConfig(t *testing.T, retentionLine string) hearthd.Config {
	t.Helper()
	value, err := hearthd.LoadConfig(writeRetentionConfig(t, retentionLine))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func writeRetentionConfig(t *testing.T, retentionLine string) string {
	t.Helper()
	contents := "http_addr: 127.0.0.1:8080\nnats_url: nats://127.0.0.1:4222\n" +
		"sqlite_path: hearth.db\nhousehold_timezone: UTC\n" + retentionLine
	path := filepath.Join(t.TempDir(), "hearth.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
