package esphome

import "github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"

type transportMode uint8

const (
	transportUnset transportMode = iota
	transportEncrypted
	transportPlaintext
)

// Transport selects Noise with a key or explicitly authorized plaintext. The zero
// value is an unspecified discovery override, never a worker transport.
type Transport struct {
	mode transportMode
	key  nativeapi.PSK
}

// EncryptedTransport selects Noise even if discovery advertises no encryption.
func EncryptedTransport(key nativeapi.PSK) Transport {
	return Transport{mode: transportEncrypted, key: key}
}

// PlaintextTransport explicitly authorizes an unencrypted connection.
func PlaintextTransport() Transport { return Transport{mode: transportPlaintext} }
