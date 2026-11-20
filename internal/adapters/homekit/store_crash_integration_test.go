//go:build integration

package homekit //nolint:testpackage // Kills the real store process at its pre-rename boundary.

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreSurvivesKilledWriterBeforeRename(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "store")
	s, err := openStore(dir)
	require.NoError(t, err)
	exposure, err := s.expose(Exposure{DeviceID: "original", Kind: KindSwitch})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run", "^TestHomeKitStoreCrashChild$")
	child.Dir = dir
	child.Env = append(os.Environ(), "HEARTH_HOMEKIT_TEST_CRASH_CHILD=1")
	stdout, err := child.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, child.Start())
	joined := false
	defer func() {
		if !joined {
			cancel()
			_ = child.Wait()
		}
	}()
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "waiting before rename\n", line)
	require.NoError(t, child.Process.Kill())
	err = child.Wait()
	joined = true
	require.Error(t, err)
	reopened, err := openStore(dir)
	require.NoError(t, err)
	assert.Equal(t, exposure.AID, reopened.snapshot().Exposures["original"].AID)
	assert.NotContains(t, reopened.snapshot().Exposures, "interrupted")
}

func TestHomeKitStoreCrashChild(t *testing.T) {
	t.Parallel()
	if os.Getenv("HEARTH_HOMEKIT_TEST_CRASH_CHILD") != "1" {
		return
	}
	dir, err := os.Getwd()
	require.NoError(t, err)
	s, err := openStore(dir)
	require.NoError(t, err)
	s.beforeRename = func() error {
		_, writeErr := os.Stdout.WriteString("waiting before rename\n")
		require.NoError(t, writeErr)
		<-t.Context().Done()
		return t.Context().Err()
	}
	_, _ = s.expose(Exposure{DeviceID: "interrupted", Kind: KindSwitch})
	t.Fatal("expected parent to kill the child before rename")
}
