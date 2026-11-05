package dbload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type budgetLedger struct {
	LimitNS     int64     `json:"limit_ns"`
	UsedNS      int64     `json:"used_ns"`
	ActiveSince time.Time `json:"active_since,omitempty"`
}

// WithBudget accounts for execution across separate commands, excluding coding time.
// An interrupted unclosed ledger requires explicit inspection instead of a reset.
func WithBudget(ctx context.Context, root string, limit time.Duration, action func(context.Context) error) error {
	if root == "" {
		return action(ctx)
	}
	if err := requireOwnedDirectory(root); err != nil {
		return err
	}
	path := filepath.Join(root, "budget.json")
	ledger := budgetLedger{LimitNS: limit.Nanoseconds()}
	raw, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(raw, &ledger); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !ledger.ActiveSince.IsZero() {
		return errors.New("campaign ledger has an unfinished command; inspect retained state before resuming")
	}
	remaining := time.Duration(ledger.LimitNS - ledger.UsedNS)
	if remaining <= 0 {
		return errors.New("campaign runtime budget exhausted")
	}
	ledger.ActiveSince = time.Now().UTC()
	if err = WriteJSON(path, ledger); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	started := time.Now()
	actionErr := action(ctx)
	ledger.UsedNS += time.Since(started).Nanoseconds()
	ledger.ActiveSince = time.Time{}
	return errors.Join(actionErr, WriteJSON(path, ledger))
}

func rssBytes() (int64, error) {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && fields[0] == "VmRSS:" {
			n, parseErr := strconv.ParseInt(fields[1], 10, 64)
			return n * 1024, parseErr
		}
	}
	return 0, fmt.Errorf("VmRSS unavailable")
}

func watchResources(ctx context.Context, cancel context.CancelCauseFunc, root string, cfg Config) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := resourceCheck(root, cfg, 0); err != nil {
				cancel(err)
				return
			}
			if rss, err := rssBytes(); err == nil && rss > cfg.MaxWorkerRSS {
				cancel(fmt.Errorf("worker RSS %d exceeds limit %d", rss, cfg.MaxWorkerRSS))
				return
			}
		}
	}
}
