package sqlite //nolint:testpackage // Pin private cause access separately from public error traversal.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestTransactionRollbackErrorKeepsPrimaryClassesPrivate(t *testing.T) {
	t.Parallel()
	for _, primary := range []error{
		fmt.Errorf("SENSITIVE-conflict: %w", automations.ErrRevisionConflict),
		&automations.DefinitionError{Issues: []automations.DefinitionIssue{{Path: "/steps", Message: "SENSITIVE-definition"}}},
		&automations.FunctionReferenceError{StepID: "private", ReasonCode: "SENSITIVE-function"},
		context.Canceled,
		context.DeadlineExceeded,
	} {
		t.Run(fmt.Sprintf("%T/%v", primary, primary), func(t *testing.T) {
			t.Parallel()
			cleanup := errors.New("SENSITIVE-cleanup")
			failure := &transactionRollbackError{primary: primary, rollback: cleanup}
			wrapped := fmt.Errorf("write operation: %w", failure)
			retained, ok := errors.AsType[interface {
				error
				Cause() error
			}](wrapped)
			if !ok || retained.Cause() != primary { //nolint:errorlint // Require the exact retained value.
				t.Fatal("diagnostic owner cannot inspect original cause")
			}
			if failure.Error() != "automation transaction rollback failed" || !errors.Is(wrapped, cleanup) {
				t.Fatal("cleanup text or classification changed")
			}
			assertPrimaryClassesPrivate(t, wrapped, primary)
		})
	}
}

func assertPrimaryClassesPrivate(t *testing.T, wrapped, primary error) {
	t.Helper()
	for _, class := range []error{primary, automations.ErrRevisionConflict, automations.ErrInvalidAutomation, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(wrapped, class) {
			t.Fatalf("primary class exposed: %T", class)
		}
	}
	if _, ok := errors.AsType[*automations.DefinitionError](wrapped); ok {
		t.Fatal("primary definition details exposed")
	}
	if _, ok := errors.AsType[*automations.FunctionReferenceError](wrapped); ok {
		t.Fatal("primary Function details exposed")
	}
}
