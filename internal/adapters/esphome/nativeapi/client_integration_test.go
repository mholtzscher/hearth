//go:build integration

package nativeapi_test

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/nativeapitest"
)

// listen serves device on a loopback listener for each accepted connection and returns the dial target.
func listen(t *testing.T, device nativeapitest.Device) (string, uint16) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			session := device.Serve(conn)
			t.Cleanup(session.Close)
		}
	}()
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.ParseUint(portText, 10, 16)
	return host, uint16(port)
}

func TestNetDialerConnectsOverTCP(t *testing.T) {
	t.Parallel()
	host, port := listen(t, scriptedDevice(t))
	config := clientConfig(t)
	config.Address, config.Port = host, port
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := nativeapi.NetDialer{}.Dial(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	entities, err := conn.ListEntities(ctx)
	if err != nil || len(entities) != 4 {
		t.Fatalf("entities = %d, %v", len(entities), err)
	}
	if subscribeStatesErr := conn.SubscribeStates(ctx); subscribeStatesErr != nil {
		t.Fatal(subscribeStatesErr)
	}
	if got := nextEvent(t, conn); got.Key != 10 || got.Number != 1572 {
		t.Fatalf("state = %+v", got)
	}
	if sendErr := conn.Send(ctx, nativeapi.SwitchCommand{Key: 11}); sendErr != nil {
		t.Fatal(sendErr)
	}
}

func TestNetDialerReportsWrongKeyOverTCP(t *testing.T) {
	t.Parallel()
	host, port := listen(t, scriptedDevice(t))
	config := clientConfig(t)
	config.Address, config.Port, config.PSK = host, port, testPSK(t, 7)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := (nativeapi.NetDialer{}).Dial(ctx, config); !errors.Is(err, nativeapi.ErrEncryptionKeyRejected) {
		t.Fatalf("error = %v", err)
	}
}

func TestNetDialerHonorsContextDeadline(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		// Accept and never answer, as a hung device would.
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.ParseUint(portText, 10, 16)
	config := clientConfig(t)
	config.Address, config.Port = host, uint16(port)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, dialErr := (nativeapi.NetDialer{}).Dial(ctx, config); !errors.Is(dialErr, context.DeadlineExceeded) {
		t.Fatalf("error = %v", dialErr)
	}
}
