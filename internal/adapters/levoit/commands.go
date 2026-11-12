package levoit

import (
	"context"
	"errors"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

func (a *Adapter) execute(ctx context.Context, runtime *deviceRuntime, key string,
	responder adapter.Responder, mutate func(device) error, requested status) error {
	if err := acquire(ctx, runtime.gate); err != nil {
		return err
	}
	defer func() { <-runtime.gate }()
	a.mu.RLock()
	active := runtime.active
	a.mu.RUnlock()
	if !active {
		return responder.RejectUnavailable("Purifier is not in the current account inventory")
	}
	if err := mutate(runtime.device); err != nil {
		a.providerFailure(ctx, "command", err)
		if failure, ok := errors.AsType[*providerError](err); ok && failure.uncertain {
			// No authoritative accept/reject evidence exists. Core retains its existing
			// transport failure, not a definite effect rejection or fabricated State.
			return err
		}
		return responder.RejectUnavailable("Purifier command failed: " + failureCode(err))
	}
	evidence, err := responder.Accept()
	if err != nil {
		return err
	}
	// VeSync status snapshots lag controls by minutes. Only acknowledged controls
	// are predicted; scheduled cloud reports may overwrite them immediately.
	requested.ReceivedAt = time.Now().UTC()
	return a.publish(ctx, runtime, requested, key, evidence)
}
