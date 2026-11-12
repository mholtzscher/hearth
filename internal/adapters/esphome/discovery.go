package esphome

import (
	"context"
	"net/netip"
)

// DiscoveryConfig selects optional LAN discovery and explicitly permits unencrypted candidates.
type DiscoveryConfig struct {
	Enabled                bool
	AllowInsecurePlaintext bool
}

// DiscoveryRecord is a resolved ESPHome DNS-SD advertisement, not authenticated device identity.
type DiscoveryRecord struct {
	MAC       MAC
	Address   string
	Port      uint16
	Encrypted bool
}

// DiscoverySource reports resolved candidates until cancellation. Run must join its work before returning and
// must not close records. The Adapter owns candidate policy and worker lifecycle.
type DiscoverySource interface {
	Run(context.Context, chan<- DiscoveryRecord) error
}

type discoveryError struct{ err error }

func (*discoveryError) Error() string     { return "esphome discovery failed" }
func (err *discoveryError) Unwrap() error { return err.err }

// discoveredDevice applies credentials and exposure without using unauthenticated TXT as transport authorization.
// An explicit key always selects Noise, even when an advertisement omits its encryption field.
func (a *Adapter) discoveredDevice(record DiscoveryRecord) (Device, string) {
	if _, blocked := a.config.Blocklist[record.MAC]; blocked {
		return Device{}, reasonDeviceBlocked
	}
	address, err := netip.ParseAddr(record.Address)
	if err != nil || address.IsUnspecified() || address.IsMulticast() || record.Port == 0 ||
		(address.Is6() && address.IsLinkLocalUnicast() && address.Zone() == "") {
		return Device{}, reasonDeviceUnreachable
	}
	device := Device{MAC: record.MAC, Address: record.Address, Port: record.Port}
	for _, configured := range a.config.Devices {
		if configured.MAC != record.MAC {
			continue
		}
		device = configured
		if device.Address == "" {
			device.Address = record.Address
		}
		if device.Port == 0 {
			device.Port = record.Port
		}
		break
	}
	switch {
	case device.EncryptionKeyConfigured:
		device.InsecurePlaintext = false
	case record.Encrypted:
		return Device{}, reasonEncryptionKeyRequired
	case device.InsecurePlaintext || a.config.Discovery.AllowInsecurePlaintext:
		device.InsecurePlaintext = true
	default:
		return Device{}, reasonPlaintextRejected
	}
	return device, ""
}
