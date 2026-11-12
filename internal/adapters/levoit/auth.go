package levoit

import (
	"context"
	"crypto/md5" //nolint:gosec // VeSync's password protocol requires MD5, not password storage.
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func terminalID(email string) string {
	id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("hearth-adapter-levoit/"+email))
	return "2" + hex.EncodeToString(id[:])
}

func (c *client) authentication(ctx context.Context, refresh bool) (authSession, error) {
	if err := c.ready(); err != nil {
		return authSession{}, err
	}
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	if session.token != "" {
		return session, nil
	}
	if !refresh {
		return authSession{}, &providerError{code: reasonSessionExpired}
	}
	if err := acquire(ctx, c.loginGate); err != nil {
		return authSession{}, err
	}
	defer func() { <-c.loginGate }()
	c.mu.Lock()
	session, nextLogin := c.session, c.nextLogin
	c.mu.Unlock()
	if session.token != "" {
		return session, nil
	}
	if c.now().Before(nextLogin) {
		return authSession{}, &providerError{code: reasonProviderUnavailable}
	}
	session, err := c.login(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.session = session
		return session, nil
	}
	switch failureCode(err) {
	case "authentication_rejected", reasonUnsupportedAuth:
		c.blocked = failureCode(err)
	default:
		c.nextLogin = c.now().Add(time.Minute)
	}
	return authSession{}, err
}

func (c *client) login(ctx context.Context) (authSession, error) {
	// This is the exact upstream password transform, transported only over TLS.
	digest := md5.Sum([]byte(c.credentials.Password)) //nolint:gosec // Required by VeSync's wire protocol.
	body := c.authBody("authByPWDOrOTM")
	body["email"], body["password"] = c.credentials.Email, hex.EncodeToString(digest[:])
	body["authProtocolType"], body["appID"], body["sourceAppID"] = "generic", "eldodkfj", "eldodkfj"
	result, err := c.request(ctx, "/globalPlatform/api/accountAuth/v1/authByPWDOrOTM", body, nil)
	if err != nil {
		return authSession{}, err
	}
	var authorization struct {
		AccountID string   `json:"accountID"`
		Code      string   `json:"authorizeCode"`
		MFA       []string `json:"mfaMethodList"`
	}
	if json.Unmarshal(result, &authorization) != nil {
		return authSession{}, &providerError{code: reasonInvalidResponse}
	}
	if len(authorization.MFA) != 0 && authorization.Code == "" {
		return authSession{}, &providerError{code: reasonUnsupportedAuth}
	}
	if authorization.AccountID == "" || authorization.Code == "" {
		return authSession{}, &providerError{code: reasonInvalidResponse}
	}
	body = c.authBody("loginByAuthorizeCode4Vesync")
	body["authorizeCode"], body["emailSubscriptions"] = authorization.Code, false
	result, err = c.request(ctx, "/user/api/accountManage/v1/loginByAuthorizeCode4Vesync", body, nil)
	if err != nil {
		return authSession{}, err
	}
	var login struct {
		AccountID string `json:"accountID"`
		Token     string `json:"token"`
		Country   string `json:"countryCode"`
		Region    string `json:"currentRegion"`
	}
	if json.Unmarshal(result, &login) != nil || login.AccountID == "" || login.Token == "" {
		return authSession{}, &providerError{code: reasonInvalidResponse}
	}
	if login.Country != "US" || (login.Region != "" && login.Region != "US") {
		return authSession{}, &providerError{code: reasonUnsupportedAuth}
	}
	return authSession{token: login.Token, accountID: login.AccountID}, nil
}

func (c *client) authBody(method string) map[string]any {
	c.traceCounter++
	return map[string]any{
		"method":          method,
		"acceptLanguage":  "en",
		"accountID":       "",
		"token":           "",
		"clientInfo":      "pyvesync",
		"clientType":      "vesyncApp",
		"clientVersion":   "VeSync " + appVersion,
		"debugMode":       false,
		"osInfo":          "Android",
		"terminalId":      c.terminalID,
		"timeZone":        c.config.TimeZone,
		"userCountryCode": "US",
		"traceId":         fmt.Sprintf("APP%s%d-%05d", c.terminalID[28:32], c.now().Unix(), c.traceCounter),
	}
}
