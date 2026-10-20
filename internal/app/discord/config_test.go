package discord_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/app/discord"
)

func TestDestinationConfigBounds(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		key, name, path string
		valid           bool
	}{
		{"home", "Household", "/secret", true},
		{"home", strings.Repeat("a", 200), "/secret", true},
		{"home", strings.Repeat("a", 201), "/secret", false},
		{"home", " \n", "/secret", false},
		{"home", "\xff", "/secret", false},
		{"bad.key", "Household", "/secret", false},
		{strings.Repeat("a", 64), "Household", "/secret", false},
		{"home", "Household", " ", false},
	} {
		config := discord.Config{
			AdapterID:    "discord-main",
			NATSURL:      "nats://127.0.0.1:4222",
			Destinations: map[string]discord.DestinationConfig{test.key: {Name: test.name, WebhookURLFile: test.path}},
		}
		if err := config.Validate(); (err == nil) != test.valid {
			t.Errorf("valid=%t err=%v", test.valid, err)
		}
	}
}

func TestDestinationCountBound(t *testing.T) {
	t.Parallel()
	for _, count := range []int{64, 65} {
		destinations := make(map[string]discord.DestinationConfig, count)
		for index := range count {
			destinations[fmt.Sprintf("route-%02d", index)] = discord.DestinationConfig{
				Name: "Household", WebhookURLFile: "/secret",
			}
		}
		config := discord.Config{
			AdapterID:    "discord-main",
			NATSURL:      "nats://127.0.0.1:4222",
			Destinations: destinations,
		}
		if err := config.Validate(); (count == 64) != (err == nil) {
			t.Fatalf("%d destinations: %v", count, err)
		}
	}
}
