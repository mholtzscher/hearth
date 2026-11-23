package agenteval

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// syntheticTokens is the usage each scripted response reports.
const syntheticTokens = 1

// ScriptedModel replays a scenario's reference trajectory in place of a
// provider. The self-test uses it to prove every scenario's checks can pass
// through real Core without hosted spend.
type ScriptedModel struct {
	mu        sync.Mutex
	steps     []ScriptStep
	next      int
	inventory Inventory
}

// NewScriptedModel replays steps in order.
func NewScriptedModel(steps []ScriptStep) *ScriptedModel {
	return &ScriptedModel{steps: steps}
}

// SetInventory supplies the IDs placeholders resolve to; RunTrial calls it
// once the household is seeded.
func (scripted *ScriptedModel) SetInventory(inventory Inventory) {
	scripted.mu.Lock()
	defer scripted.mu.Unlock()
	scripted.inventory = inventory
}

// WithTools ignores tool binding: the script already names its calls.
func (scripted *ScriptedModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return scripted, nil
}

// Generate returns the next step.
func (scripted *ScriptedModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	scripted.mu.Lock()
	defer scripted.mu.Unlock()
	if scripted.next >= len(scripted.steps) {
		return nil, errors.New("scripted model: reference trajectory exhausted")
	}
	step := scripted.steps[scripted.next]
	scripted.next++
	var message *schema.Message
	if step.Reply != nil {
		text, err := Resolve(*step.Reply, scripted.inventory)
		if err != nil {
			return nil, err
		}
		message = schema.AssistantMessage(text, nil)
	} else {
		arguments, err := Resolve(step.Arguments, scripted.inventory)
		if err != nil {
			return nil, err
		}
		message = schema.AssistantMessage("", []schema.ToolCall{{
			ID: fmt.Sprintf("reference-%d", scripted.next), Type: "function",
			Function: schema.FunctionCall{Name: step.Call, Arguments: arguments},
		}})
	}
	// Synthetic usage keeps the recorder's fail-closed usage rule meaningful.
	message.ResponseMeta = &schema.ResponseMeta{
		Usage: &schema.TokenUsage{
			PromptTokens:     syntheticTokens,
			CompletionTokens: syntheticTokens,
			TotalTokens:      syntheticTokens + syntheticTokens,
		},
	}
	return message, nil
}

// Stream is unused: the Agent generates without streaming.
func (scripted *ScriptedModel) Stream(
	context.Context, []*schema.Message, ...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("scripted model: streaming is not supported")
}
