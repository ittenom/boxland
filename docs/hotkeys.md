# Boxland — hotkey & focus reference

> The shared command bus (`web/src/command-bus/`) registers every hotkey on
> every surface. The Settings rebinder reads from this same registry.
> Conventions below MUST hold across Asset Manager, Entity Manager,
> Mapmaker, Sandbox, and Settings — enforce on PR review.

## Global

| Key | Action |
|---|---|
| `Esc` | Close the topmost modal / palette / overlay. If nothing is open, deselect. |
| `Tab` / `Shift+Tab` | Move focus through interactive elements in **reading order** (top→bottom, left→right). |
| `Ctrl/Cmd + K` | Open the command palette (Sandbox; later, every surface). |
| `?` | Open the hotkey-cheatsheet modal. |
| `Ctrl/Cmd + S` | "Save current draft" on the active surface. Network errors surface as toasts; never silent. |
| `Ctrl/Cmd + Z` | Undo via the surface's command bus. |
| `Ctrl/Cmd + Shift + Z` *or* `Ctrl/Cmd + Y` | Redo. |

## Mapmaker

| Key | Action |
|---|---|
| `[` / `]` | Cycle to the previous / next layer. |
| `1`–`9` | Select tile palette swatch by slot. |
| `B` | Brush tool. |
| `R` | Rect tool. |
| `F` | Fill tool. |
| `I` | Eyedrop / inspector. |
| `E` | Eraser. |
| `H` | Open the per-realm HUD editor (`/design/maps/{id}/hud`). |
| `Space` (hold) | Pan camera. |
| `+` / `-` | Zoom (always integer-scale). |

## Asset Manager

| Key | Action |
|---|---|
| `/` | Focus the search input. |
| `U` | Open upload modal. |
| `Enter` | Open the selected asset. |
| `Delete` | Move selection to trash (with confirm). |

## Entity Manager

| Key | Action |
|---|---|
| `/` | Focus the search input. |
| `N` | New entity type. |
| `D` | Duplicate selected. |

## Sandbox

| Key | Action |
|---|---|
| `Cmd/Ctrl + K` | Command palette (designer-only commands when realm=designer). |
| `F` | Toggle freeze-tick. |
| `.` | Step-tick (one tick forward; only while frozen). |
| `G` | Toggle godmode. |
| `Tab` | Cycle inspected entity. |
| `~` | Open spawn palette. |

## Character Generator

Active on `/design/characters/generator/{id}` and `/play/characters/{id}/edit`.

| Key | Action |
|---|---|
| `1` / `2` / `3` | Switch to **Look** / **Sheet** / **Talents** tab. |
| `[` / `]` | Cycle the active animation in the preview. |
| `+` / `-` | Zoom the preview (integer scale). |
| `R` | Randomize selections (deterministic per click; player mode keeps this). |
| `Ctrl/Cmd + S` | Save (designer: save draft; player: save & finalize). |
| `Esc` | Close any open modal; otherwise no-op. |

**Designer-only:**

| Key | Action |
|---|---|
| `Shift + R` | Reset all selections (player mode hides Reset). |
| `Shift + C` | Copy recipe JSON to clipboard for debugging. |

Hotkeys are suppressed while focus is in any text input (recipe name, search,
form fields). The action bar buttons are also keyboard-reachable via `Tab`.

## Game (player surfaces)

| Key | Action |
|---|---|
| `WASD` / Arrow keys | Move (default; rebindable in Settings). |
| `Mouse-click` | Move-to / interact (long-press = interact). |
| Right-click | Interact-at (server-routed). |
| Gamepad | Mapped via the shared rebinder; defaults match a generic XInput layout. |
| `C` | (Spectator) Toggle follow / free-cam camera. |

## Focus rules

1. Visible focus is **always** present; the pixel-CSS `:focus-visible` outline is not optional.
2. `Tab` order is the DOM order. Restructure the DOM rather than fight tab order with `tabindex` values; reserve `tabindex="0"` for non-input focusable elements (canvases, custom widgets) and `tabindex="-1"` for programmatic focus targets only.
3. Modals trap focus while open and restore it to the opener on close.
4. Hotkeys never fire while focus is in a text field unless the binding is explicitly marked `whileTyping: true` in the command-bus registration.

## Adding a new hotkey

1. Add the binding to the surface's command-bus registration.
2. Update this doc under the relevant surface section.
3. Add the binding to the in-app cheatsheet (Settings → Hotkeys).

## Out of scope for v1

- Per-user custom keymaps beyond the rebinder (e.g., scriptable macros).
- Modifier-only hotkeys (e.g., bare `Alt`); too easy to trigger by accident.
