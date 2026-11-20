package hap

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
)

func TestHearthManagedWriteAuthorizationAndTimedWrite(t *testing.T) {
	a := accessory.NewOutlet(accessory.Info{Name: "ABC"})
	c := a.Outlet.On
	c.Permissions = append(c.Permissions, characteristic.PermissionTimedWrite)
	s, err := NewServer(NewMemStore(), a.A)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	c.ManagedSetValueRequestFunc = func(value interface{}, _ *http.Request) (interface{}, int) {
		calls++
		if value != true {
			t.Fatalf("raw value = %#v", value)
		}
		return nil, 0
	}
	put := func(pid int) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"characteristics":[{"aid":%d,"iid":%d,"value":true}],"pid":%d}`, a.Id, c.Id, pid)
		req := httptest.NewRequest(http.MethodPut, "/characteristics", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.ss.Handler.ServeHTTP(w, req)
		return w
	}
	w := put(42)
	if w.Code == http.StatusNoContent || calls != 0 {
		t.Fatalf("unauthorized write: status=%d calls=%d", w.Code, calls)
	}
	s.setSession("192.0.2.1:1234", &session{})
	subscribe := fmt.Sprintf(`{"characteristics":[{"aid":%d,"iid":%d,"ev":true}]}`, a.Id, c.Id)
	w = httptest.NewRecorder()
	s.ss.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/characteristics", strings.NewReader(subscribe)))
	if w.Code != http.StatusNoContent || calls != 0 {
		t.Fatalf("timed characteristic must allow event-only subscription: status=%d calls=%d", w.Code, calls)
	}
	w = put(42)
	if !strings.Contains(w.Body.String(), "-70410") || calls != 0 {
		t.Fatalf("missing timed write: %s calls=%d", w.Body, calls)
	}
	req := httptest.NewRequest(http.MethodPut, "/prepare", nil)
	s.SetTimedWrite(5000, 42, req)
	w = put(41)
	if !strings.Contains(w.Body.String(), "-70410") || calls != 0 {
		t.Fatalf("wrong timed write id: %s calls=%d", w.Body, calls)
	}
	s.SetTimedWrite(5000, 42, req)
	w = put(42)
	if w.Code != http.StatusNoContent || calls != 1 || c.Value() != false {
		t.Fatalf("valid timed write: status=%d calls=%d value=%v", w.Code, calls, c.Value())
	}
	s.SetTimedWrite(1, 42, req)
	ss, _ := s.getSession(req.RemoteAddr)
	ss.twr.deadline = time.Now().Add(-time.Second)
	w = put(42)
	if !strings.Contains(w.Body.String(), "-70410") || calls != 1 {
		t.Fatalf("expired timed write: %s calls=%d", w.Body, calls)
	}
}

func TestHearthManagedWriteReceivesDecodedNull(t *testing.T) {
	a := accessory.NewOutlet(accessory.Info{Name: "ABC"})
	s, err := NewServer(NewMemStore(), a.A)
	if err != nil {
		t.Fatal(err)
	}
	s.setSession("192.0.2.1:1234", &session{})
	calls := 0
	a.Outlet.On.ManagedSetValueRequestFunc = func(value interface{}, _ *http.Request) (interface{}, int) {
		calls++
		if value != nil {
			t.Fatalf("value = %#v", value)
		}
		return nil, -70410
	}
	body := fmt.Sprintf(`{"characteristics":[{"aid":%d,"iid":%d,"value":null}]}`, a.Id, a.Outlet.On.Id)
	req := httptest.NewRequest(http.MethodPut, "/characteristics", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.ss.Handler.ServeHTTP(w, req)
	if calls != 1 || !strings.Contains(w.Body.String(), "-70410") {
		t.Fatalf("null write: status=%d body=%s calls=%d", w.Code, w.Body, calls)
	}
	body = fmt.Sprintf(`{"characteristics":[{"aid":%d,"iid":%d,"ev":true}]}`, a.Id, a.Outlet.On.Id)
	req = httptest.NewRequest(http.MethodPut, "/characteristics", strings.NewReader(body))
	w = httptest.NewRecorder()
	s.ss.Handler.ServeHTTP(w, req)
	if calls != 1 || w.Code != http.StatusNoContent {
		t.Fatalf("event-only write: status=%d body=%s calls=%d", w.Code, w.Body, calls)
	}
}

type hearthFailStore struct {
	Store
	key string
	err error
}

func (s hearthFailStore) Set(key string, value []byte) error {
	if key == s.key {
		return s.err
	}
	return s.Store.Set(key, value)
}

func TestHearthConfigPersistenceFailure(t *testing.T) {
	for _, key := range []string{"schema", "version", "configHash"} {
		t.Run(key, func(t *testing.T) {
			failure := errors.New("store failed")
			store := hearthFailStore{Store: NewMemStore(), key: key, err: failure}
			_, err := NewServer(store, accessory.NewOutlet(accessory.Info{Name: "ABC"}).A)
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestHearthInvalidPinDoesNotExposePin(t *testing.T) {
	s, err := NewServer(NewMemStore(), accessory.NewOutlet(accessory.Info{Name: "ABC"}).A)
	if err != nil {
		t.Fatal(err)
	}
	s.Pin = "12345678"
	if err := s.prepare(); err == nil || strings.Contains(err.Error(), s.Pin) {
		t.Fatalf("error = %v", err)
	}
}
