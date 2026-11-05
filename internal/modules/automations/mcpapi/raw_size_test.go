package mcpapi_test

import (
	"strings"
	"testing"

	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/mcpapi"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestAutomationMCPAcceptsLargeOuterWhitespace(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"create_automation", "replace_automation"} {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			recorder := newRecordingAutomations()
			server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
			automationsapi.Register(server, recorder)
			arguments := `{"definition":` + definitionDocument(t, 1)
			if tool == "replace_automation" {
				arguments += `,"automation_id":"` + createdIDForInput(t) + `","expected_revision":1`
			}
			arguments += strings.Repeat(" ", 192*1024) + `}`
			result := callAutomationWireTool(t, server, tool, arguments)
			if result.IsError || recorder.callCount != 1 {
				t.Fatalf("padded arguments rejected: %s calls=%d", result.Structured, recorder.callCount)
			}
		})
	}
}

func TestAutomationMCPCanonicalRawSizeSurvivesBinding(t *testing.T) {
	t.Parallel()
	const canonicalLimit = 128 * 1024
	for _, tool := range []string{"create_automation", "replace_automation"} {
		for _, test := range []struct {
			name      string
			size      int
			wantError bool
			wantCalls int
		}{
			{name: "at limit", size: canonicalLimit, wantCalls: 1},
			{name: "over limit", size: canonicalLimit + 1, wantError: true, wantCalls: 0},
		} {
			t.Run(tool+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				recorder := newRecordingAutomations()
				server := mcptools.New(mcptools.Config{Name: "canonical-size", Version: "1"})
				automationsapi.Register(server, recorder)
				definition := definitionDocument(t, 1)
				definition = definition[:len(definition)-1] + strings.Repeat(
					" ",
					test.size-len(definition),
				) + `}`
				arguments := `{"definition":` + definition
				if tool == "replace_automation" {
					arguments += `,"automation_id":"` + createdIDForInput(t) + `","expected_revision":1`
				}
				result := callAutomationWireTool(t, server, tool, arguments+`}`)
				if result.IsError != test.wantError || recorder.callCount != test.wantCalls {
					t.Fatalf("size=%d result=%s calls=%d", len(definition), result.Structured, recorder.callCount)
				}
				if result.IsError &&
					!strings.Contains(string(result.Structured), `"failure_code":"invalid_automation"`) {
					t.Fatalf(
						"oversized canonical document admitted: %s calls=%d",
						result.Structured,
						recorder.callCount,
					)
				}
			})
		}
	}
}
