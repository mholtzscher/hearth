package pirateweather

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	publishTimeout = 30 * time.Second
	reasonPrefix   = "adapter.hearth-adapter-pirateweather."
)

// Session is the SDK capability consumed by the serial poll loop.
type Session interface {
	Register(context.Context, adapter.Registration) (adapter.Binding, error)
	SetHealth(context.Context, adapter.HealthReport) error
	ReportEntityAvailability(context.Context, []adapter.EntityAvailabilityReport) error
	PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
}

// Adapter owns one configured weather location. Run has one lifecycle owner;
// Functions may execute concurrently and share only the provider throttle.
type Adapter struct {
	session      Session
	client       *client
	logger       *slog.Logger
	measurements []measurement
}

func New(
	session Session,
	config Config,
	apiKey string,
	transport http.RoundTripper,
	logger *slog.Logger,
) (*Adapter, error) {
	if session == nil {
		return nil, errors.New("weather Session is required")
	}
	provider, err := newClient(config, apiKey, transport)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Adapter{session: session, client: provider, measurements: measurements(),
		logger: logger.With(slog.String("component", "pirateweather"))}, nil
}

func (a *Adapter) Functions() []adapter.Function { return a.client.functions() }

// Run registers once, then acquires new conditions after each completed interval.
// No missed ticks or failed HTTP attempts are replayed.
func (a *Adapter) Run(ctx context.Context) error {
	ids, err := a.register(ctx)
	if err != nil {
		return err
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = a.poll(ctx, ids); err != nil {
			if errors.Is(err, adapter.ErrClosed) || errors.Is(err, adapter.ErrRuntimeFenced) {
				return err
			}
			if ctx.Err() == nil {
				a.logger.WarnContext(ctx, "weather evidence publication failed",
					slog.String("event", "pirateweather.publication_failed"), slog.String("error_code", "sdk_failed"))
			}
		}
		timer := time.NewTimer(a.client.config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (a *Adapter) poll(ctx context.Context, ids map[string]string) error {
	report, requestErr := a.client.current(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	publication, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	if requestErr != nil {
		reason := reasonProviderUnavailable
		if failure, ok := errors.AsType[*providerError](requestErr); ok {
			reason = failure.reason
		}
		a.logger.WarnContext(ctx, "weather acquisition failed", slog.String("event", "pirateweather.request_failed"),
			slog.String("error_code", reason))
		return a.session.SetHealth(publication, adapter.HealthReport{Status: adapter.HealthUnhealthy,
			SourceObservedAt: a.client.now().UTC(), ReasonCode: reasonPrefix + reason})
	}
	if err := a.session.SetHealth(
		publication,
		adapter.HealthReport{Status: adapter.HealthHealthy, SourceObservedAt: report.ReceivedAt},
	); err != nil {
		return err
	}
	availability := make([]adapter.EntityAvailabilityReport, 0, len(a.measurements))
	for _, m := range a.measurements {
		entry := adapter.EntityAvailabilityReport{EntityID: ids[m.key], SourceObservedAt: report.ReceivedAt,
			Status: adapter.AvailabilityUnavailable, ReasonCode: reasonPrefix + "measurement_unavailable"}
		if value, present := report.Values[m.key]; present {
			observation, err := m.observation(entry.EntityID, value, report)
			if err != nil {
				return err
			}
			if _, err = a.session.PublishObservation(publication, observation); err != nil {
				return err
			}
			entry.Status, entry.ReasonCode = adapter.AvailabilityAvailable, ""
		}
		availability = append(availability, entry)
	}
	return a.session.ReportEntityAvailability(publication, availability)
}
