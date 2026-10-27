package automations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

const notificationDestination = notifications.DestinationID("ndst_01980000-0000-7000-8000-000000000001")

func notificationNode(id string) automations.Step {
	return automations.Step{ID: automations.StepID(id), Body: automations.NotificationStep{
		DestinationID: notificationDestination,
		Title:         literalText("Freezer warning"),
		Message:       literalText(" The freezer is warm.\n"),
	}}
}

// Every arm preserves its static message and identity; notifications must not shift Command positions.
func TestNotificationDefinitionNestedRoundTripPreservesCommandPositions(t *testing.T) {
	t.Parallel()
	definition := branchingFixture(t)
	first := definition.Steps[0]
	second := first
	second.ID = "second-command"
	definition.Steps = []automations.Step{
		notificationNode("top"), first,
		{ID: "if", Body: automations.IfStep{
			Conditions: triggerPredicate("a"), Then: []automations.Step{notificationNode("then"), second},
			Else: []automations.Step{notificationNode("else")},
		}},
		{ID: "choose", Body: automations.ChooseStep{
			Branches: []automations.ChooseBranch{
				{ID: "arm", Conditions: triggerPredicate("a"), Steps: []automations.Step{notificationNode("chosen")}},
			},
			Default: []automations.Step{notificationNode("default")},
		}},
	}
	raw, err := automations.EncodeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := automations.DecodeDefinition(raw)
	if err != nil || !reflect.DeepEqual(decoded.Steps, definition.Steps) {
		t.Fatalf("nested round trip = %#v, %v; want %#v", decoded.Steps, err, definition.Steps)
	}
	leaves := automations.CommandLeaves(decoded.Steps)
	if len(leaves) != 2 || leaves[0].ID != first.ID || leaves[1].ID != second.ID {
		t.Fatalf("Command positions = %#v", leaves)
	}
	want := delayDocument(
		`{"id":"notify","kind":"notification","destination_id":"ndst_01980000-0000-7000-8000-000000000001","title":{"literal":""},"message":{"literal":"  警告  "}}`,
	)
	decoded, err = automations.DecodeDefinition(want)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Steps[0].Kind() != automations.StepKindNotification ||
		string(literalBytes(decoded.Steps[0].Body.(automations.NotificationStep).Message)) != `"  警告  "` {
		t.Fatalf("decoded static message = %#v", decoded.Steps[0])
	}
	got, err := automations.EncodeDefinition(decoded)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("canonical bytes = %s, %v; want %s", got, err, want)
	}
}

// Typed and wire boundaries share the standalone message limits, including rune rather than byte counts.
func TestNotificationDefinitionMessageAndIdentityBounds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		body  automations.NotificationStep
		valid bool
	}{
		{
			"empty title",
			automations.NotificationStep{
				DestinationID: notificationDestination,
				Title:         literalText(""),
				Message:       literalText("x"),
			},
			true,
		},
		{
			"maximum unicode",
			automations.NotificationStep{
				DestinationID: notificationDestination,
				Title:         literalText(strings.Repeat("警", 256)),
				Message:       literalText(strings.Repeat("告", 2000)),
			},
			true,
		},
		{
			"long title",
			automations.NotificationStep{
				DestinationID: notificationDestination,
				Title:         literalText(strings.Repeat("警", 257)),
				Message:       literalText("x"),
			},
			false,
		},
		{
			"long message",
			automations.NotificationStep{
				DestinationID: notificationDestination,
				Title:         literalText(""),
				Message:       literalText(strings.Repeat("告", 2001)),
			},
			false,
		},
		{
			"empty message",
			automations.NotificationStep{
				DestinationID: notificationDestination,
				Title:         literalText(""),
				Message:       literalText(""),
			},
			false,
		},
		{
			"whitespace",
			automations.NotificationStep{
				DestinationID: notificationDestination,
				Title:         literalText(""),
				Message:       literalText(" \t\n\u2003"),
			},
			false,
		},
		{"missing destination", automations.NotificationStep{Title: literalText(""), Message: literalText("x")}, false},
		{
			"wrong identity type",
			automations.NotificationStep{
				DestinationID: "ntf_01980000-0000-7000-8000-000000000001",
				Title:         literalText(""),
				Message:       literalText("x"),
			},
			false,
		},
		{
			"wrong UUID version",
			automations.NotificationStep{
				DestinationID: "ndst_01980000-0000-4000-8000-000000000001",
				Title:         literalText(""),
				Message:       literalText("x"),
			},
			false,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			definition := branchingFixture(t)
			definition.Steps = []automations.Step{{ID: "notify", Body: test.body}}
			_, typedErr := automations.EncodeDefinition(definition)
			node, err := json.Marshal(
				map[string]any{
					"id":             "notify",
					"kind":           "notification",
					"destination_id": test.body.DestinationID,
					"title":          test.body.Title,
					"message":        test.body.Message,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			_, wireErr := automations.DecodeDefinition(delayDocument(string(node)))
			if test.valid {
				if typedErr != nil || wireErr != nil {
					t.Fatalf("valid typed/wire input rejected: %v / %v", typedErr, wireErr)
				}
			} else if !errors.Is(typedErr, automations.ErrInvalidAutomation) || !errors.Is(wireErr, automations.ErrInvalidAutomation) {
				t.Fatalf("invalid typed/wire input accepted: %v / %v", typedErr, wireErr)
			}
		})
	}
	for _, body := range []automations.StepBody{
		&automations.NotificationStep{DestinationID: notificationDestination, Title: literalText(""), Message: literalText("x")},
		(*automations.NotificationStep)(nil),
		automations.NotificationStep{DestinationID: notificationDestination, Title: literalText("\xff"), Message: literalText("x")},
		automations.NotificationStep{DestinationID: notificationDestination, Title: literalText(""), Message: literalText("\xff")},
	} {
		definition := branchingFixture(t)
		definition.Steps = []automations.Step{{ID: "notify", Body: body}}
		if _, err := automations.EncodeDefinition(definition); !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Fatalf("invalid body accepted: %#v, %v", body, err)
		}
	}
}

func TestNotificationDefinitionRejectsUnknownAndMissingWireFields(t *testing.T) {
	t.Parallel()
	baseline := map[string]any{
		"id":             "notify",
		"kind":           "notification",
		"destination_id": notificationDestination,
		"title":          literalText(""),
		"message":        literalText("x"),
	}
	for _, field := range []string{"id", "kind", "destination_id", "title", "message"} {
		t.Run("missing "+field, func(t *testing.T) {
			t.Parallel()
			node := make(map[string]any)
			for key, value := range baseline {
				if key != field {
					node[key] = value
				}
			}
			assertInvalidNotificationWire(t, node)
		})
		t.Run("null "+field, func(t *testing.T) {
			t.Parallel()
			node := maps.Clone(baseline)
			node[field] = nil
			assertInvalidNotificationWire(t, node)
		})
	}
	for _, field := range []string{"extra", "entity_id", "parameters", "adapter_id", "local_key", "duration_ms"} {
		t.Run("unknown "+field, func(t *testing.T) {
			t.Parallel()
			node := maps.Clone(baseline)
			node[field] = "unexpected"
			assertInvalidNotificationWire(t, node)
		})
	}
}

func assertInvalidNotificationWire(t *testing.T, node map[string]any) {
	t.Helper()
	raw, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = automations.DecodeDefinition(
		delayDocument(string(raw)),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("invalid wire accepted: %s, %v", raw, err)
	}
}

// The shared 32-effect bound covers all arms, not only the selected execution path.
func TestNotificationDefinitionCombinedSideEffectBound(t *testing.T) {
	t.Parallel()
	for _, commands := range []bool{false, true} {
		for _, count := range []int{32, 33} {
			t.Run(fmt.Sprintf("commands=%t/count=%d", commands, count), func(t *testing.T) {
				t.Parallel()
				definition := branchingFixture(t)
				leaf := notificationNode("notify")
				if commands {
					leaf = definition.Steps[0]
				}
				definition.Steps = []automations.Step{{ID: "route", Body: automations.IfStep{
					Conditions: triggerPredicate("a"), Then: commandSequence(leaf, "then", 16),
					Else: commandSequence(notificationNode("notify"), "else", count-16),
				}}}
				_, err := automations.EncodeDefinition(definition)
				assertSideEffectBoundary(t, "typed", count, err)
				_, err = automations.DecodeDefinition(wireDefinition(t, definition))
				assertSideEffectBoundary(t, "wire", count, err)
			})
		}
	}
}

func assertSideEffectBoundary(t *testing.T, boundary string, count int, err error) {
	t.Helper()
	if count == 32 && err != nil {
		t.Fatalf("%s: 32 side effects rejected: %v", boundary, err)
	}
	if count == 33 && !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("%s: 33 side effects accepted: %v", boundary, err)
	}
}

type definitionNotifications struct {
	destinations map[notifications.DestinationID]notifications.Destination
	lookupErr    error
}

func (stub *definitionNotifications) GetDestination(
	_ context.Context,
	id notifications.DestinationID,
) (notifications.Destination, error) {
	if stub.lookupErr != nil {
		return notifications.Destination{}, stub.lookupErr
	}
	destination, ok := stub.destinations[id]
	if !ok {
		return notifications.Destination{}, notifications.ErrDestinationNotFound
	}
	return destination, nil
}

func (*definitionNotifications) Send(context.Context, notifications.SendInput) (notifications.Attempt, error) {
	return notifications.Attempt{}, errors.New("definition validation must not send")
}

func (*definitionNotifications) GetAttempt(context.Context, notifications.AttemptID) (notifications.Attempt, error) {
	return notifications.Attempt{}, errors.New("definition validation must not read attempts")
}
