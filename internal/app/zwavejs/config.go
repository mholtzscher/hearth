// Package zwavejs assembles the hearth-adapter-zwavejs process: it loads and
// validates the trusted Z-Wave JS server endpoint and supervises the Z-Wave JS
// Adapter and its SDK Session under one lifecycle.
package zwavejs

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

// Config is the operator configuration for one Z-Wave JS Adapter instance.
type Config struct {
	AdapterID string        `yaml:"adapter_id"`
	NATSURL   string        `yaml:"nats_url"`
	ZWaveJS   ZWaveJSConfig `yaml:"zwave_js"`
}

// ZWaveJSConfig is the plain WebSocket endpoint of the Z-Wave JS server
// embedded in Z-Wave JS UI. The embedded server offers neither authentication
// nor TLS, so v1 has no credential, token, or certificate field at all: the
// endpoint must stay on loopback or a trusted private network.
//
//nolint:revive // ZWaveJSConfig keeps the upstream Z-Wave JS product name from the Adapter specification.
type ZWaveJSConfig struct {
	URL string `yaml:"url"`
}

// LoadConfig strictly decodes one Z-Wave JS YAML file and validates every
// trusted-endpoint constraint.
func LoadConfig(path string) (Config, error) {
	var value Config
	if err := platformconfig.LoadFile(path, &value); err != nil {
		return Config{}, err
	}
	if err := value.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config %q: %w", path, err)
	}
	return value, nil
}

// Validate enforces the complete static configuration contract. Every failure
// names its field and never repeats a rejected URL, which may contain
// credentials.
func (value Config) Validate() error {
	if err := platformconfig.ValidateSlug("adapter_id", value.AdapterID); err != nil {
		return err
	}
	if err := platformconfig.ValidateNATSURL(value.NATSURL); err != nil {
		return err
	}
	return validateZWaveJSServerURL(value.ZWaveJS.URL)
}

// validateZWaveJSServerURL requires an absolute lowercase ws:// URL with an
// explicit host and port, no user info, query, or fragment, and an empty or root
// path. It rejects wss://, credentials, tokens, path variants, and malformed
// ports, and never returns the URL in an error.
func validateZWaveJSServerURL(value string) error {
	// Match the exact lowercase scheme before parsing: url.Parse lowercases
	// parsed.Scheme, so a case-variant scheme like WS:// would otherwise pass
	// here and only fail later in the WebSocket dial.
	lowercaseScheme := strings.HasPrefix(value, "ws://")
	parsed, err := url.Parse(value)
	if err != nil || !lowercaseScheme || parsed.Scheme != "ws" ||
		parsed.Hostname() == "" || parsed.Port() == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		strings.Contains(value, "#") {
		return errors.New(
			"zwave_js.url must be an absolute ws:// URL with an explicit host and port " +
				"and no user info, path, query, or fragment",
		)
	}
	port, err := strconv.ParseUint(parsed.Port(), 10, 16)
	if err != nil || port == 0 {
		return errors.New("zwave_js.url must contain a valid TCP port")
	}
	return nil
}
