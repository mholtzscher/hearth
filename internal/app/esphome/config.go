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
	AdapterID string          `yaml:"adapter_id"`
	NATSURL   string          `yaml:"nats_url"`
	Devices   []DeviceConfig  `yaml:"devices"`
	Discovery DiscoveryConfig `yaml:"discovery"`
	Blocklist []string        `yaml:"blocklist"`
}

// DiscoveryConfig enables DNS-SD browsing and explicitly authorizes plaintext devices on the trusted LAN.
type DiscoveryConfig struct {
	Enabled                bool `yaml:"enabled"`
	AllowInsecurePlaintext bool `yaml:"allow_insecure_plaintext"`
}

// DeviceConfig describes a static device or MAC-keyed discovery overrides. A nil Port uses the static default or
// inherits the discovered port when Address is empty.
type DeviceConfig struct {
	MAC               string   `yaml:"mac"`
	Address           string   `yaml:"address"`
	Port              *int     `yaml:"port"`
	EncryptionKeyFile string   `yaml:"encryption_key_file"`
	InsecurePlaintext bool     `yaml:"insecure_plaintext"`
	Include           []string `yaml:"include"`
	Exclude           []string `yaml:"exclude"`
}

// Validate checks static configuration without opening encryption key files.
// Errors identify fields by index without repeating their values.
func (value Config) Validate() error {
	_, _, err := value.prepare()
	return err
}

type preparedDevice struct {
	source   DeviceConfig
	mac      esphomeadapter.MAC
	exposure esphomeadapter.Exposure
}

// prepare shares parsed values within validation and assembly without reading key files.
func (value Config) prepare() ([]preparedDevice, map[esphomeadapter.MAC]struct{}, error) {
	if err := platformconfig.ValidateSlug("adapter_id", value.AdapterID); err != nil {
		return nil, nil, err
	}
	if err := platformconfig.ValidateNATSURL(value.NATSURL); err != nil {
		return nil, nil, err
	}
	if len(value.Devices) > 64 || (len(value.Devices) == 0 && !value.Discovery.Enabled) {
		return nil, nil, errors.New("devices must contain 1 to 64 entries unless discovery is enabled")
	}
	if value.Discovery.AllowInsecurePlaintext && !value.Discovery.Enabled {
		return nil, nil, errors.New("discovery.allow_insecure_plaintext requires discovery.enabled")
	}
	blocked, err := blockedMACs(value.Blocklist)
	if err != nil {
		return nil, nil, err
	}
	devices := make([]preparedDevice, 0, len(value.Devices))
	seen := make(map[esphomeadapter.MAC]struct{}, len(value.Devices))
	for index, device := range value.Devices {
		field := fmt.Sprintf("devices[%d]", index)
		if !macPattern.MatchString(device.MAC) {
			return nil, nil, fmt.Errorf("%s.mac must contain six colon-separated hexadecimal octets", field)
		}
		mac, parseErr := esphomeadapter.ParseMAC(device.MAC)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("%s.mac is invalid", field)
		}
		if _, exists := seen[mac]; exists {
			return nil, nil, fmt.Errorf("%s.mac must be unique", field)
		}
		seen[mac] = struct{}{}
		exposure, prepareErr := device.prepare(field, value.Discovery.Enabled)
		if prepareErr != nil {
			return nil, nil, prepareErr
		}
		devices = append(devices, preparedDevice{source: device, mac: mac, exposure: exposure})
	}
	return devices, blocked, nil
}

func (device DeviceConfig) prepare(field string, discovery bool) (esphomeadapter.Exposure, error) {
	if (device.Address != "" || !discovery) && !validAddress(device.Address) {
		return esphomeadapter.Exposure{}, fmt.Errorf(
			"%s.address must be an IP address or unicast DNS hostname (not .local)",
			field,
		)
	}
	if device.Port != nil && (*device.Port < 1 || *device.Port > 65535) {
		return esphomeadapter.Exposure{}, fmt.Errorf("%s.port must be between 1 and 65535", field)
	}
	if device.InsecurePlaintext && device.EncryptionKeyFile != "" {
		return esphomeadapter.Exposure{}, fmt.Errorf(
			"%s.insecure_plaintext and encryption_key_file are mutually exclusive",
			field,
		)
	}
	if (device.Address != "" || !discovery) && !device.InsecurePlaintext &&
		strings.TrimSpace(device.EncryptionKeyFile) == "" {
		return esphomeadapter.Exposure{}, fmt.Errorf("%s.encryption_key_file is required", field)
	}
	include, err := parseRefs(device.Include)
	if err != nil {
		return esphomeadapter.Exposure{}, fmt.Errorf("%s.include contains an invalid entity reference", field)
	}
	exclude, err := parseRefs(device.Exclude)
	if err != nil {
		return esphomeadapter.Exposure{}, fmt.Errorf("%s.exclude contains an invalid entity reference", field)
	}
	for ref := range include {
		if _, exists := exclude[ref]; exists {
			return esphomeadapter.Exposure{}, fmt.Errorf("%s.include and %s.exclude must not overlap", field, field)
		}
	}
	return esphomeadapter.Exposure{Include: include, Exclude: exclude}, nil
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
	devices, blocked, err := value.prepare()
	if err != nil {
		return esphomeadapter.Config{}, err
	}
	result := esphomeadapter.Config{
		Static:    make([]esphomeadapter.Device, 0, len(devices)),
		Overrides: make(map[esphomeadapter.MAC]esphomeadapter.DiscoveryOverride),
		Blocklist: blocked,
		Discovery: esphomeadapter.DiscoveryConfig{
			Enabled:                value.Discovery.Enabled,
			AllowInsecurePlaintext: value.Discovery.AllowInsecurePlaintext,
		},
	}
	for index, prepared := range devices {
		device := prepared.source
		if _, isBlocked := blocked[prepared.mac]; isBlocked {
			continue
		}
		var transport esphomeadapter.Transport
		if device.InsecurePlaintext {
			transport = esphomeadapter.PlaintextTransport()
		}
		if device.EncryptionKeyFile != "" {
			loaded, keyErr := readKeyFile(device.EncryptionKeyFile, index)
			if keyErr != nil {
				return esphomeadapter.Config{}, keyErr
			}
			transport = esphomeadapter.EncryptedTransport(loaded)
		}
		port := 0
		if device.Port != nil {
			port = *device.Port
		}
		if device.Address == "" {
			result.Overrides[prepared.mac] = esphomeadapter.DiscoveryOverride{
				Port: uint16(port), Transport: transport, Exposure: prepared.exposure,
			}
			continue
		}
		if port == 0 {
			port = defaultPort
		}
		result.Static = append(result.Static, esphomeadapter.Device{
			MAC: prepared.mac, Address: device.Address, Port: uint16(port),
			Transport: transport, Exposure: prepared.exposure,
		})
	}
	return result, nil
}

func blockedMACs(values []string) (map[esphomeadapter.MAC]struct{}, error) {
	blocked := make(map[esphomeadapter.MAC]struct{}, len(values))
	for index, value := range values {
		mac, err := esphomeadapter.ParseMAC(value)
		if err != nil || !macPattern.MatchString(value) {
			return nil, fmt.Errorf("blocklist[%d] must contain six colon-separated hexadecimal octets", index)
		}
		if _, duplicate := blocked[mac]; duplicate {
			return nil, fmt.Errorf("blocklist[%d] must be unique", index)
		}
		blocked[mac] = struct{}{}
	}
	return blocked, nil
}

func readKeyFile(path string, index int) (nativeapi.PSK, error) {
	file, err := os.Open(path)
	if err != nil {
		return nativeapi.PSK{}, &KeyFileError{Index: index, Reason: "could not be read"}
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, maxKeyFileBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(contents) > maxKeyFileBytes {
		return nativeapi.PSK{}, &KeyFileError{Index: index, Reason: "could not be read or exceeds 4096 bytes"}
	}
	key, err := nativeapi.ParsePSK(strings.TrimSpace(string(contents)))
	if err != nil {
		return nativeapi.PSK{}, &KeyFileError{Index: index, Reason: "must contain a valid 32-byte base64 key"}
	}
	return key, nil
}
