package hearthd_test

import (
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/app/hearthd"
)

func TestHouseholdTimezoneValidation(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", "Local", "+05:30", "-0700", "05:30", " UTC", "Mars/Olympus"} {
		config := hearthd.Config{
			HouseholdTimezone: name,
			HTTPAddr:          "127.0.0.1:8080",
			NATSURL:           "nats://127.0.0.1:4222",
			SQLitePath:        "hearth.db",
		}
		if err := config.Validate(); err == nil {
			t.Fatalf("timezone %q unexpectedly accepted", name)
		}
	}
	for _, name := range []string{"UTC", "America/New_York", "Europe/London", "Asia/Kolkata"} {
		config := hearthd.Config{
			HouseholdTimezone: name,
			HTTPAddr:          "127.0.0.1:8080",
			NATSURL:           "nats://127.0.0.1:4222",
			SQLitePath:        "hearth.db",
		}
		if err := config.Validate(); err != nil {
			t.Fatalf("timezone %q: %v", name, err)
		}
		location, err := config.LoadHouseholdTimezone()
		if err != nil || location.String() != name {
			t.Fatalf("location = %v, %v", location, err)
		}
	}
}

func TestAutomationHistoryRetentionConfiguration(t *testing.T) {
	t.Parallel()
	for _, setting := range []string{"", "automation_history_retention: 0s\n"} {
		config := loadRetentionConfig(t, setting)
		if config.AutomationHistoryRetention != 30*24*time.Hour ||
			config.EffectiveAutomationHistoryRetention() != 30*24*time.Hour {
			t.Fatalf("default automation retention = %s", config.AutomationHistoryRetention)
		}
	}
	minimum := loadRetentionConfig(t, "automation_history_retention: 24h\n")
	if minimum.EffectiveAutomationHistoryRetention() != 24*time.Hour {
		t.Fatalf("explicit automation retention = %s", minimum.AutomationHistoryRetention)
	}
	for _, duration := range []time.Duration{-time.Hour, 24*time.Hour - time.Nanosecond} {
		minimum.AutomationHistoryRetention = duration
		if err := minimum.Validate(); err == nil {
			t.Fatalf("retention %s unexpectedly accepted", duration)
		}
	}
	if (hearthd.Config{}).EffectiveAutomationHistoryRetention() != 30*24*time.Hour {
		t.Fatal("programmatic retention default missing")
	}
}
