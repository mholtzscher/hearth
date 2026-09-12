package nats //nolint:testpackage // Tests exercise package-private JetStream stream configuration.

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// TestProvisionDeviceFactStreamCreatesAndValidatesRequiredConfiguration pins the
// one Device Fact stream: FileStorage, LimitsPolicy retention, a seven-day and
// one-gibibyte bound, no message-size limit, oldest-first discard, an explicit
// two-hour duplicate window and no subject transform. It also proves
// provisioning creates no consumer, because consumer policy belongs to each
// reader.
func TestProvisionDeviceFactStreamCreatesAndValidatesRequiredConfiguration(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	if err := ProvisionDeviceFactStream(context.Background(), js); err != nil {
		t.Fatal(err)
	}
	stream, err := js.Stream(context.Background(), DeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	info, err := stream.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertDeviceFactStreamMatchesPinnedSettings(t, info.Config)
	if info.State.Consumers != 0 {
		t.Fatalf("provisioning created %d consumers, want none", info.State.Consumers)
	}
	if secondErr := ProvisionDeviceFactStream(context.Background(), js); secondErr != nil {
		t.Fatalf("second provisioning: %v", secondErr)
	}
	if validationErr := ValidateDeviceFactStream(context.Background(), js); validationErr != nil {
		t.Fatal(validationErr)
	}
}

// assertDeviceFactStreamMatchesPinnedSettings compares every observable stream
// setting to its pinned literal, so a mutated production constant cannot keep
// this test passing.
func assertDeviceFactStreamMatchesPinnedSettings(t *testing.T, config jetstream.StreamConfig) {
	t.Helper()
	checks := []struct {
		setting string
		got     any
		want    any
	}{
		{"name", config.Name, "HEARTH_DEVICE_FACTS_V1"},
		{"subjects", config.Subjects, []string{"hearth.v1.core.fact.>"}},
		{"storage", config.Storage, jetstream.FileStorage},
		{"retention", config.Retention, jetstream.LimitsPolicy},
		{"max age", config.MaxAge, 7 * 24 * time.Hour},
		{"max bytes", config.MaxBytes, int64(1 << 30)},
		{"max messages", config.MaxMsgs, int64(-1)},
		{"max message size", config.MaxMsgSize, int32(-1)},
		{"discard", config.Discard, jetstream.DiscardOld},
		{"duplicate window", config.Duplicates, 2 * time.Hour},
		{"subject transform", config.SubjectTransform, (*jetstream.SubjectTransformConfig)(nil)},
		{"sealed", config.Sealed, false},
		{"no acknowledgements", config.NoAck, false},
	}
	for _, check := range checks {
		if !reflect.DeepEqual(check.got, check.want) {
			t.Errorf("stream %s = %#v, want %#v", check.setting, check.got, check.want)
		}
	}
}

func TestValidateDeviceFactStreamRejectsMissingStream(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	err := ValidateDeviceFactStream(context.Background(), js)
	if err == nil || !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Fatalf("validation error = %v", err)
	}
}

// TestValidateDeviceFactStreamRejectsSubjectTransform proves the readiness path
// rejects a transform, not only provisioning: a stream that rewrites canonical
// fact subjects still PubAcks the original subject, so the relay would delete an
// outbox row whose fact no consumer can read under its canonical subject.
// Readiness must fail on the mutation instead.
func TestValidateDeviceFactStreamRejectsSubjectTransform(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	ctx := context.Background()
	config := deviceFactStreamConfig()
	config.SubjectTransform = &jetstream.SubjectTransformConfig{
		Source: ">", Destination: "hearth.v1.rewritten.fact.>",
	}
	if _, err := js.CreateStream(ctx, config); err != nil {
		t.Fatal(err)
	}
	stream, err := js.Stream(ctx, DeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Config.SubjectTransform == nil {
		t.Fatal("the live stream carries no subject transform, so this test proves nothing")
	}
	if validationErr := ValidateDeviceFactStream(ctx, js); validationErr == nil {
		t.Fatal("readiness validation accepted a subject transform")
	}
}

// TestValidateDeviceFactStreamRejectsSealedStream proves the live readiness path
// rejects a stream an operator sealed after provisioning. A sealed stream accepts
// no publication, so the relay could never drain a row or make progress, and
// readiness must fail instead of reporting a publisher that cannot publish.
func TestValidateDeviceFactStreamRejectsSealedStream(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	ctx := context.Background()
	if _, err := js.CreateStream(ctx, deviceFactStreamConfig()); err != nil {
		t.Fatal(err)
	}
	sealed := deviceFactStreamConfig()
	sealed.Sealed = true
	if _, err := js.UpdateStream(ctx, sealed); err != nil {
		t.Fatal(err)
	}
	stream, err := js.Stream(ctx, DeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Config.Sealed {
		t.Fatal("the live stream is not sealed, so this test proves nothing")
	}
	if validationErr := ValidateDeviceFactStream(ctx, js); validationErr == nil {
		t.Fatal("readiness validation accepted a sealed stream")
	}
}

// TestValidateDeviceFactStreamConfigRejectsSealed proves the readiness predicate
// rejects a sealed stream in isolation. The broker rewrites MaxAge and Discard
// when it seals a stream, so only a directly supplied configuration can show that
// Sealed alone fails validation.
func TestValidateDeviceFactStreamConfigRejectsSealed(t *testing.T) {
	t.Parallel()
	if err := validateDeviceFactStreamConfig(deviceFactStreamConfig()); err != nil {
		t.Fatalf("required configuration rejected: %v", err)
	}
	sealed := deviceFactStreamConfig()
	sealed.Sealed = true
	if err := validateDeviceFactStreamConfig(sealed); err == nil {
		t.Fatal("validation accepted a sealed stream configuration")
	}
}

func TestProvisionDeviceFactStreamRejectsMismatchedExistingConfiguration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*jetstream.StreamConfig)
	}{
		{"subject", func(config *jetstream.StreamConfig) {
			config.Subjects = []string{"hearth.v1.core.fact.entity.>"}
		}},
		{"storage", func(config *jetstream.StreamConfig) { config.Storage = jetstream.MemoryStorage }},
		{"retention", func(config *jetstream.StreamConfig) { config.Retention = jetstream.WorkQueuePolicy }},
		{"max age", func(config *jetstream.StreamConfig) {
			config.MaxAge = time.Hour
			config.Duplicates = time.Minute
		}},
		{"max bytes", func(config *jetstream.StreamConfig) { config.MaxBytes = 1 << 20 }},
		{"max messages", func(config *jetstream.StreamConfig) { config.MaxMsgs = 16 }},
		{"max message size", func(config *jetstream.StreamConfig) { config.MaxMsgSize = 1024 }},
		{"discard", func(config *jetstream.StreamConfig) { config.Discard = jetstream.DiscardNew }},
		{"duplicate window", func(config *jetstream.StreamConfig) { config.Duplicates = time.Minute }},
		{"subject transform", func(config *jetstream.StreamConfig) {
			config.SubjectTransform = &jetstream.SubjectTransformConfig{
				Source: ">", Destination: "hearth.v1.rewritten.fact.>",
			}
		}},
		{"no acknowledgements", func(config *jetstream.StreamConfig) { config.NoAck = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, _, js := startJetStream(t)
			config := deviceFactStreamConfig()
			test.mutate(&config)
			if _, err := js.CreateStream(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			err := ProvisionDeviceFactStream(context.Background(), js)
			if err == nil || !strings.Contains(err.Error(), "does not match") {
				t.Fatalf("provisioning error = %v", err)
			}
		})
	}
}
