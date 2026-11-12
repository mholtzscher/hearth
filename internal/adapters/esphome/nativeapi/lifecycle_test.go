package nativeapi_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
)

func TestCanceledSendWritesNoCommand(t *testing.T) {
	t.Parallel()
	conn, session, err := openPipe(t, scriptedDevice(t), clientConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sendErr := conn.Send(ctx, nativeapi.SwitchCommand{Key: 11, On: true}); !errors.Is(sendErr, context.Canceled) {
		t.Fatalf("send error = %v", sendErr)
	}
	if pushErr := session.Push(&apipb.PingRequest{}); pushErr != nil {
		t.Fatal(pushErr)
	}
	if got := nextReceived(t, session); got.ID != 8 {
		t.Fatalf("received command after cancellation: %v", got.Message)
	}
}

func TestKeepaliveFollowsLateTraffic(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		device := scriptedDevice(t)
		device.IgnorePings = true
		conn, session, err := openPipe(t, device, clientConfig(t))
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Second)
		synctest.Wait()
		if message := <-session.Received(); message.ID != 7 {
			t.Fatalf("not a ping: %v", message)
		}
		time.Sleep(time.Second)
		if pushErr := session.Push(&apipb.PingResponse{}); pushErr != nil {
			t.Fatal(pushErr)
		}
		time.Sleep(20 * time.Second)
		synctest.Wait()
		select {
		case message := <-session.Received():
			if message.ID != 7 {
				t.Fatalf("not a ping: %v", message)
			}
		default:
			t.Fatal("no ping 20 seconds after late traffic")
		}
		if conn.Err() != nil {
			t.Fatalf("connection ended: %v", conn.Err())
		}
	})
}

func TestDoneSignalsFailureBeforeBufferedStatesDrain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		conn, session, err := openPipe(t, scriptedDevice(t), clientConfig(t))
		if err != nil {
			t.Fatal(err)
		}
		if subscribeStatesErr := conn.SubscribeStates(context.Background()); subscribeStatesErr != nil {
			t.Fatal(subscribeStatesErr)
		}
		synctest.Wait()
		session.Close()
		synctest.Wait()
		select {
		case <-conn.Done():
		default:
			t.Fatal("termination not signaled with buffered State")
		}
		if conn.Err() == nil {
			t.Fatal("missing failure cause")
		}
		if len(conn.Events()) != 3 {
			t.Fatalf("test needs buffered State: %d", len(conn.Events()))
		}
	})
}

func TestSendAfterCloseIsRejected(t *testing.T) {
	t.Parallel()
	conn, _, err := openPipe(t, scriptedDevice(t), clientConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := conn.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if sendErr := conn.Send(
		context.Background(),
		nativeapi.SwitchCommand{Key: 11, On: true},
	); !errors.Is(
		sendErr,
		nativeapi.ErrClosed,
	) {
		t.Fatalf("send after close = %v", sendErr)
	}
}
