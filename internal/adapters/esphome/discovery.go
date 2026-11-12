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

// DiscoveryOverride supplies MAC-keyed policy without defining a static endpoint.
// Port zero inherits the advertisement; Transport zero inherits discovery policy.
type DiscoveryOverride struct {
	Port      uint16
	Transport Transport
	Exposure  Exposure
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
	override := a.config.Overrides[record.MAC]
	device.Transport = override.Transport
	device.Exposure = override.Exposure
	if override.Port != 0 {
		device.Port = override.Port
	}
	switch {
	case device.Transport.mode == transportEncrypted:
	case record.Encrypted:
		return Device{}, reasonEncryptionKeyRequired
	case device.Transport.mode == transportPlaintext || a.config.Discovery.AllowInsecurePlaintext:
		device.Transport = PlaintextTransport()
	default:
		return Device{}, reasonPlaintextRejected
	}
	return device, ""
}
