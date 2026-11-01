package dashboards

import (
	"context"
	"encoding/json"
	"testing"

	viewsv1 "github.com/mholtzscher/hearth/contracts/views/v1"
)

func TestSharedExamples(t *testing.T) {
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
			d, err := ParseDefinition(example.Definition)
			if (err == nil) != example.Valid {
				t.Fatalf("valid=%v err=%v", example.Valid, err)
			}
			if example.Valid {
				if _, err := NewService(nil, nil).PresentView(context.Background(), d); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ReadDefinition(example.Definition); err != nil {
				t.Fatalf("recoverable: %v", err)
			}
		})
	}
}
