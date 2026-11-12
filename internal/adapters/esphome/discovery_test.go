package esphome //nolint:testpackage // Exercises the discovery policy and worker supervisor with controlled sources.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func discoveredFixture(t *testing.T, address string) DiscoveryRecord {
	t.Helper()
	mac, err := ParseMAC("02:00:00:00:00:01")
	if err != nil {
		t.Fatal(err)
	}
	return DiscoveryRecord{MAC: mac, Address: address, Port: 6053}
}

func TestDiscoveryTransportAndBlocklistPolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                                        string
		global, individual, key, encrypted, blocked bool
		want                                        string
		plaintext                                   bool
	}{
		{name: "secure default", want: reasonPlaintextRejected},
		{name: "global plaintext", global: true, plaintext: true},
		{name: "individual plaintext", individual: true, plaintext: true},
		{name: "encrypted requires key", global: true, encrypted: true, want: reasonEncryptionKeyRequired},
		{name: "explicit key uses Noise", global: true, key: true},
		{name: "encrypted with key", global: true, key: true, encrypted: true},
		{name: "blocklist wins", global: true, key: true, encrypted: true, blocked: true, want: reasonDeviceBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			record := discoveredFixture(t, "192.0.2.10")
			record.Encrypted = tc.encrypted
			config := Config{Discovery: DiscoveryConfig{Enabled: true, AllowInsecurePlaintext: tc.global},
				Devices: []Device{
					{
						MAC:                     record.MAC,
						InsecurePlaintext:       tc.individual,
						EncryptionKeyConfigured: tc.key,
						PSK:                     harnessPSK(),
						Exposure: Exposure{
							Exclude: map[EntityRef]struct{}{{Type: "light", Name: "Lamp"}: {}},
						},
					},
				}}
			if tc.blocked {
				config.Blocklist = map[MAC]struct{}{record.MAC: {}}
			}
			a := &Adapter{config: config}
			device, reason := a.discoveredDevice(record)
			if reason != tc.want {
				t.Fatalf("reason = %s, want %s", reason, tc.want)
			}
			if reason == "" && (device.InsecurePlaintext != tc.plaintext || device.Address != record.Address ||
				device.Port != 6053 || len(device.Exposure.Exclude) != 1) {
				t.Fatalf("resolved device = %+v", device)
			}
		})
	}
}

func TestDiscoveryRejectsInvalidResolvedEndpoints(t *testing.T) {
	t.Parallel()
	a := &Adapter{config: Config{Discovery: DiscoveryConfig{Enabled: true, AllowInsecurePlaintext: true}}}
	for _, address := range []string{"node.local", "", "0.0.0.0", "224.0.0.251", "fe80::1"} {
		if _, reason := a.discoveredDevice(discoveredFixture(t, address)); reason != reasonDeviceUnreachable {
			t.Errorf("accepted unusable address %q: %s", address, reason)
		}
	}
	record := discoveredFixture(t, "192.0.2.1")
	record.Port = 0
	if _, reason := a.discoveredDevice(record); reason != reasonDeviceUnreachable {
		t.Fatal("accepted zero port")
	}
}

type channelDiscovery struct {
	records  chan DiscoveryRecord
	done     chan struct{}
	instance *Adapter
}

func (source *channelDiscovery) Run(ctx context.Context, records chan<- DiscoveryRecord) error {
	defer close(source.done)
	for {
		select {
		case <-ctx.Done():
			return nil
		case record := <-source.records:
			select {
			case records <- record:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func runDiscoveryHarness(
	t *testing.T,
	session *fakeSession,
	dialer *pipeDialer,
	config Config,
) (*channelDiscovery, func() error) {
	t.Helper()
	source := &channelDiscovery{records: make(chan DiscoveryRecord), done: make(chan struct{})}
	instance, err := newAdapter(session, config, slog.New(slog.DiscardHandler), dialer, harnessTiming(), source)
	if err != nil {
		t.Fatal(err)
	}
	source.instance = instance
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan error, 1)
	go func() { finished <- instance.Run(ctx) }()
	stop := sync.OnceValue(func() error { cancel(); return <-finished })
	t.Cleanup(func() {
		if stopErr := stop(); stopErr != nil {
			t.Error(stopErr)
		}
	})
	return source, stop
}

func TestDiscoveryRegistersOnceAndAddressChangePreservesIdentityAndJoinsOldWorker(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		record := discoveredFixture(t, "192.0.2.10")
		session := &fakeSession{}
		dialer := newPipeDialer()
		device := testDevice(record.MAC.String())
		device.Plaintext = true
		dialer.devices[record.Address] = device
		dialer.devices["192.0.2.20"] = device
		source, stop := runDiscoveryHarness(t, session, dialer,
			Config{Discovery: DiscoveryConfig{Enabled: true, AllowInsecurePlaintext: true}})
		source.records <- record
		synctest.Wait()
		if got := len(callsOfKind(session.snapshot(), "register")); got != 1 {
			t.Fatalf("registrations = %d", got)
		}
		id := entityIDFor(t, session, "0/sensor/Temperature")
		old := dialer.latest(t, record.Address)
		source.records <- record
		synctest.Wait()
		if got := len(callsOfKind(session.snapshot(), "register")); got != 1 {
			t.Fatalf("duplicate registered %d times", got)
		}
		record.Address = "192.0.2.20"
		source.records <- record
		synctest.Wait()
		if got := len(callsOfKind(session.snapshot(), "register")); got != 2 {
			t.Fatalf("new address registrations = %d", got)
		}
		if entityIDFor(t, session, "0/sensor/Temperature") != id ||
			lastAvailability(session.snapshot())[id].Status != adapter.AvailabilityAvailable {
			t.Fatal("address change lost identity or availability")
		}
		select {
		case <-old.Done():
		default:
			t.Fatal("old connection not closed before replacement")
		}
		if err := stop(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-source.done:
		default:
			t.Fatal("discovery not joined")
		}
		select {
		case <-dialer.latest(t, record.Address).Done():
		default:
			t.Fatal("device not joined")
		}
	})
}

func TestDiscoveryReconcilesOwnedEntitiesAndNeverDowngradesEncryptedAdvertisement(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		record := discoveredFixture(t, "192.0.2.10")
		session := &fakeSession{
			owned: []adapter.OwnedMapping{{BindingKey: record.MAC.BindingKey(), EntityKey: "old", EntityID: "ent_old"}},
		}
		dialer := newPipeDialer()
		device := testDevice(record.MAC.String())
		device.Plaintext = true
		dialer.devices[record.Address] = device
		source, _ := runDiscoveryHarness(t, session, dialer,
			Config{Discovery: DiscoveryConfig{Enabled: true, AllowInsecurePlaintext: true}})
		synctest.Wait()
		if lastAvailability(session.snapshot())["ent_old"].ReasonCode != reasonDiscoveryPending {
			t.Fatal("not pending rediscovery")
		}
		source.records <- record
		synctest.Wait()
		id := entityIDFor(t, session, "0/sensor/Temperature")
		record.Encrypted = true
		source.records <- record
		synctest.Wait()
		if lastAvailability(session.snapshot())[id].ReasonCode != reasonEncryptionKeyRequired {
			t.Fatal("encrypted device not gated")
		}
		if got := len(callsOfKind(session.snapshot(), "register")); got != 1 {
			t.Fatalf("downgrade registered: %d", got)
		}
		unavailableReports := 0
		for _, entry := range callsOfKind(session.snapshot(), "availability") {
			for _, report := range entry.availability {
				if report.EntityID == id && report.ReasonCode == reasonEncryptionKeyRequired {
					unavailableReports++
				}
			}
		}
		if unavailableReports != 1 {
			t.Errorf("same transition reported %d times, want 1", unavailableReports)
		}
		responder, done := command(t, source.instance, session, id, "cmd_skipped", `{"value":false}`)
		synctest.Wait()
		if err := <-done; err != nil || responder.result() != "unavailable" ||
			responder.message != "ESPHome device is awaiting discovery" {
			t.Errorf("stopped-device command = %s %q, %v", responder.result(), responder.message, err)
		}
		select {
		case <-dialer.latest(t, record.Address).Done():
		default:
			t.Fatal("plaintext connection retained after encryption advertisement")
		}
	})
}

func TestDiscoveryBlocklistAndIdentityChecksBeforeRegistration(t *testing.T) {
	t.Parallel()
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "identity mismatch", true: "blocklist"}[blocked], func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				record := discoveredFixture(t, "192.0.2.10")
				session := &fakeSession{
					owned: []adapter.OwnedMapping{
						{BindingKey: record.MAC.BindingKey(), EntityKey: "old", EntityID: "ent_old"},
					},
				}
				dialer := newPipeDialer()
				device := testDevice("02:00:00:00:00:02")
				device.Plaintext = true
				dialer.devices[record.Address] = device
				config := Config{Discovery: DiscoveryConfig{Enabled: true, AllowInsecurePlaintext: true}}
				want := reasonDeviceIdentityMismatch
				if blocked {
					config.Blocklist = map[MAC]struct{}{record.MAC: {}}
					want = reasonDeviceBlocked
				}
				source, _ := runDiscoveryHarness(t, session, dialer, config)
				source.records <- record
				synctest.Wait()
				if len(callsOfKind(session.snapshot(), "register")) != 0 {
					t.Fatal("unapproved identity registered")
				}
				if lastAvailability(session.snapshot())["ent_old"].ReasonCode != want {
					t.Fatalf("want %s", want)
				}
			})
		})
	}
}

type failedDiscovery struct{ err error }

func (source failedDiscovery) Run(context.Context, chan<- DiscoveryRecord) error { return source.err }

func TestDiscoveryFailureStopsAdapter(t *testing.T) {
	t.Parallel()
	failure := errors.New("multicast socket failed")
	var logs bytes.Buffer
	instance, err := newAdapter(&fakeSession{}, Config{Discovery: DiscoveryConfig{Enabled: true}},
		slog.New(slog.NewJSONHandler(&logs, nil)), newPipeDialer(), harnessTiming(), failedDiscovery{err: failure})
	if err != nil {
		t.Fatal(err)
	}
	if err = instance.Run(t.Context()); !errors.Is(err, failure) {
		t.Fatalf("discovery failure = %v", err)
	}
	if !strings.Contains(logs.String(), `"error_code":"discovery_failed"`) ||
		!strings.Contains(
			logs.String(),
			`"operation":"discovery"`,
		) || strings.Contains(logs.String(), failure.Error()) {
		t.Fatalf("unsafe or unclassified diagnostic: %s", logs.String())
	}
}

func TestDiscoveryPendingOwnedCommandIsUnavailable(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		record := discoveredFixture(t, "192.0.2.10")
		session := &fakeSession{
			owned: []adapter.OwnedMapping{{BindingKey: record.MAC.BindingKey(), EntityKey: "old", EntityID: "ent_old"}},
		}
		source, _ := runDiscoveryHarness(t, session, newPipeDialer(), Config{Discovery: DiscoveryConfig{Enabled: true}})
		synctest.Wait()
		responder, done := command(t, source.instance, session, "ent_old", "cmd_pending", `{"value":false}`)
		synctest.Wait()
		if err := <-done; err != nil || responder.result() != "unavailable" {
			t.Fatalf("pending command = %s, %v", responder.result(), err)
		}
	})
}

func TestDiscoveryConfiguredKeyNeverFallsBackToPlaintext(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		record := discoveredFixture(t, "192.0.2.10")
		session := &fakeSession{
			owned: []adapter.OwnedMapping{{BindingKey: record.MAC.BindingKey(), EntityKey: "old", EntityID: "ent_old"}},
		}
		dialer := newPipeDialer()
		device := testDevice(record.MAC.String())
		device.Plaintext = true
		dialer.devices[record.Address] = device
		source, _ := runDiscoveryHarness(t, session, dialer, Config{
			Discovery: DiscoveryConfig{Enabled: true, AllowInsecurePlaintext: true},
			Devices:   []Device{{MAC: record.MAC, PSK: harnessPSK(), EncryptionKeyConfigured: true}},
		})
		source.records <- record // Omitted encryption TXT must not override configured credentials.
		synctest.Wait()
		if len(callsOfKind(session.snapshot(), "register")) != 0 {
			t.Fatal("configured key downgraded")
		}
		if lastAvailability(session.snapshot())["ent_old"].ReasonCode != reasonPlaintextRejected {
			t.Fatal("wrong rejection reason")
		}
	})
}

func TestDiscoveryDoesNotDuplicateOrReplaceStaticEndpoint(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		record := discoveredFixture(t, "192.0.2.20")
		session := &fakeSession{}
		dialer := newPipeDialer()
		dialer.devices["192.0.2.10"] = testDevice(record.MAC.String())
		config := Config{Discovery: DiscoveryConfig{Enabled: true, AllowInsecurePlaintext: true},
			Devices: []Device{harnessDevice(t, record.MAC.String(), "192.0.2.10")}}
		source, _ := runDiscoveryHarness(t, session, dialer, config)
		synctest.Wait()
		source.records <- record
		synctest.Wait()
		if got := len(callsOfKind(session.snapshot(), "register")); got != 1 {
			t.Fatalf("static registrations = %d", got)
		}
		dialer.mu.Lock()
		defer dialer.mu.Unlock()
		if len(dialer.sessions[record.Address]) != 0 {
			t.Fatal("discovery overrode static endpoint")
		}
	})
}

type holdingDialer struct{}

func (holdingDialer) Dial(ctx context.Context, _ nativeapi.DialConfig) (nativeapi.Conn, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestDiscoveryLimitCandidateStartsWhenCapacityIsFreed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		a, err := newAdapter(
			&fakeSession{},
			Config{Discovery: DiscoveryConfig{Enabled: true, AllowInsecurePlaintext: true}},
			slog.New(slog.DiscardHandler),
			holdingDialer{},
			harnessTiming(),
			failedDiscovery{},
		)
		if err != nil {
			t.Fatal(err)
		}
		supervisor := &deviceSupervisor{
			adapter:  a,
			ctx:      ctx,
			active:   map[MAC]managedDevice{},
			owned:    map[string]map[string]string{},
			failures: make(chan error, maximumDevices+1),
			skipped:  map[MAC]string{},
			pending:  map[MAC]Device{},
		}
		defer func() {
			cancel()
			for _, current := range supervisor.active {
				_ = current.handle.Stop(context.Background())
			}
		}()
		for index := range maximumDevices {
			supervisor.start(
				Device{
					MAC:               MAC{2, 0, 0, 0, byte(index), 1},
					Address:           "192.0.2.10",
					Port:              6053,
					InsecurePlaintext: true,
				},
			)
		}
		record := discoveredFixture(t, "192.0.2.20")
		record.MAC = MAC{2, 0, 0, 0, 64, 1}
		if err = supervisor.discovered(record); err != nil {
			t.Fatal(err)
		}
		if len(supervisor.active) != maximumDevices {
			t.Fatal("runtime exceeded limit")
		}
		record.MAC = MAC{2, 0, 0, 0, 0, 1}
		record.Encrypted = true // Missing credentials stop this worker and free a slot.
		if err = supervisor.discovered(record); err != nil {
			t.Fatal(err)
		}
		if _, admitted := supervisor.active[MAC{2, 0, 0, 0, 64, 1}]; !admitted ||
			len(supervisor.active) != maximumDevices {
			t.Fatal("limit-skipped candidate was lost despite newly available capacity")
		}
	})
}
