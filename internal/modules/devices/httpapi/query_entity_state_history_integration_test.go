//go:build integration

package httpapi //nolint:testpackage // Verify the raw-body deadline through the registered router.

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHistoryQuerySlowBodyDeadline(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{})
	server := httptest.NewServer(router)
	defer server.Close()
	connection, err := net.DialTimeout("tcp", strings.TrimPrefix(server.URL, "http://"), time.Second)
	require.NoError(t, err)
	defer connection.Close()
	require.NoError(t, connection.SetDeadline(time.Now().Add(8*time.Second)))
	_, err = fmt.Fprintf(
		connection,
		"POST /v1/entities/%s/state/history HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: 1000\r\nConnection: close\r\n\r\n{",
		apiEntityID,
	)
	require.NoError(t, err)
	// Leave the declared body incomplete. The operation must time out the read,
	// rather than wait for the peer or reach the service double.
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusRequestTimeout, response.StatusCode)
}
