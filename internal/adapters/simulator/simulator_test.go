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
	linkedObservations  []adapter.Observation
	healthReports       []adapter.HealthReport
	availabilityReports []adapter.EntityAvailabilityReport
	entityEvents        []adapter.EntityEvent
}

func (session *recordingSession) PublishObservation(
	_ context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	session.observations = append(session.observations, observation)
	return "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab", nil
}

func (session *recordingSession) PublishEntityEvent(
	_ context.Context,
	event adapter.EntityEvent,
) (adapter.EntityEventID, error) {
	session.entityEvents = append(session.entityEvents, event)
	return "evt_01890f47-7a6b-7c4d-8e9f-0123456789ab", nil
}

type recordingEvidence struct{ session *recordingSession }

func (evidence recordingEvidence) PublishObservation(
	_ context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	evidence.session.linkedObservations = append(evidence.session.linkedObservations, observation)
	return "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab", nil
}

func (session *recordingSession) evidence() adapter.CommandEvidence {
	return recordingEvidence{session: session}
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
	evidence adapter.CommandEvidence
}

func (responder *recordingResponder) Accept() (adapter.CommandEvidence, error) {
	responder.accepted = true
	return responder.evidence, nil
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
		simulatoradapter.ScenarioEntityEvents,
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
	responder := &recordingResponder{evidence: session.evidence()}
	if handlerErr := handler(context.Background(), adapter.Command{
		ID: "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab", CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		EntityID: simulatorEntityID, OperationName: "set", Parameters: json.RawMessage(`{"value":true}`),
		Deadline: time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano),
	}, responder); handlerErr != nil {
		t.Fatal(handlerErr)
	}
	if !responder.accepted || responder.rejected || len(session.observations) != 1 ||
		len(session.linkedObservations) != 1 {
		t.Fatalf(
			"responder = %#v, ordinary observations = %#v, linked observations = %#v",
			responder,
			session.observations,
			session.linkedObservations,
		)
	}
	if len(session.healthReports) != 1 || session.healthReports[0].Status != adapter.HealthHealthy ||
		len(session.availabilityReports) != 1 ||
		session.availabilityReports[0].Status != adapter.AvailabilityAvailable {
		t.Fatalf("health = %#v, availability = %#v", session.healthReports, session.availabilityReports)
	}
	refresh := session.linkedObservations[0]
	if string(refresh.Value) != "true" {
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
	responder := &recordingResponder{evidence: session.evidence()}
	if err = handler(context.Background(), adapter.Command{
		ID:            "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		EntityID:      simulatorEntityID, OperationName: "set", Parameters: json.RawMessage(`{"value":true}`),
		Deadline: time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano),
	}, responder); err != nil {
		t.Fatal(err)
	}
	if !responder.accepted || responder.rejected || len(session.availabilityReports) != 2 ||
		session.availabilityReports[1].Status != adapter.AvailabilityAvailable || len(session.observations) != 0 ||
		len(session.linkedObservations) != 1 {
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
			responder := &recordingResponder{evidence: session.evidence()}
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
				len(session.observations) != 0 || len(session.linkedObservations) != 0 {
				t.Fatalf(
					"responder = %#v, ordinary observations = %#v, linked observations = %#v",
					responder,
					session.observations,
					session.linkedObservations,
				)
			}
		})
	}
}

// This test protects the entity-events scenario contract and fails if the
// event source Entity is not bound before publication, if the generated
// support loses either synthetic name, or if an unsupported name is published.
func TestEntityEventsScenarioEmitsValidatedSyntheticReports(t *testing.T) {
	t.Parallel()
	session := &recordingSession{}
	simulated, err := simulatoradapter.New(session, simulatoradapter.ScenarioEntityEvents)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = simulated.EmitEntityEvent(
		context.Background(), simulatoradapter.EntityEventSinglePress,
	); err == nil {
		t.Fatal("EmitEntityEvent published before the event source Entity was bound")
	}
	support := simulated.EntityEventSupport()
	if len(support.Events.Names) != 2 ||
		support.Events.Names[0] != simulatoradapter.EntityEventSinglePress ||
		support.Events.Names[1] != simulatoradapter.EntityEventDoublePress {
		t.Fatalf("event support = %#v", support)
	}
	if initializeErr := simulated.InitializeEntityEventSource(
		context.Background(), simulatorEntityID,
	); initializeErr != nil {
		t.Fatal(initializeErr)
	}
	if len(session.availabilityReports) != 1 ||
		session.availabilityReports[0].EntityID != simulatorEntityID ||
		session.availabilityReports[0].Status != adapter.AvailabilityAvailable {
		t.Fatalf("availability = %#v", session.availabilityReports)
	}
	for _, name := range []string{
		simulatoradapter.EntityEventSinglePress, simulatoradapter.EntityEventDoublePress,
	} {
		eventID, emitErr := simulated.EmitEntityEvent(context.Background(), name)
		if emitErr != nil {
			t.Fatal(emitErr)
		}
		if eventID == "" {
			t.Fatalf("EmitEntityEvent(%q) returned no identity", name)
		}
	}
	if len(session.entityEvents) != 2 {
		t.Fatalf("entity events = %#v", session.entityEvents)
	}
	for _, event := range session.entityEvents {
		if event.EntityID != simulatorEntityID {
			t.Fatalf("entity event = %#v", event)
		}
	}
	if _, err = simulated.EmitEntityEvent(context.Background(), "triple_press"); err == nil {
		t.Fatal("EmitEntityEvent accepted an unsupported name")
	}
	if len(session.entityEvents) != 2 {
		t.Fatalf("unsupported name published a report: %#v", session.entityEvents)
	}
	if len(session.observations) != 0 {
		t.Fatalf("entity events published observations: %#v", session.observations)
	}
}
