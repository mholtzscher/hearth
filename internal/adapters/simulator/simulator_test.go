package simulator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const simulatorEntityID = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"

type recordingPublisher struct {
	observations []adapter.Observation
}

func (publisher *recordingPublisher) PublishObservation(
	_ context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	publisher.observations = append(publisher.observations, observation)
	return "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab", nil
}

type recordingResponder struct {
	accepted bool
	rejected bool
}

func (responder *recordingResponder) Accept() error {
	responder.accepted = true
	return nil
}

func (responder *recordingResponder) Reject(string) error {
	responder.rejected = true
	return nil
}

func TestFailureMatrixScenariosAreRecognized(t *testing.T) {
	for _, scenario := range []string{
		ScenarioHappy, ScenarioDuplicate, ScenarioDelayedSourceTime, ScenarioFutureClockSkew,
		ScenarioMalformed, ScenarioUnavailableAdapter, ScenarioUpstreamRejection,
		ScenarioNoOpRefresh, ScenarioOverlappingCommands, ScenarioOutcomeTimeout,
		ScenarioInterruptedCommand, ScenarioRestartBeforeAck,
	} {
		if !ValidScenario(scenario) {
			t.Fatalf("scenario %q is not recognized", scenario)
		}
	}
	if ValidScenario("unknown") {
		t.Fatal("unknown scenario was recognized")
	}
}

func TestHappyScenarioAcceptsAndPublishesLinkedRefresh(t *testing.T) {
	publisher := &recordingPublisher{}
	simulated, err := New(publisher, ScenarioHappy)
	if err != nil {
		t.Fatal(err)
	}
	if err := simulated.PublishInitial(context.Background(), simulatorEntityID); err != nil {
		t.Fatal(err)
	}
	handler, err := simulated.CommandHandler(simulatorEntityID)
	if err != nil {
		t.Fatal(err)
	}
	responder := &recordingResponder{}
	commandID := "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	if err := handler(context.Background(), adapter.Command{
		ID: commandID, CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		EntityID: simulatorEntityID, OperationName: "set", Parameters: json.RawMessage(`{"value":true}`),
		Deadline: time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano),
	}, responder); err != nil {
		t.Fatal(err)
	}
	if !responder.accepted || responder.rejected || len(publisher.observations) != 2 {
		t.Fatalf("responder = %#v, observations = %#v", responder, publisher.observations)
	}
	refresh := publisher.observations[1]
	if refresh.RefreshForCommand == nil || *refresh.RefreshForCommand != commandID || string(refresh.Value) != "true" {
		t.Fatalf("refresh = %#v", refresh)
	}
}

func TestFailureScenariosRejectOrWithholdOutcome(t *testing.T) {
	tests := []struct {
		scenario     string
		wantAccepted bool
		wantRejected bool
	}{
		{ScenarioUpstreamRejection, false, true},
		{ScenarioOutcomeTimeout, true, false},
		{ScenarioInterruptedCommand, true, false},
	}
	for _, test := range tests {
		t.Run(test.scenario, func(t *testing.T) {
			publisher := &recordingPublisher{}
			simulated, err := New(publisher, test.scenario)
			if err != nil {
				t.Fatal(err)
			}
			handler, err := simulated.CommandHandler(simulatorEntityID)
			if err != nil {
				t.Fatal(err)
			}
			responder := &recordingResponder{}
			err = handler(context.Background(), adapter.Command{
				ID:            "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				EntityID:      simulatorEntityID, OperationName: "set", Parameters: json.RawMessage(`{"value":true}`),
				Deadline: time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano),
			}, responder)
			if err != nil {
				t.Fatal(err)
			}
			if responder.accepted != test.wantAccepted || responder.rejected != test.wantRejected ||
				len(publisher.observations) != 0 {
				t.Fatalf("responder = %#v, observations = %#v", responder, publisher.observations)
			}
		})
	}
}
