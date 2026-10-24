//go:build e2e

package hearthd //nolint:testpackage // Exercises private application assembly through real transports.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// A closed completion signal is reusable by readiness, assertions and cleanup;
// err is written before close and read only after observing completion.
type functionCoreRun struct {
	address string
	stop    context.CancelFunc
	done    chan struct{}
	err     error
}

func startFunctionCore(t *testing.T, config Config) *functionCoreRun {
	t.Helper()
	address, options := reserveLoopbackListener(t)
	config.HTTPAddr = address
	ctx, stop := context.WithCancel(t.Context())
	run := &functionCoreRun{address: address, stop: stop, done: make(chan struct{})}
	go func() {
		run.err = runWithOptions(ctx, config, slog.New(slog.DiscardHandler), options)
		close(run.done)
	}()
	t.Cleanup(func() { stop(); run.join(t) })
	client := newNonPoolingHTTPClient(t)
	waitForMatrixCondition(t, 15*time.Second, func() (bool, error) {
		select {
		case <-run.done:
			return false, errors.Join(errors.New("Core exited before readiness"), run.err)
		default:
		}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+address+"/readyz", nil)
		if err != nil {
			return false, err
		}
		response, err := client.Do(request)
		if err != nil {
			return false, nil
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		return response.StatusCode == http.StatusOK, nil
	})
	return run
}

func (run *functionCoreRun) join(t *testing.T) {
	t.Helper()
	select {
	case <-run.done:
		if run.err != nil {
			t.Errorf("Core shutdown: %v", run.err)
		}
	case <-time.After(20 * time.Second):
		t.Error("Core did not join admitted function work")
	}
}

func functionNetworkRequest(
	ctx context.Context,
	client *http.Client,
	address, method, path, body string,
) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, "http://"+address+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	return response.StatusCode, raw, err
}

func successfulFunctionCoreRequest(
	t *testing.T,
	client *http.Client,
	run *functionCoreRun,
	method, path, body string,
) []byte {
	t.Helper()
	status, raw, err := functionNetworkRequest(t.Context(), client, run.address, method, path, body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("%s %s: %d %s %v", method, path, status, raw, err)
	}
	return raw
}

func seedInterruptedFunctionInvocation(t *testing.T, path string) adapters.InvocationID {
	t.Helper()
	database, err := platformdb.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	repository := adapterssqlite.NewAdapterRepository(database)
	function, err := repository.GetFunction(t.Context(), "restart-functions", "inspect")
	if err != nil {
		t.Fatal(err)
	}
	id, err := adapters.NewInvocationID()
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := adapters.PrepareInvocation(
		function,
		adapters.InvokeInput{
			AdapterID:       function.AdapterID,
			FunctionKey:     function.Descriptor.Key,
			CatalogRevision: function.CatalogRevision,
			Arguments:       json.RawMessage(functionExactObject),
		},
		id,
		// Synthetic pre-correction wall time, with no host-clock change.
		time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	invocation, err := repository.Admit(t.Context(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	return invocation.ID
}
