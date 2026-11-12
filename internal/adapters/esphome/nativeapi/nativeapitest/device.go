// Package nativeapitest provides a scripted ESPHome device for tests. It speaks the real Noise handshake and framing
// over any [net.Conn], so tests can use [net.Pipe] (unit) or a loopback listener (integration). It proves scripted
// contracts, not compatibility with real firmware.
package nativeapitest

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/flynn/noise"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
)

const (
	idHelloRequest           = 1
	idAuthenticationRequest  = 3
	idDisconnectRequest      = 5
	idDisconnectResponse     = 6
	idPingRequest            = 7
	idPingResponse           = 8
	idDeviceInfoRequest      = 9
	idListEntitiesRequest    = 11
	idSubscribeStatesRequest = 20
	receivedBuffer           = 256
)

// Device scripts one firmware instance.
type Device struct {
	PSK nativeapi.PSK
	// Name and MAC appear in the server hello; MAC is compact lowercase hexadecimal. An empty MAC omits the extension.
	Name string
	MAC  string
	// APIMajor and APIMinor default to 1.19.
	APIMajor, APIMinor uint32
	Info               *apipb.DeviceInfoResponse
	// Entities are ListEntities*Response messages, sent in order before ListEntitiesDoneResponse.
	Entities []proto.Message
	// States are sent in order when the client subscribes.
	States []proto.Message
	// Plaintext serves the unencrypted API. Encrypted clients receive a plaintext marker and are rejected.
	Plaintext bool
	// PasswordRequired rejects the empty legacy authentication request.
	PasswordRequired bool
	// AutoAuthenticate models passwordless firmware with the legacy authentication handler compiled out.
	AutoAuthenticate bool
	// IgnorePings leaves client pings unanswered.
	IgnorePings bool
}

// Message is one message the client sent after the handshake.
type Message struct {
	ID      uint16
	Message proto.Message
}

// Session is one served connection.
type Session struct {
	conn      net.Conn
	writeMu   sync.Mutex
	encrypt   *noise.CipherState
	plaintext bool
	received  chan Message
	done      chan struct{}
	closeOnce sync.Once
	err       error
}

// Serve runs the device on conn in a goroutine. Close the Session or conn to stop it.
func (device Device) Serve(conn net.Conn) *Session {
	session := &Session{
		conn:      conn,
		received:  make(chan Message, receivedBuffer),
		done:      make(chan struct{}),
		plaintext: device.Plaintext,
	}
	go session.run(device)
	return session
}

// Received delivers every post-handshake client message except hello, device-info, list, subscribe, and (when
// answered) ping requests, which the device answers itself.
func (session *Session) Received() <-chan Message { return session.received }

// Done closes when the session ends.
func (session *Session) Done() <-chan struct{} { return session.done }

// Err reports why the session ended.
func (session *Session) Err() error {
	<-session.done
	return session.err
}

// Push sends one message to the client.
func (session *Session) Push(message proto.Message) error {
	id, data, err := encode(message)
	if err != nil {
		return err
	}
	return session.SendRaw(id, data)
}

// SendRaw sends one message with an arbitrary type and payload over the selected transport.
func (session *Session) SendRaw(id uint16, data []byte) error {
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	if session.plaintext {
		frame := binary.AppendUvarint([]byte{0}, uint64(len(data)))
		frame = binary.AppendUvarint(frame, uint64(id))
		_, err := session.conn.Write(append(frame, data...))
		return err
	}
	if session.encrypt == nil {
		return errors.New("handshake not complete")
	}
	plaintext := make([]byte, 4, 4+len(data)) //nolint:mnd // The wire header contains two 16-bit fields.
	binary.BigEndian.PutUint16(plaintext[0:2], id)
	binary.BigEndian.PutUint16(plaintext[2:4], uint16(len(data))) //nolint:gosec // Test payloads are small.
	plaintext = append(plaintext, data...)
	ciphertext, err := session.encrypt.Encrypt(nil, nil, plaintext)
	if err != nil {
		return err
	}
	return writeFrame(session.conn, ciphertext)
}

// Close ends the session by closing the stream.
func (session *Session) Close() { _ = session.conn.Close() }

func (session *Session) finish(err error) {
	session.closeOnce.Do(func() {
		session.err = err
		_ = session.conn.Close()
		close(session.received)
		close(session.done)
	})
}

func (session *Session) run(device Device) {
	reader := bufio.NewReader(session.conn)
	if device.Plaintext {
		session.runPlaintext(device, reader)
		return
	}
	decrypt, err := session.handshake(device, reader)
	if err != nil {
		session.finish(err)
		return
	}
	for {
		id, data, readMessageErr := readMessage(reader, decrypt)
		if readMessageErr != nil {
			session.finish(readMessageErr)
			return
		}
		if answerErr := session.answer(device, id, data); answerErr != nil {
			session.finish(answerErr)
			return
		}
	}
}

func (session *Session) runPlaintext(device Device, reader *bufio.Reader) {
	marker, err := reader.Peek(1)
	if err != nil {
		session.finish(err)
		return
	}
	if marker[0] != 0 {
		_, _ = readFrame(reader)
		_, _ = session.conn.Write([]byte{0, 0, 1})
		session.finish(errors.New("device requires plaintext"))
		return
	}
	for {
		id, data, readErr := readPlainMessage(reader)
		if readErr != nil {
			session.finish(readErr)
			return
		}
		if answerErr := session.answer(device, id, data); answerErr != nil {
			session.finish(answerErr)
			return
		}
	}
}

func readPlainMessage(reader *bufio.Reader) (uint16, []byte, error) {
	marker, err := reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	if marker != 0 {
		return 0, nil, errors.New("bad plaintext indicator")
	}
	length, err := binary.ReadUvarint(reader)
	if err != nil {
		return 0, nil, err
	}
	id, err := binary.ReadUvarint(reader)
	if err != nil {
		return 0, nil, err
	}
	if length > 65535 || id > 65535 {
		return 0, nil, errors.New("test frame exceeds limit")
	}
	payload := make([]byte, int(length))
	_, err = io.ReadFull(reader, payload)
	return uint16(id), payload, err
}

func (session *Session) handshake(device Device, reader *bufio.Reader) (*noise.CipherState, error) {
	clientHello, err := readFrame(reader)
	if err != nil {
		return nil, err
	}
	if len(clientHello) != 0 {
		return nil, errors.New("unexpected client hello payload")
	}
	request, err := readFrame(reader)
	if err != nil {
		return nil, err
	}
	hello := []byte{0x01}
	hello = append(hello, device.Name...)
	hello = append(hello, 0)
	if device.MAC != "" {
		hello = append(hello, device.MAC...)
		hello = append(hello, 0)
	}
	if writeFrameErr := writeFrame(session.conn, hello); writeFrameErr != nil {
		return nil, writeFrameErr
	}
	state, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256),
		Random:                rand.Reader,
		Pattern:               noise.HandshakeNN,
		Prologue:              []byte("NoiseAPIInit\x00\x00"),
		PresharedKey:          device.PSK[:],
		PresharedKeyPlacement: 0,
	})
	if err != nil {
		return nil, err
	}
	if len(request) == 0 || request[0] != 0 {
		return nil, errors.New("bad handshake frame")
	}
	if _, _, _, readMessageErr := state.ReadMessage(nil, request[1:]); readMessageErr != nil {
		_ = writeFrame(session.conn, append([]byte{0x01}, "Handshake MAC failure"...))
		return nil, fmt.Errorf("handshake: %w", readMessageErr)
	}
	response, decrypt, encrypt, err := state.WriteMessage(nil, nil)
	if err != nil {
		return nil, err
	}
	session.writeMu.Lock()
	session.encrypt = encrypt
	session.writeMu.Unlock()
	if writeFrameErr := writeFrame(session.conn, append([]byte{0x00}, response...)); writeFrameErr != nil {
		return nil, writeFrameErr
	}
	return decrypt, nil
}

func (session *Session) answer(device Device, id uint16, data []byte) error {
	switch id {
	case idAuthenticationRequest:
		return session.answerAuthentication(device)
	case idHelloRequest:
		major, minor := device.APIMajor, device.APIMinor
		if major == 0 && minor == 0 {
			major, minor = 1, 19
		}
		return session.Push(&apipb.HelloResponse{
			ApiVersionMajor: major, ApiVersionMinor: minor, Name: device.Name, ServerInfo: "nativeapitest",
		})
	case idDeviceInfoRequest:
		info := device.Info
		if info == nil {
			info = &apipb.DeviceInfoResponse{}
		}
		return session.Push(info)
	case idListEntitiesRequest:
		for _, entity := range device.Entities {
			if err := session.Push(entity); err != nil {
				return err
			}
		}
		return session.Push(&apipb.ListEntitiesDoneResponse{})
	case idSubscribeStatesRequest:
		for _, state := range device.States {
			if err := session.Push(state); err != nil {
				return err
			}
		}
		return nil
	case idPingRequest:
		if !device.IgnorePings {
			return session.Push(&apipb.PingResponse{})
		}
	case idDisconnectRequest:
		_ = session.Push(&apipb.DisconnectResponse{})
		return errors.New("client disconnected")
	}
	message, err := decode(id, data)
	if err != nil {
		return err
	}
	select {
	case session.received <- Message{ID: id, Message: message}:
	default:
		return errors.New("received buffer full")
	}
	return nil
}

func (session *Session) answerAuthentication(device Device) error {
	if device.AutoAuthenticate && !device.PasswordRequired {
		return nil
	}
	//nolint:staticcheck // Emulate pre-1.14 firmware.
	return session.Push(&apipb.AuthenticationResponse{InvalidPassword: device.PasswordRequired})
}

func readFrame(reader *bufio.Reader) ([]byte, error) {
	var header [3]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	if header[0] != 0x01 { //nolint:mnd // Noise frame indicator on the scripted wire.
		return nil, errors.New("bad indicator")
	}
	payload := make([]byte, binary.BigEndian.Uint16(header[1:]))
	_, err := io.ReadFull(reader, payload)
	return payload, err
}

func writeFrame(writer io.Writer, payload []byte) error {
	header := []byte{0x01, 0, 0}
	binary.BigEndian.PutUint16(header[1:], uint16(len(payload))) //nolint:gosec // Test payloads are small.
	_, err := writer.Write(append(header, payload...))
	return err
}

func readMessage(reader *bufio.Reader, decrypt *noise.CipherState) (uint16, []byte, error) {
	ciphertext, err := readFrame(reader)
	if err != nil {
		return 0, nil, err
	}
	plaintext, err := decrypt.Decrypt(nil, nil, ciphertext)
	if err != nil {
		return 0, nil, err
	}
	if len(plaintext) < 4 || int(binary.BigEndian.Uint16(plaintext[2:4])) != len(plaintext)-4 {
		return 0, nil, errors.New("bad message header")
	}
	return binary.BigEndian.Uint16(plaintext[0:2]), bytes.Clone(plaintext[4:]), nil
}

func encode(message proto.Message) (uint16, []byte, error) {
	id, ok := messageID(message.ProtoReflect().Descriptor())
	if !ok {
		return 0, nil, fmt.Errorf("no id for %s", message.ProtoReflect().Descriptor().FullName())
	}
	data, err := proto.Marshal(message)
	return id, data, err
}

func decode(id uint16, data []byte) (proto.Message, error) {
	messages := apipb.File_api_proto.Messages()
	for index := range messages.Len() {
		descriptor := messages.Get(index)
		if candidate, ok := messageID(descriptor); ok && candidate == id {
			messageType, err := protoregistry.GlobalTypes.FindMessageByName(descriptor.FullName())
			if err != nil {
				return nil, err
			}
			message := messageType.New().Interface()
			return message, proto.Unmarshal(data, message)
		}
	}
	return nil, fmt.Errorf("unknown message id %d", id)
}

func messageID(descriptor protoreflect.MessageDescriptor) (uint16, bool) {
	id, ok := proto.GetExtension(descriptor.Options(), apipb.E_Id).(uint32)
	return uint16(id), ok && id != 0 //nolint:gosec // Schema IDs fit in 16 bits.
}
