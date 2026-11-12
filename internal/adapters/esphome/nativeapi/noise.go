package nativeapi

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/flynn/noise"
)

const (
	// noiseIndicator starts every encrypted frame; plaintextIndicator starts every plaintext frame.
	noiseIndicator     = 0x01
	plaintextIndicator = 0x00
	// noiseProtocol is the only protocol a server hello may select.
	noiseProtocol = 0x01
	// frameHeaderSize is the indicator byte plus the 16-bit big-endian encrypted length.
	frameHeaderSize = 3
	// maximumFrameSize is the largest encrypted length the 16-bit header can carry.
	maximumFrameSize = 0xFFFF
	// messageHeaderSize is the encrypted 16-bit message type plus the 16-bit data length.
	messageHeaderSize = 4
	// maximumMessageData leaves room for the message header and the 16-byte authentication tag.
	maximumMessageData = maximumFrameSize - messageHeaderSize - 16
	// handshakeRejectMAC is the firmware's reason when the pre-shared keys differ.
	handshakeRejectMAC = "Handshake MAC failure"
	// pskSize is the Noise pre-shared key length.
	pskSize = 32
)

// prologue is "NoiseAPIInit" followed by the empty client hello's 16-bit length.
const prologue = "NoiseAPIInit\x00\x00"

//nolint:gochecknoglobals // The immutable Noise cipher suite is shared across handshakes.
var cipherSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)

// PSK is a 32-byte Noise pre-shared key. Its formatting never reveals the key.
type PSK [pskSize]byte

// ParsePSK decodes the standard base64 text that ESPHome configuration uses. Errors never repeat the input.
func ParsePSK(base64Text string) (PSK, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(base64Text))
	if err != nil || len(decoded) != pskSize {
		return PSK{}, errors.New("encryption key must be base64 text that decodes to 32 bytes")
	}
	var psk PSK
	copy(psk[:], decoded)
	return psk, nil
}

// String redacts the key.
func (PSK) String() string { return "[redacted]" }

// GoString redacts the key.
func (PSK) GoString() string { return "nativeapi.PSK([redacted])" }

// serverHello carries the optional identity extensions of a Noise server hello.
type serverHello struct {
	name string
	// mac is the compact lowercase hexadecimal MAC, or empty when the firmware omits it.
	mac string
}

// noiseTransport frames and encrypts messages after a completed handshake.
type noiseTransport struct {
	reader  *bufio.Reader
	writer  io.Writer
	encrypt *noise.CipherState
	decrypt *noise.CipherState
}

// handshake performs the client side of the ESPHome Noise handshake on an open stream. When expectMAC is not empty
// and the server hello carries a MAC, a mismatch ends the handshake before the device's response is read.
func handshake(
	reader *bufio.Reader,
	writer io.Writer,
	psk PSK,
	expectMAC string,
) (*noiseTransport, serverHello, error) {
	state, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           cipherSuite,
		Random:                rand.Reader,
		Pattern:               noise.HandshakeNN,
		Initiator:             true,
		Prologue:              []byte(prologue),
		PresharedKey:          psk[:],
		PresharedKeyPlacement: 0,
	})
	if err != nil {
		return nil, serverHello{}, fmt.Errorf("start noise handshake: %w", err)
	}
	first, _, _, err := state.WriteMessage(nil, nil)
	if err != nil {
		return nil, serverHello{}, fmt.Errorf("write noise handshake: %w", err)
	}
	// An empty client hello frame, then the handshake frame: a zero status byte and the first Noise message.
	request := append([]byte{noiseIndicator, 0, 0}, frameHeader(1+len(first))...)
	request = append(request, 0)
	request = append(request, first...)
	if _, writeErr := writer.Write(request); writeErr != nil {
		return nil, serverHello{}, fmt.Errorf("send client hello: %w", writeErr)
	}

	helloPayload, err := readHandshakeFrame(reader)
	if err != nil {
		return nil, serverHello{}, err
	}
	hello, err := parseServerHello(helloPayload)
	if err != nil {
		return nil, serverHello{}, err
	}
	if expectMAC != "" && hello.mac != "" && hello.mac != expectMAC {
		return nil, hello, ErrIdentityMismatch
	}

	response, err := readHandshakeFrame(reader)
	if err != nil {
		return nil, hello, err
	}
	if len(response) == 0 {
		return nil, hello, errors.New("esphome: empty handshake frame")
	}
	if response[0] != 0 {
		if string(response[1:]) == handshakeRejectMAC {
			return nil, hello, ErrEncryptionKeyRejected
		}
		return nil, hello, errors.New("esphome: device rejected the handshake")
	}
	_, encrypt, decrypt, err := state.ReadMessage(nil, response[1:])
	if err != nil {
		// A failed authentication tag means the keys differ even though the device did not say so.
		return nil, hello, ErrEncryptionKeyRejected
	}
	if encrypt == nil || decrypt == nil {
		return nil, hello, errors.New("esphome: incomplete noise handshake")
	}
	return &noiseTransport{reader: reader, writer: writer, encrypt: encrypt, decrypt: decrypt}, hello, nil
}

// readHandshakeFrame reads one unencrypted handshake frame and classifies a plaintext answer.
func readHandshakeFrame(reader *bufio.Reader) ([]byte, error) {
	var header [frameHeaderSize]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, fmt.Errorf("read handshake frame: %w", err)
	}
	switch header[0] {
	case noiseIndicator:
	case plaintextIndicator:
		return nil, ErrPlaintextDevice
	default:
		return nil, errors.New("esphome: invalid frame indicator")
	}
	payload := make([]byte, binary.BigEndian.Uint16(header[1:]))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, fmt.Errorf("read handshake frame: %w", err)
	}
	return payload, nil
}

// parseServerHello reads the selected protocol and the optional NUL-terminated name and MAC extensions.
func parseServerHello(payload []byte) (serverHello, error) {
	if len(payload) == 0 || payload[0] != noiseProtocol {
		return serverHello{}, fmt.Errorf("%w: unsupported noise protocol", ErrIncompatibleAPI)
	}
	var hello serverHello
	rest := payload[1:]
	nameEnd := bytes.IndexByte(rest, 0)
	if nameEnd < 0 {
		return hello, nil
	}
	hello.name = string(rest[:nameEnd])
	rest = rest[nameEnd+1:]
	if macEnd := bytes.IndexByte(rest, 0); macEnd >= 0 {
		hello.mac = strings.ToLower(string(rest[:macEnd]))
	}
	return hello, nil
}

// readMessage reads, decrypts, and splits one data frame.
func (transport *noiseTransport) readMessage() (uint16, []byte, error) {
	var header [frameHeaderSize]byte
	if _, err := io.ReadFull(transport.reader, header[:]); err != nil {
		return 0, nil, err
	}
	if header[0] != noiseIndicator {
		return 0, nil, errors.New("esphome: invalid frame indicator")
	}
	ciphertext := make([]byte, binary.BigEndian.Uint16(header[1:]))
	if _, err := io.ReadFull(transport.reader, ciphertext); err != nil {
		return 0, nil, err
	}
	plaintext, err := transport.decrypt.Decrypt(nil, nil, ciphertext)
	if err != nil {
		return 0, nil, errors.New("esphome: frame failed authentication")
	}
	if len(plaintext) < messageHeaderSize {
		return 0, nil, errors.New("esphome: short message header")
	}
	messageType := binary.BigEndian.Uint16(plaintext[0:2])
	dataLength := int(binary.BigEndian.Uint16(plaintext[2:4]))
	if dataLength != len(plaintext)-messageHeaderSize {
		return 0, nil, errors.New("esphome: message length mismatch")
	}
	return messageType, plaintext[messageHeaderSize:], nil
}

// writeMessage encrypts and writes one data frame. The caller serializes writes.
func (transport *noiseTransport) writeMessage(messageType uint16, data []byte) error {
	if len(data) > maximumMessageData {
		return ErrFrameTooLarge
	}
	plaintext := make([]byte, messageHeaderSize, messageHeaderSize+len(data))
	binary.BigEndian.PutUint16(plaintext[0:2], messageType)
	binary.BigEndian.PutUint16(plaintext[2:4], uint16(len(data))) //nolint:gosec // Bounded by maximumMessageData.
	plaintext = append(plaintext, data...)
	ciphertext, err := transport.encrypt.Encrypt(nil, nil, plaintext)
	if err != nil {
		return fmt.Errorf("encrypt frame: %w", err)
	}
	frame := append(frameHeader(len(ciphertext)), ciphertext...)
	_, err = transport.writer.Write(frame)
	return err
}

// frameHeader encodes the indicator and 16-bit length. Callers bound length to maximumFrameSize.
func frameHeader(length int) []byte {
	header := []byte{noiseIndicator, 0, 0}
	binary.BigEndian.PutUint16(header[1:], uint16(length)) //nolint:gosec // Callers bound the length.
	return header
}
