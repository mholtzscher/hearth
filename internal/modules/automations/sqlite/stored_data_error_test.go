package sqlite //nolint:testpackage // Verifies private decoding causes and their public classification.

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

func TestStoredValidationCausesStayPrivate(t *testing.T) {
	t.Parallel()
	_, definitionErr := automationRecordFromRow(dbsqlc.AutomationDefinition{
		ID: "aut_01890f47-7a6b-7c4d-8e9f-0123456789ab", Revision: 1,
		DefinitionJson: `{"name":"private","enabled":true,"triggers":[],"steps":[]}`,
	})
	_, factErr := factFromRow(historySummaryRow{
		ID: "private", FactID: sql.NullString{String: "private", Valid: true},
		FactFamily:      sql.NullString{String: "observation", Valid: true},
		FactEntityID:    sql.NullString{String: "private", Valid: true},
		FactVariant:     sql.NullString{String: "applied", Valid: true},
		FactCausationID: sql.NullString{String: "private", Valid: true},
		FactEmittedAt:   sql.NullString{String: "2026-09-22T00:00:00.000000000Z", Valid: true},
		FactValueJson:   sql.NullString{String: "true", Valid: true},
	})
	for _, test := range []struct {
		name            string
		err, causeClass error
	}{
		{"definition", definitionErr, automations.ErrInvalidAutomation},
		{"Fact", factErr, automations.ErrInvalidDeviceFact},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			wrapped := fmt.Errorf("read history: %w", test.err)
			stored, ok := errors.AsType[*storedDataError](wrapped)
			if !ok || !errors.Is(stored.Cause(), test.causeClass) {
				t.Fatalf("lost private validation cause: %v", wrapped)
			}
			var definition *automations.DefinitionError
			if errors.Is(wrapped, automations.ErrInvalidAutomation) ||
				errors.Is(wrapped, automations.ErrInvalidDeviceFact) || errors.As(wrapped, &definition) {
				t.Fatalf("stored failure acquired caller/wire classification: %v", wrapped)
			}
			if strings.Contains(wrapped.Error(), "private") {
				t.Fatalf("stored cause leaked into error text: %v", wrapped)
			}
		})
	}
}
