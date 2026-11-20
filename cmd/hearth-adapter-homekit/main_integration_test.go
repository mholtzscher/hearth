//go:build integration

package main

import (
	"fmt"
	"testing"

	"github.com/mholtzscher/hearth/cmd/internal/cmdtest"
)

func TestHomeKitExecutableCancelsDuringStartup(t *testing.T) {
	t.Parallel()
	binary := cmdtest.Build(t, ".")
	natsURL := cmdtest.StartProcessNATS(t)
	config := fmt.Sprintf("adapter_id: homekit-test\nnats_url: %q\ncore_url: %q\ndata_dir: %q\n",
		natsURL, "http://127.0.0.1:"+cmdtest.FreeLoopbackPort(t), t.TempDir())
	cmdtest.CheckStartupCancellation(t, binary, config, "hearth-adapter-homekit")
}
