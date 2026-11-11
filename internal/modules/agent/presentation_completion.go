package agent

import (
	"bytes"
	"encoding/json"

	"github.com/cloudwego/eino/schema"
)

const completeTurnArgument = "complete_turn"

const (
	presentViewTool       = "present_view"
	presentAutomationTool = "present_automation"
)

const visualCompletionKey = "hearth_visual_completion"

// Local completion is durable conversation evidence, not provider continuation.
func isVisualCompletion(message *schema.Message) bool {
	return message.Extra[visualCompletionKey] == true
}

func (trace *turnTrace) canCompletePresentation() bool {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return trace.completeTurn && !trace.incompleteBatch && !trace.requiresReply && len(trace.failures) == 0
}

// Only lookups, presentations, and the bundled skill may end a turn silently.
// Any other tool, including one added later, may change household state or
// attempt an external effect, so its outcome needs a reply.
func requiresOutcomeReply(name string) bool {
	switch name {
	case presentViewTool, presentAutomationTool, "skill",
		"get_adapter", "get_adapter_function", "get_adapter_function_invocation", "get_automation",
		"get_automation_history_entry", "get_command", "get_device", "get_entity", "get_notification",
		"get_notification_destination", "list_adapter_function_invocations", "list_adapter_functions",
		"list_adapter_health_history", "list_adapters", "list_automation_history", "list_automations",
		"list_commands", "list_devices", "list_entities", "list_entity_availability_history",
		"list_entity_commands", "list_entity_events", "list_entity_state_history",
		"list_notification_destinations", "list_notifications", "query_entity_state_history":
		return false
	default:
		return true
	}
}

func isPresentationTool(name string) bool {
	return name == presentViewTool || name == presentAutomationTool
}

// Only the agent's copy is extended. Closed Automation variants must admit the
// flag too; the nested household definition schema remains unchanged.
//
//nolint:goconst // JSON Schema keywords are clearer as literals.
func presentationCompletionSchema(raw []byte) ([]byte, error) {
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	flag := map[string]any{
		"type": "boolean",
		"description": "Set true on the final presentation only when the visuals fully answer this request. " +
			"Ends the turn without another model response after all calls succeed. " +
			"Omit or use false if more work, a Command outcome summary, or a text answer is needed.",
	}
	objects := []map[string]any{document}
	if variants, ok := document["oneOf"].([]any); ok {
		for _, variant := range variants {
			if object, isObject := variant.(map[string]any); isObject {
				objects = append(objects, object)
			}
		}
	}
	for _, object := range objects {
		properties, ok := object["properties"].(map[string]any)
		if !ok {
			properties = map[string]any{}
			object["properties"] = properties
		}
		properties[completeTurnArgument] = flag
	}
	return json.Marshal(document)
}
