package homekit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
)

const hapStartupLimit = 8 * time.Second

type runningHAP struct {
	store  hap.Store
	setup  SetupIdentity
	epoch  uint64
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func launchHAP(
	ctx context.Context,
	config RuntimeConfig,
	data storeFile,
	children []*accessory.A,
) (*runningHAP, error) {
	hap.DisableLogging()
	disk, err := openHAPStore(pairingPath(config.DataDir, data.PairEpoch))
	if err != nil {
		return nil, err
	}
	root := accessory.NewBridge(
		accessory.Info{Name: config.BridgeName, Manufacturer: "Hearth", Model: "HomeKit Bridge"},
	)
	root.Id = 1
	if err = assignServiceIDs(root.Info.S, 0); err != nil {
		return nil, err
	}
	server, err := hap.NewServer(disk, root.A, children...)
	if err != nil {
		return nil, fmt.Errorf("prepare HAP bridge: %w", err)
	}
	server.Pin, server.SetupId = data.Setup.Code, data.Setup.ID
	server.Addr = net.JoinHostPort("", strconv.Itoa(config.ListenPort))
	server.Ifaces = config.Interfaces
	ctx, cancel := context.WithCancel(ctx)
	run := &runningHAP{store: disk, setup: data.Setup, epoch: data.PairEpoch, cancel: cancel, done: make(chan struct{})}
	ready := make(chan struct{})
	server.Ready = func() { close(ready) }
	go func() {
		defer close(run.done)
		run.err = server.ListenAndServe(ctx)
	}()
	timeout := time.NewTimer(hapStartupLimit)
	defer timeout.Stop()
	select {
	case <-ready:
		return run, nil
	case <-run.done:
		cancel()
		if run.err == nil {
			return nil, errors.New("HAP server exited before it was ready")
		}
		return nil, run.err
	case <-ctx.Done():
		run.stop()
		return nil, ctx.Err()
	case <-timeout.C:
		run.stop()
		return nil, errors.New("HAP server did not become ready")
	}
}

func (run *runningHAP) stop() {
	run.cancel()
	<-run.done
}

func (run *runningHAP) active() bool {
	select {
	case <-run.done:
		return false
	default:
		return true
	}
}

func (run *runningHAP) controllerCount() (int, error) {
	keys, err := run.store.KeysWithSuffix(".pairing")
	if err != nil {
		return 0, err
	}
	for _, key := range keys {
		raw, readErr := run.store.Get(key)
		var paired hap.Pairing
		if readErr != nil || json.Unmarshal(raw, &paired) != nil || paired.Name == "" {
			return 0, errors.New("could not confirm HAP controller records")
		}
	}
	return len(keys), nil
}

func pairingPath(dir string, epoch uint64) string {
	if epoch == 0 {
		return filepath.Join(dir, "hap")
	}
	return filepath.Join(dir, "hap-"+strconv.FormatUint(epoch, 10))
}

func openHAPStore(path string) (hap.Store, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return nil, fmt.Errorf("create private HAP store: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect HAP store: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("HAP store must be a private directory")
	}
	return hap.NewFsStore(path), nil
}
