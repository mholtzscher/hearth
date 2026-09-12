package devices

import "sync"

// commandTransitionStripeCount is the fixed number of per-Command transition
// stripes. A fixed set bounds memory for any number of Commands; unrelated
// Commands that hash to one stripe serialize conservatively instead.
const commandTransitionStripeCount = 64

// commandTransitionStripes keeps one Command's durable transition order while
// its fact is enqueued. Every post-creation transition path acquires the
// stripe before entering its SQLite transaction and holds it through fact
// enqueue, so published facts for one Command follow durable transition order
// even when an Observation-driven satisfaction races acceptance. Each path
// acquires at most one stripe, and always before SQLite, so no lock ordering
// cycle exists across Commands.
type commandTransitionStripes struct {
	stripes [commandTransitionStripeCount]sync.Mutex
}

// lock acquires the stripe owning id and returns the matching unlock. Callers
// must release exactly once.
func (stripes *commandTransitionStripes) lock(id CommandID) func() {
	stripe := &stripes.stripes[commandTransitionStripeIndex(id)]
	stripe.Lock()
	return stripe.Unlock
}

// commandTransitionStripeIndex hashes one canonical Command ID onto a stripe.
// It is deliberately stable across processes and never randomizes, so the
// transition order of one Command cannot depend on map iteration or process
// startup.
func commandTransitionStripeIndex(id CommandID) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	hash := uint64(offset64)
	for index := range len(id) {
		hash ^= uint64(id[index])
		hash *= prime64
	}
	return hash % commandTransitionStripeCount
}
