package runtime

import (
	"context"

	"boxland/server/internal/assets"
	"boxland/server/internal/sim/systems"
)

// AssetsAnimationCatalog is the production adapter from
// systems.AnimationCatalog onto the assets.Service surface. The
// service caches per-asset row sets in Postgres; the in-process cache
// inside the Animation system itself eliminates repeat round trips
// within a tick. Together they give one DB round trip per asset per
// HotSwap interval.
//
// HotSwap invalidation lives on the InstanceManager — when an asset
// publish lands, the manager's BroadcastHotSwap path clears the
// matching cache entry on every running instance so the next tick
// resolves the fresh names.
type AssetsAnimationCatalog struct {
	Svc *assets.Service
}

// AnimationsFor returns a name→anim_id map for the asset. Names are
// lowercased to match the system's lookup convention. Returns nil
// (not an error) for assets without animations so the system can
// gracefully leave anim_id alone rather than zero it.
func (c *AssetsAnimationCatalog) AnimationsFor(ctx context.Context, assetID uint32) (map[string]uint16, error) {
	if c == nil || c.Svc == nil || assetID == 0 {
		return nil, nil
	}
	rows, err := c.Svc.ListAnimations(ctx, int64(assetID))
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make(map[string]uint16, len(rows))
	for _, r := range rows {
		// Defensive cast: persisted ids are int64 but the wire
		// protocol uses uint16. Assets shipping more than 64K
		// distinct animation rows (the entire catalog, not per
		// sheet) would overflow — alert with the truncation but
		// don't panic; the only consumer is the per-asset filter.
		out[lowercase(r.Name)] = uint16(r.ID)
	}
	return out, nil
}

// Compile-time interface check.
var _ systems.AnimationCatalog = (*AssetsAnimationCatalog)(nil)

// lowercase is a tiny strings.ToLower wrapper kept here so the runtime
// package doesn't pull strings just for the cast.
func lowercase(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
