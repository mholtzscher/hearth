package outbox_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/platform/outbox"
)

type memorySource struct {
	mu         sync.Mutex
	rows       []int
	deleted    []int
	readErr    error
	deleteErr  error
	poison     error
	poisonAt   int
	reads      int
	readHook   func(context.Context) error
	deleteHook func(context.Context) error
}

func (source *memorySource) Read(ctx context.Context, limit int) ([]int, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.reads++
	if source.readHook != nil {
		if err := source.readHook(ctx); err != nil {
			return nil, err
		}
	}
	if source.readErr != nil {
		return nil, source.readErr
	}
	var pending []int
	for _, row := range source.rows[:min(limit, len(source.rows))] {
		if row == source.poisonAt {
			return pending, source.poison
		}
		pending = append(pending, row)
	}
	return pending, nil
}

func (source *memorySource) Prepare(row int) (*natsgo.Msg, error) {
	message := natsgo.NewMsg("facts." + strconv.Itoa(row))
	message.Header.Set(natsgo.MsgIdHdr, strconv.Itoa(row))
	message.Data = []byte("payload-" + strconv.Itoa(row))
	return message, nil
}

func (source *memorySource) Delete(ctx context.Context, row int) error {
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.deleteHook != nil {
		if err := source.deleteHook(ctx); err != nil {
			return err
		}
	}
	if source.deleteErr != nil {
		return source.deleteErr
	}
	if len(source.rows) == 0 || source.rows[0] != row {
		return errors.New("out of order delete")
	}
	source.rows = source.rows[1:]
	source.deleted = append(source.deleted, row)
	return nil
}

func (source *memorySource) snapshot() ([]int, []int, int) {
	source.mu.Lock()
	defer source.mu.Unlock()
	return slices.Clone(source.rows), slices.Clone(source.deleted), source.reads
}

func (source *memorySource) fail(readErr, deleteErr error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.readErr, source.deleteErr = readErr, deleteErr
}

func (source *memorySource) enqueue(rows ...int) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.rows = append(source.rows, rows...)
}

func relayConfig(source *memorySource) outbox.Config[int] {
	return outbox.Config[int]{
		Source: source, Stream: "FACTS", Timing: outbox.DefaultTiming(),
		IsPoison: func(err error) bool { return source.poison != nil && errors.Is(err, source.poison) },
		Report:   func(context.Context, outbox.DeliveryError[int]) {},
	}
}

type successfulPublisher struct{}

func (successfulPublisher) PublishMsg(
	context.Context,
	*natsgo.Msg,
	...jetstream.PublishOpt,
) (*jetstream.PubAck, error) {
	return &jetstream.PubAck{Stream: "FACTS"}, nil
}

type publisherFunc func(context.Context, *natsgo.Msg) (*jetstream.PubAck, error)

func (publish publisherFunc) PublishMsg(
	ctx context.Context,
	message *natsgo.Msg,
	_ ...jetstream.PublishOpt,
) (*jetstream.PubAck, error) {
	return publish(ctx, message)
}

func startRelay(t *testing.T, publisher outbox.Publisher, config outbox.Config[int]) *outbox.Relay[int] {
	t.Helper()
	relay, err := outbox.Start(publisher, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relay.Stop(context.Background()) })
	return relay
}

// Each stage must retain the oldest row, fail readiness, obey backoff despite
// hints, and recover with stable bytes rather than skipping ahead.
func TestDeliveryFailuresRetainOrderAndRecover(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"list", "publish", "missing ack", "wrong stream", "delete"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) { exerciseDeliveryFailure(t, stage) })
		})
	}
}

func exerciseDeliveryFailure(t *testing.T, stage string) {
	t.Helper()
	source := &memorySource{rows: []int{1, 2}}
	config := relayConfig(source)
	publisher := &failingPublisher{stage: stage, cause: errors.New("private upstream detail")}
	publisher.broken.Store(true)
	if stage == "list" {
		source.readErr = publisher.cause
	}
	if stage == "delete" {
		source.deleteErr = publisher.cause
	}
	relay := startRelay(t, publisher, config)
	synctest.Wait()
	rows, _, reads := source.snapshot()
	if relay.Fault() == nil || !relay.Active() || !slices.Equal(rows, []int{1, 2}) {
		t.Fatalf("failure lost work or readiness state: rows=%v fault=%v", rows, relay.Fault())
	}
	if strings.Contains(relay.Fault().Error(), "private upstream detail") {
		t.Fatal("fault exposed upstream text")
	}
	for range 100 {
		relay.Notify()
	}
	synctest.Wait()
	_, _, afterHints := source.snapshot()
	if afterHints != reads {
		t.Fatal("wake hints bypassed retry backoff")
	}
	publisher.broken.Store(false)
	source.fail(nil, nil)
	time.Sleep(time.Second)
	synctest.Wait()
	_, deleted, _ := source.snapshot()
	if relay.Fault() != nil || !slices.Equal(deleted, []int{1, 2}) {
		t.Fatalf("did not recover in order: deleted=%v fault=%v", deleted, relay.Fault())
	}
	assertStablePublications(t, publisher.messages)
}

type failingPublisher struct {
	stage    string
	cause    error
	broken   atomic.Bool
	messages []*natsgo.Msg
}

func (publisher *failingPublisher) PublishMsg(
	_ context.Context,
	msg *natsgo.Msg,
	_ ...jetstream.PublishOpt,
) (*jetstream.PubAck, error) {
	publisher.messages = append(publisher.messages, msg)
	if publisher.broken.Load() {
		switch publisher.stage {
		case "publish":
			return nil, publisher.cause
		case "missing ack":
			return nil, nil //nolint:nilnil // Missing PubAck is the protocol failure under test.
		case "wrong stream":
			return &jetstream.PubAck{Stream: "OTHER"}, nil
		}
	}
	return &jetstream.PubAck{Stream: "FACTS", Duplicate: true}, nil
}

func assertStablePublications(t *testing.T, messages []*natsgo.Msg) {
	t.Helper()
	if len(messages) < 2 {
		t.Fatalf("publications = %d, want both rows", len(messages))
	}
	for _, msg := range messages[:len(messages)-1] {
		if msg.Subject != "facts.1" || string(msg.Data) != "payload-1" || msg.Header.Get(natsgo.MsgIdHdr) != "1" {
			t.Fatalf("retry changed or skipped oldest publication: %+v", msg)
		}
	}
	if last := messages[len(messages)-1]; last.Subject != "facts.2" || string(last.Data) != "payload-2" {
		t.Fatalf("second publication = %+v", last)
	}
}

func TestRediscoveryAndWakeDeliverWithoutBacklogReadinessGate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		source := &memorySource{}
		relay := startRelay(t, successfulPublisher{}, relayConfig(source))
		synctest.Wait()
		source.enqueue(1) // No hint: poll must rediscover durable work.
		time.Sleep(5 * time.Second)
		synctest.Wait()
		_, deleted, _ := source.snapshot()
		if !slices.Equal(deleted, []int{1}) {
			t.Fatal("poll lost committed work")
		}
		source.enqueue(2)
		relay.Notify()
		synctest.Wait()
		_, deleted, _ = source.snapshot()
		if !slices.Equal(deleted, []int{1, 2}) || !relay.Active() || relay.Fault() != nil {
			t.Fatalf("wake did not publish healthy work: %v", deleted)
		}
	})
}

func TestPoisonDeliversOlderPrefixThenPreservesBlockedRows(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		poison := errors.New("corrupt stored row")
		source := &memorySource{rows: []int{1, 2, 3, 4}, poisonAt: 3, poison: poison}
		config := relayConfig(source)
		config.Timing.BatchSize = 3
		relay := startRelay(t, successfulPublisher{}, config)
		synctest.Wait()
		rows, deleted, _ := source.snapshot()
		if relay.Active() || !errors.Is(relay.Fault(), poison) || !slices.Equal(deleted, []int{1, 2}) ||
			!slices.Equal(rows, []int{3, 4}) {
			t.Fatalf(
				"poison did not preserve ordered evidence: deleted=%v rows=%v fault=%v",
				deleted,
				rows,
				relay.Fault(),
			)
		}
		if err := relay.Drain(t.Context()); !errors.Is(err, poison) {
			t.Fatalf("Drain = %v", err)
		}
	})
}

func TestDrainKeepsInFlightOwnerAndJoinsConcurrentCallers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		source := &memorySource{rows: []int{1}}
		config := relayConfig(source)
		entered := make(chan context.Context, 1)
		release := make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		attempts := 0
		publisher := publisherFunc(func(ctx context.Context, _ *natsgo.Msg) (*jetstream.PubAck, error) {
			attempts++
			entered <- ctx
			<-release
			return &jetstream.PubAck{Stream: "FACTS"}, nil
		})
		relay := startRelay(t, publisher, config)
		publication := <-entered
		results := make(chan error, 2)
		for range 2 {
			go func() { results <- relay.Drain(t.Context()) }()
		}
		synctest.Wait()
		if relay.Active() || publication.Err() != nil || attempts != 1 {
			t.Fatal("drain replaced or canceled the publisher")
		}
		unblock()
		for range 2 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		_, deleted, _ := source.snapshot()
		if attempts != 1 || !slices.Equal(deleted, []int{1}) {
			t.Fatal("drain published twice or lost work")
		}
		select {
		case <-relay.Closed():
		default:
			t.Fatal("successful drain did not join")
		}
		if err := relay.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestDrainExpiryCancelsButDoesNotClaimJoinOrRestartPublication(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		source := &memorySource{rows: []int{1}}
		config := relayConfig(source)
		entered := make(chan context.Context, 1)
		release := make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		attempts := 0
		publisher := publisherFunc(func(ctx context.Context, _ *natsgo.Msg) (*jetstream.PubAck, error) {
			attempts++
			entered <- ctx
			<-release // Deliberately ignore cancellation to distinguish it from joining.
			return nil, ctx.Err()
		})
		relay := startRelay(t, publisher, config)
		publication := <-entered
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := relay.Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Drain = %v", err)
		}
		if publication.Err() != context.Canceled || relay.Active() {
			t.Fatal("expiry did not cancel publication and readiness")
		}
		select {
		case <-relay.Closed():
			t.Fatal("deadline claimed completion of blocked I/O")
		default:
		}
		unblock()
		if err := relay.Stop(t.Context()); !errors.Is(err, context.Canceled) {
			t.Fatalf("Stop = %v", err)
		}
		if err := relay.Drain(t.Context()); !errors.Is(err, context.Canceled) {
			t.Fatalf("later Drain = %v", err)
		}
		rows, deleted, _ := source.snapshot()
		if attempts != 1 || len(deleted) != 0 || !slices.Equal(rows, []int{1}) {
			t.Fatal("timed-out drain restarted publication or discarded work")
		}
	})
}

func TestStartRejectsInvalidDependenciesAndBounds(t *testing.T) {
	t.Parallel()
	if relay, err := outbox.Start(nil, relayConfig(&memorySource{})); err == nil {
		_ = relay.Stop(t.Context())
		t.Fatal("relay accepted a nil publisher")
	}
	for name, invalidate := range map[string]func(*outbox.Config[int]){
		"source":     func(config *outbox.Config[int]) { config.Source = nil },
		"stream":     func(config *outbox.Config[int]) { config.Stream = "" },
		"classifier": func(config *outbox.Config[int]) { config.IsPoison = nil },
		"reporter":   func(config *outbox.Config[int]) { config.Report = nil },
		"batch":      func(config *outbox.Config[int]) { config.Timing.BatchSize = 0 },
		"poll":       func(config *outbox.Config[int]) { config.Timing.PollInterval = 0 },
		"retry":      func(config *outbox.Config[int]) { config.Timing.RetryBackoff = 0 },
		"operation":  func(config *outbox.Config[int]) { config.Timing.OperationTimeout = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config := relayConfig(&memorySource{})
			invalidate(&config)
			if relay, err := outbox.Start(successfulPublisher{}, config); err == nil {
				_ = relay.Stop(t.Context())
				t.Fatal("invalid relay started")
			}
		})
	}
}

func TestEveryIOStageHasAnOperationDeadline(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"list", "publish", "delete"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				source := &memorySource{rows: []int{1}}
				config := relayConfig(source)
				var publisher outbox.Publisher = successfulPublisher{}
				block := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
				switch stage {
				case "list":
					source.readHook = block
				case "publish":
					publisher = publisherFunc(
						func(ctx context.Context, _ *natsgo.Msg) (*jetstream.PubAck, error) { return nil, block(ctx) },
					)
				case "delete":
					source.deleteHook = block
				}
				relay := startRelay(t, publisher, config)
				time.Sleep(5 * time.Second)
				synctest.Wait()
				failure, ok := errors.AsType[*outbox.DeliveryError[int]](relay.Fault())
				_, deleted, _ := source.snapshot()
				if !ok || failure.Stage != stage || !errors.Is(failure, context.DeadlineExceeded) ||
					len(deleted) != 0 {
					t.Fatalf("unbounded or misclassified %s: fault=%v deleted=%v", stage, relay.Fault(), deleted)
				}
			})
		})
	}
}
