# How to add a new ECS component

> The whole point of the `Configurable[T]` pattern (PLAN.md §4n) is that
> adding a new component should NOT require touching any UI code.
> If you're editing a Templ file as part of a new component, stop and
> re-read this guide.

PLAN.md §144 / §128.

---

## Walkthrough

You're adding `Stamina{max, regen_per_sec, current}` so designers can
configure it on entity types and the runtime can drive it through
automations.

### 1. Pick a kind id

`internal/entities/components/registry.go` declares the `Kind`
constants. Add yours next to the existing ones. Convention: lower-snake
matches the JSON column.

```go
const (
    // ...
    KindStamina Kind = "stamina"
)
```

### 2. Write the typed config struct + Validate

Create `internal/entities/components/stamina.go` (or fold into an
existing file if it groups thematically — `automation_components.go`
holds the v1 batch):

```go
package components

import (
    "encoding/json"
    "errors"
    "boxland/server/internal/configurable"
)

type Stamina struct {
    Max    int32 `json:"max"`
    Regen  int32 `json:"regen_per_sec"`
    Current int32 `json:"current"`
}

func (s Stamina) Validate() error {
    if s.Max <= 0 {
        return errors.New("stamina: max must be > 0")
    }
    if s.Current < 0 || s.Current > s.Max {
        return errors.New("stamina: current must be in [0, max]")
    }
    return nil
}
```

### 3. Register the Definition

`simpleDef[T]` (declared in `automation_components.go`) collapses the
boring decode/validate boilerplate. The Descriptor is the **only**
thing the form renderer cares about; every field appears in the editor
automatically.

```go
var staminaDef = simpleDef(KindStamina,
    Stamina{Max: 100, Current: 100},
    []configurable.FieldDescriptor{
        {Key: "max",           Label: "Max",            Kind: configurable.KindInt, Default: 100},
        {Key: "regen_per_sec", Label: "Regen / sec",    Kind: configurable.KindInt, Default: 5},
        {Key: "current",       Label: "Initial value",  Kind: configurable.KindInt, Default: 100},
    })
```

### 4. Wire it into `Default()`

```go
func Default() *Registry {
    r := NewRegistry()
    // ...existing components...
    r.Register(staminaDef)
    return r
}
```

### 5. Add a test

`registry_test.go` already enumerates every built-in kind in
`TestDefault_RegistersBuiltins`; add `KindStamina` to that list. If
your component has interesting Validate logic, write a focused test
in `stamina_test.go`.

### 6. Hot-reload + verify

Restart the server. Open Entity Manager → any entity type → "Add
component". The new "Stamina" entry appears with three input fields
laid out by the generic form renderer.

That's it. No Templ edits, no JS module wiring, no migration. The
form renderer reads the Descriptor at request time; the publish
pipeline + runtime hot-swap (PLAN.md §132/§133) handle the rest.

---

## What if I need a custom input control?

If `KindString`, `KindInt`, `KindEnum`, `KindAssetRef`,
`KindEntityTypeRef`, `KindColor`, `KindVec2`, `KindRange`, `KindNested`,
`KindList` aren't enough, **DO NOT** start writing Templ partials.

Instead:

1. Add a new `FieldKind` constant in
   `internal/configurable/configurable.go`.
2. Add the renderer case in `views/form.templ`'s switch.
3. Add the matching TS-side input in
   `web/src/<surface>/form-renderer/` (lazy-loaded).

This adds a vocabulary entry **once** and every existing component +
trigger + action picks it up automatically. The cost of the per-kind
case is paid one time and shared across ~40 callers.

## What about the runtime?

For v1 the registry only stores config metadata. Runtime systems live
in `internal/sim/systems/` and use `world.Stores().Stamina` to read +
mutate per-tick. Wire your system in `Default()` (the same pattern
`Default()` follows in `internal/sim/systems/`) when the behaviour is
ready; the form renderer + persistence already work the moment the
Definition exists.
