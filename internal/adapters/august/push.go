package august

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	pushOrigin = "https://ps.pndsn.com"
	// Public US subscription key from the pinned august-yale setup.ts.
	pushSubscribeKey = "sub-c-1030e062-0ebe-11e5-a5c2-0619f8945a4f"
	// Subscribe is a long poll, not an August read; leave room for the server's wait.
	pushTimeout = 310 * time.Second
)

type pushTarget struct {
	DeviceID lockID
	Channel  string
}

type pushReport struct {
	DeviceID lockID
	Report   report
}

type pushClient struct {
	http *http.Client
}

func newPushClient(transport http.RoundTripper) *pushClient {
	return &pushClient{
		http: &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("push redirects are not supported")
		}},
	}
}

// Run owns one subscription generation. The caller owns recovery and resynchronization.
func (p *pushClient) Run(
	ctx context.Context,
	targets []pushTarget,
	deliver func(context.Context, pushReport) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	selected, channels, err := preparePushTargets(targets)
	if err != nil || deliver == nil {
		return &providerError{code: codeInvalid}
	}
	token, region := "0", ""
	for {
		var batch pushBatch
		batch, err = p.subscribe(ctx, channels, token, region)
		if err != nil {
			return err
		}
		if err = deliverPushBatch(ctx, batch, selected, deliver); err != nil {
			return err
		}
		token, region = batch.Cursor.Token, strconv.Itoa(*batch.Cursor.Region)
	}
}

func deliverPushBatch(
	ctx context.Context,
	batch pushBatch,
	selected map[string]lockID,
	deliver func(context.Context, pushReport) error,
) error {
	for _, message := range batch.Messages {
		if err := ctx.Err(); err != nil {
			return err
		}
		id, ok := selected[message.Channel]
		if !ok || message.Type != 0 {
			continue
		}
		reading, ok := decodePushMessage(message.Data, id, time.Now().UTC())
		if ok {
			if err := deliver(ctx, pushReport{DeviceID: id, Report: reading}); err != nil {
				return err
			}
		}
	}
	return nil
}

func preparePushTargets(targets []pushTarget) (map[string]lockID, string, error) {
	if len(targets) == 0 || len(targets) > 64 {
		return nil, "", &providerError{code: codeInvalid}
	}
	selected := make(map[string]lockID, len(targets))
	ids := make(map[lockID]bool, len(targets))
	channels := make([]string, 0, len(targets))
	for _, target := range targets {
		id, err := parseLockID(string(target.DeviceID))
		if err != nil || id != target.DeviceID || ids[id] || !validPushChannel(target.Channel) {
			return nil, "", &providerError{code: codeInvalid}
		}
		if _, exists := selected[target.Channel]; exists {
			return nil, "", &providerError{code: codeInvalid}
		}
		ids[id], selected[target.Channel] = true, id
		channels = append(channels, target.Channel)
	}
	return selected, strings.Join(channels, ","), nil
}

// Restrict channels to unreserved ASCII, avoiding path and comma ambiguity.
func validPushChannel(channel string) bool {
	if len(channel) == 0 || len(channel) > 92 || channel == "." || channel == ".." {
		return false
	}
	for _, c := range channel {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && !strings.ContainsRune("-_.~", c) {
			return false
		}
	}
	return true
}

type pushBatch struct {
	Cursor struct {
		Token  string `json:"t"`
		Region *int   `json:"r"`
	} `json:"t"`
	Messages []struct {
		Channel string          `json:"c"`
		Type    int             `json:"e"`
		Data    json.RawMessage `json:"d"`
	} `json:"m"`
}

func (p *pushClient) subscribe(ctx context.Context, channels, token, region string) (pushBatch, error) {
	var batch pushBatch
	attempt, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	query := url.Values{"tt": {token}, "uuid": {"hearth-august"}}
	if region != "" {
		query.Set("tr", region)
	}
	req, err := http.NewRequestWithContext(attempt, http.MethodGet,
		pushOrigin+"/v2/subscribe/"+pushSubscribeKey+"/"+channels+"/0?"+query.Encode(), nil)
	if err != nil {
		return batch, &providerError{code: codeInvalid}
	}
	resp, err := p.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return batch, ctx.Err()
		}
		return batch, &providerError{code: codeUnavailable, cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return batch, &providerError{code: codeUnavailable, httpStatus: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maximumResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return batch, ctx.Err()
		}
		return batch, &providerError{code: codeUnavailable, cause: err}
	}
	if len(body) > maximumResponseBytes || json.Unmarshal(body, &batch) != nil ||
		!validPushToken(
			batch.Cursor.Token,
		) || batch.Cursor.Region == nil || *batch.Cursor.Region < 0 || batch.Messages == nil {
		return batch, &providerError{code: codeInvalid}
	}
	return batch, nil
}

func validPushToken(token string) bool {
	if len(token) == 0 || len(token) > 32 {
		return false
	}
	for _, c := range token {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
