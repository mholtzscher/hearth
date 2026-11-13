package nativeapi //nolint:testpackage // The codec is intentionally private to the selected transport.

import (
	"bufio"
	"bytes"
	"errors"
	"testing"
)

func TestPlaintextFramesMatchWireContract(t *testing.T) {
	t.Parallel()
	var wire bytes.Buffer
	transport := plaintextTransport{writer: &wire}
	// State push, type25, payload: fixed32 key1 and float32 value1.
	payload := []byte{13, 1, 0, 0, 0, 21, 0, 0, 128, 63}
	if err := transport.writeMessage(25, payload); err != nil {
		t.Fatal(err)
	}
	want := append([]byte{0, 10, 25}, payload...)
	if !bytes.Equal(wire.Bytes(), want) {
		t.Fatalf("frame = %x, want %x", wire.Bytes(), want)
	}
	transport.reader = bufio.NewReader(bytes.NewReader(want))
	id, got, err := transport.readMessage()
	if err != nil || id != 25 || !bytes.Equal(got, payload) {
		t.Fatalf("decoded = %d %x %v", id, got, err)
	}
}

func TestPlaintextRejectsBadHeadersAndOversizedFrames(t *testing.T) {
	t.Parallel()
	for _, wire := range [][]byte{
		{0, 128, 128, 4},             // length65536
		{0, 255, 255, 255, 255, 255}, // overflowing varint
		{1, 0, 0},                    // encrypted marker: never switch transports
		{0, 0, 128, 128, 4},          // message type65536
	} {
		transport := plaintextTransport{reader: bufio.NewReader(bytes.NewReader(wire))}
		if _, _, err := transport.readMessage(); err == nil {
			t.Errorf("accepted invalid header %x", wire)
		}
	}
	var wire bytes.Buffer
	transport := plaintextTransport{writer: &wire}
	if err := transport.writeMessage(1, make([]byte, 65536)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversize write = %v", err)
	}
	if wire.Len() != 0 {
		t.Fatal("oversize frame wrote bytes")
	}
}
