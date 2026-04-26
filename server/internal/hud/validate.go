package hud

import (
	"context"
	"errors"
	"fmt"

	"boxland/server/internal/automations"
	"boxland/server/internal/flags"
)

// ResolveDeps is the cross-resource bag the publish-time validator
// consults. Only the bits we actually need; kept narrow so tests can
// supply fakes easily.
//
// FlagKeys: every key currently defined in map_flags for the realm.
// ActionGroupNames: every name in map_action_groups for the realm.
// All compared case-sensitively; the storage layer normalizes on write.
type ResolveDeps struct {
	FlagKeys         map[string]flags.Kind
	ActionGroupNames map[string]struct{}
}

// LoadResolveDeps assembles ResolveDeps for a realm in two queries.
// Used by the publish handler. No N+1 — one query per dependency
// kind, each scoped by map_id.
func LoadResolveDeps(ctx context.Context, mapID int64, fs *flags.Service, ar AutoResolver) (ResolveDeps, error) {
	out := ResolveDeps{
		FlagKeys:         map[string]flags.Kind{},
		ActionGroupNames: map[string]struct{}{},
	}
	if fs != nil {
		all, err := fs.LoadAll(ctx, mapID)
		if err != nil {
			return out, fmt.Errorf("hud: load flags: %w", err)
		}
		for _, f := range all {
			out.FlagKeys[f.Key] = f.Kind
		}
	}
	if ar != nil {
		names, err := ar.ListActionGroupNames(ctx, mapID)
		if err != nil {
			return out, fmt.Errorf("hud: load action groups: %w", err)
		}
		for _, n := range names {
			out.ActionGroupNames[n] = struct{}{}
		}
	}
	return out, nil
}

// AutoResolver lists the names of action groups for a given map. Tiny
// interface so tests can supply a fixture without depending on the
// automations package's repo type.
type AutoResolver interface {
	ListActionGroupNames(ctx context.Context, mapID int64) ([]string, error)
}

// ResolveAndValidate runs structural Validate first, then walks the
// layout cross-checking every binding + every button action_group
// against deps. Returns nil if the layout is internally consistent
// AND every external reference resolves.
//
// Designers see "flag 'gold' not defined" rather than "binding error"
// — actionable error messages, not type-system jargon.
func (l Layout) ResolveAndValidate(reg *Registry, deps ResolveDeps) error {
	if err := l.Validate(reg); err != nil {
		return err
	}
	for _, anchor := range AllAnchors {
		stack, ok := l.Anchors[anchor]
		if !ok {
			continue
		}
		for i, w := range stack.Widgets {
			cfg, err := reg.Decode(w.Type, w.Config)
			if err != nil {
				return fmt.Errorf("hud: anchor %q widget %d: %w", anchor, i, err)
			}
			if bp, ok := cfg.(BindingProvider); ok {
				for _, ref := range bp.Bindings() {
					if err := resolveBinding(ref, deps); err != nil {
						return fmt.Errorf("hud: anchor %q widget %d: %w", anchor, i, err)
					}
				}
			}
			if btn, ok := cfg.(*ButtonConfig); ok {
				if _, present := deps.ActionGroupNames[btn.ActionGroup]; !present {
					return fmt.Errorf("hud: anchor %q widget %d: action group %q not defined for this realm",
						anchor, i, btn.ActionGroup)
				}
			}
		}
	}
	return nil
}

// resolveBinding checks that a parsed BindingRef points at something
// that exists in the realm.
func resolveBinding(ref BindingRef, deps ResolveDeps) error {
	switch ref.Kind {
	case BindFlag:
		if _, ok := deps.FlagKeys[ref.Key]; !ok {
			return fmt.Errorf("flag %q not defined for this realm", ref.Key)
		}
	case BindEntity:
		// Entity resources are validated structurally (fixed enum); the
		// "host" entity is guaranteed by the player session. No DB
		// check needed at this layer.
	case BindTime:
		// Channels are a closed set, validated at parse time.
	default:
		return errors.New("unknown binding kind")
	}
	return nil
}

// Compile-time assertion that automations is imported even when no
// Conditions are present (we still depend on its types via Layout).
var _ = automations.CondAnd
