# Boxland TUI — Design

**Status:** Approved
**Date:** 2026-05-01
**Surface:** #2 in the foundation spec's §9 decomposition
**Foundation spec:** `docs/superpowers/specs/2026-04-30-elixir-phoenix-pixi-foundation-design.md`
**Scope:** A single-binary terminal launcher for Boxland, packaged via Burrito for macOS arm64 and Linux amd64. v1 ships with a deliberately tight 2-item menu (Install, Run Server) on the right architectural foundation so future menu items layer in without restructuring.

---

## Locked decisions

| Decision | Choice |
|---|---|
| TUI library | [`term_ui`](https://hex.pm/packages/term_ui) (1.0-rc) |
| Packaging | [Burrito](https://github.com/burrito-elixir/burrito) `~> 1.5`, single binary |
| Targets | macOS arm64, Linux amd64 |
| Menu in v1 | Install, Run Server, Quit (Re-check Install replaces Install after first success) |
| Server runtime UX | Toggle (Run Server ↔ Stop Server) with modal log view |
| Quit semantics | Quit kills everything (TUI + Phoenix + BEAM) |
| Postgres + Redis | docker-compose-managed, generated into `~/.boxland/services/docker-compose.yml` |
| First-run wizard | 9 idempotent stages run via "Install" menu item |
| Designer account | Created later via web signup (Designer Auth UI surface), NOT in TUI v1 |
| Distribution | Manual upload of binaries to a public URL; no Homebrew/notarization in v1 |
| Self-update | Deferred to v1.5+ |
| Visual treatment | 6-line gradient `BOXLAND` logo (cfonts/figlet generated), pink→orange brand gradient, lots of color via term_ui's truecolor |
| Config files | `~/.boxland/config.exs` (user-editable Elixir config) + `~/.boxland/secrets.exs` (mode 0600) |
| Log handling | Custom `:logger` handler with 5000-entry ring buffer, broadcast to subscribed views |
| `runtime.exs` | Refactored to be tolerant of missing env vars in `:prod` (loads `~/.boxland/config.exs` if present) |
| Tests | ExUnit pure logic + injectable-deps stage tests + one `@moduletag :integration` e2e |

---

## Table of contents

1. [Architecture & supervision tree](#1-architecture--supervision-tree)
2. [Menu state machine](#2-menu-state-machine)
3. [Install workflow](#3-install-workflow)
4. [Run Server runtime view](#4-run-server-runtime-view)
5. [Visual design](#5-visual-design)
6. [Burrito packaging & distribution](#6-burrito-packaging--distribution)
7. [Testing strategy](#7-testing-strategy)
8. [Cross-cutting (config, log pipe, errors)](#8-cross-cutting)

---

## 1. Architecture & supervision tree

The TUI is a supervised process inside the same `Boxland.Application` we already built. Phoenix becomes a *child sub-supervisor* — present in the tree but with no children of its own at boot. The TUI tells it when to spawn or terminate them.

```
Boxland.Application                      ← top-level supervisor (one_for_one)
├── Boxland.Repo                         ← always-on
├── Phoenix.PubSub                       ← always-on
├── Boxland.TUI.LogBackend               ← Logger handler + ring buffer (always-on)
├── Boxland.Server.Supervisor            ← ★ Phoenix sub-supervisor — empty by default
│   └── (children added at runtime when "Run Server" toggles ON):
│       ├── BoxlandWeb.Endpoint
│       ├── DNSCluster
│       └── BoxlandWeb.Telemetry
└── Boxland.TUI.Server                   ← term_ui process; the user-facing thing
```

`Boxland.Server.Supervisor` uses `:rest_for_one`. The TUI starts/stops Phoenix children via `Supervisor.start_child/2` and `Supervisor.terminate_child/2`. Restarting the server is `terminate_child` then `start_child` — same BEAM, no release re-launch.

`Boxland.TUI.LogBackend` is a `:logger` handler (modern OTP 21+ API, not the legacy `Logger.Backends.*` interface) that buffers the most recent N log entries (size-bounded ring buffer, default 5000) and forwards new entries to subscribers.

### Why this shape

- **Same BEAM, two roles.** TUI runs in process A, Phoenix supervisor in process B. Stopping Phoenix doesn't take down the BEAM. Quitting the TUI cleanly stops the Phoenix supervisor and the BEAM exits.
- **Logs flow in-process.** No subprocess pipes, no stdout parsing, no PTY headaches. Logger → LogBackend → LogViewer is just message passing.
- **Repo is always on.** Install needs `Boxland.Release.migrate/0` before Phoenix starts. Repo always-on removes a "did I forget to start X" class of bugs.

### Entry point

The Burrito-built binary's default CMD is the TUI:

```bash
boxland               # opens the TUI
boxland install       # non-interactive install (CI / scripts)
boxland run           # non-interactive server start (foreground, logs to stdout)
boxland --version
```

### Module layout

```
lib/boxland/tui/
├── server.ex                 # GenServer — the term_ui main loop
├── log_backend.ex            # :logger handler + ring buffer GenServer
├── menu.ex                   # menu state machine
├── install.ex                # 9-stage install workflow
├── server_runtime.ex         # the "Run Server" view state
├── theme.ex                  # color palette + figlet logo data
└── views/
    ├── menu_view.ex          # term_ui view: logo + menu list + footer
    └── runtime_view.ex       # term_ui view: logo strip + log pane + status footer

lib/boxland/server/
└── supervisor.ex             # Boxland.Server.Supervisor (the sub-supervisor)

lib/boxland/cli/
├── install.ex                # `boxland install` subcommand impl
└── run.ex                    # `boxland run` subcommand impl
```

Views call into `Boxland.TUI.Install` and `Boxland.Server.Supervisor`; no domain logic in views.

---

## 2. Menu state machine

### Two persisted states, one runtime state

| State | Lives in | Meaning |
|---|---|---|
| `installed_at` | `~/.boxland/installed` (file: ISO timestamp + version) | Last successful Install. Presence drives menu shape. |
| `server_status` | runtime (`Boxland.Server.Supervisor.status/0`) | `:stopped` or `:running`. Always `:stopped` at TUI boot. |

### Menu shape per state

```
PRE-INSTALL                  POST-INSTALL, server stopped     POST-INSTALL, server running
─────────────                ────────────────────────────     ────────────────────────────
★ Install                    ★ Run Server                     ⏵ Server running 0:32 (status)
◌ Run Server (disabled)      ◌ Re-check Install               ★ Stop Server
  Quit                         Quit                             Quit
```

- ★ = featured (accent color, glyph prefix, larger row)
- ◌ = available but demoted (muted)
- (disabled) = greyed; navigation skips
- ⏵ = top status indicator with live elapsed time (not selectable)

### Transitions

```
Pre-install ─[Install succeeds]→ Post-install (server stopped)
Post-install (server stopped) ─[Run Server toggle]→ Runtime view (server running)
Runtime view ─[Esc / Stop Server]→ Post-install (server stopped)
Any state ─[Quit / Ctrl-C]→ Stop Phoenix → BEAM exit
```

### What gets persisted vs recomputed

**Persisted** — `~/.boxland/installed`:
```
2026-05-01T12:34:56.789Z
0.1.0
```

**Recomputed at runtime** (every Install / Re-check):
- Which dependencies are present (libvips, Docker, etc.)
- Whether docker services are up and healthy
- Whether migrations are current

### Quit semantics

- Pre-install → plain exit
- Post-install, server stopped → plain exit
- Post-install, server running → terminate Phoenix children → BEAM exits (no confirmation in v1)

`SIGINT` (Ctrl-C) and the Quit menu item route through the same teardown path.

---

## 3. Install workflow

### Critical refactor: tolerant `runtime.exs`

A Burrito-built binary boots in `:prod` mode. The foundation's `runtime.exs` raises if `DATABASE_URL` etc. aren't set in prod. We refactor it to be tolerant of missing env vars: if `~/.boxland/installed` exists, runtime.exs reads `~/.boxland/config.exs` to populate config; if the marker is missing, it skips prod-only configuration.

```elixir
import Config

if config_env() == :prod do
  user_config = Path.expand("~/.boxland/config.exs")

  if File.exists?(user_config) do
    Code.eval_file(user_config)
  else
    # Pre-install state: skip prod config requirements.
    # The TUI handles missing-config gracefully and routes user to Install.
    :ok
  end
end
```

### Where Install writes things

```
~/.boxland/                      ← mode 0700
├── installed                    ← Marker: ISO timestamp + version
├── config.exs                   ← Generated runtime config
├── secrets.exs                  ← SECRET_KEY_BASE (mode 0600)
└── services/
    ├── docker-compose.yml       ← Generated from template
    ├── pg_data/                 ← Postgres bind mount
    └── minio_data/              ← MinIO bind mount
```

### The 9 Install stages

Each stage is a function in `Boxland.TUI.Install` returning `:ok | {:error, %{stage, reason, suggestion}}`. All stages are idempotent.

| # | Stage | What it does | Failure mode |
|---|---|---|---|
| 1 | **Pre-flight scan** | Detect OS, package manager, what's already installed (docker, libvips). No mutations. | Always succeeds. |
| 2 | **OS packages** | Install missing OS-level deps via the detected package manager. v1 scope: `libvips`. | If sudo needed on Linux: surface install instructions, don't escalate. |
| 3 | **Docker check** | Verify `docker info` returns 0. v1 does NOT install Docker. | Surface install URL + abort. |
| 4 | **Data directory** | `mkdir -p ~/.boxland/services/{pg_data,minio_data}` with mode 0700. | Permission errors → abort. |
| 5 | **Secrets** | Generate `SECRET_KEY_BASE` (64 random bytes, base64) → write `~/.boxland/secrets.exs` mode 0600. | Disk/permission → abort. |
| 6 | **Config file** | Render `~/.boxland/config.exs` from template. | Same. |
| 7 | **docker-compose** | Render `~/.boxland/services/docker-compose.yml` (volumes prefixed `boxland_user_` to avoid dev-repo collision). | Same. |
| 8 | **Bring up services** | `docker compose -f ~/.boxland/services/docker-compose.yml up -d`, poll health up to 30s. | Health timeout → abort with compose ps output. |
| 9 | **Migrations** | Load `~/.boxland/config.exs`, set env in BEAM, call `Boxland.Release.migrate/0`. | Connection / migration error → abort. |

After all 9 succeed: write `~/.boxland/installed` with timestamp + version, transition menu state.

### Visual layout during Install

Install replaces the menu screen with a single-pane Install view:

```
╔══════════════════════════════════════════════════════════════════════════╗
║                    [Boxland logo, gradient]                              ║
║                                                                          ║
║   Installing…                                                            ║
║                                                                          ║
║   ✓  Pre-flight scan         (Mac arm64, Homebrew detected)              ║
║   ✓  Install libvips         (already installed)                         ║
║   ✓  Docker check            (Docker Desktop running)                    ║
║   ✓  Create data directory   (~/.boxland)                                ║
║   ✓  Generate secrets        (SECRET_KEY_BASE written)                   ║
║   ✓  Write config            (~/.boxland/config.exs)                     ║
║   ✓  Write docker-compose    (~/.boxland/services/docker-compose.yml)    ║
║   ⏵  Bring up services       Starting postgres, redis, minio…            ║
║      ↳ [✓] postgres is healthy                                           ║
║      ↳ [✓] redis is healthy                                              ║
║      ↳ [⏵] minio still starting…                                         ║
║   ○  Migrations                                                          ║
║                                                                          ║
║   Press Esc to abort                                                     ║
╚══════════════════════════════════════════════════════════════════════════╝
```

Glyphs: ○ pending, ⏵ in-progress (spinner), ✓ done, ✗ failed.

On failure, the failed stage shows ✗ + reason + suggestion banner. User can retry (`R`) or return to menu (`Esc`). Re-running picks up where it left off (idempotent).

### Re-running Install (post-install)

Same 9 stages run; existing files trigger short-circuits. A healthy install completes in ~2 seconds. Surfaces the specific stage needing attention if anything's broken.

### Subcommand `boxland install`

Same workflow without the TUI: plain text to stdout, one line per stage transition. Failures exit non-zero. Useful for scripts / CI.

---

## 4. Run Server runtime view

### What happens when "Run Server" toggles ON

```
1. Menu hands off → enter runtime view
2. Boxland.Server.Supervisor.start_children() called
   ├── Adds BoxlandWeb.Endpoint
   ├── Adds DNSCluster
   └── Adds BoxlandWeb.Telemetry
3. Logger fires "Running BoxlandWeb.Endpoint with Bandit ..."
4. LogBackend captures + forwards to mounted LogViewer
5. Status footer begins ticking elapsed time
6. URL strip displays http://localhost:4000
```

Supervisor strategy `:rest_for_one`: a later child crash restarts only it (and any after); Phoenix's own supervision handles in-Endpoint failures.

### Layout

```
╔══════════════════════════════════════════════════════════════════════════╗
║              [B O X L A N D]  (compact 2-line gradient strip)            ║
╠══════════════════════════════════════════════════════════════════════════╣
║  ●  Server running 0:32          http://localhost:4000  ↗                ║
╠══════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  23:45:12.834 [info]  Running BoxlandWeb.Endpoint with Bandit 1.10.4 …  ║
║  23:45:12.851 [info]  Access BoxlandWeb.Endpoint at http://localhost…   ║
║  23:45:13.002 [debug] QUERY OK source="schema_migrations" db=2.1ms       ║
║  23:45:14.117 [info]  GET / sent 200 in 4ms                              ║
║                                                                          ║
║  [scrolling ▼ — newest at bottom]                                        ║
║                                                                          ║
╠══════════════════════════════════════════════════════════════════════════╣
║  [Esc] Stop server   [Q] Quit   [↑↓ PgUp/PgDn] Scroll   [Home/End] Jump ║
╚══════════════════════════════════════════════════════════════════════════╝
```

Three regions stacked vertically: logo strip (2 rows, compact), status strip (1 row, live elapsed time + URL), log pane (fills remaining height — auto-scroll unless user has scrolled up), key hints footer.

### Log streaming

The runtime view's `LogViewer` subscribes to `Boxland.TUI.LogBackend` on mount, drains the buffer (so the user sees boot lines from the moment they entered Run Server), and renders new entries as they arrive.

**Levels shown by default:** all of them. Logger's own level filter (`:info` in prod, `:debug` in dev) is the only filter in v1.

### Stop sequence

Triggered by Esc (back to menu), the new "Stop Server" menu item, or Q (quit entire TUI):

```
1. Boxland.Server.Supervisor.stop_children()
   - terminate_child(BoxlandWeb.Telemetry)
   - terminate_child(DNSCluster)
   - terminate_child(BoxlandWeb.Endpoint)        ← last (lets it drain in-flight requests)
2. Logger fires shutdown messages → captured + displayed
3. Status indicator → "Stopping…" with spinner
4. After supervisor.status() == :stopped:
   - Esc/Stop: return to menu (post-install, server stopped)
   - Q: continue to BEAM exit
```

### Edge cases

| Scenario | Behavior |
|---|---|
| Phoenix Endpoint fails to start (port in use) | Red banner with reason; logs already captured; Esc back to menu. |
| Database disconnects mid-run | Ecto pool retries; logs show reconnect attempts; server stays "running". |
| Esc during start | Stop sequence runs, terminating whatever started; back to menu. |
| Q during start/stop | Same as Esc → continue to BEAM exit once `:stopped`. |
| `~/.boxland/installed` deleted while running | No effect on running server; next TUI launch is back in pre-install. |

### Subcommand `boxland run`

Same Phoenix start sequence, no TUI: synchronous start, Logger writes to stdout (no LogBackend), process stays alive until SIGINT/SIGTERM, then stop sequence + exit.

### NOT in v1

- Per-level log filtering UI
- Log search/grep within LogViewer
- Saving logs to file
- `[R] Restart` shortcut (Esc → toggle on works fine)
- Multiple instance views

---

## 5. Visual design

### Logo

A 6-line gradient ASCII rendition of `BOXLAND`. Generated once at design time (via cfonts/figlet), stored as a string constant in `lib/boxland/tui/theme.ex`:

```
 ██████╗  ██████╗ ██╗  ██╗██╗      █████╗ ███╗   ██╗██████╗
 ██╔══██╗██╔═══██╗ ██╗██╔╝██║     ██╔══██╗████╗  ██║██╔══██╗
 ██████╔╝██║   ██║  ███╔╝ ██║     ███████║██╔██╗ ██║██║  ██║
 ██╔══██╗██║   ██║██╔██╗  ██║     ██╔══██║██║╚██╗██║██║  ██║
 ██████╔╝╚██████╔╝██║ ██╗ ███████╗██║  ██║██║ ╚████║██████╔╝
 ╚═════╝  ╚═════╝ ╚═╝ ╚═╝ ╚══════╝╚═╝  ╚═╝╚═╝  ╚═══╝╚═════╝
```

### Gradient rendering

Each column gets a per-column color interpolated between two endpoints:

- Truecolor (24-bit RGB) via term_ui style structs
- For each ASCII row, render character-by-character with `lerp(left_rgb, right_rgb, col / total_cols)`
- Term_ui auto-degrades to 256-color or 16-color on terminals without truecolor

**Two sizes:**
- **Full logo** (6 lines) — menu screen, top-centered
- **Compact strip** (2 lines) — atop the runtime view

### Color palette

Defined in `lib/boxland/tui/theme.ex` as `colors/0` returning a map:

| Token | RGB | Used for |
|---|---|---|
| `accent_warm` | `#ff9ec7` (soft pink) | Logo gradient left, featured menu items, primary highlights |
| `accent_warm_end` | `#ffb86b` (warm orange) | Logo gradient right |
| `accent_cool` | `#5ccfe6` (cool blue) | Status indicators, active progress glyphs |
| `success` | `#3dd97c` (green) | ✓ checkmarks, healthy services |
| `warning` | `#f4c430` (amber) | ⚠ warnings, slow-starting services |
| `error` | `#f06870` (rose-red) | ✗ failures |
| `text` | terminal default | Menu items, body text |
| `text_muted` | dim default | Demoted items, secondary descriptions |
| `text_subtle` | dimmer | Disabled items, footer hints, log timestamps |
| `border` | `gray40` | Box borders, separators |

No background fill — keeps terminal's bg theme intact.

### Menu screen layout

```
                        ╔═════════════════════════════╗
                        ║       (full BOXLAND         ║
                        ║         gradient logo)      ║
                        ╚═════════════════════════════╝

                        ┌──────────────────────────────┐
                        │                              │
                        │  ▶  Run Server               │   ← featured (accent_warm, bold)
                        │     ↳ Phoenix on :4000        │   ← description below (text_muted)
                        │                              │
                        │  ⟲  Re-check Install         │   ← demoted (text_muted)
                        │     ↳ Verify deps + heal     │
                        │                              │
                        │  ⏻  Quit                     │   ← muted (text_subtle)
                        │                              │
                        └──────────────────────────────┘

                ──────────────────────────────────────────────
                  v0.1.0 · ~/.boxland · installed 2026-05-01
                ──────────────────────────────────────────────
                  [↑↓ j/k] Navigate   [Enter] Select   [Q] Quit
```

Selected item: left-bar marker `▎` in `accent_warm` + bold name.

### Spinners

Stage-in-progress: term_ui's spinner widget if 1.0 ships with one; otherwise rotate `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏` (Braille) at 100ms intervals. Color: `accent_cool`. Stage-complete: ✓ in `success`. Stage-failed: ✗ in `error`.

### Tiny-terminal degradation

- Min: 80 cols × 24 rows
- Below: logo collapses to one-line text version, descriptions hidden
- Below 60 cols: TUI declines to render with a one-line message

### macOS terminal compatibility

Both Apple Terminal and iTerm2 support truecolor + the Unicode glyphs used (✓ ✗ ▶ ⟲ ⏻ ▎ ↳ ●). iTerm2 auto-links `http://localhost:4000`.

---

## 6. Burrito packaging & distribution

### Burrito setup

```elixir
# mix.exs deps
{:burrito, "~> 1.5"}

# mix.exs releases
releases: [
  boxland: [
    include_executables_for: [:unix],
    applications: [runtime_tools: :permanent],
    steps: [:assemble, &Burrito.wrap/1],
    burrito: [
      targets: [
        macos_arm64: [os: :darwin, cpu: :aarch64],
        linux_amd64: [os: :linux, cpu: :x86_64]
      ]
    ]
  ]
]
```

### What `mix release` produces

```
burrito_out/
├── boxland_macos_arm64        # ~50-80 MB self-contained executable
└── boxland_linux_amd64
```

Each binary: bundled ERTS, all OTP apps, Hex deps, priv/static, compiled release. First-time launch self-extracts to `~/.cache/.burrito/boxland/<version>/` and re-execs.

### First-time build setup

```bash
mix burrito.install         # downloads Zig (~70MB) + ERTS tarballs (~50MB/target)
```

Cached in `~/.local/share/.burrito_lib/`. Subsequent builds reuse.

### Build commands

```bash
MIX_ENV=prod mix release                            # all targets
MIX_ENV=prod mix release --target macos_arm64       # single target
MIX_ENV=prod mix release --target linux_amd64
```

Wrapped in a `just build-release` recipe.

### Versioning

`mix.exs` `version: "0.1.0"`. Embedded in binary, accessible via `Application.spec(:boxland, :vsn)`. Shown in: menu footer, `boxland --version`, `~/.boxland/installed`.

### Distribution for v1

- Manual upload of `boxland_macos_arm64` and `boxland_linux_amd64` to a public URL (GitHub Releases, S3, etc.).
- README explains: download → `chmod +x` → run.
- **Mac Gatekeeper note:** binary isn't notarized; users get "unidentified developer" warning on first launch. Workaround: `xattr -d com.apple.quarantine ./boxland_macos_arm64`.
- **No Homebrew formula** in v1.
- **No auto-update** in v1 (deferred to v1.5+).

### `.gitignore`

```
burrito_out/
```

### NOT in v1 distribution

- Apple notarization / code signing
- Windows binary
- Mac Intel binary
- Linux ARM binary
- Homebrew tap
- `.deb` / `.rpm` packages
- Self-update mechanism
- CI-built binaries on tagged releases

---

## 7. Testing strategy

### 1. Pure logic tests (most of the suite)

```elixir
test "menu items reorder when install completes" do
  pre = Menu.items(installed_at: nil, server_status: :stopped)
  post = Menu.items(installed_at: ~U[2026-05-01 12:00:00Z], server_status: :stopped)
  assert hd(pre).id == :install
  assert hd(post).id == :run_server
  assert Enum.find(post, &(&1.id == :install)).style == :demoted
end
```

Plain ExUnit. Tests for `Menu`, `Install`, `LogBackend`, `Theme`, `ServerRuntime`. ~40-50 tests.

### 2. Stage execution tests (injectable deps)

Each Install stage takes its system dependencies as injectable functions. Default impls call the real system; tests pass mocks.

```elixir
defmodule Boxland.TUI.Install do
  def stage_2_os_packages(deps \\ default_deps()) do
    case deps.detect_pm.() do
      {:ok, :brew} -> deps.run_cmd.("brew", ["install", "vips"])
      ...
    end
  end

  defp default_deps do
    %{
      detect_pm: &Boxland.TUI.Install.System.detect_package_manager/0,
      run_cmd: &System.cmd/2
    }
  end
end

# Test:
test "stage 2 calls brew install vips on Mac with Homebrew" do
  deps = %{
    detect_pm: fn -> {:ok, :brew} end,
    run_cmd: fn cmd, args -> send(self(), {:cmd, cmd, args}); {"", 0} end
  }
  assert :ok = Install.stage_2_os_packages(deps)
  assert_received {:cmd, "brew", ["install", "vips"]}
end
```

Filesystem ops use temp dirs. ~15-20 tests.

### 3. End-to-end smoke (slow; CI-gated)

```elixir
@moduletag :integration
test "full install + start server + stop + quit" do
  # Real Install against a test data dir + separate compose project
  # Start supervisor, hit /healthz, stop, cleanup
end
```

Skipped by default; opt-in via `mix test --include integration`. Railway runner has Docker. Runs on main-branch pushes only. 1-2 tests.

### What we DON'T test

Snapshot-test rendered terminal output. Term_ui's view rendering is the lib's concern. We test:
- View functions return the expected widget tree (`view(state) → tree`)
- Update functions transform state correctly

```elixir
test "menu view shows Install featured when not installed" do
  state = %Menu.State{installed_at: nil, server_status: :stopped, selected: 0}
  tree = MenuView.render(state)
  assert tree |> find_widget(:item, :install) |> style == :featured
end
```

### Test count target

- Pure logic: ~40-50
- Stage execution: ~15-20
- E2E smoke: 1-2 (`@moduletag :integration`)

Total ~60-70 for the TUI surface. Project total: 71 (foundation) + ~65 (TUI) ≈ 135.

### Where tests live

```
test/boxland/tui/
├── menu_test.exs
├── install_test.exs
├── log_backend_test.exs
├── theme_test.exs
├── server_runtime_test.exs
└── views/
    ├── menu_view_test.exs
    └── runtime_view_test.exs

test/boxland/tui/integration/
└── full_install_test.exs              # @moduletag :integration
```

---

## 8. Cross-cutting

### Config files

#### `~/.boxland/config.exs`

Generated at Install Stage 6. Contains all prod config that `runtime.exs` would otherwise read from env vars:

```elixir
import Config

# Boxland user config — written by Install on first run.
# Hand-edit to change ports, OAuth credentials, S3 endpoint, etc.
# Re-running Install regenerates this file IF it doesn't exist;
# existing file is preserved (no Install stage overwrites it).

config :boxland, Boxland.Repo,
  url: "ecto://boxland:boxland@localhost:5432/boxland",
  pool_size: 10

config :boxland, BoxlandWeb.Endpoint,
  url: [host: "localhost", port: 4000, scheme: "http"],
  http: [ip: {127, 0, 0, 1}, port: 4000]

config :boxland, :redis_url, "redis://localhost:6379"

config :ex_aws,
  access_key_id: "boxland",
  secret_access_key: "boxland-dev-secret"

config :ex_aws, :s3,
  host: "localhost:9000",
  bucket: "boxland",
  scheme: "http://"

config :boxland, :cdn_base_url, "http://localhost:9000/boxland"

config :boxland, :oauth,
  google: [client_id: "", client_secret: ""],
  apple: [client_id: "", client_secret: ""],
  discord: [client_id: "", client_secret: ""]

config :boxland, Boxland.Mailer, adapter: Swoosh.Adapters.Local

import_config "secrets.exs"
```

#### `~/.boxland/secrets.exs`

Mode 0600 so tokens can't be world-read:

```elixir
import Config

# Generated on Install. Regenerating requires DELETING this file.

config :boxland, BoxlandWeb.Endpoint,
  secret_key_base: "<64+ bytes of url-safe base64>"
```

### Log pipe

`Boxland.TUI.LogBackend` is a `:logger` handler GenServer. Wired up in `Boxland.Application.start/2`:

```elixir
children = [
  Boxland.Repo,
  Phoenix.PubSub.child_spec(name: Boxland.PubSub),
  {Boxland.TUI.LogBackend, buffer_size: 5000},
  Boxland.Server.Supervisor,
  Boxland.TUI.Server
]

# After supervisor start:
:logger.add_handler(:boxland_tui, Boxland.TUI.LogBackend.Handler, %{})
```

#### State

```elixir
%LogBackend.State{
  buffer: :queue.new(),
  buffer_size: 5000,
  buffer_count: 0,
  subscribers: %{}    # %{pid => monitor_ref}
}
```

#### Operations

| Operation | What it does |
|---|---|
| `log/2` (handler callback) | Format event → push into queue → drop oldest if at capacity → broadcast `{:log_entry, formatted}` |
| `subscribe/1` | Monitor pid; add to subscribers; flush existing buffer to new subscriber as `{:log_buffer, entries}` |
| `unsubscribe/1` | Remove + demonitor |
| `flush/0` | Empty buffer (used when "Run Server" stops, to avoid stale logs in next run) |
| `:DOWN` handler | Auto-cleanup when subscriber pid dies |

#### Format

```
HH:MM:SS.mmm [level] message  metadata=value
```

Color per level (Section 5 palette). Multi-line messages split into entries with continuation indent.

### Error handling

#### Install stage failures

Each stage returns `:ok | {:error, %{stage, reason, suggestion}}`. Install view:
1. Renders failed stage with ✗ in error color
2. Shows reason as sub-line
3. Shows `suggestion` (if present) in banner
4. Stops execution; subsequent stages stay `○ pending`
5. Offers `[R] Retry` or `[Esc] Back to menu`

Example:
```
✗  Docker check               Docker daemon not reachable
   ↳ Install Docker Desktop from https://docker.com/products/docker-desktop
     and ensure it's running, then press R to retry.

[R] Retry   [Esc] Back to menu
```

#### Runtime view failures

Phoenix child failing to start (port in use): supervisor returns error, runtime view captures + displays red banner; logs from the failure visible in log pane; `Esc` to menu.

#### TUI process crash

TUI is `restart: :permanent` under `Boxland.Application`. On crash:
1. Supervisor restarts it
2. New TUI re-reads `~/.boxland/installed`, redraws menu
3. Phoenix sub-supervisor unaffected (sibling under same top-level supervisor)

Log buffer preserved (LogBackend is sibling).

#### `~/.boxland/` corruption / incomplete state

Re-running Install heals it (idempotent stages). Stage 5/6 preserve existing files; stages 4/7/8/9 are inherently idempotent. Manually deleting `~/.boxland/installed` puts the user back in pre-install.

### Upgrade detection

`~/.boxland/installed` format:
```
2026-05-01T12:34:56.789Z
0.1.0
```

On TUI launch:
1. Read marker
2. Compare to `Application.spec(:boxland, :vsn)`
3. If different: `upgrade_pending: true`
4. Menu shows banner: `⚠ Boxland upgraded from 0.1.0 → 0.2.0 — Re-check Install recommended`
5. "Re-check Install" gets featured style (overrides "Run Server")

After successful Re-check, marker's version is rewritten and banner clears.

### NOT in v1

- Settings menu item (users edit config files by hand)
- Secret rotation flow (delete `~/.boxland/secrets.exs` and re-run Install)
- Backup / restore
- Multi-instance support (one `~/.boxland/` per machine; running two TUI instances against the same data dir would conflict on docker-compose project name)
- Log file output (LogBackend is in-memory only; `boxland run` users capture stdout themselves)
- Detection of "Boxland already running" on launch (port-bind failure surfaces it; cleaner PID-file lock check is v1.5)

---

## Open follow-ups (intentional)

The foundation review of the prior surface flagged two precommit blockers. Verify they're still addressed before TUI implementation begins:

1. `.formatter.exs` excludes `lib/boxland_web/proto/*.pb.ex` ✓ (commit `78e945d` from foundation)
2. AccessPolicy unused-variable warnings silenced ✓ (same commit)

## What this spec does NOT do

- Plan out specific implementation steps (that's the writing-plans skill, next)
- Cover Settings UI (out for v1; users edit config files)
- Cover the Update menu item / self-update mechanism (deferred to v1.5+)
- Cover Mac notarization or non-Mac/Linux distribution
- Cover game-server-vs-dev-server distinction (single "Run Server" item; mode is configured in the running app)

---

## Appendix — key file additions

```
lib/boxland/
├── tui/
│   ├── server.ex
│   ├── log_backend.ex
│   ├── menu.ex
│   ├── install.ex
│   ├── server_runtime.ex
│   ├── theme.ex
│   └── views/
│       ├── menu_view.ex
│       └── runtime_view.ex
├── server/supervisor.ex
└── cli/
    ├── install.ex
    └── run.ex

priv/templates/
├── user_config.exs.eex                # Stage 6 source
└── docker_compose.yml.eex             # Stage 7 source

config/runtime.exs                      # MODIFIED — tolerant of missing user config in :prod

mix.exs                                 # MODIFIED — adds :burrito dep + releases config

# (justfile and README updated for build-release recipe + Burrito user instructions)
```
