package dashboards_test

import (
	"context"
	"encoding/json"
	"testing"

	viewsv1 "github.com/mholtzscher/hearth/contracts/views/v1"
	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

func TestSharedExamples(t *testing.T) {
	t.Parallel()
	var examples []struct {
		Name       string          `json:"name"`
		Valid      bool            `json:"valid"`
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.Unmarshal(viewsv1.Examples, &examples); err != nil {
		t.Fatal(err)
	}
	for _, example := range examples {
		t.Run(example.Name, func(t *testing.T) {
			t.Parallel()
			d, err := dashboards.ParseDefinition(example.Definition)
			if (err == nil) != example.Valid {
				t.Fatalf("valid=%v err=%v", example.Valid, err)
			}
			if example.Valid {
				if _, presentErr := dashboards.NewService(nil, nil).
					PresentView(context.Background(), d); presentErr != nil {
					t.Fatal(presentErr)
				}
			}
			if _, readErr := dashboards.ReadDefinition(example.Definition); readErr != nil {
				t.Fatalf("recoverable: %v", readErr)
			}
		})
	}
}
