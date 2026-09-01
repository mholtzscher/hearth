package adapter

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

const (
	requestAttemptTimeout      = time.Second
	heartbeatInterval          = 5 * time.Second
	releaseTimeout             = 5 * time.Second
	maximumSoftwareVersionSize = 128
	maximumHealthReasonSize    = 128
)

var (
	slugPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	reasonCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*(\.[a-z0-9][a-z0-9_-]*)+$`)
)

type adapterClaimRequest struct {
	AdapterID       string `json:"adapter_id"`
	RuntimeID       string `json:"runtime_id"`
	SoftwareName    string `json:"software_name"`
	SoftwareVersion string `json:"software_version"`
}

type adapterClaimResponse struct {
	Status string             `json:"status"`
	Error  *adapterClaimError `json:"error,omitempty"`
}

type adapterClaimError struct {
	Code       string  `json:"code"`
	Message    string  `json:"message"`
	RetryAfter *string `json:"retry_after,omitempty"`
}

type adapterHeartbeatRequest struct {
	ExternalSystem externalSystemHealth `json:"external_system"`
}

type externalSystemHealth struct {
	Status           string        `json:"status"`
	SourceObservedAt string        `json:"source_observed_at"`
	Reason           *healthReason `json:"reason,omitempty"`
}

type healthReason struct {
	Code string `json:"code"`
}

type adapterHeartbeatResponse struct {
	Status         string        `json:"status"`
	LeaseExpiresAt string        `json:"lease_expires_at,omitempty"`
	Error          *adapterError `json:"error,omitempty"`
}

type adapterReleaseRequest struct{}

type adapterReleaseResponse struct {
	Status string        `json:"status"`
	Error  *adapterError `json:"error,omitempty"`
}

type adapterError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type preparedRequest struct {
	id             string
	correlationID  string
	responseSchema string
	kind           string
	subject        string
	payload        []byte
}

func validateConfig(ctx context.Context, config Config) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := natswire.AdapterClaimSubject(config.AdapterID); err != nil {
		return &ValidationError{Err: err}
	}
	if !slugPattern.MatchString(config.SoftwareName) {
		return &ValidationError{Err: errors.New("software name must be a subject-safe slug")}
	}
	if !utf8.ValidString(config.SoftwareVersion) ||
		!validTextLength(config.SoftwareVersion, maximumSoftwareVersionSize) {
		return &ValidationError{Err: errors.New("software version must contain 1 to 128 characters")}
	}
	if config.NATSURL == "" {
		return &ValidationError{Err: errors.New("NATS URL is required")}
	}
	return nil
}

//nolint:gocognit // Claim retry keeps one envelope across transport and active-runtime retries.
func (session *Session) claim(ctx context.Context, config Config) error {
	subject, err := natswire.AdapterClaimSubject(config.AdapterID)
	if err != nil {
		return &ValidationError{Err: err}
	}
	runtimeID, err := newID("run")
	if err != nil {
		return err
	}
	request, err := prepareRequest(
		session, "clm", contractsv1.AdapterClaimRequestSchemaID,
		contractsv1.AdapterClaimResponseSchemaID, "Adapter claim", subject,
		adapterClaimRequest{
			AdapterID: config.AdapterID, RuntimeID: runtimeID, SoftwareName: config.SoftwareName,
			SoftwareVersion: config.SoftwareVersion,
		},
	)
	if err != nil {
		return err
	}
	for {
		attemptContext, cancelAttempt := context.WithTimeout(ctx, requestAttemptTimeout)
		response, requestErr := sendSessionRequest[adapterClaimResponse](attemptContext, session, request)
		cancelAttempt()
		if requestErr != nil {
			if retryErr := waitForRequestRetry(ctx, requestErr); retryErr != nil {
				return retryErr
			}
			continue
		}
		if response.Data.Status == statusAccepted {
			session.runtimeID = runtimeID
			session.heartbeatInterval = heartbeatInterval
			return nil
		}
		if response.Data.Error == nil {
			return errors.New("adapter claim rejection omitted error")
		}
		switch response.Data.Error.Code {
		case "adapter_active":
			if response.Data.Error.RetryAfter == nil {
				return errors.New("active Adapter claim rejection omitted retry time")
			}
			retryAfter, parseErr := time.Parse(time.RFC3339Nano, *response.Data.Error.RetryAfter)
			if parseErr != nil {
				return fmt.Errorf("parse Adapter claim retry time: %w", parseErr)
			}
			delay := max(time.Until(retryAfter), requestRetryWait)
			if waitErr := waitForRetry(ctx, delay); waitErr != nil {
				return waitErr
			}
		default:
			return fmt.Errorf("unknown Adapter claim rejection %q", response.Data.Error.Code)
		}
	}
}

func (session *Session) SetHealth(ctx context.Context, report HealthReport) error {
	if err := session.sessionError(); err != nil {
		return err
	}
	if err := validateHealthReport(report, session.softwareName); err != nil {
		return &ValidationError{Err: err}
	}
	report.SourceObservedAt = report.SourceObservedAt.UTC()

	session.stateMutex.Lock()
	if session.terminalErr != nil {
		err := session.terminalErr
		session.stateMutex.Unlock()
		return err
	}
	if report.Status == HealthUnknown && session.desiredHealth.Status != HealthUnknown {
		session.stateMutex.Unlock()
		return &ValidationError{Err: errors.New("health cannot return to unknown in one runtime")}
	}
	session.desiredHealth = report
	session.desiredGeneration++
	generation := session.desiredGeneration
	session.stateMutex.Unlock()
	session.wakeHeartbeat()

	for {
		session.stateMutex.Lock()
		if session.terminalErr != nil {
			err := session.terminalErr
			session.stateMutex.Unlock()
			return err
		}
		if session.ackedGeneration >= generation {
			session.stateMutex.Unlock()
			return nil
		}
		notified := session.heartbeatNotify
		session.stateMutex.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-notified:
		}
	}
}

func (session *Session) runHeartbeats(ctx context.Context) {
	defer close(session.heartbeatDone)
	timer := time.NewTimer(session.heartbeatInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-session.heartbeatWake:
		case <-timer.C:
		}
		if err := session.sendLatestHeartbeat(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, ErrClosed) ||
				errors.Is(err, ErrRuntimeFenced) {
				return
			}
			session.logger.ErrorContext(ctx, "Adapter heartbeat loop stopped", "error", err)
			session.markClosed()
			return
		}
		timer.Reset(session.heartbeatInterval)
	}
}

//nolint:gocognit // One loop serializes retries, acknowledgements, and superseding health reports.
func (session *Session) sendLatestHeartbeat(ctx context.Context) error {
	for {
		session.stateMutex.Lock()
		if session.terminalErr != nil {
			err := session.terminalErr
			session.stateMutex.Unlock()
			return err
		}
		report := session.desiredHealth
		generation := session.desiredGeneration
		session.stateMutex.Unlock()

		subject, err := natswire.AdapterHeartbeatSubject(session.adapterID, session.runtimeID)
		if err != nil {
			return err
		}
		request, err := prepareRequest(
			session, "hbt", contractsv1.AdapterHeartbeatRequestSchemaID,
			contractsv1.AdapterHeartbeatResponseSchemaID, "Adapter heartbeat", subject,
			adapterHeartbeatRequest{ExternalSystem: wireHealthReport(report)},
		)
		if err != nil {
			return err
		}
		attemptContext, cancelAttempt := context.WithTimeout(ctx, requestAttemptTimeout)
		response, requestErr := sendSessionRequest[adapterHeartbeatResponse](attemptContext, session, request)
		cancelAttempt()
		if requestErr != nil {
			if retryErr := waitForRequestRetry(ctx, requestErr); retryErr != nil {
				return retryErr
			}
			continue
		}
		if response.Data.Status == statusRejected {
			if response.Data.Error != nil && response.Data.Error.Code == "runtime_fenced" {
				session.markFenced()
				return ErrRuntimeFenced
			}
			return errors.New("adapter heartbeat was rejected without runtime fencing")
		}
		_, leaseParseErr := time.Parse(time.RFC3339Nano, response.Data.LeaseExpiresAt)
		if leaseParseErr != nil {
			return fmt.Errorf("parse Adapter heartbeat lease expiry: %w", leaseParseErr)
		}

		session.stateMutex.Lock()
		if generation > session.ackedGeneration {
			session.ackedGeneration = generation
		}
		session.notifyHeartbeatLocked()
		moreRecent := session.desiredGeneration > generation
		session.stateMutex.Unlock()
		if !moreRecent {
			return nil
		}
	}
}

func (session *Session) close() error {
	session.handlerMutex.Lock()
	session.closing = true
	session.handlerMutex.Unlock()

	session.stateMutex.Lock()
	fenced := errors.Is(session.terminalErr, ErrRuntimeFenced)
	if session.terminalErr == nil {
		session.terminalErr = ErrClosed
	}
	if session.lifecycleCancel != nil {
		session.lifecycleCancel()
	}
	session.signalClosedLocked()
	session.stateMutex.Unlock()
	<-session.heartbeatDone

	session.stateMutex.Lock()
	fenced = fenced || errors.Is(session.terminalErr, ErrRuntimeFenced)
	session.stateMutex.Unlock()
	if !fenced {
		releaseContext, cancelRelease := context.WithTimeout(context.Background(), releaseTimeout)
		releaseErr := session.release(releaseContext)
		cancelRelease()
		if errors.Is(releaseErr, ErrRuntimeFenced) {
			session.markFenced()
			fenced = true
		} else if releaseErr != nil {
			session.logger.Warn("release Adapter runtime; lease expiry will end it", "error", releaseErr)
		}
	}

	session.handlerWait.Wait()
	if fenced {
		session.connection.Close()
		return ErrRuntimeFenced
	}
	if err := session.connection.Drain(); err != nil {
		session.connection.Close()
		return err
	}
	return nil
}

func (session *Session) release(ctx context.Context) error {
	subject, err := natswire.AdapterReleaseSubject(session.adapterID, session.runtimeID)
	if err != nil {
		return err
	}
	request, err := prepareRequest(
		session, "rel", contractsv1.AdapterReleaseRequestSchemaID,
		contractsv1.AdapterReleaseResponseSchemaID, "Adapter release", subject,
		adapterReleaseRequest{},
	)
	if err != nil {
		return err
	}
	for {
		attemptContext, cancelAttempt := context.WithTimeout(ctx, requestAttemptTimeout)
		response, requestErr := sendPrepared[adapterReleaseResponse](attemptContext, session, request)
		cancelAttempt()
		if requestErr != nil {
			if retryErr := waitForRequestRetry(ctx, requestErr); retryErr != nil {
				return retryErr
			}
			continue
		}
		if response.Data.Status == statusAccepted {
			return nil
		}
		if response.Data.Error != nil && response.Data.Error.Code == "runtime_fenced" {
			return ErrRuntimeFenced
		}
		return errors.New("adapter release was rejected without runtime fencing")
	}
}

func prepareRequest[Req any](
	session *Session,
	prefix, requestSchema, responseSchema, kind, subject string,
	data Req,
) (preparedRequest, error) {
	requestID, err := newID(prefix)
	if err != nil {
		return preparedRequest{}, err
	}
	correlationID, err := newID("cor")
	if err != nil {
		return preparedRequest{}, err
	}
	envelope := natswire.Envelope[Req]{
		ID: requestID, Schema: requestSchema, EmittedAt: nowString(),
		CorrelationID: correlationID, Data: data,
	}
	payload, err := natswire.Encode(session.validator, requestSchema, envelope)
	if err != nil {
		return preparedRequest{}, &ValidationError{Err: err}
	}
	return preparedRequest{
		id: requestID, correlationID: correlationID, responseSchema: responseSchema,
		kind: kind, subject: subject, payload: payload,
	}, nil
}

func sendPrepared[Resp any](
	ctx context.Context,
	session *Session,
	request preparedRequest,
) (natswire.Envelope[Resp], error) {
	message := &natsgo.Msg{Subject: request.subject, Header: make(natsgo.Header), Data: request.payload}
	natswire.InjectTrace(ctx, message.Header)
	reply, err := session.connection.RequestMsgWithContext(ctx, message)
	if err != nil {
		return natswire.Envelope[Resp]{}, err
	}
	response, err := natswire.Decode[Resp](session.validator, request.responseSchema, reply.Data)
	if err != nil {
		return natswire.Envelope[Resp]{}, fmt.Errorf("invalid %s response: %w", request.kind, err)
	}
	if response.CausationID == nil || *response.CausationID != request.id {
		return natswire.Envelope[Resp]{}, errors.New("response causation ID does not match request")
	}
	if response.CorrelationID != request.correlationID {
		return natswire.Envelope[Resp]{}, errors.New("response correlation ID does not match request")
	}
	return response, nil
}

func sendSessionRequest[Resp any](
	ctx context.Context,
	session *Session,
	request preparedRequest,
) (natswire.Envelope[Resp], error) {
	if err := session.sessionError(); err != nil {
		return natswire.Envelope[Resp]{}, err
	}
	response, err := sendPrepared[Resp](ctx, session, request)
	if err != nil {
		if terminalErr := session.sessionError(); terminalErr != nil {
			return natswire.Envelope[Resp]{}, terminalErr
		}
		return natswire.Envelope[Resp]{}, err
	}
	if terminalErr := session.sessionError(); terminalErr != nil {
		return natswire.Envelope[Resp]{}, terminalErr
	}
	return response, nil
}

func validateHealthReport(report HealthReport, softwareName string) error {
	if report.SourceObservedAt.IsZero() {
		return errors.New("health source observation time is required")
	}
	switch report.Status {
	case HealthUnknown, HealthHealthy:
		if report.ReasonCode != "" {
			return fmt.Errorf("%s health must omit a reason", report.Status)
		}
	case HealthUnhealthy:
		if report.ReasonCode == "" {
			return errors.New("unhealthy health requires a reason code")
		}
	default:
		return errors.New("health status is invalid")
	}
	if report.ReasonCode == "" {
		return nil
	}
	if !utf8.ValidString(report.ReasonCode) ||
		utf8.RuneCountInString(report.ReasonCode) > maximumHealthReasonSize ||
		!reasonCodePattern.MatchString(report.ReasonCode) {
		return errors.New("health reason code must be a lowercase dotted identifier of at most 128 characters")
	}
	if strings.HasPrefix(report.ReasonCode, "hearth.") ||
		strings.HasPrefix(report.ReasonCode, "adapter."+softwareName+".") {
		return nil
	}
	return fmt.Errorf("health reason code must use hearth or adapter.%s", softwareName)
}

func wireHealthReport(report HealthReport) externalSystemHealth {
	wire := externalSystemHealth{
		Status: string(report.Status), SourceObservedAt: report.SourceObservedAt.UTC().Format(time.RFC3339Nano),
	}
	if report.ReasonCode != "" {
		wire.Reason = &healthReason{Code: report.ReasonCode}
	}
	return wire
}

func (session *Session) sessionError() error {
	session.stateMutex.Lock()
	defer session.stateMutex.Unlock()
	return session.terminalErr
}

func (session *Session) wakeHeartbeat() {
	select {
	case session.heartbeatWake <- struct{}{}:
	default:
	}
}

func (session *Session) notifyHeartbeatLocked() {
	close(session.heartbeatNotify)
	session.heartbeatNotify = make(chan struct{})
}

func (session *Session) signalClosedLocked() {
	session.closedOnce.Do(func() { close(session.closed) })
	session.notifyHeartbeatLocked()
}

func (session *Session) markClosed() {
	session.stateMutex.Lock()
	if session.terminalErr == nil {
		session.terminalErr = ErrClosed
	}
	if session.lifecycleCancel != nil {
		session.lifecycleCancel()
	}
	session.signalClosedLocked()
	session.stateMutex.Unlock()
	session.connection.Close()
}

func (session *Session) markFenced() {
	session.handlerMutex.Lock()
	session.closing = true
	session.handlerMutex.Unlock()

	session.stateMutex.Lock()
	session.terminalErr = ErrRuntimeFenced
	if session.lifecycleCancel != nil {
		session.lifecycleCancel()
	}
	session.signalClosedLocked()
	session.stateMutex.Unlock()
	session.connection.Close()
}

func validTextLength(value string, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return length >= 1 && length <= maximum
}

func waitForRequestRetry(ctx context.Context, requestErr error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if !isTransientRequestError(requestErr) {
		return requestErr
	}
	return waitForRetry(ctx, requestRetryWait)
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isTransientRequestError(err error) bool {
	return errors.Is(err, natsgo.ErrDisconnected) ||
		errors.Is(err, natsgo.ErrNoResponders) ||
		errors.Is(err, natsgo.ErrTimeout) ||
		errors.Is(err, context.DeadlineExceeded)
}
