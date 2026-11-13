package nativeapi

import "errors"

// Connection failures that callers distinguish. Every other failure is an ordinary error that callers treat as the
// device being unreachable.
var (
	// ErrEncryptionKeyRejected reports that the device refused the Noise handshake because the keys differ.
	ErrEncryptionKeyRejected = errors.New("esphome: encryption key rejected")
	// ErrPlaintextDevice reports that the device answered without encryption. The client never falls back.
	ErrPlaintextDevice = errors.New("esphome: device does not use encryption")
	// ErrIdentityMismatch reports that the device's MAC address differs from the expected one.
	ErrIdentityMismatch = errors.New("esphome: device MAC does not match configuration")
	// ErrIncompatibleAPI reports an unsupported protocol or API version.
	ErrIncompatibleAPI = errors.New("esphome: incompatible API version")
	// ErrFrameTooLarge reports a frame that exceeds the protocol's 16-bit length.
	ErrFrameTooLarge = errors.New("esphome: frame exceeds limit")
	// ErrSlowConsumer reports that the state buffer filled; the connection ends rather than drop a state.
	ErrSlowConsumer = errors.New("esphome: state buffer overflow")
	// ErrKeepaliveTimeout reports that the device sent nothing for the silence limit.
	ErrKeepaliveTimeout = errors.New("esphome: device stopped responding")
	// ErrClosed reports a connection closed by its owner.
	ErrClosed = errors.New("esphome: connection closed")
)
