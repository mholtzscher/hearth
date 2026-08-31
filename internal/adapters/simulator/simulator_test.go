package simulator_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	simulatoradapter "github.com/mholtzscher/hearth/internal/adapters/simulator"

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

func (responder *recordingResponder) RejectUnavailable(message string) error {
	return responder.Reject(message)
}

func TestFailureMatrixScenariosAreRecognized(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{
		simulatoradapter.ScenarioHappy, simulatoradapter.ScenarioDelayedSourceTime, simulatoradapter.ScenarioFutureClockSkew,
		simulatoradapter.ScenarioUnavailableAdapter, simulatoradapter.ScenarioUpstreamRejection,
		simulatoradapter.ScenarioNoOpRefresh, simulatoradapter.ScenarioOverlappingCommands, simulatoradapter.ScenarioOutcomeTimeout,
		simulatoradapter.ScenarioInterruptedCommand, simulatoradapter.ScenarioRestartBeforeAck,
	} {
		if !simulatoradapter.ValidScenario(scenario) {
			t.Fatalf("scenario %q is not recognized", scenario)
		}
	}
	for _, scenario := range []string{"unknown", "duplicate", "malformed"} {
		if simulatoradapter.ValidScenario(scenario) {
			t.Fatalf("non-runtime scenario %q was recognized", scenario)
		}
	}
}

func TestHappyScenarioAcceptsAndPublishesLinkedRefresh(t *testing.T) {
	t.Parallel()
	publisher := &recordingPublisher{}
	simulated, err := simulatoradapter.New(publisher, simulatoradapter.ScenarioHappy)
	if err != nil {
		t.Fatal(err)
	}
	if publishErr := simulated.PublishInitial(context.Background(), simulatorEntityID); publishErr != nil {
		t.Fatal(publishErr)
	}
	handler, err := simulated.CommandHandler(simulatorEntityID)
	if err != nil {
		t.Fatal(err)
	}
	responder := &recordingResponder{}
	commandID := "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	if handlerErr := handler(context.Background(), adapter.Command{
		ID: commandID, CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		EntityID: simulatorEntityID, OperationName: "set", Parameters: json.RawMessage(`{"value":true}`),
		Deadline: time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano),
	}, responder); handlerErr != nil {
		t.Fatal(handlerErr)
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
	t.Parallel()
	tests := []struct {
		scenario     string
		wantAccepted bool
		wantRejected bool
	}{
		{simulatoradapter.ScenarioUpstreamRejection, false, true},
		{simulatoradapter.ScenarioOutcomeTimeout, true, false},
		{simulatoradapter.ScenarioInterruptedCommand, true, false},
	}
	for _, test := range tests {
		t.Run(test.scenario, func(t *testing.T) {
			t.Parallel()
			publisher := &recordingPublisher{}
			simulated, err := simulatoradapter.New(publisher, test.scenario)
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
