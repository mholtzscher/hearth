package nats //nolint:testpackage // Tests the real durable callback and acknowledgement boundary.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type healthApplierFunc func(context.Context, devices.AdapterHealthUpdate) (devices.HealthProjectionResult, error)

func (f healthApplierFunc) ApplyAdapterHealth(
	ctx context.Context,
	update devices.AdapterHealthUpdate,
) (devices.HealthProjectionResult, error) {
	return f(ctx, update)
}

func healthConsumerMessage(idSuffix string) *natsgo.Msg {
	id := "ahu_01890f47-7a6b-7c4d-8e9f-0123456789" + idSuffix
	message := natsgo.NewMsg("hearth.v1.core.adapter-health.zigbee-main")
	message.Header.Set(natsgo.MsgIdHdr, id)
	message.Data = []byte(
		`{"id":"` + id + `","schema":"urn:hearth:schema:adapter-health-update:v1","emitted_at":"2026-10-18T12:00:00Z","correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab","data":{"adapter_id":"zigbee-main","runtime_id":null,"revision":9223372036854775807,"status":"unknown","source":"core","reason_code":"hearth.starting","changed_at":"2026-10-18T12:00:00Z","evidence_at":"2026-10-18T12:00:00Z"}}`,
	)
	return message
}

//nolint:gocognit // Keep the blocked callback, broker evidence, and joined shutdown in one lifecycle proof.
func TestAdapterHealthConsumerWaitsForCommitAndJoinsCallback(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	createAdapterHealthTestStream(t, js)
	consumer, err := ProvisionAdapterHealthConsumer(context.Background(), js, "HEARTH_ADAPTER_HEALTH_V1")
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan devices.AdapterHealthUpdate, 1)
	release := make(chan struct{})
	running, err := StartAdapterHealthConsumer(
		context.Background(),
		consumer,
		validator,
		healthApplierFunc(
			func(_ context.Context, update devices.AdapterHealthUpdate) (devices.HealthProjectionResult, error) {
				entered <- update
				<-release
				return devices.HealthProjectionResult{Disposition: devices.HealthProjectionApplied}, nil
			},
		),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		running.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = running.Drain(ctx)
	}()
	if _, publishErr := js.PublishMsg(context.Background(), healthConsumerMessage("ab")); publishErr != nil {
		close(release)
		t.Fatal(publishErr)
	}
	select {
	case update := <-entered:
		if update.Snapshot.Revision != 9223372036854775807 || update.Snapshot.AdapterID != "zigbee-main" ||
			update.Snapshot.RuntimeID != nil {
			close(release)
			t.Fatalf("mapping lost snapshot: %+v", update)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("callback not entered")
	}
	info, err := consumer.Info(context.Background())
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	if info.NumAckPending != 1 || info.AckFloor.Stream != 0 {
		close(release)
		t.Fatalf("acknowledged before commit: %+v", info)
	}
	deadline, cancel := context.WithCancel(context.Background())
	cancel()
	if drainErr := running.Drain(deadline); !errors.Is(drainErr, context.Canceled) {
		close(release)
		t.Fatalf("blocked drain = %v", drainErr)
	}
	select {
	case <-running.Closed():
		close(release)
		t.Fatal("reported joined before callback exited")
	default:
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if drainErr := running.Drain(ctx); drainErr != nil {
		t.Fatal(drainErr)
	}
	// Flush the asynchronous Ack to the server before checking the broker state.
	for {
		currentInfo, infoErr := consumer.Info(ctx)
		if infoErr != nil {
			t.Fatal(infoErr)
		}
		if currentInfo.NumAckPending == 0 && currentInfo.AckFloor.Stream == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("commit was not acknowledged")
		case <-time.After(time.Millisecond):
		}
	}
}

//nolint:gocognit // Each case exercises rejection, the real Term advisory, and subsequent durable progress together.
func TestAdapterHealthConsumerTerminatesPermanentFailuresAndContinues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		mutate      func(*natsgo.Msg)
		domainError error
	}{
		{name: "schema", mutate: func(m *natsgo.Msg) { m.Data = []byte(`{}`) }},
		{name: "route", mutate: func(m *natsgo.Msg) { m.Subject = "hearth.v1.core.adapter-health.other" }},
		{name: "header identity", mutate: func(m *natsgo.Msg) { m.Header.Set(natsgo.MsgIdHdr, "different") }},
		{name: "trace", mutate: func(m *natsgo.Msg) { m.Header.Set("traceparent", "broken") }},
		{name: "noncanonical trace header", mutate: func(m *natsgo.Msg) { m.Header.Set("Traceparent", "broken") }},
		{name: "ambiguous identity", mutate: func(m *natsgo.Msg) { m.Header.Add(natsgo.MsgIdHdr, "different") }},
		{name: "size", mutate: func(m *natsgo.Msg) { m.Header.Set("padding", strings.Repeat("x", 16384)) }},
		{name: "revision conflict", domainError: devices.ErrHealthRevisionConflict},
		{name: "corrupt evidence", domainError: devices.ErrCorruptHealthProjection},
		{name: "invalid domain update", domainError: devices.ErrInvalidAdapterHealthUpdate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, conn, js := startJetStream(t)
			terminated, err := conn.SubscribeSync("$JS.EVENT.ADVISORY.CONSUMER.MSG_TERMINATED.>")
			if err != nil {
				t.Fatal(err)
			}
			if flushErr := conn.Flush(); flushErr != nil {
				t.Fatal(flushErr)
			}
			createAdapterHealthTestStream(t, js)
			consumer, err := ProvisionAdapterHealthConsumer(context.Background(), js, "HEARTH_ADAPTER_HEALTH_V1")
			if err != nil {
				t.Fatal(err)
			}
			validator, err := contractsv1.Compile()
			if err != nil {
				t.Fatal(err)
			}
			calls := make(chan string, 4)
			running, err := StartAdapterHealthConsumer(
				context.Background(),
				consumer,
				validator,
				healthApplierFunc(
					func(_ context.Context, update devices.AdapterHealthUpdate) (devices.HealthProjectionResult, error) {
						calls <- update.MessageID
						if strings.HasSuffix(update.MessageID, "ab") {
							return devices.HealthProjectionResult{}, tc.domainError
						}
						return devices.HealthProjectionResult{Disposition: devices.HealthProjectionDuplicate}, nil
					},
				),
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				running.Stop()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = running.Drain(ctx)
			}()
			bad, good := healthConsumerMessage("ab"), healthConsumerMessage("ac")
			if tc.mutate != nil {
				tc.mutate(bad)
			}
			for _, message := range []*natsgo.Msg{bad, good} {
				if _, publishErr := js.PublishMsg(context.Background(), message); publishErr != nil {
					t.Fatal(publishErr)
				}
			}
			expectedCalls := 1
			if tc.domainError != nil {
				expectedCalls = 2
			}
			for i := range expectedCalls {
				select {
				case id := <-calls:
					if i == expectedCalls-1 && id != good.Header.Get(natsgo.MsgIdHdr) {
						t.Fatalf("invalid input reached domain or blocked following message: %s", id)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("permanent failure held the only pending slot")
				}
			}
			advisory, err := terminated.NextMsg(5 * time.Second)
			if err != nil {
				t.Fatalf("permanent input was not terminated: %v", err)
			}
			if !strings.Contains(string(advisory.Data), `"stream_seq":1`) {
				t.Fatalf("wrong terminated message: %s", advisory.Data)
			}
		})
	}
}

func TestAdapterHealthConsumerDelayedRedeliveryAfterTransientFailure(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	createAdapterHealthTestStream(t, js)
	consumer, err := ProvisionAdapterHealthConsumer(context.Background(), js, "HEARTH_ADAPTER_HEALTH_V1")
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	calls := make(chan time.Time, 2)
	attempts := 0
	running, err := StartAdapterHealthConsumer(
		context.Background(),
		consumer,
		validator,
		healthApplierFunc(
			func(_ context.Context, _ devices.AdapterHealthUpdate) (devices.HealthProjectionResult, error) {
				attempts++
				calls <- time.Now()
				if attempts == 1 {
					return devices.HealthProjectionResult{}, errors.New("commit failed")
				}
				return devices.HealthProjectionResult{Disposition: devices.HealthProjectionApplied}, nil
			},
		),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		running.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = running.Drain(ctx)
	}()
	if _, publishErr := js.PublishMsg(context.Background(), healthConsumerMessage("ab")); publishErr != nil {
		t.Fatal(publishErr)
	}
	var times [2]time.Time
	for i := range 2 {
		select {
		case times[i] = <-calls:
		case <-time.After(5 * time.Second):
			t.Fatal("failed transaction did not redeliver")
		}
	}
	if times[1].Sub(times[0]) < time.Second {
		t.Fatalf("transient failure hot-looped: %v", times[1].Sub(times[0]))
	}
}
