//go:build integration || e2e

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// waitForCoreHealthz waits until /healthz serves, which happens only after
// startup completes, so a 200 means serving began independently of the startup
// retention pass running in its worker.
func waitForCoreHealthz(
	ctx context.Context,
	t *testing.T,
	httpAddress string,
	runErrors <-chan error,
) {
	t.Helper()
	waitForMatrixCondition(t, 30*time.Second, func() (bool, error) {
		select {
		case runErr := <-runErrors:
			if runErr != nil {
				return false, runErr
			}
			return false, context.Canceled
		default:
		}
		request, requestErr := http.NewRequestWithContext(
			ctx, http.MethodGet, "http://"+httpAddress+"/healthz", nil,
		)
		if requestErr != nil {
			return false, requestErr
		}
		response, responseErr := http.DefaultClient.Do(request)
		if responseErr != nil {
			return false, nil
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK, nil
	})
}
