//go:build integration

package automations_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// Lookup outages must retain their operational identity and never become invalid input;
// neither create nor replace may persist a definition whose references could not be checked.
func TestServiceNotificationLookupErrorsPreserveClassificationBeforeWrites(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		err     error
		invalid bool
	}{
		{"missing destination", fmt.Errorf("lookup: %w", notifications.ErrDestinationNotFound), true},
		{"invalid destination", fmt.Errorf("lookup: %w", notifications.ErrInvalidInput), true},
		{"storage outage", errors.New("storage unavailable"), false},
		{"canceled lookup", context.Canceled, false},
		{"lookup deadline", context.DeadlineExceeded, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &delayDefinitionWrites{
				Repository: automationssqlite.NewAutomationRepository(
					openAutomationDatabase(t),
					runtimeTestDependencies(),
				),
			}
			stub := &definitionNotifications{destinations: map[notifications.DestinationID]notifications.Destination{
				notificationDestination: {ID: notificationDestination},
			}}
			dependencies := runtimeTestDependencies()
			dependencies.Notifications = stub
			service := automations.NewService(repository, newScriptedDevices(), dependencies)
			definition := branchingFixture(t)
			definition.Steps = []automations.Step{notificationNode("notify")}
			created, err := service.CreateAutomation(t.Context(), definition)
			if err != nil {
				t.Fatal(err)
			}
			repository.creates, repository.replacements = 0, 0
			stub.lookupErr = test.err
			_, createErr := service.CreateAutomation(t.Context(), definition)
			definition.Name = "Replacement"
			_, replaceErr := service.ReplaceAutomation(t.Context(), created.ID, created.Revision, definition)
			for _, err := range []error{createErr, replaceErr} {
				if !errors.Is(err, test.err) || errors.Is(err, automations.ErrInvalidAutomation) != test.invalid {
					t.Fatalf("lookup error classification = %v; invalid = %t", err, test.invalid)
				}
			}
			if repository.creates != 0 || repository.replacements != 0 {
				t.Fatal("failed lookup reached persistence")
			}
			stored, err := service.GetAutomation(t.Context(), created.ID)
			if err != nil || !reflect.DeepEqual(stored, created) {
				t.Fatalf("failed replacement changed record: %#v, %v", stored, err)
			}
		})
	}
}

// Create/replace must resolve even unselected destinations before writes; availability is not validity.
func TestServiceNotificationReferencesPermitUnavailableButRejectMissingBeforeWrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := &delayDefinitionWrites{
		Repository: automationssqlite.NewAutomationRepository(openAutomationDatabase(t), runtimeTestDependencies()),
	}
	stub := &definitionNotifications{destinations: map[notifications.DestinationID]notifications.Destination{
		notificationDestination: {ID: notificationDestination, Name: "Offline destination", Available: false},
	}}
	dependencies := runtimeTestDependencies()
	dependencies.Notifications = stub
	service := automations.NewService(repository, newScriptedDevices(), dependencies)
	definition := branchingFixture(t)
	definition.Steps = []automations.Step{notificationNode("notify")}
	created, err := service.CreateAutomation(ctx, definition)
	if err != nil {
		t.Fatalf("known unavailable destination rejected: %v", err)
	}
	definition.Name = "Replacement"
	replaced, err := service.ReplaceAutomation(ctx, created.ID, created.Revision, definition)
	if err != nil {
		t.Fatalf("known unavailable replacement rejected: %v", err)
	}
	repository.creates, repository.replacements = 0, 0
	missing := notifications.DestinationID("ndst_01980000-0000-7000-8000-000000000002")
	node := notificationNode("missing")
	body := node.Body.(automations.NotificationStep)
	body.DestinationID = missing
	node.Body = body
	definition.Steps = []automations.Step{{ID: "route", Body: automations.IfStep{
		Conditions: triggerPredicate(
			"a",
		),
		Then: []automations.Step{notificationNode("known")},
		Else: []automations.Step{node},
	}}}
	if _, err = service.CreateAutomation(
		ctx,
		definition,
	); !errors.Is(err, automations.ErrInvalidAutomation) ||
		!errors.Is(err, notifications.ErrDestinationNotFound) {
		t.Fatalf("missing create reference = %v", err)
	}
	if _, err = service.ReplaceAutomation(
		ctx,
		replaced.ID,
		replaced.Revision,
		definition,
	); !errors.Is(err, automations.ErrInvalidAutomation) ||
		!errors.Is(err, notifications.ErrDestinationNotFound) {
		t.Fatalf("missing replace reference = %v", err)
	}
	if repository.creates != 0 || repository.replacements != 0 {
		t.Fatal("missing references reached persistence")
	}
	stored, err := service.GetAutomation(ctx, replaced.ID)
	if err != nil || !reflect.DeepEqual(stored, replaced) {
		t.Fatalf("failed replacement changed record: %#v, %v", stored, err)
	}
	dependencies.Notifications = nil
	unconfigured := automations.NewService(repository, newScriptedDevices(), dependencies)
	definition.Steps = []automations.Step{notificationNode("notify")}
	if _, err = unconfigured.CreateAutomation(
		ctx,
		definition,
	); err == nil ||
		errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("unconfigured destination validation = %v", err)
	}
}
