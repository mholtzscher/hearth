package discord_test

import (
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
