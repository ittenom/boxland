package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/rueidis"

	"boxland/server/internal/maps"
)

// InstanceManager owns every live MapInstance in this process. The WS
// gateway, sandbox launcher, and "Push to Live" pipeline all reach
// instances through here.
//
// Concurrency: GetOrCreate is the only mutating method and it
// synchronizes via a per-key sync.Mutex so two simultaneous JoinMaps for
// the same instance don't double-build it.
type InstanceManager struct {
	pool        *pgxpool.Pool
	redis       rueidis.Client
	mapsService *maps.Service

	mu        sync.Mutex
	instances map[string]*MapInstance // keyed by instance_id
	building  map[string]chan struct{} // in-flight construction barriers
}

// NewInstanceManager constructs the registry.
func NewInstanceManager(pool *pgxpool.Pool, redis rueidis.Client, mapsService *maps.Service) *InstanceManager {
	return &InstanceManager{
		pool:        pool,
		redis:       redis,
		mapsService: mapsService,
		instances:   make(map[string]*MapInstance),
		building:    make(map[string]chan struct{}),
	}
}

// GetOrCreate returns an existing instance or builds a new one. Safe for
// concurrent callers; only the first creator pays the recovery cost.
//
// instanceID conventions (PLAN.md §1 "Sandbox vs. live"):
//   * "live:{map_id}:0"           -- the canonical shared instance
//   * "live:{map_id}:party:{n}"   -- per-party shared instance
//   * "live:{map_id}:user:{n}"    -- per-user instance
//   * "sandbox:{designer_id}:{map_id}"  -- sandbox instance
func (mgr *InstanceManager) GetOrCreate(ctx context.Context, mapID uint32, instanceID string) (*MapInstance, error) {
	mgr.mu.Lock()
	if mi, ok := mgr.instances[instanceID]; ok {
		mgr.mu.Unlock()
		return mi, nil
	}
	if ch, ok := mgr.building[instanceID]; ok {
		// Another goroutine is mid-build; wait for it.
		mgr.mu.Unlock()
		<-ch
		mgr.mu.Lock()
		mi := mgr.instances[instanceID]
		mgr.mu.Unlock()
		if mi == nil {
			return nil, fmt.Errorf("runtime: instance %q failed to construct", instanceID)
		}
		return mi, nil
	}
	ch := make(chan struct{})
	mgr.building[instanceID] = ch
	mgr.mu.Unlock()

	mi, err := NewMapInstance(ctx, mgr.pool, mgr.redis, mgr.mapsService, mapID, instanceID)

	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	delete(mgr.building, instanceID)
	close(ch)
	if err != nil {
		return nil, fmt.Errorf("runtime: build %q: %w", instanceID, err)
	}
	mgr.instances[instanceID] = mi
	return mi, nil
}

// Get returns an existing instance or nil. Does not create.
func (mgr *InstanceManager) Get(instanceID string) *MapInstance {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.instances[instanceID]
}

// All returns a snapshot of every live instance. The returned slice is
// owned by the caller; safe to iterate while instances close.
func (mgr *InstanceManager) All() []*MapInstance {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	out := make([]*MapInstance, 0, len(mgr.instances))
	for _, mi := range mgr.instances {
		out = append(out, mi)
	}
	return out
}
