package nats //nolint:testpackage // Tests exercise package-private JetStream stream configuration.

import (
	"reflect"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

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
