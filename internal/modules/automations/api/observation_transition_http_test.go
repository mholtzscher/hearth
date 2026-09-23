package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/api"
)

// HTTP create/get/replace must preserve previous_comparisons using the
// canonical value_pointer field. This fails if strict request decoding drops or
// rejects the new field, if persistence loses it, or if a response leaks the
// deprecated pointer alias instead of the public wire contract.
func TestAutomationAPIObservationPreviousComparisonsRoundTrip(t *testing.T) {
	t.Parallel()
	router, _, _ := newAutomationHTTP(t, newAPIDevices())
	createDocument := observationTransitionDefinition(t, 20)
	createdResponse := performJSON(router, http.MethodPost, "/v1/automations", createDocument)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", createdResponse.Code, createdResponse.Body.String())
	}
	created := decodeAutomation(t, createdResponse)
	assertPreviousComparisonOperand(t, created.Definition.Triggers[0].PreviousComparisons, "20")

	getResponse := performJSON(router, http.MethodGet, "/v1/automations/"+created.ID, "")
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status = %d: %s", getResponse.Code, getResponse.Body.String())
	}
	assertPreviousComparisonOperand(
		t, decodeAutomation(t, getResponse).Definition.Triggers[0].PreviousComparisons, "20",
	)

	replacement := observationTransitionDefinition(t, 22)
	response := performJSON(router, http.MethodPut, "/v1/automations/"+created.ID,
		fmt.Sprintf(`{"expected_revision":%d,"definition":%s}`, created.Revision, replacement))
	if response.Code != http.StatusOK {
		t.Fatalf("replace status = %d: %s", response.Code, response.Body.String())
	}
	updated := decodeAutomation(t, response)
	if updated.Revision != created.Revision+1 {
		t.Fatalf("replacement revision = %d, want %d", updated.Revision, created.Revision+1)
	}
	assertPreviousComparisonOperand(t, updated.Definition.Triggers[0].PreviousComparisons, "22")

	// Inspect the actual JSON response as well as typed decoding: only the
	// canonical wire member should be emitted.
	var decoded struct {
		Definition struct {
			Triggers []struct {
				Previous []map[string]json.RawMessage `json:"previous_comparisons"`
			} `json:"triggers"`
		} `json:"definition"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	comparison := decoded.Definition.Triggers[0].Previous[0]
	if string(comparison["value_pointer"]) != `"/temperature"` {
		t.Fatalf("canonical pointer = %s, want /temperature", comparison["value_pointer"])
	}
	if _, present := comparison["pointer"]; present {
		t.Fatalf("response emitted deprecated pointer alias: %s", response.Body.String())
	}
}

func observationTransitionDefinition(t *testing.T, previousThreshold int) string {
	t.Helper()
	document := definitionDocument(t, 1)
	return strings.Replace(document, `"comparisons":[`, fmt.Sprintf(
		`"previous_comparisons":[{"value_pointer":"/temperature","operator":"lte","operand":%d}],"comparisons":[`,
		previousThreshold,
	), 1)
}

func assertPreviousComparisonOperand(
	t *testing.T,
	comparisons []automationsapi.AutomationComparisonBody,
	want string,
) {
	t.Helper()
	if len(comparisons) != 1 {
		t.Fatalf("previous comparisons = %#v, want one", comparisons)
	}
	comparison := comparisons[0]
	if comparison.Pointer != "/temperature" || comparison.Operator != "lte" || string(comparison.Operand) != want {
		t.Fatalf("previous comparison = %#v, want pointer /temperature, lte, operand %s", comparison, want)
	}
}
