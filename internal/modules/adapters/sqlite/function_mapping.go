package sqlite

import (
	"bytes"
	"encoding/json"
	"io"
	"time"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/adapters/sqlite/dbsqlc"
)

func decodeFunctionJSON(raw string, target any) error {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return adapters.ErrFunctionPersistence
	}
	return nil
}

func functionTimestamp(at time.Time) string { return at.UTC().Format("2006-01-02T15:04:05.000000000Z") }

// Retained rows are a separate boundary. Compile each descriptor once and reuse
// its output validator for stored and proposed terminal evidence.
func mapFunctionInvocation(row dbsqlc.AdapterFunctionInvocation) (adapters.Invocation, *functionschema.Schema, error) {
	id, idErr := adapters.ParseInvocationID(row.ID)
	revision, revisionErr := adapters.ParseCatalogID(row.CatalogRevision)
	runtime, runtimeErr := adapters.ParseRuntimeID(row.RuntimeID)
	if idErr != nil || revisionErr != nil || runtimeErr != nil || adapters.ValidateAdapterID(row.AdapterID) != nil {
		return adapters.Invocation{}, nil, adapters.ErrFunctionPersistence
	}
	var descriptor adapters.Descriptor
	if decodeFunctionJSON(row.DescriptorJson, &descriptor) != nil || string(descriptor.Key) != row.FunctionKey {
		return adapters.Invocation{}, nil, adapters.ErrFunctionPersistence
	}
	descriptor, input, output, err := adapters.PrepareDescriptor(descriptor)
	if err != nil {
		return adapters.Invocation{}, nil, adapters.ErrFunctionPersistence
	}
	arguments, err := input.Normalize(json.RawMessage(row.ArgumentsJson))
	if err != nil {
		return adapters.Invocation{}, nil, adapters.ErrFunctionPersistence
	}
	start, startErr := time.Parse(time.RFC3339Nano, row.StartedAt)
	deadline, deadlineErr := time.Parse(time.RFC3339Nano, row.Deadline)
	if startErr != nil || deadlineErr != nil || !adapters.ValidFunctionTime(start) ||
		!adapters.ValidFunctionTime(deadline) ||
		!deadline.Equal(start.Add(time.Duration(descriptor.TimeoutMS)*time.Millisecond)) {
		return adapters.Invocation{}, nil, adapters.ErrFunctionPersistence
	}
	invocation := adapters.Invocation{
		ID:              id,
		CatalogRevision: revision,
		Function:        descriptor,
		Arguments:       arguments,
		Route: adapters.RouteSnapshot{
			AdapterID: row.AdapterID,
			RuntimeID: runtime,
		},
		StartedAt: start,
		Deadline:  deadline,
	}
	if row.Status == "running" {
		if row.CompletedAt.Valid || row.ResultJson.Valid || row.ReasonCode.Valid {
			return adapters.Invocation{}, nil, adapters.ErrFunctionPersistence
		}
		return invocation, output, nil
	}
	at, err := time.Parse(time.RFC3339Nano, row.CompletedAt.String)
	if err != nil {
		return adapters.Invocation{}, nil, adapters.ErrFunctionPersistence
	}
	evidence, err := functionRowEvidence(row)
	if err != nil {
		return adapters.Invocation{}, nil, err
	}
	evidence, err = adapters.PrepareFunctionEvidence(evidence, output, deadline, at)
	if err != nil {
		return adapters.Invocation{}, nil, adapters.ErrFunctionPersistence
	}
	invocation.Completion = &adapters.Completion{At: at, Evidence: evidence}
	return invocation, output, nil
}

func functionRowEvidence(row dbsqlc.AdapterFunctionInvocation) (adapters.Evidence, error) {
	if row.Status == "succeeded" {
		if !row.ResultJson.Valid || row.ReasonCode.Valid {
			return nil, adapters.ErrFunctionPersistence
		}
		return adapters.Succeeded{Result: json.RawMessage(row.ResultJson.String)}, nil
	}
	if row.ResultJson.Valid || !row.ReasonCode.Valid {
		return nil, adapters.ErrFunctionPersistence
	}
	switch row.Status {
	case "failed":
		return adapters.Failed{ReasonCode: row.ReasonCode.String}, nil
	case "not_executed":
		return adapters.NotExecuted{ReasonCode: row.ReasonCode.String}, nil
	case "unknown":
		return adapters.Unknown{ReasonCode: row.ReasonCode.String}, nil
	default:
		return nil, adapters.ErrFunctionPersistence
	}
}
