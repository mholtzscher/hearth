package nats //nolint:testpackage // Controls response receipt time at the actual wire-classification boundary.

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestLateFunctionReplyCanProveNonExecutionButNotTimelyEffects(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	output, err := functionschema.Compile(json.RawMessage(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	invocation := adapters.Invocation{
		ID:        "afi_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		StartedAt: start,
		Deadline:  start.Add(time.Second),
	}
	invoker := &FunctionInvoker{validator: validator}
	for _, scenario := range []struct {
		status string
		want   adapters.Evidence
	}{
		{"not_executed", adapters.NotExecuted{ReasonCode: "adapter_busy"}},
		{"failed", adapters.Unknown{ReasonCode: "outcome_timeout"}},
		{"succeeded", adapters.Unknown{ReasonCode: "outcome_timeout"}},
	} {
		t.Run(scenario.status, func(t *testing.T) {
			t.Parallel()
			data := functionInvokeResponse{
				InvocationID: string(invocation.ID),
				Status:       scenario.status,
				ReasonCode:   "adapter_busy",
			}
			if scenario.status == "succeeded" {
				data.ReasonCode = ""
				data.Result = json.RawMessage(`{}`)
			}
			id := string(invocation.ID)
			payload, encodeErr := natswire.Encode(
				validator,
				contractsv1.AdapterFunctionInvokeResponseSchemaID,
				natswire.Envelope[functionInvokeResponse]{
					ID:     "rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
					Schema: contractsv1.AdapterFunctionInvokeResponseSchemaID,
					EmittedAt: start.Format(
						time.RFC3339Nano,
					),
					CorrelationID: testCorrelationID,
					CausationID:   &id,
					Data:          data,
				},
			)
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			evidence := invoker.responseEvidence(
				invocation,
				output,
				testCorrelationID,
				payload,
				invocation.Deadline.Add(time.Millisecond),
			)
			if !reflect.DeepEqual(evidence, scenario.want) {
				t.Fatalf("late evidence=%+v want=%+v", evidence, scenario.want)
			}
		})
	}
}
