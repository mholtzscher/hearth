package hap

import (
	dnslog "github.com/brutella/hap/internal/dnssd/log"
	"github.com/brutella/hap/log"
)

// DisableLogging suppresses HAP and DNS-SD package logs process-wide. Call it
// before NewServer to suppress logs emitted during setup as well as serving.
func DisableLogging() {
	log.Debug.Disable()
	log.Info.Disable()
	dnslog.Debug.Disable()
	dnslog.Info.Disable()
}
