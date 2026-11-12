package esphome

import "sync"

// router maps canonical Entity IDs to the worker that owns their device. Workers update their own Entities after
// each registration; HandleCommand reads concurrently.
type router struct {
	mu       sync.RWMutex
	ready    bool
	byEntity map[string]*worker
}

func newRouter() *router {
	return &router{byEntity: map[string]*worker{}}
}

// seedOwned retains command ownership while a previously registered device awaits rediscovery.
func (r *router) seedOwned(owned map[string]map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entities := range owned {
		for _, entityID := range entities {
			r.byEntity[entityID] = nil
		}
	}
	r.ready = true
}

// start installs each worker's startup-owned Entities and admits Commands.
func (r *router) start(workers []*worker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, owner := range workers {
		for _, entityID := range owner.owned {
			r.byEntity[entityID] = owner
		}
	}
	r.ready = true
}

// assign routes entityIDs to owner. Registration never moves an Entity between devices, so it only adds.
func (r *router) assign(owner *worker, entityIDs []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entityID := range entityIDs {
		r.byEntity[entityID] = owner
	}
}

func (r *router) lookup(entityID string) (*worker, bool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	owner, found := r.byEntity[entityID]
	return owner, found, r.ready
}
