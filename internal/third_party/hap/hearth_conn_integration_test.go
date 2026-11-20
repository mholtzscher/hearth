//go:build integration

package hap

import (
	"bytes"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// One encrypted connection transports both HTTP responses and asynchronous
// events. The peer must be able to authenticate every frame in socket order.
func TestHearthConsecutiveEncryptedWritesDoNotInterleave(t *testing.T) {
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()
	if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	sent := &session{}
	rx := &session{decryptKey: sent.encryptKey}
	conn := newConn(server)
	conn.ss = sent
	payloads := []string{strings.Repeat("A", 1600), strings.Repeat("B", 1600)}
	var writes sync.WaitGroup
	writeErrors := make(chan error, len(payloads))
	for _, payload := range payloads {
		payload := payload
		writes.Add(1)
		go func() {
			defer writes.Done()
			_, err := conn.Write([]byte(payload))
			writeErrors <- err
		}()
	}
	seen := map[string]bool{}
	for range payloads {
		plaintext, err := rx.Decrypt(peer)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(plaintext)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(raw, []byte(payloads[0])) && !bytes.Equal(raw, []byte(payloads[1])) {
			t.Fatal("an encrypted write did not preserve its complete message")
		}
		seen[string(raw)] = true
	}
	writes.Wait()
	close(writeErrors)
	for err := range writeErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != len(payloads) {
		t.Fatal("peer did not receive both encrypted messages")
	}
}
