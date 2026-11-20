package characteristic

import (
	"net/http"
	"reflect"
	"testing"
)

func TestHearthManagedWriteAndNotify(t *testing.T) {
	c := NewInt("managed")
	c.Format = FormatInt32
	c.Permissions = []string{PermissionRead, PermissionWrite}
	c.MinVal, c.MaxVal = 0, 100
	if err := c.SetValue(10); err != nil {
		t.Fatal(err)
	}
	req := &http.Request{}
	var writes []interface{}
	var notices []interface{}
	c.ManagedSetValueRequestFunc = func(value interface{}, request *http.Request) (interface{}, int) {
		if request != req {
			t.Fatal("request was not preserved")
		}
		writes = append(writes, value)
		if value == float64(101) {
			return nil, -70410
		}
		return "accepted", 0
	}
	c.SetValueRequestFunc = func(interface{}, *http.Request) (interface{}, int) {
		t.Fatal("legacy callback called for managed write")
		return nil, 0
	}
	c.OnCValueUpdate(func(_ *C, value, old interface{}, request *http.Request) {
		notices = append(notices, []interface{}{value, old, request})
	})

	for _, value := range []float64{10, 20, 101} {
		response, status := c.SetValueRequest(value, req)
		if value == 101 {
			if status != -70410 || response != nil {
				t.Fatalf("invalid write: response=%v status=%d", response, status)
			}
		} else if status != 0 || response != "accepted" {
			t.Fatalf("write %v: response=%v status=%d", value, response, status)
		}
	}
	if !reflect.DeepEqual(writes, []interface{}{float64(10), float64(20), float64(101)}) {
		t.Fatalf("raw writes = %#v", writes)
	}
	if c.Value() != 10 || len(notices) != 0 {
		t.Fatalf("managed writes changed cache or notified: value=%v notices=%v", c.Value(), notices)
	}
	c.NotifyValue()
	if !reflect.DeepEqual(notices, []interface{}{[]interface{}{10, 10, (*http.Request)(nil)}}) || c.Value() != 10 {
		t.Fatalf("forced notification: value=%v notices=%#v", c.Value(), notices)
	}
	c.Permissions = []string{PermissionRead}
	_, status := c.SetValueRequest(float64(30), req)
	if status != -70404 || len(writes) != 3 {
		t.Fatalf("non-writable request: status=%d writes=%v", status, writes)
	}
	if err := c.SetValue(33); err != nil || c.Value() != 33 || len(notices) != 2 {
		t.Fatalf("local write: error=%v value=%v notices=%v", err, c.Value(), notices)
	}
}
