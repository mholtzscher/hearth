package nats //nolint:testpackage // Shared fixtures support package-private module tests.

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

func adapterServiceForTest(database *sql.DB) *adapters.Service {
	repository := adapterssqlite.NewAdapterRepository(database)
	return adapters.NewService(adapterssqlite.AdapterStores(repository), adapters.Dependencies{})
}
func testDeviceStores(repository *devicessqlite.DeviceRepository, database *sql.DB) devices.Stores {
	stores := devicessqlite.DeviceStores(repository)
	stores.AdapterReads = adapterServiceForTest(database)
	return stores
}

type commandSessionFixture struct {
	test     *testing.T
	runtimes *adapters.Service
}

func (fixture commandSessionFixture) claimedRuntimeID() adapters.RuntimeID {
	fixture.test.Helper()
	instance, err := fixture.runtimes.GetAdapter(fixture.test.Context(), "simulator")
	if err != nil {
		fixture.test.Fatal(err)
	}
	if instance.Health.Runtime == nil {
		fixture.test.Fatal("missing claimed runtime")
	}
	return instance.Health.Runtime.ID
}

func startAdapterSessionServer(
	t *testing.T,
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
) commandSessionFixture {
	t.Helper()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	runtimes := adapterServiceForTest(database)
	sessions, err := adaptersnats.StartSessionServer(connection, validator, runtimes, runtimes, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sessions.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return commandSessionFixture{t, runtimes}
}
func requestDeviceRequest[Req, Resp any](
	t *testing.T,
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	requestSchema string,
	responseSchema string,
	requestID string,
	subjectFor func(string) (string, error),
	data Req,
) natswire.Envelope[Resp] {
	t.Helper()
	subject, err := subjectFor("simulator")
	if err != nil {
		t.Fatal(err)
	}
	request := natswire.Envelope[Req]{
		ID: requestID, Schema: requestSchema, EmittedAt: "2026-08-29T15:00:00Z",
		CorrelationID: testCorrelationID, Data: data,
	}
	payload, err := natswire.Encode(validator, requestSchema, request)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := connection.RequestMsgWithContext(t.Context(), &natsgo.Msg{
		Subject: subject, Header: make(natsgo.Header), Data: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := natswire.Decode[Resp](validator, responseSchema, reply.Data)
	if err != nil {
		t.Fatal(err)
	}
	if response.CausationID == nil || *response.CausationID != requestID ||
		response.CorrelationID != testCorrelationID {
		t.Fatalf("response causality = %#v", response)
	}
	return response
}
