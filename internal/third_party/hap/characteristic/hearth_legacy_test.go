package characteristic

import (
	"net/http"
	"reflect"
	"testing"
)

// These cases document v0.0.35 behavior retained for non-managed users.
func TestHearthUpstreamLegacyWriteBehavior(t *testing.T) {
	c := NewInt("legacy")
	c.Format = FormatInt32
	c.Permissions = []string{PermissionRead, PermissionWrite}
	c.MinVal, c.MaxVal = 0, 100
	c.SetValue(10)
	var writes []interface{}
	var notifications []interface{}
	c.SetValueRequestFunc = func(v interface{}, _ *http.Request) (interface{}, int) {
		writes = append(writes, v)
		return 42, 0
	}
	c.OnCValueUpdate(func(_ *C, v, _ interface{}, _ *http.Request) {
		notifications = append(notifications, v)
	})
	req := &http.Request{}
	if _, status := c.SetValueRequest(float64(10), req); status != 0 || len(writes) != 0 {
		t.Fatalf("equal write: status=%d writes=%v", status, writes)
	}
	response, status := c.SetValueRequest(float64(101), req)
	if status != 0 || response != 42 || !reflect.DeepEqual(writes, []interface{}{100}) ||
		c.Value() != 100 || !reflect.DeepEqual(notifications, []interface{}{100}) {
		t.Fatalf("clamped write: status=%d response=%v writes=%v cache=%v notifications=%v",
			status, response, writes, c.Value(), notifications)
	}
}
