//go:build integration

package homekit //nolint:testpackage // Exercises the coordinator with a real HAP listener and store.

import (
	"log/slog"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/entitytypes/powerv1"
)

func TestRebuildPublishesTwoExposureChangesInOneHAPGeneration(t *testing.T) {
	t.Parallel()
	socket, err := net.ListenTCP("tcp", &net.TCPAddr{})
	require.NoError(t, err)
	addr := socket.Addr().(*net.TCPAddr)
	require.NoError(t, socket.Close())
	dir := filepath.Join(t.TempDir(), "bridge")
	disk, err := openStore(dir)
	require.NoError(t, err)
	r := &Runtime{
		config: RuntimeConfig{
			DataDir: dir, BridgeName: "Hearth Reload " + strconv.Itoa(addr.Port), ListenPort: addr.Port,
			Interfaces: []string{"lo"}, ResyncInterval: time.Minute,
		},
		disk: disk, logger: slog.New(slog.DiscardHandler),
		inventory: Inventory{ReadAt: time.Now(), Devices: map[string]Device{}, Entities: map[string]Entity{}},
	}
	for _, id := range []string{"a", "b"} {
		r.inventory.Devices[id] = Device{ID: id, Name: id, Kind: "relay"}
		r.inventory.Entities[id] = Entity{ID: id, DeviceID: id, Type: powerv1.TypeID}
	}
	require.NoError(t, r.startHAP(t.Context()))
	defer r.stopHAP()
	before := r.hap
	version := readConfigurationNumber(t, before)
	coordinator := reconciler{r: r}
	var replies []chan mutationResult
	for _, id := range []string{"a", "b"} {
		reply := make(chan mutationResult, 1)
		replies = append(replies, reply)
		coordinator.mutate(t.Context(), bridgeMutation{
			ctx: t.Context(), reply: reply,
			desired: Exposure{DeviceID: id, Kind: KindSwitch, Entities: []RoleAssignment{{Role: "on", EntityID: id}}},
		})
	}
	require.Same(t, before, r.hap)
	for _, reply := range replies {
		require.Empty(t, reply, "an exposure was acknowledged before reload")
	}
	select {
	case <-coordinator.reload:
		coordinator.reloadNow(t.Context())
	case <-time.After(5 * time.Second):
		t.Fatal("exposure changes did not schedule a reload")
	}
	require.NotNil(t, r.hap)
	assert.NotSame(t, before, r.hap)
	assert.False(t, before.active())
	assert.Equal(t, version+1, readConfigurationNumber(t, r.hap))
	require.Len(t, r.accessors, 2)
	require.Nil(t, coordinator.reload, "another reload was scheduled")
	for _, reply := range replies {
		require.Len(t, reply, 1, "no result from the combined reload")
		result := <-reply
		require.NoError(t, result.err)
		require.True(t, result.changed)
	}
}

func readConfigurationNumber(t *testing.T, run *runningHAP) uint64 {
	t.Helper()
	raw, err := run.store.Get("version")
	require.NoError(t, err)
	version, err := strconv.ParseUint(string(raw), 10, 16)
	require.NoError(t, err)
	return version
}
