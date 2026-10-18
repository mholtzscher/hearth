package natswire_test

import (
	"testing"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

func TestAdapterHealthRoute(t *testing.T) {
	t.Parallel()
	subject, err := natswire.AdapterHealthSubject("zigbee-main")
	if err != nil || subject != "hearth.v1.core.adapter-health.zigbee-main" {
		t.Fatalf("subject=%q err=%v", subject, err)
	}
	route, err := natswire.ParseAdapterHealthSubject(subject)
	if err != nil || route.AdapterID != "zigbee-main" {
		t.Fatalf("route=%+v err=%v", route, err)
	}
	for _, invalid := range []string{"", "hearth.v1.core.adapter-health.*", "hearth.v1.adapter.adapter-health.a", "hearth.v1.core.adapter-health.a.b", "hearth.v1.core.adapter-health.A"} {
		if _, parseErr := natswire.ParseAdapterHealthSubject(invalid); parseErr == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	if _, routeErr := natswire.AdapterHealthSubject("a.b"); routeErr == nil {
		t.Fatal("accepted multi-token Adapter")
	}
}
