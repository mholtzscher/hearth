// Package esphome assembles the hearth-adapter-esphome process.
package esphome

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"regexp"
	"strings"

	esphomeadapter "github.com/mholtzscher/hearth/internal/adapters/esphome"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

const (
	defaultPort     = 6053
	maxKeyFileBytes = 4096
)

var macPattern = regexp.MustCompile(`^[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}$`)

// Config is the operator configuration for one ESPHome Adapter instance.
type Config struct {
	AdapterID string         `yaml:"adapter_id"`
	NATSURL   string         `yaml:"nats_url"`
	Devices   []DeviceConfig `yaml:"devices"`
}

// DeviceConfig describes a device and its exposure overrides. A nil Port uses the default.
type DeviceConfig struct {
	MAC               string   `yaml:"mac"`
	Address           string   `yaml:"address"`
	Port              *int     `yaml:"port"`
	EncryptionKeyFile string   `yaml:"encryption_key_file"`
	Include           []string `yaml:"include"`
	Exclude           []string `yaml:"exclude"`
}

// Validate checks static configuration without opening encryption key files.
// Errors identify fields by index without repeating their values.
func (value Config) Validate() error {
	if err := platformconfig.ValidateSlug("adapter_id", value.AdapterID); err != nil {
		return err
	}
	if err := platformconfig.ValidateNATSURL(value.NATSURL); err != nil {
		return err
	}
	if len(value.Devices) < 1 || len(value.Devices) > 64 {
		return errors.New("devices must contain 1 to 64 entries")
	}
	seen := make(map[esphomeadapter.MAC]struct{}, len(value.Devices))
	for index, device := range value.Devices {
		field := fmt.Sprintf("devices[%d]", index)
		if !macPattern.MatchString(device.MAC) {
			return fmt.Errorf("%s.mac must contain six colon-separated hexadecimal octets", field)
		}
		mac, err := esphomeadapter.ParseMAC(device.MAC)
		if err != nil {
			return fmt.Errorf("%s.mac is invalid", field)
		}
		if _, exists := seen[mac]; exists {
			return fmt.Errorf("%s.mac must be unique", field)
		}
		seen[mac] = struct{}{}
		if err = device.validate(field); err != nil {
			return err
		}
	}
	return nil
}

func (device DeviceConfig) validate(field string) error {
	if !validAddress(device.Address) {
		return fmt.Errorf("%s.address must be an IP address or unicast DNS hostname (not .local)", field)
	}
	if device.Port != nil && (*device.Port < 1 || *device.Port > 65535) {
		return fmt.Errorf("%s.port must be between 1 and 65535", field)
	}
	if strings.TrimSpace(device.EncryptionKeyFile) == "" {
		return fmt.Errorf("%s.encryption_key_file is required", field)
	}
	include, err := parseRefs(device.Include)
	if err != nil {
		return fmt.Errorf("%s.include contains an invalid entity reference", field)
	}
	exclude, err := parseRefs(device.Exclude)
	if err != nil {
		return fmt.Errorf("%s.exclude contains an invalid entity reference", field)
	}
	for ref := range include {
		if _, exists := exclude[ref]; exists {
			return fmt.Errorf("%s.include and %s.exclude must not overlap", field, field)
		}
	}
	return nil
}

func validAddress(address string) bool {
	if ip, err := netip.ParseAddr(address); err == nil {
		return ip.Zone() == ""
	}
	name := strings.TrimSuffix(address, ".")
	if name == "" || len(name) > 253 || strings.HasSuffix(strings.ToLower(name), ".local") ||
		strings.EqualFold(name, "local") {
		return false
	}
	for label := range strings.SplitSeq(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
			digit := c >= '0' && c <= '9'
			if !letter && !digit && c != '-' {
				return false
			}
		}
	}
	return true
}

func parseRefs(values []string) (map[esphomeadapter.EntityRef]struct{}, error) {
	refs := make(map[esphomeadapter.EntityRef]struct{}, len(values))
	for _, value := range values {
		ref, err := esphomeadapter.ParseEntityRef(value)
		if err != nil {
			return nil, err
		}
		refs[ref] = struct{}{}
	}
	return refs, nil
}

// KeyFileError identifies a key-file failure without exposing its path or contents.
type KeyFileError struct {
	Index  int
	Reason string
}

func (err *KeyFileError) Error() string {
	return fmt.Sprintf("devices[%d].encryption_key_file %s", err.Index, err.Reason)
}

// adapterConfig validates, loads bounded key files, and prepares the Adapter configuration.
func adapterConfig(value Config) (esphomeadapter.Config, error) {
	if err := value.Validate(); err != nil {
		return esphomeadapter.Config{}, err
	}
	result := esphomeadapter.Config{Devices: make([]esphomeadapter.Device, 0, len(value.Devices))}
	for index, device := range value.Devices {
		file, err := os.Open(device.EncryptionKeyFile)
		if err != nil {
			return esphomeadapter.Config{}, &KeyFileError{Index: index, Reason: "could not be read"}
		}
		contents, readErr := io.ReadAll(io.LimitReader(file, maxKeyFileBytes+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(contents) > maxKeyFileBytes {
			return esphomeadapter.Config{}, &KeyFileError{
				Index:  index,
				Reason: "could not be read or exceeds 4096 bytes",
			}
		}
		key, err := nativeapi.ParsePSK(strings.TrimSpace(string(contents)))
		if err != nil {
			return esphomeadapter.Config{}, &KeyFileError{
				Index:  index,
				Reason: "must contain a valid 32-byte base64 key",
			}
		}
		mac, _ := esphomeadapter.ParseMAC(device.MAC)
		include, _ := parseRefs(device.Include)
		exclude, _ := parseRefs(device.Exclude)
		port := defaultPort
		if device.Port != nil {
			port = *device.Port
		}
		result.Devices = append(result.Devices, esphomeadapter.Device{
			MAC: mac, Address: device.Address, Port: uint16(port), PSK: key,
			Exposure: esphomeadapter.Exposure{Include: include, Exclude: exclude},
		})
	}
	return result, nil
}
