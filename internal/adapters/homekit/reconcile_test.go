package homekit //nolint:testpackage // Tests the health classification owned by the runtime coordinator.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBridgeHealthRequiresHAPAndRecentInventoryAndFacts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		active    bool
		age       time.Duration
		factFault bool
		reason    string
	}{
		{"ready", true, time.Minute, false, ""},
		{"listener stopped", false, time.Minute, false, healthHAP},
		{"no recent inventory", true, 4 * time.Minute, false, healthCore},
		{"fact feed lost", true, time.Minute, true, healthFacts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.reason, healthReason(tc.active, tc.age, tc.factFault, time.Minute))
		})
	}
}

func TestRebuildRequestsShareOneWindow(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		coordinator := reconciler{}
		started := time.Now()
		coordinator.scheduleReload()
		first := coordinator.reload
		time.Sleep(time.Second)
		coordinator.scheduleReload()
		assert.Equal(t, first, coordinator.reload)
		<-coordinator.reload
		assert.Equal(t, reloadDelay, time.Since(started))
	})
}
