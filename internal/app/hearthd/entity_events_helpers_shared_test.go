//go:build integration || e2e

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
)

// startCoreNATSServer starts a JetStream-enabled NATS server for an assembly
// test that needs Core's durable Observation or Entity Event consumer.
func startCoreNATSServer(t *testing.T) *natsserver.Server {
	t.Helper()
	return natstest.StartServer(t)
}

// newNonPoolingHTTPClient returns a client whose requests never share a pooled
// connection and never reuse the process-wide default client. net/http can dial
// a connection while another request is served on a pooled one, leaving a
// socket that no request was ever sent on. Core cannot tell that socket apart
// from a slow client, so a test that keeps the default client's pooling could
// let that client-side artifact decide its shutdown evidence; the
// unfinished-connection case is covered deliberately by
// TestRunCancellationForceClosesUnfinishedClientConnection.
func newNonPoolingHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	transport := &http.Transport{DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

func waitForCoreHTTPStatus(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	httpAddress, path string,
	runErrors <-chan error,
) {
	t.Helper()
	waitForMatrixCondition(t, 15*time.Second, func() (bool, error) {
		select {
		case runErr := <-runErrors:
			if runErr != nil {
				return false, runErr
			}
			return false, context.Canceled
		default:
		}
		request, requestErr := http.NewRequestWithContext(
			ctx, http.MethodGet, "http://"+httpAddress+path, nil,
		)
		if requestErr != nil {
			return false, requestErr
		}
		response, responseErr := client.Do(request)
		if responseErr != nil {
			return false, nil
		}
		// Draining before closing releases the connection for the next poll
		// instead of leaving an unread body behind; a failed read only means the
		// next poll retries.
		_, _ = io.Copy(io.Discard, response.Body)
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK, nil
	})
}
