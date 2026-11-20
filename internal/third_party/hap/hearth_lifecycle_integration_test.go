//go:build integration

package hap

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/internal/dnssd"
)

type hearthResponder struct {
	started chan struct{}
	release chan struct{}
	stopped chan struct{}
	fail    error
}

func (r *hearthResponder) Add(dnssd.Service) (dnssd.ServiceHandle, error) { return nil, nil }
func (r *hearthResponder) Remove(dnssd.ServiceHandle)                     {}
func (r *hearthResponder) Close()                                         {}
func (r *hearthResponder) Debug(context.Context, dnssd.ReadFunc)          {}
func (r *hearthResponder) Respond(ctx context.Context) error {
	return r.RespondReady(ctx, nil)
}
func (r *hearthResponder) RespondReady(ctx context.Context, ready func()) error {
	defer close(r.stopped)
	close(r.started)
	select {
	case <-r.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	if r.fail != nil {
		return r.fail
	}
	ready()
	<-ctx.Done()
	return ctx.Err()
}

func hearthWait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for lifecycle")
	}
}

func TestHearthClosedConnectionLeavesNotificationRegistry(t *testing.T) {
	s, err := NewServer(NewMemStore(), accessory.NewOutlet(accessory.Info{Name: "Registry test"}).A)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan struct{}, 1)
	closed := make(chan struct{}, 1)
	s.ss.ConnState = func(c net.Conn, state http.ConnState) {
		s.connStateEvent(c, state)
		switch state {
		case http.StateNew:
			accepted <- struct{}{}
		case http.StateClosed:
			closed <- struct{}{}
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.ss.Serve(&listener{ln})
	}()
	defer func() {
		_ = s.ss.Close()
		hearthWait(t, done)
	}()
	for attempt := 0; attempt < 2; attempt++ {
		client, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		addr := client.LocalAddr().String()
		hearthWait(t, accepted)
		registered := conns()[addr] != nil
		_ = client.Close()
		hearthWait(t, closed)
		if !registered || conns()[addr] != nil {
			t.Errorf("connection %s: registered when accepted=%t, still registered after close=%t",
				addr, registered, conns()[addr] != nil)
		}
	}
}

func TestHearthReadinessAndEarlyServeFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "early serve failure"
		if fail {
			name = "registration failure"
		}
		t.Run(name, func(t *testing.T) {
			r := &hearthResponder{
				started: make(chan struct{}),
				release: make(chan struct{}),
				stopped: make(chan struct{}),
			}
			failure := errors.New("advertisement failed")
			if fail {
				r.fail = failure
			}
			s, err := NewServer(NewMemStore(), accessory.NewOutlet(accessory.Info{Name: "ABC"}).A)
			if err != nil {
				t.Fatal(err)
			}
			s.Addr = "127.0.0.1:0"
			s.newResponder = func() (dnssd.Responder, error) { return r, nil }
			ready := make(chan struct{})
			s.Ready = func() { close(ready) }
			result := make(chan error, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() { result <- s.ListenAndServe(ctx) }()
			hearthWait(t, r.started)
			select {
			case <-ready:
				t.Fatal("ready before registration")
			default:
			}
			addr := s.ln.Addr().String()
			close(r.release)
			if !fail {
				hearthWait(t, ready)
				if err := s.ln.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-result:
				if err == nil || (fail && !errors.Is(err, failure)) {
					t.Fatalf("serve error = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("server did not return without context cancellation")
			}
			hearthWait(t, r.stopped)
			if fail {
				select {
				case <-ready:
					t.Fatal("ready despite registration failure")
				default:
				}
			}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				t.Fatalf("listener leaked: %v", err)
			}
			ln.Close()
		})
	}
}

func TestHearthRealResponderLifecycle(t *testing.T) {
	s, err := NewServer(NewMemStore(), accessory.NewOutlet(accessory.Info{Name: "HearthTest"}).A)
	if err != nil {
		t.Fatal(err)
	}
	s.Addr = "127.0.0.1:0"
	ready := make(chan struct{})
	s.Ready = func() { close(ready) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- s.ListenAndServe(ctx) }()
	select {
	case <-ready:
	case err := <-result:
		t.Fatalf("startup failed: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("registration did not complete")
	}
	conn, err := net.DialTimeout("tcp", s.ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("listener not reachable after readiness: %v", err)
	}
	conn.Close()
	cancel()
	select {
	case <-result:
	case <-time.After(10 * time.Second):
		t.Fatal("server did not stop")
	}
}
