//go:build integration

package hearthd //nolint:testpackage // Shared fixtures support package-private module tests.

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
)

func startFixtureHealthDelivery(
	t *testing.T,
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	service *devices.Service,
	database *sql.DB,
) func() {
	t.Helper()
	runtimeRepository := adapterssqlite.NewAdapterRepository(database)
	ctx := context.Background()
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	if err = adaptersnats.ProvisionHealthStream(ctx, js); err != nil {
		t.Fatal(err)
	}
	durable, err := devicesnats.ProvisionAdapterHealthConsumer(ctx, js, adaptersnats.HealthStreamName)
	if err != nil {
		t.Fatal(err)
	}
	if err = runtimeRepository.QueueCurrentHealthSnapshots(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	consumer, err := devicesnats.StartAdapterHealthConsumer(ctx, durable, validator, service, nil)
	if err != nil {
		t.Fatal(err)
	}
	relay, err := adaptersnats.StartHealthRelay(js, runtimeRepository, validator, nil)
	if err != nil {
		t.Fatal(err)
	}
	var closeOnce sync.Once
	closeDelivery := func() {
		closeOnce.Do(func() {
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if drainErr := relay.Drain(shutdown); drainErr != nil {
				t.Error(drainErr)
				_ = relay.Stop(context.Background())
			}
			if drainErr := consumer.Drain(shutdown); drainErr != nil {
				t.Error(drainErr)
				consumer.Stop()
				_ = consumer.Drain(context.Background())
			}
		})
	}
	t.Cleanup(closeDelivery)
	return closeDelivery
}
