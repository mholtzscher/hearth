package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/cmd/internal/cmdtest"
)

// This test protects executable bootstrap and fails if invalid logging flags
// do not fail fast before configuration load, echo the rejected value, or
// misreport the configuration failure in either format. It also fails if a
// run failure does not emit process.failed with the failed startup stage and
// its error code.
func TestMainLoggingFlagsAndConfigFailure(t *testing.T) {
	t.Parallel()
	binary := cmdtest.Build(t, ".")

	t.Run("invalid flags", func(t *testing.T) {
		t.Parallel()
		cmdtest.CheckInvalidLogFlags(t, binary)
	})
	t.Run("missing config text", func(t *testing.T) {
		t.Parallel()
		cmdtest.CheckMissingConfigText(t, binary)
	})
	t.Run("missing config json", func(t *testing.T) {
		t.Parallel()
		cmdtest.CheckMissingConfigJSON(t, binary, "hearth-adapter-zigbee2mqtt")
	})
	t.Run("startup cancellation", func(t *testing.T) {
		t.Parallel()
		natsURL := cmdtest.StartProcessNATS(t)
		configYAML :=
			"adapter_id: \"test-zigbee2mqtt\"\n" +
				"nats_url: \"" + natsURL + "\"\n" +
				"mqtt:\n" +
				"  url: \"tcp://127.0.0.1:1883\"\n" +
				"  base_topic: \"zigbee2mqtt\"\n"
		cmdtest.CheckStartupCancellation(t, binary, configYAML, "hearth-adapter-zigbee2mqtt")
	})
	t.Run("run failure reports stage", func(t *testing.T) {
		t.Parallel()
		checkReportRunFailureEmitsStageAndCode(t)
	})
}

// checkReportRunFailureEmitsStageAndCode protects staged executable failure
// reporting and fails if a run failure does not emit process.failed with the
// stage and error code from the failed startup. It exercises the reporting
// helper in-process because a refused NATS endpoint never fails fast: the
// SDK session retries the initial connection in the background by design, so
// no network failure can deterministically produce a run failure here. The
// stage-to-code mapping itself is proven in the app package tests.
func checkReportRunFailureEmitsStageAndCode(t *testing.T) {
	t.Helper()
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	reportRunFailure(context.Background(), logger, errors.New("run exploded"))
	stderr := output.String()
	for _, want := range []string{"process.failed", "stage=run", "error_code=run_failed"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("failure report lacks %q: %q", want, stderr)
		}
	}
}
