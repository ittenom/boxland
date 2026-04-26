package hud

import (
	"errors"
	"fmt"
	"strings"
)

// BindingKind enumerates what a HUD binding-ref points at.
//
// entity:<who>:<resource>   — `who` is "host" (the player connection
//                              owns this entity) or "self" (alias).
//                              `resource` is one of:
//                                  hp_pct        — uint8 0..255
//                                  nameplate     — string
//                                  variant_id    — uint16
//                                  facing        — uint8
//                                  resource:<key>— named resource (int32)
// flag:<key>                — per-realm flag from server/internal/flags
// time:<channel>            — realm_clock | wall
//
// Format chosen so the bindings double as keys in a Map<string, value>
// on the client. The full string is the canonical id.
type BindingKind string

const (
	BindEntity BindingKind = "entity"
	BindFlag   BindingKind = "flag"
	BindTime   BindingKind = "time"
)

// BindingRef is one parsed binding string.
type BindingRef struct {
	Kind BindingKind
	// Key meaning depends on Kind:
	//   entity → who (e.g. "host")
	//   flag   → flag key (e.g. "gold")
	//   time   → channel (e.g. "realm_clock")
	Key string
	// Sub is only used for entity bindings (the resource name).
	Sub string
}

// String returns the canonical "kind:key[:sub]" string. Stable so the
// client can use it as a Map key.
func (b BindingRef) String() string {
	if b.Sub != "" {
		return string(b.Kind) + ":" + b.Key + ":" + b.Sub
	}
	return string(b.Kind) + ":" + b.Key
}

// ParseBinding parses an "entity:host:hp_pct" / "flag:gold" / "time:realm_clock"
// style ref into a BindingRef. Empty string returns an error so callers
// can distinguish "no binding" (don't call ParseBinding) from "bad binding".
func ParseBinding(s string) (BindingRef, error) {
	if s == "" {
		return BindingRef{}, errors.New("binding: empty")
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return BindingRef{}, fmt.Errorf("binding: %q wrong shape (want kind:key[:sub])", s)
	}
	kind := BindingKind(parts[0])
	switch kind {
	case BindEntity:
		if len(parts) != 3 {
			return BindingRef{}, fmt.Errorf("binding: %q entity needs sub (e.g. entity:host:hp_pct)", s)
		}
		if !validEntityWho(parts[1]) {
			return BindingRef{}, fmt.Errorf("binding: %q entity who %q (want host|self)", s, parts[1])
		}
		if !validEntityResource(parts[2]) {
			return BindingRef{}, fmt.Errorf("binding: %q entity resource %q not recognized", s, parts[2])
		}
		return BindingRef{Kind: BindEntity, Key: parts[1], Sub: parts[2]}, nil
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

// EntityResources is the closed set of fields the wire EntityState
// exposes (plus "resource:<name>" for designer-defined per-entity
// resources, handled separately). Mirrored on the client so the
// binding-picker can list them.
//
// "resource:<name>" handling: caller passes "resource:mana" → we accept
// any sub starting with "resource:" with a non-empty <name>.
func validEntityResource(s string) bool {
	switch s {
	case "hp_pct", "nameplate", "variant_id", "facing", "anim_id", "anim_frame", "tint", "x", "y":
		return true
	}
	if strings.HasPrefix(s, "resource:") && len(s) > len("resource:") {
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
// template for {kind:key[:sub]} substitutions and returns the parsed
// refs. Used by the text_label widget. Malformed substitutions error
// at validate time so designers see the problem before publish.
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
