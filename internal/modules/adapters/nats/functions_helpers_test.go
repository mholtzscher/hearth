package nats_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
)

func checkFunctionWireOutcome(t *testing.T, scenario string, want adapters.Evidence) {
	t.Helper()
	fixture := newFunctionWireFixture(t)
	var calls atomic.Int32
	if scenario != "no_responders" {
		_, err := fixture.connection.Subscribe(fixture.subject, func(message *natsgo.Msg) {
			calls.Add(1)
			if scenario == "missing_reply" {
				return
			}
			if reply, replyErr := functionTestReply(message, scenario); replyErr == nil {
				_ = message.Respond(reply)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if err = fixture.connection.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	invocation, err := fixture.service.Invoke(context.Background(), fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := fixture.repository.GetInvocation(context.Background(), invocation.ID)
	if err != nil || retained.Completion == nil {
		t.Fatalf("retained=%+v error=%v", retained, err)
	}
	if !reflect.DeepEqual(retained.Completion.Evidence, want) {
		t.Fatalf("evidence=%+v want=%+v", retained.Completion.Evidence, want)
	}
	if scenario != "no_responders" && calls.Load() != 1 {
		t.Fatalf("application dispatches=%d", calls.Load())
	}
}

type functionWireFixture struct {
	connection *natsgo.Conn
	repository *adapterssqlite.AdapterRepository
	service    *adapters.FunctionService
	input      adapters.InvokeInput
	subject    string
}

func newFunctionWireFixture(t *testing.T) functionWireFixture {
	t.Helper()
	broker := natstest.StartServer(t)
	connection, err := natsgo.Connect(broker.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := adapterssqlite.NewAdapterRepository(database)
	at := time.Now().UTC()
	const runtime = adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	const revision = adapters.CatalogID("afc_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if err = repository.ClaimAdapterRuntime(
		context.Background(),
		adapters.ClaimRuntimeWrite{
			AdapterID:       "bridge",
			RuntimeID:       runtime,
			SoftwareName:    "test",
			SoftwareVersion: "1",
			ClaimedAt:       at,
			LeaseExpiresAt:  at.Add(15 * time.Second),
		},
	); err != nil {
		t.Fatal(err)
	}
	schema := json.RawMessage(
		`{"type":"object","required":["n"],"properties":{"n":{"type":"integer"}},"additionalProperties":false}`,
	)
	if err = repository.RegisterCatalog(
		context.Background(),
		adapters.Catalog{
			ID:        revision,
			AdapterID: "bridge",
			RuntimeID: runtime,
			Functions: []adapters.Descriptor{
				{Key: "inspect", Description: "Inspect", InputSchema: schema, OutputSchema: schema, TimeoutMS: 1000},
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	service, err := adapters.NewFunctionService(
		context.Background(),
		repository,
		adaptersnats.NewFunctionInvoker(connection, validator),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Drain(context.Background()) })
	subject, err := natswire.FunctionInvokeSubject("bridge", string(runtime))
	if err != nil {
		t.Fatal(err)
	}
	return functionWireFixture{
		connection: connection,
		repository: repository,
		service:    service,
		subject:    subject,
		input: adapters.InvokeInput{
			AdapterID:       "bridge",
			FunctionKey:     "inspect",
			CatalogRevision: revision,
			Arguments:       json.RawMessage(`{"n":9007199254740993}`),
		},
	}
}

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
