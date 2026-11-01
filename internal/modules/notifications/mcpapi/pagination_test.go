package mcpapi_test

import (
	"encoding/json"
	"testing"
)

func TestListLimitAdvertisesIntegerBounds(t *testing.T) {
	t.Parallel()
	client := inputMCP(t, &inputRecorder{})
	result, err := client.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, tool := range result.Tools {
		if tool.Name != "list_notifications" && tool.Name != "list_notification_destinations" {
			continue
		}
		raw, marshalErr := json.Marshal(tool.InputSchema)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		var schema struct {
			Properties map[string]struct {
				Type    string `json:"type"`
				Minimum *int   `json:"minimum"`
				Maximum *int   `json:"maximum"`
			} `json:"properties"`
		}
		if unmarshalErr := json.Unmarshal(raw, &schema); unmarshalErr != nil {
			t.Fatal(unmarshalErr)
		}
		limit := schema.Properties["limit"]
		if limit.Type != "integer" || limit.Minimum == nil || *limit.Minimum != 1 ||
			limit.Maximum == nil || *limit.Maximum != 200 {
			t.Fatalf("%s limit schema = %s", tool.Name, raw)
		}
		seen++
	}
	if seen != 2 {
		t.Fatalf("saw %d list schemas, want 2", seen)
	}
}

func TestListLimitExactIntegerSpellings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		raw  string
		want bool
	}{
		{"1.0", true}, {"1e0", true}, {"1.0000000000000001", false},
	} {
		t.Run(test.raw, func(t *testing.T) {
			t.Parallel()
			recorder := &inputRecorder{}
			client := inputMCP(t, recorder)
			result, fields := call(t, client, "list_notifications", map[string]any{"limit": json.Number(test.raw)})
			if test.want {
				if result.IsError || len(recorder.lists) != 1 || recorder.lists[0].Limit != 1 {
					t.Fatalf("result = %#v, queries = %#v", result, recorder.lists)
				}
			} else if !result.IsError || fields["failure_code"] != "invalid_input" || len(recorder.lists) != 0 {
				t.Fatalf("result = %#v, queries = %#v", result, recorder.lists)
			}
		})
	}
}
