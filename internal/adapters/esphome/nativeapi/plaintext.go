package nativeapi

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
)

// messageTransport carries protobuf messages after explicit transport selection. Neither implementation negotiates
// a fallback through the other transport.
type messageTransport interface {
	readMessage() (uint16, []byte, error)
	writeMessage(uint16, []byte) error
}

func openTransport(reader *bufio.Reader, writer io.Writer, config DialConfig) (messageTransport, error) {
	if config.InsecurePlaintext {
		return &plaintextTransport{reader: reader, writer: writer}, nil
	}
	transport, _, err := handshake(reader, writer, config.PSK, compactMAC(config.ExpectMAC))
	return transport, err
}

// authenticateWithoutPassword supports legacy responses and firmware that compiles out the passwordless handler.
// DeviceInfo is an ordered barrier: any authentication rejection precedes it, and uses_password must be false.
func (c *conn) authenticateWithoutPassword(ctx context.Context) error {
	//nolint:staticcheck // Required by supported API1.10–1.13 firmware.
	err := c.write(ctx, &apipb.AuthenticationRequest{})
	if err != nil {
		return err
	}
	return c.request(ctx, &apipb.DeviceInfoRequest{}, func(id uint16) bool { return id == idDeviceInfoResponse },
		func(message frame) (bool, error) {
			var response apipb.DeviceInfoResponse
			if decodeErr := decodeInto(message.data, &response); decodeErr != nil {
				return true, decodeErr
			}
			if response.GetUsesPassword() { //nolint:staticcheck // Required by supported API1.10–1.13 firmware.
				return true, fmt.Errorf("%w: password-protected devices are unsupported", ErrIncompatibleAPI)
			}
			return true, nil
		})
}

func checkAuthenticationResponse(data []byte) error {
	var response apipb.AuthenticationResponse //nolint:staticcheck // Required by supported API1.10–1.13 firmware.
	if err := decodeInto(data, &response); err != nil {
		return err
	}
	if response.GetInvalidPassword() {
		return fmt.Errorf("%w: password-protected devices are unsupported", ErrIncompatibleAPI)
	}
	return nil
}

// plaintextTransport is used only after an operator opts this device into insecure plaintext.
type plaintextTransport struct {
	reader *bufio.Reader
	writer io.Writer
}

func (transport *plaintextTransport) readMessage() (uint16, []byte, error) {
	marker, err := transport.reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	if marker != plaintextIndicator {
		return 0, nil, fmt.Errorf("%w: device did not answer plaintext", ErrIncompatibleAPI)
	}
	length, err := readPlainVarint(transport.reader)
	if err != nil {
		return 0, nil, err
	}
	if length > maximumFrameSize {
		return 0, nil, ErrFrameTooLarge
	}
	id, err := readPlainVarint(transport.reader)
	if err != nil {
		return 0, nil, err
	}
	if id > math.MaxUint16 {
		return 0, nil, errors.New("esphome: message type exceeds limit")
	}
	payload := make([]byte, int(length))
	if _, err = io.ReadFull(transport.reader, payload); err != nil {
		return 0, nil, err
	}
	return uint16(id), payload, nil
}

func (transport *plaintextTransport) writeMessage(id uint16, data []byte) error {
	if len(data) > maximumFrameSize {
		return ErrFrameTooLarge
	}
	frame := []byte{plaintextIndicator}
	frame = binary.AppendUvarint(frame, uint64(len(data)))
	frame = binary.AppendUvarint(frame, uint64(id))
	frame = append(frame, data...)
	_, err := transport.writer.Write(frame)
	return err
}

// readPlainVarint bounds parsing to a protobuf uint32 rather than accepting an arbitrarily long header.
func readPlainVarint(reader io.ByteReader) (uint32, error) {
	const (
		maximumVarintBytes = 5
		bitsPerByte        = 7
		valueMask          = 0x7f
		continuationBit    = 0x80
	)
	var result uint32
	for index := range maximumVarintBytes {
		value, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		if index == maximumVarintBytes-1 && value > 15 {
			return 0, errors.New("esphome: invalid varint")
		}
		result |= uint32(value&valueMask) << (bitsPerByte * index)
		if value&continuationBit == 0 {
			return result, nil
		}
	}
	return 0, errors.New("esphome: invalid varint")
}
