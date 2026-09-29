// Package zwavejs assembles the hearth-adapter-zwavejs process. It validates the Z-Wave JS endpoint and supervises the
// Adapter and SDK Session under one lifecycle.
package zwavejs

import (
	"errors"
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

// ZWaveJSConfig holds the plain WebSocket endpoint embedded in Z-Wave JS UI. The server has no authentication or TLS.
// Keep the endpoint on loopback or a trusted private network; v1 has no credential or certificate fields.
//
//nolint:revive // ZWaveJSConfig keeps the upstream Z-Wave JS product name from the Adapter specification.
type ZWaveJSConfig struct {
	URL string `yaml:"url"`
}

// Validate checks static configuration. Errors name the field but never repeat a rejected URL, which may contain
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

// validateZWaveJSServerURL requires lowercase ws://, a host and port, and an empty or root path. It rejects user info,
// queries, fragments, and invalid ports without repeating the URL in errors.
func validateZWaveJSServerURL(value string) error {
	// Check the lowercase scheme before parsing. url.Parse lowercases Scheme, letting WS:// pass validation only to
	// fail during the WebSocket dial.
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
