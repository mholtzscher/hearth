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

type recordingSession struct {
	observations        []adapter.Observation
	healthReports       []adapter.HealthReport
	availabilityReports []adapter.EntityAvailabilityReport
}

func (session *recordingSession) PublishObservation(
	_ context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	session.observations = append(session.observations, observation)
	return "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab", nil
}

func (session *recordingSession) SetHealth(_ context.Context, report adapter.HealthReport) error {
	session.healthReports = append(session.healthReports, report)
	return nil
}

func (session *recordingSession) ReportEntityAvailability(
	_ context.Context,
	reports []adapter.EntityAvailabilityReport,
) error {
	session.availabilityReports = append(session.availabilityReports, reports...)
	return nil
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
		simulatoradapter.ScenarioAdapterUnhealthy, simulatoradapter.ScenarioEntityUnavailable,
		simulatoradapter.ScenarioUpstreamRejection,
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
	session := &recordingSession{}
	simulated, err := simulatoradapter.New(session, simulatoradapter.ScenarioHappy)
	if err != nil {
		t.Fatal(err)
	}
	if initializeErr := simulated.Initialize(context.Background(), simulatorEntityID); initializeErr != nil {
		t.Fatal(initializeErr)
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
	if !responder.accepted || responder.rejected || len(session.observations) != 2 {
		t.Fatalf("responder = %#v, observations = %#v", responder, session.observations)
	}
	if len(session.healthReports) != 1 || session.healthReports[0].Status != adapter.HealthHealthy ||
		len(session.availabilityReports) != 1 ||
		session.availabilityReports[0].Status != adapter.AvailabilityAvailable {
		t.Fatalf("health = %#v, availability = %#v", session.healthReports, session.availabilityReports)
	}
	refresh := session.observations[1]
	if refresh.RefreshForCommand == nil || *refresh.RefreshForCommand != commandID || string(refresh.Value) != "true" {
		t.Fatalf("refresh = %#v", refresh)
	}
}

func TestAdapterUnhealthyScenarioReportsHealthWithoutPublishingState(t *testing.T) {
	t.Parallel()
	session := &recordingSession{}
	simulated, err := simulatoradapter.New(session, simulatoradapter.ScenarioAdapterUnhealthy)
	if err != nil {
		t.Fatal(err)
	}
	if err = simulated.Initialize(context.Background(), simulatorEntityID); err != nil {
		t.Fatal(err)
	}
	if len(session.healthReports) != 1 || session.healthReports[0].Status != adapter.HealthUnhealthy ||
		session.healthReports[0].ReasonCode != "hearth.external_system_unavailable" ||
		len(session.availabilityReports) != 0 || len(session.observations) != 0 {
		t.Fatalf(
			"health = %#v, availability = %#v, observations = %#v",
			session.healthReports, session.availabilityReports, session.observations,
		)
	}
}

func TestEntityUnavailableScenarioRecoversThroughCommand(t *testing.T) {
	t.Parallel()
	session := &recordingSession{}
	simulated, err := simulatoradapter.New(session, simulatoradapter.ScenarioEntityUnavailable)
	if err != nil {
		t.Fatal(err)
	}
	if err = simulated.Initialize(context.Background(), simulatorEntityID); err != nil {
		t.Fatal(err)
	}
	if len(session.availabilityReports) != 1 ||
		session.availabilityReports[0].Status != adapter.AvailabilityUnavailable ||
		len(session.observations) != 0 {
		t.Fatalf("initial availability = %#v, observations = %#v", session.availabilityReports, session.observations)
	}
	handler, err := simulated.CommandHandler(simulatorEntityID)
	if err != nil {
		t.Fatal(err)
	}
	responder := &recordingResponder{}
	if err = handler(context.Background(), adapter.Command{
		ID:            "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		EntityID:      simulatorEntityID, OperationName: "set", Parameters: json.RawMessage(`{"value":true}`),
		Deadline: time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano),
	}, responder); err != nil {
		t.Fatal(err)
	}
	if !responder.accepted || responder.rejected || len(session.availabilityReports) != 2 ||
		session.availabilityReports[1].Status != adapter.AvailabilityAvailable || len(session.observations) != 1 {
		t.Fatalf(
			"responder = %#v, availability = %#v, observations = %#v",
			responder, session.availabilityReports, session.observations,
		)
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
			session := &recordingSession{}
			simulated, err := simulatoradapter.New(session, test.scenario)
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
				len(session.observations) != 0 {
				t.Fatalf("responder = %#v, observations = %#v", responder, session.observations)
			}
		})
	}
}
