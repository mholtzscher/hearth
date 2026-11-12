package august

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type loginResult struct {
	session   authSession
	validated bool
}

func acquire(ctx context.Context, gate chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *client) authenticate(ctx context.Context) error {
	_, err := c.authentication(ctx)
	return err
}

func (c *client) authentication(ctx context.Context) (authSession, error) {
	if err := c.ready(); err != nil {
		return authSession{}, err
	}
	if err := acquire(ctx, c.loginGate); err != nil {
		return authSession{}, err
	}
	defer func() { <-c.loginGate }()
	c.mu.Lock()
	session, nextLogin := c.session, c.nextLogin
	c.mu.Unlock()
	if session.token != "" && c.now().Before(session.expiresAt) {
		return session, nil
	}
	if c.now().Before(nextLogin) {
		return authSession{}, &providerError{code: codeUnavailable}
	}
	result, err := c.login(ctx)
	if err == nil && !result.validated {
		err = &providerError{code: codeValidation}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.session = result.session
		return result.session, nil
	}
	switch failureCode(err) {
	case codeAuthRejected, codeValidation:
		c.blocked = failureCode(err)
	default:
		c.nextLogin = c.now().Add(time.Minute)
	}
	return authSession{}, err
}

func (c *client) login(ctx context.Context) (loginResult, error) {
	body := struct {
		InstallID  string `json:"installId"`
		Identifier string `json:"identifier"`
		Password   string `json:"password"`
	}{c.config.InstallationID, "email:" + c.credentials.Email, c.credentials.Password}
	response, err := c.request(ctx, http.MethodPost, sessionPath, body, "")
	if err != nil {
		if failureCode(err) == codeExpired {
			return loginResult{}, &providerError{code: codeAuthRejected}
		}
		return loginResult{}, err
	}
	var payload struct {
		PasswordValid *bool  `json:"vPassword"`
		InstallValid  *bool  `json:"vInstallId"`
		ExpiresAt     string `json:"expiresAt"`
	}
	if json.Unmarshal(response.body, &payload) != nil || payload.PasswordValid == nil || payload.InstallValid == nil {
		return loginResult{}, &providerError{code: codeInvalid}
	}
	if !*payload.PasswordValid {
		return loginResult{}, &providerError{code: codeAuthRejected}
	}
	expires, err := time.Parse(time.RFC3339Nano, payload.ExpiresAt)
	token := response.header.Get("X-August-Access-Token")
	if err != nil || token == "" || !expires.After(c.now()) {
		return loginResult{}, &providerError{code: codeInvalid}
	}
	return loginResult{session: authSession{token: token, expiresAt: expires}, validated: *payload.InstallValid}, nil
}

// Authorize explicitly validates the configured installation once. It never connects to Core or PubNub.
func Authorize(ctx context.Context, config Config, credentials Credentials,
	codeReader func() (string, error), transport http.RoundTripper) error {
	c, err := newClient(config, credentials, transport)
	if err != nil {
		return err
	}
	result, err := c.login(ctx)
	if err != nil || result.validated {
		return err
	}
	if codeReader == nil {
		return errors.New("verification code input is required")
	}
	_, err = c.request(ctx, http.MethodPost, "/validation/email", struct {
		Value string `json:"value"`
	}{credentials.Email}, result.session.token)
	if err != nil {
		return err
	}
	code, err := codeReader()
	if err != nil {
		return errors.New("verification code input could not be read")
	}
	code = strings.TrimSpace(code)
	if len(code) != 6 || strings.IndexFunc(code, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return errors.New("verification code must contain six digits")
	}
	_, err = c.request(ctx, http.MethodPost, "/validate/email", struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}{credentials.Email, code}, result.session.token)
	if err != nil {
		return err
	}
	result, err = c.login(ctx)
	if err != nil {
		return err
	}
	if !result.validated {
		return &providerError{code: codeValidation}
	}
	return nil
}
