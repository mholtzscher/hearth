package hearthd

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

type Config struct {
	HTTPAddr   string `yaml:"http_addr"`
	NATSURL    string `yaml:"nats_url"`
	SQLitePath string `yaml:"sqlite_path"`
}

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

func (value Config) Validate() error {
	host, portText, err := net.SplitHostPort(value.HTTPAddr)
	if err != nil {
		return fmt.Errorf("http_addr must contain a loopback host and port: %w", err)
	}
	if host != "localhost" {
		ip := net.ParseIP(strings.Trim(host, "[]"))
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("http_addr host must be loopback")
		}
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("http_addr port must be between 1 and 65535")
	}
	if err := platformconfig.ValidateNATSURL(value.NATSURL); err != nil {
		return err
	}
	if strings.TrimSpace(value.SQLitePath) == "" {
		return fmt.Errorf("sqlite_path is required")
	}
	return nil
}
