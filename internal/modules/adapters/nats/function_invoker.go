package nats

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

type functionInvokeRequest struct {
	InvocationID    string          `json:"invocation_id"`
	CatalogRevision string          `json:"catalog_revision"`
	FunctionKey     string          `json:"function_key"`
	Arguments       json.RawMessage `json:"arguments"`
	Deadline        string          `json:"deadline"`
}
type functionInvokeResponse struct {
	InvocationID string          `json:"invocation_id"`
	Status       string          `json:"status"`
	Result       json.RawMessage `json:"result,omitempty"`
	ReasonCode   string          `json:"reason_code,omitempty"`
}

type FunctionInvoker struct {
	connection *natsgo.Conn
	validator  *contractsv1.Validator
}

const functionInvalidResponse = "invalid_response"

func NewFunctionInvoker(connection *natsgo.Conn, validator *contractsv1.Validator) *FunctionInvoker {
	return &FunctionInvoker{connection: connection, validator: validator}
}

var _ adapters.FunctionInvoker = (*FunctionInvoker)(nil)

// Invoke attempts one runtime-pinned request without application retry.
// Uncertain delivery is retained as uncertain execution.
func (invoker *FunctionInvoker) Invoke(ctx context.Context, invocation adapters.Invocation) adapters.Evidence {
	if !time.Now().Before(invocation.Deadline) {
		return adapters.NotExecuted{ReasonCode: "deadline_expired"}
	}
	subject, err := natswire.FunctionInvokeSubject(invocation.Route.AdapterID, string(invocation.Route.RuntimeID))
	if err != nil || invoker.connection == nil || invoker.validator == nil {
		return adapters.Unknown{ReasonCode: "transport_error"}
	}
	if _, err = adapters.ParseInvocationID(string(invocation.ID)); err != nil {
		return adapters.Unknown{ReasonCode: functionInvalidResponse}
	}
	_, input, output, err := adapters.PrepareDescriptor(invocation.Function)
	if err != nil {
		return adapters.Unknown{ReasonCode: functionInvalidResponse}
	}
	arguments, err := input.Normalize(invocation.Arguments)
	if err != nil || !adapters.ValidFunctionTime(invocation.StartedAt) ||
		!invocation.Deadline.Equal(
			invocation.StartedAt.Add(time.Duration(invocation.Function.TimeoutMS)*time.Millisecond),
		) {
		return adapters.Unknown{ReasonCode: functionInvalidResponse}
	}
	id := string(invocation.ID)
	correlation := "cor_" + strings.TrimPrefix(id, "afi_")
	request := natswire.Envelope[functionInvokeRequest]{
		ID:            id,
		Schema:        contractsv1.AdapterFunctionInvokeRequestSchemaID,
		EmittedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		CorrelationID: correlation,
		Data: functionInvokeRequest{
			InvocationID:    id,
			CatalogRevision: string(invocation.CatalogRevision),
			FunctionKey: string(
				invocation.Function.Key,
			),
			Arguments: arguments,
			Deadline:  invocation.Deadline.UTC().Format(time.RFC3339Nano),
		},
	}
	payload, err := natswire.Encode(invoker.validator, contractsv1.AdapterFunctionInvokeRequestSchemaID, request)
	if err != nil {
		return adapters.Unknown{ReasonCode: functionInvalidResponse}
	}
	bounded, cancel := context.WithDeadline(ctx, invocation.Deadline)
	defer cancel()
	if bounded.Err() != nil || !time.Now().Before(invocation.Deadline) {
		return adapters.NotExecuted{ReasonCode: "deadline_expired"}
	}
	if !invoker.connection.IsConnected() {
		return adapters.NotExecuted{ReasonCode: "adapter_unavailable"}
	}
	message := &natsgo.Msg{Subject: subject, Header: make(natsgo.Header), Data: payload}
	natswire.InjectTrace(ctx, message.Header)
	response, err := invoker.connection.RequestMsgWithContext(bounded, message)
	if errors.Is(err, natsgo.ErrNoResponders) {
		return adapters.NotExecuted{ReasonCode: "adapter_unavailable"}
	}
	if err != nil {
		reason := "transport_error"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, natsgo.ErrTimeout) {
			reason = "outcome_timeout"
		}
		return adapters.Unknown{ReasonCode: reason}
	}
	at := time.Now().UTC()
	return invoker.responseEvidence(invocation, output, correlation, response.Data, at)
}

func (invoker *FunctionInvoker) responseEvidence(
	invocation adapters.Invocation,
	output *functionschema.Schema,
	correlation string,
	payload []byte,
	at time.Time,
) adapters.Evidence {
	id := string(invocation.ID)
	wire, err := natswire.Decode[functionInvokeResponse](
		invoker.validator,
		contractsv1.AdapterFunctionInvokeResponseSchemaID,
		payload,
	)
	if err != nil || wire.CorrelationID != correlation || wire.CausationID == nil || *wire.CausationID != id ||
		wire.Data.InvocationID != id {
		return adapters.Unknown{ReasonCode: functionInvalidResponse}
	}
	evidence := mapFunctionResponse(wire.Data)
	if _, notExecuted := evidence.(adapters.NotExecuted); !notExecuted && !at.Before(invocation.Deadline) {
		return adapters.Unknown{ReasonCode: "outcome_timeout"}
	}
	evidence, err = adapters.PrepareFunctionEvidence(evidence, output, invocation.Deadline, at)
	if err != nil {
		return adapters.Unknown{ReasonCode: functionInvalidResponse}
	}
	return evidence
}

func mapFunctionResponse(response functionInvokeResponse) adapters.Evidence {
	switch response.Status {
	case "succeeded":
		return adapters.Succeeded{Result: response.Result}
	case "failed":
		return adapters.Failed{ReasonCode: response.ReasonCode}
	case "not_executed":
		return adapters.NotExecuted{ReasonCode: response.ReasonCode}
	case "unknown":
		return adapters.Unknown{ReasonCode: response.ReasonCode}
	default:
		return adapters.Unknown{ReasonCode: functionInvalidResponse}
	}
}
