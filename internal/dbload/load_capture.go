package dbload

import (
	"errors"
	"sync"
	"time"
)

type loadEvidence struct {
	start, end, peak float64
	err              error
}

func captureBlockLoad() func() loadEvidence {
	first, startErr := HostLoad()
	stop := make(chan struct{})
	done := make(chan loadEvidence, 1)
	go func() {
		observed := loadEvidence{start: first, peak: first, err: startErr}
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				value, err := HostLoad()
				observed.peak = max(observed.peak, value)
				observed.err = errors.Join(observed.err, err)
			case <-stop:
				last, err := HostLoad()
				observed.end = last
				observed.peak = max(observed.peak, last)
				observed.err = errors.Join(observed.err, err)
				done <- observed
				return
			}
		}
	}()
	return sync.OnceValue(func() loadEvidence { close(stop); return <-done })
}
