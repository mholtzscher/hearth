package homekit

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	healthCore  = "adapter.hearth-adapter-homekit.core_unreachable"
	healthFacts = "adapter.hearth-adapter-homekit.facts_unavailable"
	healthHAP   = "adapter.hearth-adapter-homekit.hap_server_failed"
)

type pendingMutation struct {
	device string
	reply  chan mutationResult
	result mutationResult
}

type reconciler struct {
	r           *Runtime
	live        **liveFactFeed
	fact        <-chan ObservationFact
	factDone    <-chan struct{}
	reload      <-chan time.Time
	reloadTimer *time.Timer
	pending     []pendingMutation
	lastHealth  string
	factsLostAt time.Time
}

func (r *Runtime) coordinate(ctx context.Context, live **liveFactFeed) {
	c := reconciler{r: r, live: live, fact: (*live).facts, factDone: (*live).done}
	conn := r.facts.js.Conn()
	status := conn.StatusChanged(natsgo.RECONNECTING, natsgo.DISCONNECTED, natsgo.CLOSED)
	defer conn.RemoveStatusListener(status)
	sync := time.NewTicker(r.config.ResyncInterval)
	defer sync.Stop()
	recoverTick := time.NewTicker(recoveryDelay)
	defer recoverTick.Stop()
	defer func() {
		if c.reloadTimer != nil {
			c.reloadTimer.Stop()
		}
	}()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case fact := <-c.fact:
			r.applyFact(fact)
		case <-c.factDone:
			c.disconnectFeed()
			c.health(ctx)
		case <-status:
			c.disconnectFeed()
			c.health(ctx)
		case <-sync.C:
			c.refresh(ctx)
			c.health(ctx)
		case <-recoverTick.C:
			c.recover(ctx)
		case request := <-r.mutations:
			c.mutate(ctx, request)
		case <-c.reload:
			c.reloadNow(ctx)
		}
	}
}

func (c *reconciler) disconnectFeed() {
	if c.factsLostAt.IsZero() {
		c.factsLostAt = time.Now()
	}
	stopFeed(*c.live)
	*c.live, c.fact, c.factDone = nil, nil, nil
}

func (c *reconciler) refresh(ctx context.Context) {
	changed, err := c.r.refresh(ctx)
	if changed {
		c.scheduleReload()
	}
	if err != nil && ctx.Err() == nil {
		c.r.logger.WarnContext(ctx, "Could not refresh HomeKit inventory",
			slog.String("event", "homekit.inventory_refresh_failed"))
	}
}

func (c *reconciler) recover(ctx context.Context) {
	if *c.live == nil && c.r.facts.js.Conn().IsConnected() {
		live, err := c.r.openFeed(ctx)
		if err == nil {
			*c.live, c.fact, c.factDone = live, live.facts, live.done
			c.factsLostAt = time.Time{}
			c.refresh(ctx)
		}
	}
	if c.r.hap == nil || !c.r.hap.active() {
		c.scheduleReload()
	}
	c.health(ctx)
}

func (c *reconciler) health(ctx context.Context) {
	now := time.Now()
	reason := healthReason(c.r.hap != nil && c.r.hap.active(), now.Sub(c.r.inventory.ReadAt),
		*c.live == nil && now.Sub(c.factsLostAt) >= c.r.config.ResyncInterval, c.r.config.ResyncInterval)
	if reason != c.lastHealth && c.r.reportHealth(ctx, reason) == nil {
		c.lastHealth = reason
	}
}

func healthReason(hapActive bool, inventoryAge time.Duration, factsUnavailable bool, interval time.Duration) string {
	switch {
	case !hapActive:
		return healthHAP
	case inventoryAge > 3*interval:
		return healthCore
	case factsUnavailable:
		return healthFacts
	default:
		return ""
	}
}

func (r *Runtime) reportHealth(ctx context.Context, reason string) error {
	report := adapter.HealthReport{Status: adapter.HealthHealthy, SourceObservedAt: time.Now().UTC()}
	if reason != "" {
		report.Status, report.ReasonCode = adapter.HealthUnhealthy, reason
	}
	ctx, cancel := context.WithTimeout(ctx, recoveryDelay)
	defer cancel()
	return r.session.SetHealth(ctx, report)
}

func (c *reconciler) mutate(ctx context.Context, request bridgeMutation) {
	if request.ctx.Err() != nil {
		request.reply <- mutationResult{err: request.ctx.Err()}
		return
	}
	if request.reset {
		if c.reload != nil {
			c.reloadNow(ctx)
		}
		request.reply <- c.r.rotatePairing(ctx)
		c.health(ctx)
		return
	}
	if len(c.pending) >= maxBatchWrites || slices.ContainsFunc(c.pending, func(p pendingMutation) bool {
		return p.device == request.desired.DeviceID
	}) {
		request.reply <- mutationResult{err: errBridgeBusy}
		return
	}
	result := c.r.commitExposure(request)
	if (result.err == nil && result.changed) || errors.Is(result.err, errStoreDurabilityUncertain) {
		c.scheduleReload()
		c.pending = append(c.pending, pendingMutation{
			device: request.desired.DeviceID, reply: request.reply, result: result,
		})
		return
	}
	request.reply <- result
}

func (c *reconciler) scheduleReload() {
	if c.reload != nil {
		return
	}
	c.reloadTimer = time.NewTimer(reloadDelay)
	c.reload = c.reloadTimer.C
}

func (c *reconciler) reloadNow(ctx context.Context) {
	c.reloadTimer.Stop()
	c.reload, c.reloadTimer = nil, nil
	c.r.stopHAP()
	err := c.r.startHAP(ctx)
	if err != nil && ctx.Err() == nil {
		c.r.logger.WarnContext(ctx, "Could not reload HomeKit accessories",
			slog.String("event", "homekit.reload_failed"))
	}
	for _, waiter := range c.pending {
		if err != nil {
			waiter.result.err = errors.Join(waiter.result.err, errReloadFailed)
		}
		waiter.reply <- waiter.result
	}
	c.pending = nil
	c.health(ctx)
}
