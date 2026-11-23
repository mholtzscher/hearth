// Package musicassistant implements the Music Assistant adapter.
package musicassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

const (
	requestTimeout = 3 * time.Second
	maxResponse    = 256 * 1024
	maxToken       = 4096
)

// Client owns one fixed route and its authenticated Music Assistant transport.
type Client struct {
	endpoint string
	// origin is server_url without a trailing slash; artwork URLs are rebuilt on it.
	origin   string
	token    string
	http     *http.Client
	playerIO chan struct{}
}

type playerGrouping struct {
	GroupMembers []string `json:"group_members"`
	ActiveGroup  string   `json:"active_group"`
	SyncedTo     string   `json:"synced_to"`
}

func (p playerGrouping) grouped() bool {
	return p.ActiveGroup != "" || p.SyncedTo != "" || len(p.GroupMembers) != 0
}

// New validates config before constructing the playback client.
func New(serverURL, token string, transport http.RoundTripper) (*Client, error) {
	u, err := url.Parse(serverURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New(
			"server_url must be an HTTP or HTTPS origin without credentials, path, query, or fragment",
		)
	}
	if len(token) > maxToken || !regexp.MustCompile(`^[A-Za-z0-9._~-]+$`).MatchString(token) {
		return nil, errors.New("token file must contain one Music Assistant bearer token")
	}
	if transport == nil {
		// Use HTTP/1 so HTTP/2 stream refusal cannot reissue a mutation.
		protocols := new(http.Protocols)
		protocols.SetHTTP1(true)
		transport = &http.Transport{Protocols: protocols}
	}
	u.Path = ""
	origin := u.String()
	u.Path = "/api"
	return &Client{
		endpoint: u.String(), origin: origin, token: token,
		playerIO: make(chan struct{}, 1),
		http: &http.Client{
			Transport: transport, Timeout: requestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// Serialize player observations with controls so an older poll cannot overwrite
// a newer Command-linked report from this Session.
func (c *Client) acquirePlayerIO(ctx context.Context) error {
	select {
	case c.playerIO <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) releasePlayerIO() { <-c.playerIO }

type statusError int

func (statusError) Error() string { return "Music Assistant returned an unsuccessful HTTP status" }

type rpcRequest struct {
	MessageID string `json:"message_id"`
	Command   string `json:"command"`
	Args      any    `json:"args"`
}

func (c *Client) call(ctx context.Context, id, command string, args, result any) error {
	body, err := json.Marshal(rpcRequest{MessageID: id, Command: command, Args: args})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.GetBody = nil
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return statusError(res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return err
	}
	if len(data) > maxResponse {
		return errors.New("upstream response exceeds the size limit")
	}
	return json.Unmarshal(data, result)
}
