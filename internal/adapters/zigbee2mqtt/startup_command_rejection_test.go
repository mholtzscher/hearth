package zigbee2mqtt //nolint:testpackage // Command tests exercise the private runtime state machine.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This test protects prompt ordinary rejection of adapter-local startup
// validation failures and fails if a fractional in-range value reaches MQTT,
// is classified unavailable, or leaves the responder unconsumed (which the
// session would surface as a missing response and the core as an outcome
// timeout instead of a rejection).
func TestStartupFractionalCommandRejectsWithoutPublish(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, coordinator, connection, device := commandReadyAdapterFor(
		t,
		recorder,
		session,
		"bridge-devices-wanda-synthetic.json",
	)
	startup := entityByKey(device, "startupcolortemp")
	if startup.entityID == "" {
		t.Fatal("startup Entity was not discovered")
	}
	responder := newFakeResponder(recorder, session)
	start := time.Now()
	err := z2m.HandleCommand(
		context.Background(),
		testCommand(startup.entityID, `{"mode":"value","value":250.5}`),
		responder,
	)
	if err == nil {
		t.Fatal("fractional startup command was accepted")
	}
	// The rejection lands long before the one-minute command deadline: the
	// handler returns instead of blocking for an outcome timeout.
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("fractional startup rejection took %v", elapsed)
	}
	if responder.rejected != 1 || responder.unavailable != 0 || responder.accepted != 0 {
		t.Fatalf("responder rejected=%d unavailable=%d accepted=%d",
			responder.rejected, responder.unavailable, responder.accepted)
	}
	connection.mutex.Lock()
	publications := append([]mqttPublication(nil), connection.published...)
	connection.mutex.Unlock()
	if len(publications) != 0 {
		t.Fatalf("rejected startup command reached MQTT: %#v", publications)
	}
	// Nothing was queued, so no matcher can claim a later report and no
	// deadline can fire for this command.
	if len(coordinator.attempts) != 0 {
		t.Fatal("rejected startup command queued an attempt")
	}
	if _, installed := coordinator.matchers[startup.entityID]; installed {
		t.Fatal("rejected startup command installed an outcome matcher")
	}
}

// rejectFailingResponder fails rejection publication so translator error
// propagation can be pinned.
type rejectFailingResponder struct{ rejectErr error }

func (responder rejectFailingResponder) Accept() (adapter.CommandEvidence, error) {
	return nil, errors.New("unexpected startup accept")
}

func (responder rejectFailingResponder) Reject(string) error { return responder.rejectErr }

func (responder rejectFailingResponder) RejectUnavailable(string) error {
	return errors.New("unexpected startup unavailable")
}

// This test protects rejection send-error propagation and fails if the
// startup translator hides a failed rejection publication behind the
// validation error.
func TestStartupTranslatorPreservesRejectionSendError(t *testing.T) {
	t.Parallel()
	byKey := bulbRoutedPlans(t)
	sendErr := errors.New("publish command response: no responders")
	_, _, err := translateCommand(
		context.Background(),
		commandRoute{entityID: "entity-startupcolortemp", entity: byKey["startupcolortemp"]},
		testCommand("entity-startupcolortemp", `{"mode":"value","value":250.5}`),
		rejectFailingResponder{rejectErr: sendErr},
	)
	if !errors.Is(err, sendErr) {
		t.Fatalf("translator error = %v, want rejection send error", err)
	}
}
