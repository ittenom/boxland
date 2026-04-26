package hud

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// BindingKind enumerates what a HUD binding-ref points at.
//
// entity:<who>:<resource>           — `who` is "host" (the player connection
//                                      owns this entity) or "self" (alias).
//                                      `resource` is one of:
//                                          hp_pct        — uint8 0..255
//                                          nameplate     — string
//                                          variant_id    — uint16
//                                          facing        — uint8
//                                          anim_id       — uint16
//                                          anim_frame    — uint8
//                                          tint          — color
//                                          x | y         — int32
// entity:<who>:resource:<key>       — designer-defined per-entity resource
//                                      (4-part). Used by the character
//                                      generator to surface bake-time
//                                      stats (e.g. focus, talent_points).
// character:<id>:stat:<key>         — directly addressed character stat
//                                      (4-part). Used when a HUD widget
//                                      points at a specific character
//                                      regardless of the host's id.
// flag:<key>                        — per-realm flag from server/internal/flags
// time:<channel>                    — realm_clock | wall
//
// Format chosen so the bindings double as keys in a Map<string, value>
// on the client. The full string is the canonical id.
type BindingKind string

const (
	BindEntity    BindingKind = "entity"
	BindFlag      BindingKind = "flag"
	BindTime      BindingKind = "time"
	BindCharacter BindingKind = "character"
)

// BindingRef is one parsed binding string.
//
// 2-part forms (flag, time) populate Kind + Key only.
// 3-part forms (entity:host:hp_pct) populate Kind + Key + Sub.
// 4-part forms (entity:host:resource:focus, character:42:stat:might)
// populate all four fields including Detail.
type BindingRef struct {
	Kind BindingKind
	// Key meaning depends on Kind:
	//   entity    → who (e.g. "host")
	//   flag      → flag key (e.g. "gold")
	//   time      → channel (e.g. "realm_clock")
	//   character → numeric character id as a decimal string
	Key string
	// Sub is the 3rd path segment (resource name for entity, "stat"
	// for character). Empty for 2-part forms.
	Sub string
	// Detail is the 4th path segment, populated only for 4-part forms
	// (entity:host:resource:<key>, character:<id>:stat:<key>). Empty
	// for 2/3-part forms — keeps `String()` round-trip stable for old
	// bindings.
	Detail string
}

// String returns the canonical "kind:key[:sub[:detail]]" string. Stable
// so the client can use it as a Map key. Old 2/3-part bindings round-
// trip byte-identically because Detail is empty by default.
func (b BindingRef) String() string {
	switch {
	case b.Detail != "":
		return string(b.Kind) + ":" + b.Key + ":" + b.Sub + ":" + b.Detail
	case b.Sub != "":
		return string(b.Kind) + ":" + b.Key + ":" + b.Sub
	default:
		return string(b.Kind) + ":" + b.Key
	}
}

// ParseBinding parses an "entity:host:hp_pct" / "flag:gold" /
// "time:realm_clock" / "entity:host:resource:focus" / "character:42:stat:might"
// style ref into a BindingRef. Empty string returns an error so callers
// can distinguish "no binding" (don't call ParseBinding) from "bad binding".
func ParseBinding(s string) (BindingRef, error) {
	if s == "" {
		return BindingRef{}, errors.New("binding: empty")
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 4 {
		return BindingRef{}, fmt.Errorf("binding: %q wrong shape (want kind:key[:sub[:detail]])", s)
	}
	kind := BindingKind(parts[0])
	switch kind {
	case BindEntity:
		// 3-part: entity:<who>:<resource> from the closed set.
		// 4-part: entity:<who>:resource:<name> for designer-defined
		// per-entity resources (replaces the legacy single-string
		// "resource:<name>" placeholder, which never actually parsed
		// because strings.Split splits on every colon).
		if len(parts) == 3 {
			if !validEntityWho(parts[1]) {
				return BindingRef{}, fmt.Errorf("binding: %q entity who %q (want host|self)", s, parts[1])
			}
			if !validEntityShortResource(parts[2]) {
				return BindingRef{}, fmt.Errorf("binding: %q entity resource %q not recognized", s, parts[2])
			}
			return BindingRef{Kind: BindEntity, Key: parts[1], Sub: parts[2]}, nil
		}
		if len(parts) == 4 {
			if !validEntityWho(parts[1]) {
				return BindingRef{}, fmt.Errorf("binding: %q entity who %q (want host|self)", s, parts[1])
			}
			if parts[2] != "resource" {
				return BindingRef{}, fmt.Errorf("binding: %q 4-part entity must be entity:<who>:resource:<key>", s)
			}
			if err := validateBindingKey(parts[3]); err != nil {
				return BindingRef{}, fmt.Errorf("binding: %q resource key: %w", s, err)
			}
			return BindingRef{Kind: BindEntity, Key: parts[1], Sub: "resource", Detail: parts[3]}, nil
		}
		return BindingRef{}, fmt.Errorf("binding: %q entity needs sub (e.g. entity:host:hp_pct)", s)
	case BindCharacter:
		// character:<id>:stat:<key> — points at a specific character's
		// resolved stat value. <id> is the character's numeric id (so
		// it survives renames); the runtime maps id -> {stat -> int}
		// after the next bake.
		if len(parts) != 4 {
			return BindingRef{}, fmt.Errorf("binding: %q character must be character:<id>:stat:<key>", s)
		}
		if _, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
			return BindingRef{}, fmt.Errorf("binding: %q character id %q not numeric", s, parts[1])
		}
		if parts[2] != "stat" {
			return BindingRef{}, fmt.Errorf("binding: %q character 3rd segment must be 'stat'", s)
		}
		if err := validateBindingKey(parts[3]); err != nil {
			return BindingRef{}, fmt.Errorf("binding: %q stat key: %w", s, err)
		}
		return BindingRef{Kind: BindCharacter, Key: parts[1], Sub: "stat", Detail: parts[3]}, nil
	case BindFlag:
		if len(parts) != 2 {
			return BindingRef{}, fmt.Errorf("binding: %q flag uses kind:key (no sub)", s)
		}
		if err := validateBindingKey(parts[1]); err != nil {
			return BindingRef{}, err
		}
		return BindingRef{Kind: BindFlag, Key: parts[1]}, nil
	case BindTime:
		if len(parts) != 2 {
			return BindingRef{}, fmt.Errorf("binding: %q time uses kind:channel", s)
		}
		switch parts[1] {
		case "realm_clock", "wall":
			return BindingRef{Kind: BindTime, Key: parts[1]}, nil
		default:
			return BindingRef{}, fmt.Errorf("binding: %q time channel must be realm_clock|wall", s)
		}
	default:
		return BindingRef{}, fmt.Errorf("binding: %q unknown kind %q", s, kind)
	}
}

func validEntityWho(s string) bool {
	switch s {
	case "host", "self":
		return true
	}
	return false
}

// validEntityShortResource is the closed set of fields the wire
// EntityState exposes via the 3-part binding shape. Per-entity
// designer-defined resources (e.g. "focus", "talent_points") use the
// 4-part `entity:<who>:resource:<key>` shape and are validated
// separately. Mirrored on the client so the binding-picker can list
// the closed set.
func validEntityShortResource(s string) bool {
	switch s {
	case "hp_pct", "nameplate", "variant_id", "facing", "anim_id", "anim_frame", "tint", "x", "y":
		return true
	}
	return false
}

func validateBindingKey(k string) error {
	if k == "" {
		return errors.New("binding: empty key")
	}
	if len(k) > MaxKeyLen {
		return fmt.Errorf("binding: key %q exceeds %d chars", k, MaxKeyLen)
	}
	// Match flags.validateKey's character class: lowercase letters,
	// digits, underscore. Keeps URLs + form ids safe.
	for i, r := range k {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
		if !ok {
			return fmt.Errorf("binding: key %q invalid char at %d", k, i)
		}
	}
	return nil
}

// BindingProvider is implemented by widget configs that bind to live
// data. The Layout walker collects every binding referenced by every
// widget so the broadcaster can build the binding-id table once.
type BindingProvider interface {
	// Bindings returns every BindingRef this widget config depends on.
	// Widgets with no bindings (e.g. dialog_frame) need not implement
	// the interface.
	Bindings() []BindingRef
}

// ParseTemplateBindings scans a "Gold: {flag:gold} / {entity:host:hp_pct}"
// template for {kind:key[:sub[:detail]]} substitutions and returns the
// parsed refs. Used by the text_label widget. Malformed substitutions
// error at validate time so designers see the problem before publish.
//
// The bracket scanner doesn't care about colon count — 4-part bindings
// inside braces parse the same way 2/3-part bindings do.
func ParseTemplateBindings(tmpl string) ([]BindingRef, error) {
	var out []BindingRef
	i := 0
	for i < len(tmpl) {
		open := strings.IndexByte(tmpl[i:], '{')
		if open < 0 {
			break
		}
		open += i
		close := strings.IndexByte(tmpl[open:], '}')
		if close < 0 {
			return nil, fmt.Errorf("template: unterminated '{' at %d", open)
		}
		close += open
		raw := tmpl[open+1 : close]
		ref, err := ParseBinding(raw)
		if err != nil {
			return nil, fmt.Errorf("template: %w", err)
		}
		out = append(out, ref)
		i = close + 1
	}
	return out, nil
}
