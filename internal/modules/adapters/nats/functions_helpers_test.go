package nats_test

import (
	"encoding/json"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

func functionTestReply(message *natsgo.Msg, scenario string) ([]byte, error) {
	var request natswire.Envelope[json.RawMessage]
	if err := json.Unmarshal(message.Data, &request); err != nil {
		return nil, err
	}
	data := map[string]any{
		"invocation_id": request.ID,
		"status":        "succeeded",
		"result":        json.RawMessage(`{"n":9007199254740993}`),
	}
	response := map[string]any{
		"id":     "rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"schema": contractsv1.AdapterFunctionInvokeResponseSchemaID,
		"emitted_at": time.Now().
			UTC().
			Format(time.RFC3339Nano),
		"correlation_id": request.CorrelationID,
		"causation_id":   request.ID,
		"data":           data,
	}
	switch scenario {
	case "failed", "unknown", "not_executed":
		data["status"] = scenario
		data["reason_code"] = "adapter_busy"
		delete(data, "result")
	case "invalid_schema":
		data["result"] = json.RawMessage(`{"n":"not a number"}`)
	case "wrong_correlation":
		response["correlation_id"] = "cor_01890f47-7a6b-7c4d-8e9f-0123456789ac"
	case "wrong_causation":
		response["causation_id"] = "afi_01890f47-7a6b-7c4d-8e9f-0123456789ac"
	case "wrong_identity":
		data["invocation_id"] = "afi_01890f47-7a6b-7c4d-8e9f-0123456789ac"
	}
	return json.Marshal(response)
}
