package homekit //nolint:testpackage // Tests the health classification owned by the runtime coordinator.

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBridgeMutationDoesNotWaitForInvocationDeadlineAfterShutdown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r := Runtime{mutations: make(chan bridgeMutation), stopping: ctx.Done()}
		handlerCtx, finish := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer finish()
		done := make(chan error, 1)
		go func() { done <- r.resetPairing(handlerCtx) }()
		<-r.mutations
		cancel()
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(time.Second):
			t.Fatal("admitted mutation did not unblock on bridge shutdown")
		}
	})
}

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
		assert.Equal(t, 2*time.Second, time.Since(started))
	})
}
