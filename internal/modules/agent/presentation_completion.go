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
	return message.Role == schema.Assistant && message.Content == "" && len(message.ToolCalls) == 0 &&
		message.Extra[visualCompletionKey] == true
}

func (trace *turnTrace) canCompletePresentation() bool {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return trace.completeTurn && !trace.incompleteBatch && !trace.requiresReply && len(trace.failures) == 0
}

// These catalog operations change household state or attempt an external effect.
// Their outcome needs a reply even when the model marks a later visual complete.
func requiresOutcomeReply(name string) bool {
	switch name {
	case "execute_entity_command", "invoke_adapter_function", "send_notification",
		"start_automation_run", "delete_automation", "update_entity":
		return true
	default:
		return false
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
