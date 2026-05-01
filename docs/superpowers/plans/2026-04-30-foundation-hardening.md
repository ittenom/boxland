# Foundation Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bootstrap the new Boxland Elixir/Phoenix/Pixi app to the point where it deploys to Railway as an empty shell with `/healthz` returning ok, with all schemas/auth modules/Luerl host/Protobuf pipeline in place but no user-facing UI yet. This is surface #1 of §9 in the foundation design spec.

**Architecture:** A single Phoenix 1.8 application with Ecto+Postgres, all domain contexts created with their schemas, auth scaffolding for designer (cookie) and player (JWT) realms with no UI, Luerl scripting host wired up with an empty action catalog, Protobuf codegen pipeline established, esbuild+Tailwind+TypeScript asset toolchain, multi-stage Docker image, Railway deployment config, structured logging, and health endpoints.

**Tech Stack:** Elixir 1.17 / OTP 27 / Phoenix 1.8.x / LiveView / Ecto / PostgreSQL 16 / Redis 7 / esbuild / Tailwind / TypeScript / Vix (libvips) / Luerl / Argon2 / Hammer / `protobuf` (Hex) / `ts-proto` / ExAws.S3 / Swoosh / Phoenix.PubSub.

**Spec:** `docs/superpowers/specs/2026-04-30-elixir-phoenix-pixi-foundation-design.md`

**Target location:** `/Users/cmonetti/boxland-elixir/` (new sibling directory to the existing Go reference repo at `/Users/cmonetti/boxland/`). The Go repo is untouched.

---

## Conventions used throughout

- **Working directory:** Every shell command assumes you've `cd`'d to `/Users/cmonetti/boxland-elixir/` unless explicitly noted otherwise.
- **TDD pattern:** When implementing a unit with logic, write a failing test first, run to confirm fail, implement minimum code, run to confirm pass, commit.
- **Commits:** One commit per task unless noted. Conventional-commits style (`feat:`, `chore:`, `test:`, etc.).
- **No mocks for Ecto:** All Ecto tests use `Ecto.Adapters.SQL.Sandbox` against a real Postgres test database. Each test runs in a transaction that rolls back.
- **All TypeScript:** Files in `assets/js/**` are `.ts`. Generated proto modules are `.ts`.

---

## Phase 1 — Bootstrap

### Task 1: Create new repo location and generate Phoenix app

**Files:**
- Create: `/Users/cmonetti/boxland-elixir/` (new directory)
- Create: standard Phoenix 1.8 app skeleton (many files; generated)

- [ ] **Step 1: Verify Elixir 1.17+ and Erlang/OTP 27+ installed**

Run:
```bash
elixir --version
```
Expected: shows `Elixir 1.17.x` with `Erlang/OTP 27`. If not, install via `asdf install elixir 1.17.2-otp-27` (or your version manager of choice) before proceeding.

- [ ] **Step 2: Install or update Phoenix installer**

Run:
```bash
mix archive.install hex phx_new --force
```
Expected: installs the latest `phx.new` archive (1.8.x or later).

- [ ] **Step 3: Generate Phoenix app at sibling directory**

From `/Users/cmonetti/`:
```bash
cd /Users/cmonetti && mix phx.new boxland-elixir --module Boxland --app boxland --install
```
Expected: prompts for "Fetch and install dependencies? [Yn]" — answer `y` (or skip with `--install` already passed). Generates the Phoenix project, fetches deps, installs npm/esbuild/tailwind. The `--module Boxland --app boxland` flags set the OTP app name to `:boxland` and root module to `Boxland` (drops the `_elixir` from the dirname).

- [ ] **Step 4: cd into the new project**

Run:
```bash
cd /Users/cmonetti/boxland-elixir
```

- [ ] **Step 5: Verify the generated app compiles**

Run:
```bash
mix compile
```
Expected: compiles without warnings. Phoenix 1.8 generates a clean baseline.

- [ ] **Step 6: Initial git commit**

Run:
```bash
git init
git add .
git commit -m "chore: bootstrap Phoenix 1.8 app via mix phx.new"
```

---

### Task 2: Add custom Hex dependencies

**Files:**
- Modify: `mix.exs` (deps function)

- [ ] **Step 1: Edit `mix.exs` to add new deps**

Open `mix.exs`. Locate the `deps` function. Add these dependencies inside the existing list (preserving deps Phoenix already added):

```elixir
defp deps do
  [
    # ... existing Phoenix deps ...

    # Auth & security
    {:argon2_elixir, "~> 4.0"},
    {:hammer, "~> 7.0"},

    # Wire format
    {:protobuf, "~> 0.13"},
    {:google_protos, "~> 0.4"},

    # Object storage
    {:ex_aws, "~> 2.5"},
    {:ex_aws_s3, "~> 2.5"},
    {:sweet_xml, "~> 0.7"},
    {:hackney, "~> 1.20"},

    # Image processing
    {:vix, "~> 0.30"},
    {:image, "~> 0.50"},

    # Scripting
    {:luerl, "~> 1.2"}
  ]
end
```

(Leave existing Phoenix deps — `:phoenix`, `:phoenix_ecto`, `:ecto_sql`, `:postgrex`, `:phoenix_html`, `:phoenix_live_reload`, `:phoenix_live_dashboard`, `:phoenix_live_view`, `:floki`, `:esbuild`, `:tailwind`, `:swoosh`, `:finch`, `:telemetry_metrics`, `:telemetry_poller`, `:gettext`, `:jason`, `:dns_cluster`, `:bandit` — untouched.)

- [ ] **Step 2: Fetch deps and compile**

Run:
```bash
mix deps.get && mix deps.compile
```
Expected: all deps install and compile. `argon2_elixir` and `vix` will compile native code (Argon2 reference impl; Vix needs `libvips` installed on the host — install via `brew install vips` on Mac if it fails).

- [ ] **Step 3: Verify the app still compiles**

Run:
```bash
mix compile
```
Expected: clean compile.

- [ ] **Step 4: Commit**

```bash
git add mix.exs mix.lock
git commit -m "feat: add custom deps (argon2, hammer, protobuf, ex_aws, vix, luerl)"
```

---

### Task 3: Configure runtime environment

**Files:**
- Modify: `config/runtime.exs`
- Modify: `config/config.exs`
- Modify: `config/dev.exs`
- Modify: `config/test.exs`
- Modify: `config/prod.exs`

- [ ] **Step 1: Write `config/runtime.exs` with all env-var-driven config**

Replace the `if config_env() == :prod do ... end` block in `config/runtime.exs` with:

```elixir
import Config

if System.get_env("PHX_SERVER") do
  config :boxland, BoxlandWeb.Endpoint, server: true
end

if config_env() == :prod do
  database_url =
    System.get_env("DATABASE_URL") ||
      raise """
      environment variable DATABASE_URL is missing.
      For example: ecto://USER:PASS@HOST/DATABASE
      """

  maybe_ipv6 = if System.get_env("ECTO_IPV6") in ~w(true 1), do: [:inet6], else: []

  config :boxland, Boxland.Repo,
    url: database_url,
    pool_size: String.to_integer(System.get_env("POOL_SIZE") || "10"),
    socket_options: maybe_ipv6

  secret_key_base =
    System.get_env("SECRET_KEY_BASE") ||
      raise "environment variable SECRET_KEY_BASE is missing."

  host = System.get_env("PHX_HOST") || "example.com"
  port = String.to_integer(System.get_env("PORT") || "4000")

  config :boxland, BoxlandWeb.Endpoint,
    url: [host: host, port: 443, scheme: "https"],
    http: [ip: {0, 0, 0, 0, 0, 0, 0, 0}, port: port],
    secret_key_base: secret_key_base

  # Redis
  config :boxland, :redis_url, System.fetch_env!("REDIS_URL")

  # Object storage (S3-compatible)
  config :ex_aws,
    access_key_id: System.fetch_env!("S3_ACCESS_KEY_ID"),
    secret_access_key: System.fetch_env!("S3_SECRET_ACCESS_KEY")

  config :ex_aws, :s3,
    host: System.fetch_env!("S3_HOST"),
    bucket: System.fetch_env!("S3_BUCKET"),
    scheme: "https://"

  config :boxland, :cdn_base_url, System.fetch_env!("CDN_BASE_URL")

  # OAuth
  config :boxland, :oauth,
    google: [
      client_id: System.get_env("GOOGLE_OAUTH_CLIENT_ID"),
      client_secret: System.get_env("GOOGLE_OAUTH_CLIENT_SECRET")
    ],
    apple: [
      client_id: System.get_env("APPLE_OAUTH_CLIENT_ID"),
      client_secret: System.get_env("APPLE_OAUTH_CLIENT_SECRET")
    ],
    discord: [
      client_id: System.get_env("DISCORD_OAUTH_CLIENT_ID"),
      client_secret: System.get_env("DISCORD_OAUTH_CLIENT_SECRET")
    ]

  # Mailer
  config :boxland, Boxland.Mailer,
    adapter: Swoosh.Adapters.Postmark,
    api_key: System.fetch_env!("POSTMARK_API_KEY")

  config :swoosh, :api_client, Swoosh.ApiClient.Finch
end
```

- [ ] **Step 2: Configure structured logging in prod**

Append to `config/prod.exs`:

```elixir
# Structured logs (no ANSI colors, UTC timestamps, JSON-friendly format)
# Railway aggregates structured stdout logs without an extra exporter.
config :logger, :default_handler,
  formatter: {Logger.Formatter,
    [colors: [enabled: false], format: "$time [$level] $metadata$message\n"]}

config :logger,
  level: :info,
  utc_log: true
```

- [ ] **Step 3: Configure test environment for Argon2 speed**

In `config/test.exs`, ensure Argon2 is configured for low cost (fast tests). Append:

```elixir
config :argon2_elixir,
  t_cost: 1,
  m_cost: 8
```

- [ ] **Step 4: Verify config loads and app boots**

Run:
```bash
mix phx.server
```
Expected: Phoenix starts, listens on port 4000, logs "[info] Running BoxlandWeb.Endpoint with Bandit ..."

Stop with Ctrl+C twice.

- [ ] **Step 5: Commit**

```bash
git add config/
git commit -m "chore: configure runtime env vars and test argon2 cost"
```

---

## Phase 2 — Local development environment

### Task 4: Add docker-compose for postgres + redis + minio

**Files:**
- Create: `docker-compose.yml`
- Create: `.env.dev.example`
- Modify: `.gitignore` (add `.env.dev`)

- [ ] **Step 1: Write `docker-compose.yml`**

```yaml
services:
  postgres:
    image: postgres:16
    environment:
      POSTGRES_USER: boxland
      POSTGRES_PASSWORD: boxland
      POSTGRES_DB: boxland_dev
    ports:
      - "5432:5432"
    volumes:
      - pg_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U boxland"]
      interval: 5s
      timeout: 3s
      retries: 5

  redis:
    image: redis:7
    ports:
      - "6379:6379"
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 5

  minio:
    image: minio/minio
    command: server /data --console-address ":9001"
    environment:
      MINIO_ROOT_USER: boxland
      MINIO_ROOT_PASSWORD: boxland-dev-secret
    ports:
      - "9000:9000"
      - "9001:9001"
    volumes:
      - minio_data:/data

volumes:
  pg_data:
  minio_data:
```

- [ ] **Step 2: Write `.env.dev.example`**

```
# Copy to .env.dev (which is gitignored). Source via: set -a && source .env.dev && set +a
DATABASE_URL=ecto://boxland:boxland@localhost:5432/boxland_dev
REDIS_URL=redis://localhost:6379
SECRET_KEY_BASE=replace_with_output_of_mix_phx_gen_secret
S3_HOST=localhost:9000
S3_ACCESS_KEY_ID=boxland
S3_SECRET_ACCESS_KEY=boxland-dev-secret
S3_BUCKET=boxland-dev
CDN_BASE_URL=http://localhost:9000/boxland-dev
PHX_HOST=localhost
PHX_SERVER=true
```

- [ ] **Step 3: Add `.env.dev` to `.gitignore`**

Append to `.gitignore`:
```
# Local env files
.env.dev
.env.prod
.env.local
```

- [ ] **Step 4: Bring up services and verify**

Run:
```bash
docker compose up -d
docker compose ps
```
Expected: postgres, redis, minio all show `running` with `healthy` status (postgres + redis after a few seconds).

- [ ] **Step 5: Verify Postgres reachable from mix**

Run:
```bash
mix ecto.create
```
Expected: `The database for Boxland.Repo has been created`.

- [ ] **Step 6: Commit**

```bash
git add docker-compose.yml .env.dev.example .gitignore
git commit -m "chore: add docker-compose for local postgres/redis/minio"
```

---

### Task 5: Add justfile for common commands

**Files:**
- Create: `justfile`

- [ ] **Step 1: Write `justfile`**

```just
default:
  @just --list

# Bring up local services (postgres + redis + minio)
dev-up:
  docker compose up -d

# Tear down local services
dev-down:
  docker compose down

# Run Phoenix in dev mode with hot reload
serve:
  iex -S mix phx.server

# Drop, create, and migrate dev database
db-reset:
  mix ecto.drop --force
  mix ecto.create
  mix ecto.migrate

# Run migrations
db-migrate:
  mix ecto.migrate

# Regenerate Protobuf code (Elixir + TS)
proto-gen:
  mix proto.gen

# Verify Protobuf generated files are up to date (used in CI)
proto-check:
  mix proto.gen --check

# Run full test suite
test *args:
  mix test {{args}}

# Format check + linter + typespecs + tests
ci:
  mix format --check-formatted
  mix credo --strict
  mix proto.gen --check
  mix test
```

- [ ] **Step 2: Verify just is installed**

Run:
```bash
just --version
```
Expected: prints version. If missing, install via `brew install just`.

- [ ] **Step 3: Verify justfile parses**

Run:
```bash
just
```
Expected: prints the recipe list (default-listing target).

- [ ] **Step 4: Commit**

```bash
git add justfile
git commit -m "chore: add justfile for common dev commands"
```

---

## Phase 3 — Health endpoints

### Task 6: Add HealthController with `/healthz` and `/readyz`

**Files:**
- Create: `lib/boxland_web/controllers/health_controller.ex`
- Create: `test/boxland_web/controllers/health_controller_test.exs`
- Modify: `lib/boxland_web/router.ex`

- [ ] **Step 1: Write the failing test**

Create `test/boxland_web/controllers/health_controller_test.exs`:

```elixir
defmodule BoxlandWeb.HealthControllerTest do
  use BoxlandWeb.ConnCase, async: true

  describe "GET /healthz" do
    test "returns 200 ok", %{conn: conn} do
      conn = get(conn, ~p"/healthz")
      assert response(conn, 200) == "ok"
    end
  end

  describe "GET /readyz" do
    test "returns 200 ready when db is reachable", %{conn: conn} do
      conn = get(conn, ~p"/readyz")
      assert response(conn, 200) == "ready"
    end
  end
end
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
mix test test/boxland_web/controllers/health_controller_test.exs
```
Expected: 2 failures, "no route found for GET /healthz".

- [ ] **Step 3: Write the controller**

Create `lib/boxland_web/controllers/health_controller.ex`:

```elixir
defmodule BoxlandWeb.HealthController do
  use BoxlandWeb, :controller

  @doc """
  Liveness probe: returns 200 if the BEAM is up and the HTTP layer is serving.
  Used by Railway for restart decisions. Does NOT check DB or Redis.
  """
  def healthz(conn, _params) do
    text(conn, "ok")
  end

  @doc """
  Readiness probe: returns 200 only if the app can serve traffic
  (DB reachable + Redis reachable). Returns 503 otherwise.
  """
  def readyz(conn, _params) do
    case check_db() do
      :ok -> text(conn, "ready")
      {:error, reason} ->
        conn
        |> put_status(503)
        |> text("not ready: db: #{reason}")
    end
  end

  defp check_db do
    case Ecto.Adapters.SQL.query(Boxland.Repo, "SELECT 1", []) do
      {:ok, _} -> :ok
      {:error, err} -> {:error, inspect(err)}
    end
  end
end
```

- [ ] **Step 4: Wire up routes**

Edit `lib/boxland_web/router.ex`. After the existing `scope "/", BoxlandWeb do ... end` block, add:

```elixir
scope "/", BoxlandWeb do
  get "/healthz", HealthController, :healthz
  get "/readyz", HealthController, :readyz
end
```

If the router already has a `pipe_through [:browser]` scope, place these health routes OUTSIDE that pipeline (no plugs needed for health endpoints).

- [ ] **Step 5: Run the test to verify pass**

Run:
```bash
mix test test/boxland_web/controllers/health_controller_test.exs
```
Expected: 2 tests pass.

- [ ] **Step 6: Commit**

```bash
git add lib/boxland_web/controllers/health_controller.ex \
        lib/boxland_web/router.ex \
        test/boxland_web/controllers/health_controller_test.exs
git commit -m "feat: add /healthz and /readyz endpoints"
```

---

## Phase 4 — Initial database migration

### Task 7: Generate the initial schema migration

**Files:**
- Create: `priv/repo/migrations/<timestamp>_initial_schema.exs`

- [ ] **Step 1: Generate the migration file**

Run:
```bash
mix ecto.gen.migration initial_schema
```
Expected: creates `priv/repo/migrations/<timestamp>_initial_schema.exs`. Note the path — replace `<timestamp>` references in subsequent steps with the actual filename.

- [ ] **Step 2: Replace migration contents with the full schema**

Open the generated file. Replace its contents with:

```elixir
defmodule Boxland.Repo.Migrations.InitialSchema do
  use Ecto.Migration

  def change do
    # === AUTH ===

    create table(:designers) do
      add :email, :string, null: false
      add :password_hash, :string, null: false
      add :display_name, :string, null: false
      timestamps(type: :utc_datetime)
    end
    create unique_index(:designers, [:email])

    create table(:designer_sessions) do
      add :designer_id, references(:designers, on_delete: :delete_all), null: false
      add :token_hash, :binary, null: false
      add :ip, :inet
      add :expires_at, :utc_datetime, null: false
      timestamps(type: :utc_datetime, updated_at: false)
    end
    create unique_index(:designer_sessions, [:token_hash])
    create index(:designer_sessions, [:designer_id])

    create table(:players) do
      add :email, :string
      add :password_hash, :string
      add :display_name, :string, null: false
      timestamps(type: :utc_datetime)
    end
    create unique_index(:players, [:email], where: "email IS NOT NULL")

    create table(:player_oauth_links) do
      add :player_id, references(:players, on_delete: :delete_all), null: false
      add :provider, :string, null: false
      add :provider_user_id, :string, null: false
      timestamps(type: :utc_datetime, updated_at: false)
    end
    create unique_index(:player_oauth_links, [:provider, :provider_user_id])
    create index(:player_oauth_links, [:player_id])

    create table(:player_sessions) do
      add :player_id, references(:players, on_delete: :delete_all), null: false
      add :refresh_token_hash, :binary, null: false
      add :expires_at, :utc_datetime, null: false
      timestamps(type: :utc_datetime, updated_at: false)
    end
    create unique_index(:player_sessions, [:refresh_token_hash])
    create index(:player_sessions, [:player_id])

    # === ASSETS ===

    create table(:assets) do
      add :owner_id, references(:designers, on_delete: :restrict), null: false
      add :kind, :string, null: false
      add :name, :string, null: false
      add :sha256, :binary, null: false
      add :content_url, :string, null: false
      add :byte_size, :integer, null: false
      add :mime_type, :string, null: false
      add :metadata, :map, null: false, default: %{}
      timestamps(type: :utc_datetime)
    end
    create unique_index(:assets, [:sha256])
    create index(:assets, [:owner_id])
    create index(:assets, [:kind])

    # === MAPS ===

    create table(:maps) do
      add :owner_id, references(:designers, on_delete: :restrict), null: false
      add :slug, :string, null: false
      add :name, :string, null: false
      add :width, :integer, null: false
      add :height, :integer, null: false
      timestamps(type: :utc_datetime)
    end
    create unique_index(:maps, [:owner_id, :slug])

    create table(:map_layers) do
      add :map_id, references(:maps, on_delete: :delete_all), null: false
      add :name, :string, null: false
      add :z_index, :integer, null: false
      add :tiles, :map, null: false, default: %{}
      timestamps(type: :utc_datetime)
    end
    create unique_index(:map_layers, [:map_id, :name])
    create index(:map_layers, [:map_id, :z_index])

    # === ENTITIES ===

    create table(:entity_types) do
      add :owner_id, references(:designers, on_delete: :restrict), null: false
      add :slug, :string, null: false
      add :name, :string, null: false
      add :visual_ref, :map, null: false, default: %{}
      add :animation_bindings, :map, null: false, default: %{}
      add :components, {:array, :map}, null: false, default: []
      add :scripts, {:array, :map}, null: false, default: []
      add :default_collision_mask, :string, null: false, default: "land"
      add :default_z_index, :integer, null: false, default: 25
      timestamps(type: :utc_datetime)
    end
    create unique_index(:entity_types, [:owner_id, :slug])

    # === WORLDS ===

    create table(:worlds) do
      add :owner_id, references(:designers, on_delete: :restrict), null: false
      add :slug, :string, null: false
      add :name, :string, null: false
      timestamps(type: :utc_datetime)
    end
    create unique_index(:worlds, [:owner_id, :slug])

    # === LEVELS ===

    create table(:levels) do
      add :owner_id, references(:designers, on_delete: :restrict), null: false
      add :slug, :string, null: false
      add :name, :string, null: false
      add :map_id, references(:maps, on_delete: :restrict), null: false
      add :world_id, references(:worlds, on_delete: :nilify_all)
      add :hud_config, :map, null: false, default: %{}
      add :instancing, :string, null: false, default: "shared"
      timestamps(type: :utc_datetime)
    end
    create unique_index(:levels, [:owner_id, :slug])
    create index(:levels, [:world_id])
    create index(:levels, [:map_id])

    create table(:level_entities) do
      add :level_id, references(:levels, on_delete: :delete_all), null: false
      add :entity_type_id, references(:entity_types, on_delete: :restrict), null: false
      add :pos_x, :integer, null: false
      add :pos_y, :integer, null: false
      add :z_index_override, :integer
      add :instance_overrides, :map, null: false, default: %{}
      add :script_state, :map, null: false, default: %{}
      timestamps(type: :utc_datetime)
    end
    create index(:level_entities, [:level_id])
    create index(:level_entities, [:level_id, :z_index_override])

    # === GAME RUNTIME STATE ===

    create table(:level_state) do
      add :level_id, references(:levels, on_delete: :delete_all), null: false
      add :instance_key, :string, null: false
      add :state, :binary, null: false
      add :flushed_at, :utc_datetime, null: false
      timestamps(type: :utc_datetime, updated_at: false)
    end
    create unique_index(:level_state, [:level_id, :instance_key])
  end
end
```

- [ ] **Step 3: Run the migration**

Run:
```bash
mix ecto.migrate
```
Expected: creates all tables, prints `[info] Migrations have run successfully`.

- [ ] **Step 4: Verify the schema in psql**

Run:
```bash
docker compose exec postgres psql -U boxland -d boxland_dev -c "\dt"
```
Expected: lists all tables: `designers`, `designer_sessions`, `players`, `player_oauth_links`, `player_sessions`, `assets`, `maps`, `map_layers`, `entity_types`, `worlds`, `levels`, `level_entities`, `level_state`, plus the `schema_migrations` table Ecto manages.

- [ ] **Step 5: Commit**

```bash
git add priv/repo/migrations/
git commit -m "feat: initial database schema (auth, assets, maps, entities, levels, worlds, level_state)"
```

---

## Phase 5 — Ecto schemas

> **Note on TDD pattern for schemas:** Each schema test verifies the changeset accepts valid attrs and rejects invalid ones. We bundle related schemas per task to avoid a 13-task explosion. Each task: write tests, run fail, write schemas, run pass, commit.

### Task 8: Designer + DesignerSession schemas

**Files:**
- Create: `lib/boxland/auth/designer.ex`
- Create: `lib/boxland/auth/designer_session.ex`
- Create: `test/boxland/auth/designer_test.exs`

- [ ] **Step 1: Write the failing test**

Create `test/boxland/auth/designer_test.exs`:

```elixir
defmodule Boxland.Auth.DesignerTest do
  use Boxland.DataCase, async: true

  alias Boxland.Auth.Designer
  alias Boxland.Auth.DesignerSession

  describe "Designer changeset" do
    test "valid attrs produce a valid changeset" do
      attrs = %{
        email: "alice@example.com",
        password_hash: "$argon2id$placeholder",
        display_name: "Alice"
      }
      changeset = Designer.changeset(%Designer{}, attrs)
      assert changeset.valid?
    end

    test "missing email is invalid" do
      changeset = Designer.changeset(%Designer{}, %{password_hash: "x", display_name: "A"})
      refute changeset.valid?
      assert "can't be blank" in errors_on(changeset).email
    end

    test "duplicate email returns a constraint error on insert" do
      attrs = %{email: "dup@example.com", password_hash: "x", display_name: "Dup"}
      assert {:ok, _} = %Designer{} |> Designer.changeset(attrs) |> Boxland.Repo.insert()
      assert {:error, changeset} = %Designer{} |> Designer.changeset(attrs) |> Boxland.Repo.insert()
      refute changeset.valid?
      assert "has already been taken" in errors_on(changeset).email
    end
  end

  describe "DesignerSession changeset" do
    setup do
      {:ok, designer} =
        %Designer{}
        |> Designer.changeset(%{email: "ds@example.com", password_hash: "x", display_name: "DS"})
        |> Boxland.Repo.insert()
      {:ok, designer: designer}
    end

    test "valid attrs produce a valid changeset", %{designer: d} do
      attrs = %{
        designer_id: d.id,
        token_hash: :crypto.hash(:sha256, "secret"),
        expires_at: DateTime.utc_now() |> DateTime.add(3600) |> DateTime.truncate(:second)
      }
      changeset = DesignerSession.changeset(%DesignerSession{}, attrs)
      assert changeset.valid?
    end

    test "duplicate token_hash returns a constraint error on insert", %{designer: d} do
      hash = :crypto.hash(:sha256, "shared-secret")
      attrs = %{designer_id: d.id, token_hash: hash, expires_at: DateTime.utc_now() |> DateTime.add(3600) |> DateTime.truncate(:second)}
      assert {:ok, _} = %DesignerSession{} |> DesignerSession.changeset(attrs) |> Boxland.Repo.insert()
      assert {:error, changeset} = %DesignerSession{} |> DesignerSession.changeset(attrs) |> Boxland.Repo.insert()
      refute changeset.valid?
      assert "has already been taken" in errors_on(changeset).token_hash
    end
  end
end
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
mix test test/boxland/auth/designer_test.exs
```
Expected: compile error — `Boxland.Auth.Designer` does not exist.

- [ ] **Step 3: Write Designer schema**

Create `lib/boxland/auth/designer.ex`:

```elixir
defmodule Boxland.Auth.Designer do
  @moduledoc """
  An IDE user. Owns assets, maps, levels, entities, worlds.
  Authenticated via cookie-backed session (DesignerSession).
  """
  use Ecto.Schema
  import Ecto.Changeset

  schema "designers" do
    field :email, :string
    field :password_hash, :string
    field :display_name, :string

    has_many :sessions, Boxland.Auth.DesignerSession

    timestamps(type: :utc_datetime)
  end

  def changeset(designer, attrs) do
    designer
    |> cast(attrs, [:email, :password_hash, :display_name])
    |> validate_required([:email, :password_hash, :display_name])
    |> validate_format(:email, ~r/@/)
    |> update_change(:email, &String.downcase/1)
    |> unique_constraint(:email)
  end
end
```

- [ ] **Step 4: Write DesignerSession schema**

Create `lib/boxland/auth/designer_session.ex`:

```elixir
defmodule Boxland.Auth.DesignerSession do
  @moduledoc """
  A single designer login session. Cookie carries an opaque random token
  whose sha256 is stored as `token_hash`. Sliding-window 30-day TTL.

  Note: the underlying `inet` Postgres column is read/written as a string
  in v1 to avoid pulling in ecto_network. If we need typed IP operations
  later we can swap the field type without a migration.
  """
  use Ecto.Schema
  import Ecto.Changeset

  schema "designer_sessions" do
    field :token_hash, :binary
    field :ip, :string
    field :expires_at, :utc_datetime

    belongs_to :designer, Boxland.Auth.Designer

    timestamps(type: :utc_datetime, updated_at: false)
  end

  def changeset(session, attrs) do
    session
    |> cast(attrs, [:designer_id, :token_hash, :ip, :expires_at])
    |> validate_required([:designer_id, :token_hash, :expires_at])
    |> unique_constraint(:token_hash)
    |> foreign_key_constraint(:designer_id)
  end
end
```

- [ ] **Step 5: Run test to verify pass**

Run:
```bash
mix test test/boxland/auth/designer_test.exs
```
Expected: 4 tests, 4 passed.

- [ ] **Step 6: Commit**

```bash
git add lib/boxland/auth/ test/boxland/auth/
git commit -m "feat: add Designer and DesignerSession Ecto schemas"
```

---

### Task 9: Player + PlayerOAuthLink + PlayerSession schemas

**Files:**
- Create: `lib/boxland/auth/player.ex`
- Create: `lib/boxland/auth/player_oauth_link.ex`
- Create: `lib/boxland/auth/player_session.ex`
- Create: `test/boxland/auth/player_test.exs`

- [ ] **Step 1: Write the failing test**

Create `test/boxland/auth/player_test.exs`:

```elixir
defmodule Boxland.Auth.PlayerTest do
  use Boxland.DataCase, async: true

  alias Boxland.Auth.{Player, PlayerOAuthLink, PlayerSession}

  describe "Player changeset" do
    test "valid attrs with email + password produce a valid changeset" do
      attrs = %{email: "p@example.com", password_hash: "x", display_name: "Player"}
      changeset = Player.changeset(%Player{}, attrs)
      assert changeset.valid?
    end

    test "valid attrs with no email (oauth-only player) produce a valid changeset" do
      changeset = Player.changeset(%Player{}, %{display_name: "OAuthOnly"})
      assert changeset.valid?
    end

    test "missing display_name is invalid" do
      changeset = Player.changeset(%Player{}, %{email: "x@y.com"})
      refute changeset.valid?
      assert "can't be blank" in errors_on(changeset).display_name
    end
  end

  describe "PlayerOAuthLink changeset" do
    setup do
      {:ok, player} =
        %Player{} |> Player.changeset(%{display_name: "P"}) |> Boxland.Repo.insert()
      {:ok, player: player}
    end

    test "valid attrs produce a valid changeset", %{player: p} do
      attrs = %{player_id: p.id, provider: "google", provider_user_id: "google-12345"}
      changeset = PlayerOAuthLink.changeset(%PlayerOAuthLink{}, attrs)
      assert changeset.valid?
    end

    test "duplicate (provider, provider_user_id) is rejected", %{player: p} do
      attrs = %{player_id: p.id, provider: "google", provider_user_id: "shared"}
      assert {:ok, _} = %PlayerOAuthLink{} |> PlayerOAuthLink.changeset(attrs) |> Boxland.Repo.insert()
      assert {:error, changeset} = %PlayerOAuthLink{} |> PlayerOAuthLink.changeset(attrs) |> Boxland.Repo.insert()
      refute changeset.valid?
    end
  end

  describe "PlayerSession changeset" do
    setup do
      {:ok, player} =
        %Player{} |> Player.changeset(%{display_name: "P"}) |> Boxland.Repo.insert()
      {:ok, player: player}
    end

    test "valid attrs produce a valid changeset", %{player: p} do
      attrs = %{
        player_id: p.id,
        refresh_token_hash: :crypto.hash(:sha256, "rt"),
        expires_at: DateTime.utc_now() |> DateTime.add(3600) |> DateTime.truncate(:second)
      }
      changeset = PlayerSession.changeset(%PlayerSession{}, attrs)
      assert changeset.valid?
    end
  end
end
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
mix test test/boxland/auth/player_test.exs
```
Expected: compile error — modules don't exist.

- [ ] **Step 3: Write Player schema**

Create `lib/boxland/auth/player.ex`:

```elixir
defmodule Boxland.Auth.Player do
  @moduledoc """
  A game runtime user. Authenticated via JWT (access + refresh tokens).
  Email + password OR OAuth (Google, Apple, Discord).
  """
  use Ecto.Schema
  import Ecto.Changeset

  schema "players" do
    field :email, :string
    field :password_hash, :string
    field :display_name, :string

    has_many :oauth_links, Boxland.Auth.PlayerOAuthLink
    has_many :sessions, Boxland.Auth.PlayerSession

    timestamps(type: :utc_datetime)
  end

  def changeset(player, attrs) do
    player
    |> cast(attrs, [:email, :password_hash, :display_name])
    |> validate_required([:display_name])
    |> validate_format(:email, ~r/@/, message: "must contain @ if provided", allow_nil: true)
    |> update_change(:email, fn nil -> nil; v -> String.downcase(v) end)
    |> unique_constraint(:email)
  end
end
```

- [ ] **Step 4: Write PlayerOAuthLink schema**

Create `lib/boxland/auth/player_oauth_link.ex`:

```elixir
defmodule Boxland.Auth.PlayerOAuthLink do
  @moduledoc "Links a Player to an external OAuth identity (provider + provider_user_id)."
  use Ecto.Schema
  import Ecto.Changeset

  @valid_providers ~w(google apple discord)

  schema "player_oauth_links" do
    field :provider, :string
    field :provider_user_id, :string

    belongs_to :player, Boxland.Auth.Player

    timestamps(type: :utc_datetime, updated_at: false)
  end

  def changeset(link, attrs) do
    link
    |> cast(attrs, [:player_id, :provider, :provider_user_id])
    |> validate_required([:player_id, :provider, :provider_user_id])
    |> validate_inclusion(:provider, @valid_providers)
    |> unique_constraint([:provider, :provider_user_id], name: :player_oauth_links_provider_provider_user_id_index)
    |> foreign_key_constraint(:player_id)
  end
end
```

- [ ] **Step 5: Write PlayerSession schema**

Create `lib/boxland/auth/player_session.ex`:

```elixir
defmodule Boxland.Auth.PlayerSession do
  @moduledoc "A player's refresh-token session. Refresh token hashed."
  use Ecto.Schema
  import Ecto.Changeset

  schema "player_sessions" do
    field :refresh_token_hash, :binary
    field :expires_at, :utc_datetime

    belongs_to :player, Boxland.Auth.Player

    timestamps(type: :utc_datetime, updated_at: false)
  end

  def changeset(session, attrs) do
    session
    |> cast(attrs, [:player_id, :refresh_token_hash, :expires_at])
    |> validate_required([:player_id, :refresh_token_hash, :expires_at])
    |> unique_constraint(:refresh_token_hash)
    |> foreign_key_constraint(:player_id)
  end
end
```

- [ ] **Step 6: Run test to verify pass**

Run:
```bash
mix test test/boxland/auth/player_test.exs
```
Expected: 6 tests, 6 passed.

- [ ] **Step 7: Commit**

```bash
git add lib/boxland/auth/ test/boxland/auth/player_test.exs
git commit -m "feat: add Player, PlayerOAuthLink, PlayerSession schemas"
```

---

### Task 10: Asset schema with embedded metadata

**Files:**
- Create: `lib/boxland/library/asset.ex`
- Create: `lib/boxland/library/asset/metadata.ex`
- Create: `lib/boxland/library/asset/sprite_metadata.ex`
- Create: `lib/boxland/library/asset/spritesheet_metadata.ex`
- Create: `lib/boxland/library/asset/audio_metadata.ex`
- Create: `test/boxland/library/asset_test.exs`

- [ ] **Step 1: Write the failing test**

Create `test/boxland/library/asset_test.exs`:

```elixir
defmodule Boxland.Library.AssetTest do
  use Boxland.DataCase, async: true

  alias Boxland.Library.Asset

  setup do
    {:ok, designer} =
      %Boxland.Auth.Designer{}
      |> Boxland.Auth.Designer.changeset(%{email: "d@e.com", password_hash: "x", display_name: "D"})
      |> Boxland.Repo.insert()
    {:ok, designer: designer}
  end

  test "valid sprite asset produces a valid changeset", %{designer: d} do
    attrs = %{
      owner_id: d.id,
      kind: "sprite",
      name: "goblin",
      sha256: :crypto.hash(:sha256, "fakebytes"),
      content_url: "https://cdn.example.com/sprites/aa/bb/abcdef.png",
      byte_size: 1234,
      mime_type: "image/png",
      metadata: %{"collision" => %{"kind" => "preset", "value" => "solid"}}
    }
    changeset = Asset.changeset(%Asset{}, attrs)
    assert changeset.valid?
  end

  test "invalid kind is rejected", %{designer: d} do
    attrs = %{
      owner_id: d.id,
      kind: "weird",
      name: "x",
      sha256: :crypto.hash(:sha256, "x"),
      content_url: "https://x",
      byte_size: 1,
      mime_type: "image/png",
      metadata: %{}
    }
    changeset = Asset.changeset(%Asset{}, attrs)
    refute changeset.valid?
    assert "is invalid" in errors_on(changeset).kind
  end

  test "duplicate sha256 is rejected", %{designer: d} do
    sha = :crypto.hash(:sha256, "shared")
    attrs = %{
      owner_id: d.id,
      kind: "sprite",
      name: "a",
      sha256: sha,
      content_url: "https://x",
      byte_size: 1,
      mime_type: "image/png",
      metadata: %{}
    }
    assert {:ok, _} = %Asset{} |> Asset.changeset(attrs) |> Boxland.Repo.insert()
    assert {:error, changeset} = %Asset{} |> Asset.changeset(%{attrs | name: "b"}) |> Boxland.Repo.insert()
    refute changeset.valid?
  end

  test "spritesheet metadata accepts grid + animations", %{designer: d} do
    attrs = %{
      owner_id: d.id,
      kind: "spritesheet",
      name: "hero_walk",
      sha256: :crypto.hash(:sha256, "spritesheet1"),
      content_url: "https://x",
      byte_size: 5000,
      mime_type: "image/png",
      metadata: %{
        "grid_cols" => 4,
        "grid_rows" => 2,
        "animations" => [
          %{"name" => "idle", "frames" => [0, 1, 2, 3], "fps" => 8, "loop" => true}
        ]
      }
    }
    changeset = Asset.changeset(%Asset{}, attrs)
    assert changeset.valid?
  end
end
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
mix test test/boxland/library/asset_test.exs
```
Expected: compile error — `Boxland.Library.Asset` does not exist.

- [ ] **Step 3: Write Asset schema**

Create `lib/boxland/library/asset.ex`:

```elixir
defmodule Boxland.Library.Asset do
  @moduledoc """
  A user-uploaded game file. Kind-discriminated: sprite, spritesheet, audio.
  Content-addressed via SHA256. The `metadata` jsonb field carries
  kind-specific data (validated by the Asset.Metadata helper module).
  """
  use Ecto.Schema
  import Ecto.Changeset

  @valid_kinds ~w(sprite spritesheet audio)

  schema "assets" do
    field :kind, :string
    field :name, :string
    field :sha256, :binary
    field :content_url, :string
    field :byte_size, :integer
    field :mime_type, :string
    field :metadata, :map, default: %{}

    belongs_to :owner, Boxland.Auth.Designer

    timestamps(type: :utc_datetime)
  end

  def changeset(asset, attrs) do
    asset
    |> cast(attrs, [:owner_id, :kind, :name, :sha256, :content_url, :byte_size, :mime_type, :metadata])
    |> validate_required([:owner_id, :kind, :name, :sha256, :content_url, :byte_size, :mime_type])
    |> validate_inclusion(:kind, @valid_kinds)
    |> validate_number(:byte_size, greater_than: 0)
    |> unique_constraint(:sha256)
    |> foreign_key_constraint(:owner_id)
  end
end
```

(Embedded typed-metadata schemas — for Sprite/Spritesheet/Audio — are deferred; for foundation, the `metadata` jsonb is loose and validated by the asset pipeline at ingest time. Foundation just stores it.)

- [ ] **Step 4: Run test to verify pass**

Run:
```bash
mix test test/boxland/library/asset_test.exs
```
Expected: 4 tests, 4 passed.

- [ ] **Step 5: Commit**

```bash
git add lib/boxland/library/ test/boxland/library/
git commit -m "feat: add Asset schema (kind discriminator, jsonb metadata)"
```

---

### Task 11: Map + MapLayer schemas

**Files:**
- Create: `lib/boxland/maps/map.ex`
- Create: `lib/boxland/maps/layer.ex`
- Create: `test/boxland/maps_test.exs`

- [ ] **Step 1: Write the failing test**

Create `test/boxland/maps_test.exs`:

```elixir
defmodule Boxland.MapsTest do
  use Boxland.DataCase, async: true

  alias Boxland.Maps.{Map, Layer}

  setup do
    {:ok, designer} =
      %Boxland.Auth.Designer{}
      |> Boxland.Auth.Designer.changeset(%{email: "d@e.com", password_hash: "x", display_name: "D"})
      |> Boxland.Repo.insert()
    {:ok, designer: designer}
  end

  test "valid map produces a valid changeset", %{designer: d} do
    attrs = %{owner_id: d.id, slug: "starter-village", name: "Starter Village", width: 64, height: 64}
    changeset = Map.changeset(%Map{}, attrs)
    assert changeset.valid?
  end

  test "duplicate (owner_id, slug) is rejected", %{designer: d} do
    attrs = %{owner_id: d.id, slug: "dup", name: "A", width: 32, height: 32}
    assert {:ok, _} = %Map{} |> Map.changeset(attrs) |> Boxland.Repo.insert()
    assert {:error, _} = %Map{} |> Map.changeset(attrs) |> Boxland.Repo.insert()
  end

  test "non-positive dimensions rejected", %{designer: d} do
    attrs = %{owner_id: d.id, slug: "bad", name: "Bad", width: 0, height: 64}
    changeset = Map.changeset(%Map{}, attrs)
    refute changeset.valid?
    assert "must be greater than 0" in errors_on(changeset).width
  end

  test "Layer changeset accepts tiles jsonb", %{designer: d} do
    {:ok, map} = %Map{} |> Map.changeset(%{owner_id: d.id, slug: "m", name: "M", width: 10, height: 10}) |> Boxland.Repo.insert()
    attrs = %{
      map_id: map.id,
      name: "ground",
      z_index: 0,
      tiles: %{"0,0" => %{"sprite_id" => 1}, "1,0" => %{"sheet_id" => 2, "frame" => 5}}
    }
    changeset = Layer.changeset(%Layer{}, attrs)
    assert changeset.valid?
  end
end
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
mix test test/boxland/maps_test.exs
```
Expected: compile error.

- [ ] **Step 3: Write Map schema**

Create `lib/boxland/maps/map.ex`:

```elixir
defmodule Boxland.Maps.Map do
  @moduledoc "A non-interactive grid of tiles, organized in z-indexed layers."
  use Ecto.Schema
  import Ecto.Changeset

  schema "maps" do
    field :slug, :string
    field :name, :string
    field :width, :integer
    field :height, :integer

    belongs_to :owner, Boxland.Auth.Designer
    has_many :layers, Boxland.Maps.Layer

    timestamps(type: :utc_datetime)
  end

  def changeset(map, attrs) do
    map
    |> cast(attrs, [:owner_id, :slug, :name, :width, :height])
    |> validate_required([:owner_id, :slug, :name, :width, :height])
    |> validate_number(:width, greater_than: 0)
    |> validate_number(:height, greater_than: 0)
    |> validate_format(:slug, ~r/^[a-z0-9][a-z0-9-]*$/)
    |> unique_constraint([:owner_id, :slug])
    |> foreign_key_constraint(:owner_id)
  end
end
```

- [ ] **Step 4: Write Layer schema**

Create `lib/boxland/maps/layer.ex`:

```elixir
defmodule Boxland.Maps.Layer do
  @moduledoc "A single z-indexed layer of tile placements within a Map."
  use Ecto.Schema
  import Ecto.Changeset

  schema "map_layers" do
    field :name, :string
    field :z_index, :integer
    field :tiles, :map, default: %{}

    belongs_to :map, Boxland.Maps.Map

    timestamps(type: :utc_datetime)
  end

  def changeset(layer, attrs) do
    layer
    |> cast(attrs, [:map_id, :name, :z_index, :tiles])
    |> validate_required([:map_id, :name, :z_index])
    |> unique_constraint([:map_id, :name])
    |> foreign_key_constraint(:map_id)
  end
end
```

- [ ] **Step 5: Run test to verify pass**

Run:
```bash
mix test test/boxland/maps_test.exs
```
Expected: 4 tests, 4 passed.

- [ ] **Step 6: Commit**

```bash
git add lib/boxland/maps/ test/boxland/maps_test.exs
git commit -m "feat: add Map and Layer schemas"
```

---

### Task 12: EntityType schema

**Files:**
- Create: `lib/boxland/entities/entity_type.ex`
- Create: `test/boxland/entities/entity_type_test.exs`

- [ ] **Step 1: Write the failing test**

Create `test/boxland/entities/entity_type_test.exs`:

```elixir
defmodule Boxland.Entities.EntityTypeTest do
  use Boxland.DataCase, async: true

  alias Boxland.Entities.EntityType

  setup do
    {:ok, designer} =
      %Boxland.Auth.Designer{}
      |> Boxland.Auth.Designer.changeset(%{email: "d@e.com", password_hash: "x", display_name: "D"})
      |> Boxland.Repo.insert()
    {:ok, designer: designer}
  end

  test "minimal valid entity_type", %{designer: d} do
    attrs = %{owner_id: d.id, slug: "goblin", name: "Goblin"}
    changeset = EntityType.changeset(%EntityType{}, attrs)
    assert changeset.valid?
  end

  test "components and scripts default to empty arrays", %{designer: d} do
    {:ok, et} =
      %EntityType{}
      |> EntityType.changeset(%{owner_id: d.id, slug: "barrel", name: "Barrel"})
      |> Boxland.Repo.insert()
    assert et.components == []
    assert et.scripts == []
    assert et.animation_bindings == %{}
  end

  test "accepts components and scripts arrays", %{designer: d} do
    attrs = %{
      owner_id: d.id,
      slug: "rich",
      name: "Rich Entity",
      visual_ref: %{"asset_id" => 1},
      animation_bindings: %{"idle" => "default_idle"},
      components: [%{"kind" => "movable", "config" => %{"speed_px_per_sec" => 64}}],
      scripts: [%{"hook" => "on_tick", "source" => %{"type" => "builtin", "action" => "idle"}}]
    }
    changeset = EntityType.changeset(%EntityType{}, attrs)
    assert changeset.valid?
  end

  test "duplicate (owner_id, slug) rejected", %{designer: d} do
    attrs = %{owner_id: d.id, slug: "dup", name: "Dup"}
    assert {:ok, _} = %EntityType{} |> EntityType.changeset(attrs) |> Boxland.Repo.insert()
    assert {:error, _} = %EntityType{} |> EntityType.changeset(attrs) |> Boxland.Repo.insert()
  end
end
```

- [ ] **Step 2: Run test to verify fail**

Run:
```bash
mix test test/boxland/entities/entity_type_test.exs
```
Expected: compile error.

- [ ] **Step 3: Write EntityType schema**

Create `lib/boxland/entities/entity_type.ex`:

```elixir
defmodule Boxland.Entities.EntityType do
  @moduledoc """
  A definition (template) of an interactive game object. Per-level
  placements are LevelEntities. Behavior is composed via components +
  Lua scripts attached to lifecycle hooks.
  """
  use Ecto.Schema
  import Ecto.Changeset

  schema "entity_types" do
    field :slug, :string
    field :name, :string
    field :visual_ref, :map, default: %{}
    field :animation_bindings, :map, default: %{}
    field :components, {:array, :map}, default: []
    field :scripts, {:array, :map}, default: []
    field :default_collision_mask, :string, default: "land"
    field :default_z_index, :integer, default: 25

    belongs_to :owner, Boxland.Auth.Designer

    timestamps(type: :utc_datetime)
  end

  def changeset(entity_type, attrs) do
    entity_type
    |> cast(attrs, [:owner_id, :slug, :name, :visual_ref, :animation_bindings, :components, :scripts, :default_collision_mask, :default_z_index])
    |> validate_required([:owner_id, :slug, :name])
    |> validate_format(:slug, ~r/^[a-z0-9][a-z0-9-]*$/)
    |> unique_constraint([:owner_id, :slug])
    |> foreign_key_constraint(:owner_id)
  end
end
```

- [ ] **Step 4: Run test to verify pass**

Run:
```bash
mix test test/boxland/entities/entity_type_test.exs
```
Expected: 4 passed.

- [ ] **Step 5: Commit**

```bash
git add lib/boxland/entities/ test/boxland/entities/
git commit -m "feat: add EntityType schema"
```

---

### Task 13: World, Level, LevelEntity schemas

**Files:**
- Create: `lib/boxland/worlds/world.ex`
- Create: `lib/boxland/levels/level.ex`
- Create: `lib/boxland/levels/level_entity.ex`
- Create: `test/boxland/levels_test.exs`

- [ ] **Step 1: Write the failing test**

Create `test/boxland/levels_test.exs`:

```elixir
defmodule Boxland.LevelsTest do
  use Boxland.DataCase, async: true

  alias Boxland.Worlds.World
  alias Boxland.Levels.{Level, LevelEntity}
  alias Boxland.Maps.Map
  alias Boxland.Entities.EntityType
  alias Boxland.Auth.Designer

  setup do
    {:ok, designer} =
      %Designer{}
      |> Designer.changeset(%{email: "d@e.com", password_hash: "x", display_name: "D"})
      |> Boxland.Repo.insert()
    {:ok, map} =
      %Map{}
      |> Map.changeset(%{owner_id: designer.id, slug: "m", name: "M", width: 32, height: 32})
      |> Boxland.Repo.insert()
    {:ok, et} =
      %EntityType{}
      |> EntityType.changeset(%{owner_id: designer.id, slug: "et", name: "ET"})
      |> Boxland.Repo.insert()
    {:ok, designer: designer, map: map, et: et}
  end

  describe "World" do
    test "minimal valid world", %{designer: d} do
      attrs = %{owner_id: d.id, slug: "starter", name: "Starter"}
      changeset = World.changeset(%World{}, attrs)
      assert changeset.valid?
    end

    test "duplicate (owner_id, slug) rejected", %{designer: d} do
      attrs = %{owner_id: d.id, slug: "w", name: "W"}
      assert {:ok, _} = %World{} |> World.changeset(attrs) |> Boxland.Repo.insert()
      assert {:error, _} = %World{} |> World.changeset(attrs) |> Boxland.Repo.insert()
    end
  end

  describe "Level" do
    test "valid standalone level (no world)", %{designer: d, map: m} do
      attrs = %{owner_id: d.id, slug: "tutorial", name: "Tutorial", map_id: m.id}
      changeset = Level.changeset(%Level{}, attrs)
      assert changeset.valid?
    end

    test "valid level in a world", %{designer: d, map: m} do
      {:ok, w} = %World{} |> World.changeset(%{owner_id: d.id, slug: "w", name: "W"}) |> Boxland.Repo.insert()
      attrs = %{owner_id: d.id, slug: "in-world", name: "InW", map_id: m.id, world_id: w.id}
      changeset = Level.changeset(%Level{}, attrs)
      assert changeset.valid?
    end

    test "invalid instancing rejected", %{designer: d, map: m} do
      attrs = %{owner_id: d.id, slug: "x", name: "X", map_id: m.id, instancing: "weird"}
      changeset = Level.changeset(%Level{}, attrs)
      refute changeset.valid?
      assert "is invalid" in errors_on(changeset).instancing
    end
  end

  describe "LevelEntity" do
    setup %{designer: d, map: m} do
      {:ok, level} =
        %Level{}
        |> Level.changeset(%{owner_id: d.id, slug: "lvl", name: "L", map_id: m.id})
        |> Boxland.Repo.insert()
      {:ok, level: level}
    end

    test "valid placement", %{level: lvl, et: et} do
      attrs = %{level_id: lvl.id, entity_type_id: et.id, pos_x: 100, pos_y: 200}
      changeset = LevelEntity.changeset(%LevelEntity{}, attrs)
      assert changeset.valid?
    end

    test "z_index_override is optional", %{level: lvl, et: et} do
      attrs = %{level_id: lvl.id, entity_type_id: et.id, pos_x: 0, pos_y: 0, z_index_override: 50}
      changeset = LevelEntity.changeset(%LevelEntity{}, attrs)
      assert changeset.valid?
    end
  end
end
```

- [ ] **Step 2: Run test to verify fail**

Run:
```bash
mix test test/boxland/levels_test.exs
```
Expected: compile error.

- [ ] **Step 3: Write World schema**

Create `lib/boxland/worlds/world.ex`:

```elixir
defmodule Boxland.Worlds.World do
  @moduledoc """
  A set of levels. The graph between them is emergent — transition entities
  in each level reference target levels via their `level_transition` component.
  """
  use Ecto.Schema
  import Ecto.Changeset

  schema "worlds" do
    field :slug, :string
    field :name, :string

    belongs_to :owner, Boxland.Auth.Designer
    has_many :levels, Boxland.Levels.Level

    timestamps(type: :utc_datetime)
  end

  def changeset(world, attrs) do
    world
    |> cast(attrs, [:owner_id, :slug, :name])
    |> validate_required([:owner_id, :slug, :name])
    |> validate_format(:slug, ~r/^[a-z0-9][a-z0-9-]*$/)
    |> unique_constraint([:owner_id, :slug])
    |> foreign_key_constraint(:owner_id)
  end
end
```

- [ ] **Step 4: Write Level schema**

Create `lib/boxland/levels/level.ex`:

```elixir
defmodule Boxland.Levels.Level do
  @moduledoc "A Map + entity placements + HUD config + instancing policy."
  use Ecto.Schema
  import Ecto.Changeset

  @valid_instancing ~w(shared per_party per_user)

  schema "levels" do
    field :slug, :string
    field :name, :string
    field :hud_config, :map, default: %{}
    field :instancing, :string, default: "shared"

    belongs_to :owner, Boxland.Auth.Designer
    belongs_to :map, Boxland.Maps.Map
    belongs_to :world, Boxland.Worlds.World
    has_many :entities, Boxland.Levels.LevelEntity

    timestamps(type: :utc_datetime)
  end

  def changeset(level, attrs) do
    level
    |> cast(attrs, [:owner_id, :slug, :name, :map_id, :world_id, :hud_config, :instancing])
    |> validate_required([:owner_id, :slug, :name, :map_id])
    |> validate_format(:slug, ~r/^[a-z0-9][a-z0-9-]*$/)
    |> validate_inclusion(:instancing, @valid_instancing)
    |> unique_constraint([:owner_id, :slug])
    |> foreign_key_constraint(:owner_id)
    |> foreign_key_constraint(:map_id)
    |> foreign_key_constraint(:world_id)
  end
end
```

- [ ] **Step 5: Write LevelEntity schema**

Create `lib/boxland/levels/level_entity.ex`:

```elixir
defmodule Boxland.Levels.LevelEntity do
  @moduledoc """
  An entity placement in a level (the instance of an entity_type at a
  specific coordinate, with optional per-instance overrides).
  """
  use Ecto.Schema
  import Ecto.Changeset

  schema "level_entities" do
    field :pos_x, :integer
    field :pos_y, :integer
    field :z_index_override, :integer
    field :instance_overrides, :map, default: %{}
    field :script_state, :map, default: %{}

    belongs_to :level, Boxland.Levels.Level
    belongs_to :entity_type, Boxland.Entities.EntityType

    timestamps(type: :utc_datetime)
  end

  def changeset(level_entity, attrs) do
    level_entity
    |> cast(attrs, [:level_id, :entity_type_id, :pos_x, :pos_y, :z_index_override, :instance_overrides, :script_state])
    |> validate_required([:level_id, :entity_type_id, :pos_x, :pos_y])
    |> foreign_key_constraint(:level_id)
    |> foreign_key_constraint(:entity_type_id)
  end
end
```

- [ ] **Step 6: Run test to verify pass**

Run:
```bash
mix test test/boxland/levels_test.exs
```
Expected: 7 passed.

- [ ] **Step 7: Commit**

```bash
git add lib/boxland/worlds/ lib/boxland/levels/ test/boxland/levels_test.exs
git commit -m "feat: add World, Level, LevelEntity schemas"
```

---

### Task 14: LevelState schema

**Files:**
- Create: `lib/boxland/game/level_state.ex`
- Create: `test/boxland/game/level_state_test.exs`

- [ ] **Step 1: Write the failing test**

Create `test/boxland/game/level_state_test.exs`:

```elixir
defmodule Boxland.Game.LevelStateTest do
  use Boxland.DataCase, async: true

  alias Boxland.Game.LevelState
  alias Boxland.Auth.Designer
  alias Boxland.Maps.Map
  alias Boxland.Levels.Level

  setup do
    {:ok, d} = %Designer{} |> Designer.changeset(%{email: "d@e.com", password_hash: "x", display_name: "D"}) |> Boxland.Repo.insert()
    {:ok, m} = %Map{} |> Map.changeset(%{owner_id: d.id, slug: "m", name: "M", width: 10, height: 10}) |> Boxland.Repo.insert()
    {:ok, lvl} = %Level{} |> Level.changeset(%{owner_id: d.id, slug: "l", name: "L", map_id: m.id}) |> Boxland.Repo.insert()
    {:ok, level: lvl}
  end

  test "valid state row", %{level: lvl} do
    attrs = %{level_id: lvl.id, instance_key: "shared", state: <<0, 1, 2>>, flushed_at: DateTime.utc_now() |> DateTime.truncate(:second)}
    changeset = LevelState.changeset(%LevelState{}, attrs)
    assert changeset.valid?
  end

  test "duplicate (level_id, instance_key) rejected", %{level: lvl} do
    attrs = %{level_id: lvl.id, instance_key: "shared", state: <<>>, flushed_at: DateTime.utc_now() |> DateTime.truncate(:second)}
    assert {:ok, _} = %LevelState{} |> LevelState.changeset(attrs) |> Boxland.Repo.insert()
    assert {:error, _} = %LevelState{} |> LevelState.changeset(attrs) |> Boxland.Repo.insert()
  end
end
```

- [ ] **Step 2: Run test to verify fail**

Run:
```bash
mix test test/boxland/game/level_state_test.exs
```
Expected: compile error.

- [ ] **Step 3: Write LevelState schema**

Create `lib/boxland/game/level_state.ex`:

```elixir
defmodule Boxland.Game.LevelState do
  @moduledoc """
  Persisted snapshot of mutable game state for a single running level
  instance. The runtime ECS world boots from this row + replays the
  Redis WAL since `flushed_at`.
  """
  use Ecto.Schema
  import Ecto.Changeset

  schema "level_state" do
    field :instance_key, :string
    field :state, :binary
    field :flushed_at, :utc_datetime

    belongs_to :level, Boxland.Levels.Level

    timestamps(type: :utc_datetime, updated_at: false)
  end

  def changeset(level_state, attrs) do
    level_state
    |> cast(attrs, [:level_id, :instance_key, :state, :flushed_at])
    |> validate_required([:level_id, :instance_key, :state, :flushed_at])
    |> unique_constraint([:level_id, :instance_key])
    |> foreign_key_constraint(:level_id)
  end
end
```

- [ ] **Step 4: Run test to verify pass**

Run:
```bash
mix test test/boxland/game/level_state_test.exs
```
Expected: 2 passed.

- [ ] **Step 5: Commit**

```bash
git add lib/boxland/game/ test/boxland/game/
git commit -m "feat: add LevelState schema"
```

---

## Phase 6 — Auth modules (no UI)

### Task 15: Password hashing wrapper

**Files:**
- Create: `lib/boxland/auth/password.ex`
- Create: `test/boxland/auth/password_test.exs`

- [ ] **Step 1: Write the failing test**

Create `test/boxland/auth/password_test.exs`:

```elixir
defmodule Boxland.Auth.PasswordTest do
  use ExUnit.Case, async: true
  alias Boxland.Auth.Password

  test "hash returns a string" do
    assert is_binary(Password.hash("secret"))
  end

  test "verify returns true for the matching plaintext" do
    hash = Password.hash("secret")
    assert Password.verify(hash, "secret")
  end

  test "verify returns false for a wrong plaintext" do
    hash = Password.hash("secret")
    refute Password.verify(hash, "wrong")
  end

  test "verify against a nil hash returns false" do
    refute Password.verify(nil, "anything")
  end
end
```

- [ ] **Step 2: Run test to verify fail**

Run:
```bash
mix test test/boxland/auth/password_test.exs
```
Expected: compile error.

- [ ] **Step 3: Write Password module**

Create `lib/boxland/auth/password.ex`:

```elixir
defmodule Boxland.Auth.Password do
  @moduledoc "Argon2 wrapper. Single hashing function for both auth realms."

  @doc "Hash a plaintext password. Returns the encoded Argon2 string."
  def hash(plaintext) when is_binary(plaintext) do
    Argon2.hash_pwd_salt(plaintext)
  end

  @doc """
  Verify a plaintext against a stored hash. Returns true on match.
  Returns false (without raising) if hash is nil — useful when checking
  OAuth-only players who have no password.
  """
  def verify(nil, _plaintext), do: Argon2.no_user_verify() && false
  def verify(hash, plaintext) when is_binary(hash) and is_binary(plaintext) do
    Argon2.verify_pass(plaintext, hash)
  end
end
```

- [ ] **Step 4: Run test to verify pass**

Run:
```bash
mix test test/boxland/auth/password_test.exs
```
Expected: 4 passed.

- [ ] **Step 5: Commit**

```bash
git add lib/boxland/auth/password.ex test/boxland/auth/password_test.exs
git commit -m "feat: add Argon2 password hashing wrapper"
```

---

### Task 16: Tokens module (Phoenix.Token wrappers)

**Files:**
- Create: `lib/boxland/auth/tokens.ex`
- Create: `test/boxland/auth/tokens_test.exs`

- [ ] **Step 1: Write the failing test**

Create `test/boxland/auth/tokens_test.exs`:

```elixir
defmodule Boxland.Auth.TokensTest do
  use ExUnit.Case, async: true
  alias Boxland.Auth.Tokens

  test "mint and verify a player access token" do
    token = Tokens.mint_player_access(%{player_id: 42})
    assert is_binary(token)
    assert {:ok, %{player_id: 42, realm: :player}} = Tokens.verify_game_token(token)
  end

  test "mint and verify a designer-as-sandbox token" do
    token = Tokens.mint_sandbox(%{designer_id: 7, level_id: 11})
    assert {:ok, %{player_id: 7, realm: :designer_sandbox, level_id: 11}} = Tokens.verify_game_token(token)
  end

  test "tampered token is rejected" do
    token = Tokens.mint_player_access(%{player_id: 1})
    bad = String.replace(token, ~r/.$/, "X")
    assert {:error, _} = Tokens.verify_game_token(bad)
  end
end
```

- [ ] **Step 2: Run test to verify fail**

Run:
```bash
mix test test/boxland/auth/tokens_test.exs
```
Expected: compile error.

- [ ] **Step 3: Write Tokens module**

Create `lib/boxland/auth/tokens.ex`:

```elixir
defmodule Boxland.Auth.Tokens do
  @moduledoc """
  Phoenix.Token wrappers for game socket auth. Two mintable token kinds:

    * Player access — short-lived (15 min), realm: :player
    * Designer-as-sandbox — short-lived (30 min), realm: :designer_sandbox,
      scoped to a single level_id

  Both verify through `verify_game_token/1`, which the GameSocket uses
  to accept either kind. Realm distinguishes downstream authorization.
  """

  @endpoint BoxlandWeb.Endpoint
  @namespace "game_token"
  @player_max_age 900           # 15 minutes
  @sandbox_max_age 1800         # 30 minutes

  @doc "Mint a player access token. `claims` must include `:player_id`."
  def mint_player_access(%{player_id: pid}) do
    Phoenix.Token.sign(@endpoint, @namespace, %{
      player_id: pid,
      realm: :player,
      iat: System.system_time(:second)
    })
  end

  @doc "Mint a designer-as-sandbox token, scoped to one level."
  def mint_sandbox(%{designer_id: did, level_id: lid}) do
    Phoenix.Token.sign(@endpoint, @namespace, %{
      player_id: did,
      realm: :designer_sandbox,
      level_id: lid,
      iat: System.system_time(:second)
    })
  end

  @doc """
  Verify any kind of game token. Returns the decoded claims or
  `{:error, reason}`. Enforces max_age based on the realm field.
  """
  def verify_game_token(token) do
    with {:ok, claims} <- Phoenix.Token.verify(@endpoint, @namespace, token, max_age: @sandbox_max_age),
         :ok <- enforce_age_for_realm(claims) do
      {:ok, claims}
    end
  end

  defp enforce_age_for_realm(%{realm: :player, iat: iat}) do
    if System.system_time(:second) - iat <= @player_max_age, do: :ok, else: {:error, :expired}
  end
  defp enforce_age_for_realm(%{realm: :designer_sandbox}), do: :ok
  defp enforce_age_for_realm(_), do: {:error, :invalid_claims}
end
```

- [ ] **Step 4: Run test to verify pass**

Run:
```bash
mix test test/boxland/auth/tokens_test.exs
```
Expected: 3 passed.

- [ ] **Step 5: Commit**

```bash
git add lib/boxland/auth/tokens.ex test/boxland/auth/tokens_test.exs
git commit -m "feat: add Tokens module (Phoenix.Token wrappers, two-realm verify)"
```

---

### Task 17: AccessPolicy module

**Files:**
- Create: `lib/boxland/auth/access_policy.ex`
- Create: `test/boxland/auth/access_policy_test.exs`

- [ ] **Step 1: Write the failing test**

Create `test/boxland/auth/access_policy_test.exs`:

```elixir
defmodule Boxland.Auth.AccessPolicyTest do
  use ExUnit.Case, async: true
  alias Boxland.Auth.AccessPolicy

  test "player can join the shared instance for a level" do
    assert :ok = AccessPolicy.allow_join?(%{realm: :player, player_id: 99}, "level:42:shared")
  end

  test "player can join their own user instance" do
    assert :ok = AccessPolicy.allow_join?(%{realm: :player, player_id: 99}, "level:42:user:99")
  end

  test "player cannot join another player's user instance" do
    assert {:error, _} = AccessPolicy.allow_join?(%{realm: :player, player_id: 99}, "level:42:user:42")
  end

  test "player cannot join a sandbox instance" do
    assert {:error, _} = AccessPolicy.allow_join?(%{realm: :player, player_id: 99}, "level:42:sandbox:7")
  end

  test "designer-sandbox can only join own sandbox" do
    assigns = %{realm: :designer_sandbox, player_id: 7, level_id: 42}
    assert :ok = AccessPolicy.allow_join?(assigns, "level:42:sandbox:7")
    assert {:error, _} = AccessPolicy.allow_join?(assigns, "level:42:sandbox:8")
    assert {:error, _} = AccessPolicy.allow_join?(assigns, "level:42:shared")
  end
end
```

- [ ] **Step 2: Run test to verify fail**

Run:
```bash
mix test test/boxland/auth/access_policy_test.exs
```
Expected: compile error.

- [ ] **Step 3: Write AccessPolicy module**

Create `lib/boxland/auth/access_policy.ex`:

```elixir
defmodule Boxland.Auth.AccessPolicy do
  @moduledoc """
  Decides whether a socket (with realm + player_id assigns) may join
  a given Channel topic. Realm isolation is enforced here AND at socket
  connect — defense in depth.
  """

  @type assigns :: %{required(:realm) => atom(), required(:player_id) => integer(), optional(:level_id) => integer()}

  @spec allow_join?(assigns, String.t()) :: :ok | {:error, atom()}
  def allow_join?(%{realm: realm} = assigns, "level:" <> rest) do
    case parse_topic(rest) do
      {:ok, parsed} -> check(realm, assigns, parsed)
      :error -> {:error, :bad_topic}
    end
  end

  def allow_join?(_, _topic), do: {:error, :unknown_topic}

  # ---

  defp check(:player, %{player_id: pid}, {_lvl, :shared}), do: :ok
  defp check(:player, %{player_id: pid}, {_lvl, {:user, uid}}) when pid == uid, do: :ok
  defp check(:player, _, {_lvl, {:user, _}}), do: {:error, :forbidden}
  defp check(:player, %{player_id: pid}, {_lvl, {:party, party_id}}) do
    # TODO when parties exist: check membership; for v1 reject
    {:error, :parties_not_implemented}
  end
  defp check(:player, _, {_lvl, {:sandbox, _}}), do: {:error, :forbidden}

  defp check(:designer_sandbox, %{player_id: did, level_id: my_lvl}, {topic_lvl, {:sandbox, target_did}})
       when did == target_did and my_lvl == topic_lvl, do: :ok
  defp check(:designer_sandbox, _, _), do: {:error, :forbidden}

  defp check(_, _, _), do: {:error, :forbidden}

  defp parse_topic(rest) do
    case String.split(rest, ":") do
      [lvl, "shared"] ->
        with {lvl_id, ""} <- Integer.parse(lvl), do: {:ok, {lvl_id, :shared}}
      [lvl, "user", uid] ->
        with {lvl_id, ""} <- Integer.parse(lvl), {uid_int, ""} <- Integer.parse(uid),
             do: {:ok, {lvl_id, {:user, uid_int}}}
      [lvl, "party", pid] ->
        with {lvl_id, ""} <- Integer.parse(lvl), {pid_int, ""} <- Integer.parse(pid),
             do: {:ok, {lvl_id, {:party, pid_int}}}
      [lvl, "sandbox", did] ->
        with {lvl_id, ""} <- Integer.parse(lvl), {did_int, ""} <- Integer.parse(did),
             do: {:ok, {lvl_id, {:sandbox, did_int}}}
      _ ->
        :error
    end
  end
end
```

- [ ] **Step 4: Run test to verify pass**

Run:
```bash
mix test test/boxland/auth/access_policy_test.exs
```
Expected: 5 passed.

- [ ] **Step 5: Commit**

```bash
git add lib/boxland/auth/access_policy.ex test/boxland/auth/access_policy_test.exs
git commit -m "feat: add AccessPolicy for game-socket realm isolation"
```

---

### Task 18: Designer auth context (register, authenticate, sessions)

**Files:**
- Create: `lib/boxland/auth.ex` (DesignerSession-handling functions or split into `designer_context.ex`)
- Modify: `lib/boxland/auth/designer.ex` (add registration_changeset)
- Create: `test/boxland/auth/designer_context_test.exs`

For clarity, we'll add Designer auth functions to a new `Boxland.Auth.Designers` context module (plural — context name) rather than overloading the schema.

- [ ] **Step 1: Write the failing test**

Create `test/boxland/auth/designer_context_test.exs`:

```elixir
defmodule Boxland.Auth.DesignersTest do
  use Boxland.DataCase, async: true
  alias Boxland.Auth.Designers

  describe "register_designer/1" do
    test "creates a designer with a hashed password" do
      assert {:ok, designer} = Designers.register_designer(%{email: "n@example.com", password: "supersecret123", display_name: "New"})
      assert designer.id
      assert designer.password_hash
      assert designer.password_hash != "supersecret123"
    end

    test "rejects short password" do
      assert {:error, changeset} = Designers.register_designer(%{email: "n@x.com", password: "short", display_name: "N"})
      refute changeset.valid?
    end
  end

  describe "authenticate/2" do
    setup do
      {:ok, d} = Designers.register_designer(%{email: "auth@x.com", password: "rightpassword", display_name: "Auth"})
      {:ok, designer: d}
    end

    test "returns {:ok, designer} on correct password", %{designer: d} do
      assert {:ok, returned} = Designers.authenticate("auth@x.com", "rightpassword")
      assert returned.id == d.id
    end

    test "returns :error on wrong password" do
      assert :error = Designers.authenticate("auth@x.com", "wrongpassword")
    end

    test "returns :error on unknown email" do
      assert :error = Designers.authenticate("nobody@x.com", "anything")
    end
  end

  describe "create_session/2 + fetch_session/1" do
    setup do
      {:ok, d} = Designers.register_designer(%{email: "s@x.com", password: "longenough!", display_name: "S"})
      {:ok, designer: d}
    end

    test "create + fetch round-trip", %{designer: d} do
      {:ok, plain_token} = Designers.create_session(d.id, "127.0.0.1")
      assert is_binary(plain_token)
      assert {:ok, fetched} = Designers.fetch_session(plain_token)
      assert fetched.id == d.id
    end

    test "fetch_session/1 returns :not_found for unknown token" do
      assert :not_found = Designers.fetch_session("totally-fake-token")
    end
  end
end
```

- [ ] **Step 2: Run test to verify fail**

Run:
```bash
mix test test/boxland/auth/designer_context_test.exs
```
Expected: compile error — `Boxland.Auth.Designers` does not exist.

- [ ] **Step 3: Write Designers context module**

Create `lib/boxland/auth/designers.ex`:

```elixir
defmodule Boxland.Auth.Designers do
  @moduledoc """
  Designer registration, authentication, and session management.
  Sessions are cookie-backed: a random token is generated, sha256 stored
  in the DB, plain token returned to be set as the cookie value.
  """

  import Ecto.Query
  alias Boxland.Repo
  alias Boxland.Auth.{Designer, DesignerSession, Password}

  @session_ttl_seconds 60 * 60 * 24 * 30   # 30 days
  @session_token_bytes 32

  @doc "Register a new designer. Hashes the plaintext password with Argon2."
  def register_designer(attrs) do
    case validate_password(attrs) do
      :ok ->
        attrs = Map.put(attrs, :password_hash, Password.hash(attrs[:password] || attrs["password"]))
        %Designer{}
        |> Designer.changeset(attrs)
        |> Repo.insert()

      {:error, changeset} ->
        {:error, changeset}
    end
  end

  @doc "Authenticate by email + plaintext password. Returns {:ok, designer} or :error."
  def authenticate(email, password) when is_binary(email) and is_binary(password) do
    designer = Repo.get_by(Designer, email: String.downcase(email))

    cond do
      designer && Password.verify(designer.password_hash, password) -> {:ok, designer}
      designer -> :error
      true ->
        # Constant-time path even when no user exists — Argon2.no_user_verify
        Password.verify(nil, password)
        :error
    end
  end

  @doc """
  Create a new session for a designer. Returns `{:ok, plain_token}`.
  The plain token must be set as the client's cookie value.
  """
  def create_session(designer_id, ip) do
    plain_token = :crypto.strong_rand_bytes(@session_token_bytes) |> Base.url_encode64(padding: false)
    token_hash = :crypto.hash(:sha256, plain_token)
    expires_at = DateTime.utc_now() |> DateTime.add(@session_ttl_seconds) |> DateTime.truncate(:second)

    case %DesignerSession{}
         |> DesignerSession.changeset(%{
           designer_id: designer_id,
           token_hash: token_hash,
           ip: ip,
           expires_at: expires_at
         })
         |> Repo.insert() do
      {:ok, _session} -> {:ok, plain_token}
      {:error, changeset} -> {:error, changeset}
    end
  end

  @doc "Fetch the designer for a session token. Returns {:ok, designer} | :not_found | :expired."
  def fetch_session(plain_token) when is_binary(plain_token) do
    token_hash = :crypto.hash(:sha256, plain_token)
    now = DateTime.utc_now()

    query =
      from s in DesignerSession,
        join: d in assoc(s, :designer),
        where: s.token_hash == ^token_hash,
        select: {s, d}

    case Repo.one(query) do
      nil -> :not_found
      {%DesignerSession{expires_at: expires}, _} when DateTime.compare(expires, now) == :lt -> :expired
      {_, designer} -> {:ok, designer}
    end
  end

  @doc "Revoke (delete) a session by its plain token."
  def revoke_session(plain_token) do
    token_hash = :crypto.hash(:sha256, plain_token)
    Repo.delete_all(from s in DesignerSession, where: s.token_hash == ^token_hash)
    :ok
  end

  defp validate_password(%{password: pw}) when is_binary(pw) and byte_size(pw) >= 10, do: :ok
  defp validate_password(%{"password" => pw}) when is_binary(pw) and byte_size(pw) >= 10, do: :ok
  defp validate_password(_attrs) do
    changeset =
      %Designer{}
      |> Ecto.Changeset.change()
      |> Ecto.Changeset.add_error(:password, "must be at least 10 characters")
    {:error, changeset}
  end
end
```

- [ ] **Step 4: Run test to verify pass**

Run:
```bash
mix test test/boxland/auth/designer_context_test.exs
```
Expected: 7 passed.

- [ ] **Step 5: Commit**

```bash
git add lib/boxland/auth/designers.ex test/boxland/auth/designer_context_test.exs
git commit -m "feat: add Designers context (register, authenticate, sessions)"
```

---

### Task 19: Player auth context (register, authenticate, mint tokens)

**Files:**
- Create: `lib/boxland/auth/players.ex`
- Create: `test/boxland/auth/players_test.exs`

- [ ] **Step 1: Write the failing test**

Create `test/boxland/auth/players_test.exs`:

```elixir
defmodule Boxland.Auth.PlayersTest do
  use Boxland.DataCase, async: true
  alias Boxland.Auth.Players

  describe "register_with_password/1" do
    test "creates a player with hashed password" do
      assert {:ok, p} = Players.register_with_password(%{email: "p@x.com", password: "passwordlong", display_name: "P"})
      assert p.id
      assert p.password_hash
    end

    test "rejects short password" do
      assert {:error, _} = Players.register_with_password(%{email: "p@x.com", password: "short", display_name: "P"})
    end
  end

  describe "register_with_oauth/1" do
    test "creates an oauth-only player and links the identity" do
      assert {:ok, p} = Players.register_with_oauth(%{provider: "google", provider_user_id: "g-1", email: "g@x.com", display_name: "Goog"})
      assert p.email == "g@x.com"
      assert p.password_hash == nil
    end

    test "second registration with same provider_user_id returns existing player" do
      {:ok, p1} = Players.register_with_oauth(%{provider: "google", provider_user_id: "g-2", email: "a@x.com", display_name: "X"})
      {:ok, p2} = Players.register_with_oauth(%{provider: "google", provider_user_id: "g-2", email: "a@x.com", display_name: "X"})
      assert p1.id == p2.id
    end
  end

  describe "authenticate_with_password/2" do
    setup do
      {:ok, p} = Players.register_with_password(%{email: "auth@x.com", password: "rightpassword", display_name: "A"})
      {:ok, player: p}
    end

    test "returns {:ok, player} on correct password", %{player: p} do
      assert {:ok, found} = Players.authenticate_with_password("auth@x.com", "rightpassword")
      assert found.id == p.id
    end

    test "returns :error on wrong password" do
      assert :error = Players.authenticate_with_password("auth@x.com", "wrongpassword")
    end

    test "returns :error for an oauth-only account" do
      {:ok, _p} = Players.register_with_oauth(%{provider: "google", provider_user_id: "g-3", email: "no-pw@x.com", display_name: "X"})
      assert :error = Players.authenticate_with_password("no-pw@x.com", "anything")
    end
  end

  describe "mint_refresh_token/1 and refresh/1" do
    setup do
      {:ok, p} = Players.register_with_password(%{email: "rt@x.com", password: "passwordlong", display_name: "RT"})
      {:ok, player: p}
    end

    test "mint and refresh round-trip", %{player: p} do
      {:ok, plain_refresh} = Players.mint_refresh_token(p.id)
      assert is_binary(plain_refresh)
      assert {:ok, %{access_token: at, refresh_token: new_rt, player: returned}} = Players.refresh(plain_refresh)
      assert is_binary(at)
      assert is_binary(new_rt)
      assert returned.id == p.id
      # Old refresh token should be unusable after rotation
      assert :error = Players.refresh(plain_refresh)
    end
  end
end
```

- [ ] **Step 2: Run test to verify fail**

Run:
```bash
mix test test/boxland/auth/players_test.exs
```
Expected: compile error.

- [ ] **Step 3: Write Players context module**

Create `lib/boxland/auth/players.ex`:

```elixir
defmodule Boxland.Auth.Players do
  @moduledoc """
  Player registration, authentication, and refresh-token management.
  Players auth via email+password OR OAuth. JWT-style access + refresh
  tokens are minted by `Tokens` and stored hashed here.
  """

  import Ecto.Query
  alias Boxland.Repo
  alias Boxland.Auth.{Player, PlayerOAuthLink, PlayerSession, Password, Tokens}

  @refresh_ttl_seconds 60 * 60 * 24 * 30   # 30 days
  @refresh_token_bytes 32

  def register_with_password(attrs) do
    pw = attrs[:password] || attrs["password"]
    if is_binary(pw) and byte_size(pw) >= 10 do
      attrs = Map.put(attrs, :password_hash, Password.hash(pw))
      %Player{}
      |> Player.changeset(attrs)
      |> Repo.insert()
    else
      changeset =
        %Player{}
        |> Player.changeset(attrs)
        |> Ecto.Changeset.add_error(:password, "must be at least 10 characters")
      {:error, changeset}
    end
  end

  def register_with_oauth(%{provider: provider, provider_user_id: puid} = attrs) do
    case Repo.one(from(l in PlayerOAuthLink, where: l.provider == ^provider and l.provider_user_id == ^puid, preload: :player)) do
      %PlayerOAuthLink{player: player} ->
        {:ok, player}

      nil ->
        Repo.transaction(fn ->
          {:ok, player} =
            %Player{}
            |> Player.changeset(%{email: attrs[:email], display_name: attrs[:display_name]})
            |> Repo.insert()

          {:ok, _link} =
            %PlayerOAuthLink{}
            |> PlayerOAuthLink.changeset(%{player_id: player.id, provider: provider, provider_user_id: puid})
            |> Repo.insert()

          player
        end)
    end
  end

  def authenticate_with_password(email, password) when is_binary(email) and is_binary(password) do
    player = Repo.get_by(Player, email: String.downcase(email))

    cond do
      player && Password.verify(player.password_hash, password) -> {:ok, player}
      true ->
        Password.verify(nil, password)   # constant-time
        :error
    end
  end

  def mint_refresh_token(player_id) do
    plain = :crypto.strong_rand_bytes(@refresh_token_bytes) |> Base.url_encode64(padding: false)
    hashed = :crypto.hash(:sha256, plain)
    expires_at = DateTime.utc_now() |> DateTime.add(@refresh_ttl_seconds) |> DateTime.truncate(:second)

    case %PlayerSession{}
         |> PlayerSession.changeset(%{player_id: player_id, refresh_token_hash: hashed, expires_at: expires_at})
         |> Repo.insert() do
      {:ok, _} -> {:ok, plain}
      {:error, cs} -> {:error, cs}
    end
  end

  @doc """
  Exchange a refresh token for a fresh (access, refresh) pair, rotating
  the refresh token. The old refresh token is deleted atomically.
  Returns {:ok, %{access_token, refresh_token, player}} or :error.
  """
  def refresh(plain_refresh) when is_binary(plain_refresh) do
    token_hash = :crypto.hash(:sha256, plain_refresh)
    now = DateTime.utc_now()

    Repo.transaction(fn ->
      session_q = from s in PlayerSession,
        where: s.refresh_token_hash == ^token_hash,
        preload: [:player]

      case Repo.one(session_q) do
        nil ->
          Repo.rollback(:not_found)

        %PlayerSession{expires_at: e} when DateTime.compare(e, now) == :lt ->
          Repo.rollback(:expired)

        %PlayerSession{player: player} = session ->
          Repo.delete!(session)
          access = Tokens.mint_player_access(%{player_id: player.id})
          {:ok, new_refresh} = mint_refresh_token(player.id)
          %{access_token: access, refresh_token: new_refresh, player: player}
      end
    end)
    |> case do
      {:ok, payload} -> {:ok, payload}
      {:error, _reason} -> :error
    end
  end
end
```

- [ ] **Step 4: Run test to verify pass**

Run:
```bash
mix test test/boxland/auth/players_test.exs
```
Expected: 8 passed.

- [ ] **Step 5: Commit**

```bash
git add lib/boxland/auth/players.ex test/boxland/auth/players_test.exs
git commit -m "feat: add Players context (register, authenticate, refresh tokens)"
```

---

## Phase 7 — Protobuf codegen pipeline

### Task 20: Wire up protobuf compilation

**Files:**
- Create: `schemas/common.proto`
- Create: `schemas/game_snapshot.proto`
- Create: `schemas/game_input.proto`
- Create: `schemas/game_event.proto`
- Create: `schemas/game_control.proto`
- Create: `lib/mix/tasks/proto.gen.ex`
- Modify: `mix.exs` (compilers list)
- Create: `assets/package.json` (or modify existing) for ts-proto

- [ ] **Step 1: Verify protoc is installed**

Run:
```bash
protoc --version
```
Expected: `libprotoc 3.x` or higher. If not, `brew install protobuf` on Mac.

- [ ] **Step 2: Install protoc-gen-elixir plugin**

Run:
```bash
mix escript.install hex protobuf --force
```
Expected: installs the `protoc-gen-elixir` escript. Add `~/.mix/escripts` to PATH if not already (`export PATH="$HOME/.mix/escripts:$PATH"`).

- [ ] **Step 3: Install ts-proto in npm**

Run:
```bash
cd assets && npm install --save-dev ts-proto && cd ..
```
Expected: ts-proto installed to assets/node_modules.

- [ ] **Step 4: Write minimal proto schemas**

Create `schemas/common.proto`:

```protobuf
syntax = "proto3";
package boxland.common;

message Vec2 {
  sint32 x = 1;
  sint32 y = 2;
}
```

Create `schemas/game_snapshot.proto`:

```protobuf
syntax = "proto3";
package boxland.game;

message Snapshot {
  uint32 tick = 1;
  uint64 server_time_ms = 2;
}
```

Create `schemas/game_input.proto`:

```protobuf
syntax = "proto3";
package boxland.game;

message Input {
  uint64 client_time_ms = 1;
  oneof verb {
    Move move = 10;
  }
  message Move {
    sint32 dx = 1;
    sint32 dy = 2;
  }
}
```

Create `schemas/game_event.proto`:

```protobuf
syntax = "proto3";
package boxland.game;

message GameEvent {
  uint32 event_type = 1;
}
```

Create `schemas/game_control.proto`:

```protobuf
syntax = "proto3";
package boxland.game;

message JoinAck {
  uint64 server_time_ms = 1;
}
```

(These are deliberately minimal stubs — full schemas land in the Game Runtime surface spec.)

- [ ] **Step 5: Write the mix task**

Create `lib/mix/tasks/proto.gen.ex`:

```elixir
defmodule Mix.Tasks.Proto.Gen do
  @moduledoc """
  Generate Elixir + TypeScript modules from .proto schemas.

  Usage:
      mix proto.gen           # generate
      mix proto.gen --check   # verify generated files are up-to-date (CI)
  """
  use Mix.Task

  @schemas_dir "schemas"
  @elixir_out "lib/boxland_web/proto"
  @ts_out "assets/js/proto"

  def run(args) do
    check = "--check" in args

    File.mkdir_p!(@elixir_out)
    File.mkdir_p!(@ts_out)

    proto_files =
      Path.wildcard("#{@schemas_dir}/*.proto")
      |> Enum.sort()

    if Enum.empty?(proto_files) do
      Mix.shell().error("No .proto files found in #{@schemas_dir}/")
      exit({:shutdown, 1})
    end

    if check do
      run_check(proto_files)
    else
      run_generate(proto_files)
    end
  end

  defp run_generate(proto_files) do
    # Elixir generation via protoc-gen-elixir
    elixir_args = [
      "--proto_path=#{@schemas_dir}",
      "--elixir_out=plugins=grpc:#{@elixir_out}"
      | proto_files
    ]
    case System.cmd("protoc", elixir_args, stderr_to_stdout: true) do
      {_, 0} -> :ok
      {out, code} ->
        Mix.shell().error("protoc (elixir) failed (exit #{code}):\n#{out}")
        exit({:shutdown, code})
    end

    # TypeScript generation via ts-proto
    ts_plugin = Path.expand("assets/node_modules/.bin/protoc-gen-ts_proto")
    ts_args = [
      "--proto_path=#{@schemas_dir}",
      "--plugin=protoc-gen-ts_proto=#{ts_plugin}",
      "--ts_proto_out=#{@ts_out}",
      "--ts_proto_opt=esModuleInterop=true,outputServices=false,useOptionals=messages"
      | proto_files
    ]
    case System.cmd("protoc", ts_args, stderr_to_stdout: true) do
      {_, 0} -> Mix.shell().info("Generated proto modules to #{@elixir_out} and #{@ts_out}")
      {out, code} ->
        Mix.shell().error("protoc (ts) failed (exit #{code}):\n#{out}")
        exit({:shutdown, code})
    end
  end

  defp run_check(proto_files) do
    # Snapshot current generated dirs
    snapshot_elixir = snapshot_dir(@elixir_out)
    snapshot_ts = snapshot_dir(@ts_out)

    run_generate(proto_files)

    fresh_elixir = snapshot_dir(@elixir_out)
    fresh_ts = snapshot_dir(@ts_out)

    if snapshot_elixir == fresh_elixir and snapshot_ts == fresh_ts do
      Mix.shell().info("Generated proto modules are up-to-date.")
    else
      Mix.shell().error("Generated proto modules are STALE. Run `mix proto.gen` and commit.")
      exit({:shutdown, 1})
    end
  end

  defp snapshot_dir(dir) do
    case File.ls(dir) do
      {:ok, files} ->
        files
        |> Enum.sort()
        |> Enum.map(fn f -> {f, File.read!(Path.join(dir, f))} end)
      _ -> []
    end
  end
end
```

- [ ] **Step 6: Run mix proto.gen**

Run:
```bash
mix proto.gen
```
Expected: generates files in `lib/boxland_web/proto/` and `assets/js/proto/`. Output: "Generated proto modules to ..."

- [ ] **Step 7: Verify generated files exist**

Run:
```bash
ls lib/boxland_web/proto/ assets/js/proto/
```
Expected: shows generated `.ex` and `.ts` files.

- [ ] **Step 8: Verify the app still compiles with generated code**

Run:
```bash
mix compile
```
Expected: clean compile.

- [ ] **Step 9: Commit**

```bash
git add schemas/ lib/mix/tasks/ lib/boxland_web/proto/ assets/js/proto/ assets/package.json assets/package-lock.json
git commit -m "feat: add protobuf codegen pipeline (mix proto.gen) + minimal stub schemas"
```

---

## Phase 8 — Luerl scripting host stub

### Task 21: Boxland.Scripting.Host with sandbox + empty catalog

**Files:**
- Create: `lib/boxland/scripting/host.ex`
- Create: `lib/boxland/scripting/catalog.ex`
- Create: `lib/boxland_logic/.gitkeep` (empty actions dir)
- Create: `test/boxland/scripting/host_test.exs`

- [ ] **Step 1: Write the failing test**

Create `test/boxland/scripting/host_test.exs`:

```elixir
defmodule Boxland.Scripting.HostTest do
  use ExUnit.Case, async: true
  alias Boxland.Scripting.Host

  describe "evaluate/1" do
    test "runs a trivial Lua expression" do
      assert {:ok, [3.0]} = Host.evaluate("return 1 + 2")
    end

    test "returns multiple values" do
      assert {:ok, [1, 2, 3]} = Host.evaluate("return 1, 2, 3")
    end

    test "io.* is not available (sandboxed)" do
      assert {:error, _} = Host.evaluate("io.write('hi')")
    end

    test "os.execute is not available (sandboxed)" do
      assert {:error, _} = Host.evaluate("return os.execute('ls')")
    end

    test "returns an error for syntax errors" do
      assert {:error, _} = Host.evaluate("function ( bad")
    end
  end
end
```

- [ ] **Step 2: Run test to verify fail**

Run:
```bash
mix test test/boxland/scripting/host_test.exs
```
Expected: compile error.

- [ ] **Step 3: Write Host module**

Create `lib/boxland/scripting/host.ex`:

```elixir
defmodule Boxland.Scripting.Host do
  @moduledoc """
  Sandboxed Luerl runtime for entity scripts.

  v1 sandbox: blocks `io.*`, `os.execute`, `loadstring`, `load`, `dofile`, `require`.
  Per-call instruction/memory/wall-clock limits will be added when the
  Game Runtime spec wires entity tick-loop calls through here.
  """

  @doc """
  Evaluate a Lua source string in a fresh sandboxed Luerl state.
  Returns `{:ok, results}` where results is the list of return values,
  or `{:error, reason}` on parse/runtime/sandbox error.
  """
  @spec evaluate(String.t()) :: {:ok, list()} | {:error, term()}
  def evaluate(source) when is_binary(source) do
    state = sandboxed_state()
    try do
      case :luerl.do(source, state) do
        {results, _new_state} -> {:ok, results}
      end
    rescue
      e -> {:error, Exception.message(e)}
    catch
      kind, reason -> {:error, {kind, reason}}
    end
  end

  @doc "Build a fresh Luerl state with the sandbox locks applied."
  def sandboxed_state do
    :luerl_sandbox.init()
  end
end
```

(`luerl_sandbox.init/0` is provided by Luerl itself and removes io, os, loadstring, etc.)

- [ ] **Step 4: Write Catalog module (stub)**

Create `lib/boxland/scripting/catalog.ex`:

```elixir
defmodule Boxland.Scripting.Catalog do
  @moduledoc """
  Loads built-in script actions from `lib/boxland_logic/actions/*.lua`.
  Each .lua file returns a behavior descriptor with name, params, run.

  Foundation ships with an EMPTY catalog. Surface specs (Behavior Editor,
  Game Runtime) populate this directory with built-in actions.
  """

  @actions_dir Path.join([:code.priv_dir(:boxland) || "priv", "../lib/boxland_logic/actions"])

  @doc "List built-in action names available in the catalog."
  def list do
    case File.ls(@actions_dir) do
      {:ok, files} ->
        files
        |> Enum.filter(&String.ends_with?(&1, ".lua"))
        |> Enum.map(&Path.rootname/1)
        |> Enum.sort()
      _ ->
        []
    end
  end

  @doc "Load and parse a single built-in action by name."
  def load(name) when is_binary(name) do
    path = Path.join(@actions_dir, "#{name}.lua")
    if File.exists?(path) do
      source = File.read!(path)
      case Boxland.Scripting.Host.evaluate(source) do
        {:ok, [descriptor]} when is_list(descriptor) -> {:ok, descriptor}
        {:ok, _} -> {:error, :invalid_descriptor}
        {:error, reason} -> {:error, reason}
      end
    else
      {:error, :not_found}
    end
  end
end
```

- [ ] **Step 5: Create empty actions dir**

Run:
```bash
mkdir -p lib/boxland_logic/actions && touch lib/boxland_logic/actions/.gitkeep
```

- [ ] **Step 6: Run test to verify pass**

Run:
```bash
mix test test/boxland/scripting/host_test.exs
```
Expected: 5 passed.

- [ ] **Step 7: Commit**

```bash
git add lib/boxland/scripting/ lib/boxland_logic/ test/boxland/scripting/
git commit -m "feat: add Luerl scripting host with sandbox + empty action catalog"
```

---

## Phase 9 — TypeScript on the asset toolchain

### Task 22: Convert assets/js to TypeScript

**Files:**
- Rename: `assets/js/app.js` → `assets/js/app.ts`
- Create: `assets/tsconfig.json`
- Modify: `config/config.exs` (esbuild args)
- Create: `assets/.eslintrc.json` or biome config (deferred — minimal lint for foundation)

- [ ] **Step 1: Rename app.js to app.ts**

Run:
```bash
git mv assets/js/app.js assets/js/app.ts
```

- [ ] **Step 2: Write tsconfig.json**

Create `assets/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "strict": true,
    "noUncheckedIndexedAccess": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "forceConsistentCasingInFileNames": true,
    "isolatedModules": true,
    "allowJs": false,
    "noEmit": true,
    "jsx": "preserve"
  },
  "include": ["js/**/*"]
}
```

- [ ] **Step 3: Update esbuild args to point to .ts**

In `config/config.exs`, find the `:esbuild` config block (Phoenix generated it). Update `args:` to use `js/app.ts`:

```elixir
config :esbuild,
  version: "0.20.0",
  default: [
    args:
      ~w(js/app.ts --bundle --target=es2022 --splitting --format=esm --outdir=../priv/static/assets --external:/fonts/* --external:/images/*),
    cd: Path.expand("../assets", __DIR__),
    env: %{"NODE_PATH" => Path.expand("../deps", __DIR__)}
  ]
```

(esbuild handles TypeScript natively without a separate transpile step.)

- [ ] **Step 4: Verify the asset bundle still builds**

Run:
```bash
mix assets.build
```
Expected: builds without error (esbuild compiles app.ts to app.js in priv/static/assets/).

- [ ] **Step 5: Optional — verify type-checking works**

Run:
```bash
cd assets && npx tsc --noEmit && cd ..
```
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add assets/js/app.ts assets/tsconfig.json config/config.exs
git rm assets/js/app.js 2>/dev/null || true
git commit -m "chore: convert assets/js to TypeScript"
```

---

## Phase 10 — Release tooling and Docker

### Task 23: Boxland.Release module + auto-migrate on boot

**Files:**
- Create: `lib/boxland/release.ex`
- Modify: `lib/boxland/application.ex` (start callback)

- [ ] **Step 1: Write the Release module**

Create `lib/boxland/release.ex`:

```elixir
defmodule Boxland.Release do
  @moduledoc "Release-time helpers (migrations, etc.) callable via `bin/boxland eval`."
  @app :boxland

  def migrate do
    load_app()
    for repo <- repos() do
      {:ok, _, _} = Ecto.Migrator.with_repo(repo, &Ecto.Migrator.run(&1, :up, all: true))
    end
  end

  def rollback(repo, version) do
    load_app()
    {:ok, _, _} = Ecto.Migrator.with_repo(repo, &Ecto.Migrator.run(&1, :down, to: version))
  end

  defp repos do
    Application.fetch_env!(@app, :ecto_repos)
  end

  defp load_app do
    Application.load(@app)
  end
end
```

- [ ] **Step 2: Wire auto-migrate-on-boot into Application**

Edit `lib/boxland/application.ex`. In the `start/2` callback, before the existing `children = [...]` list, add the auto-migrate gate. Final shape of the start callback:

```elixir
def start(_type, _args) do
  if System.get_env("RUN_MIGRATIONS_ON_BOOT") == "true" do
    Boxland.Release.migrate()
  end

  children = [
    # ... existing children list (Boxland.Repo, Boxland.PubSub, BoxlandWeb.Endpoint, etc.) ...
  ]

  opts = [strategy: :one_for_one, name: Boxland.Supervisor]
  Supervisor.start_link(children, opts)
end
```

Gate on env var alone (not `Mix.env()` — that's unavailable in a compiled release). In dev, never set `RUN_MIGRATIONS_ON_BOOT=true` (use `mix ecto.migrate` explicitly). In prod, the Dockerfile sets it.

- [ ] **Step 3: Verify the app still boots locally (without migrate-on-boot)**

Run:
```bash
mix phx.server
```
Expected: starts normally, doesn't auto-migrate (env var not set).

Stop with Ctrl+C twice.

- [ ] **Step 4: Commit**

```bash
git add lib/boxland/release.ex lib/boxland/application.ex
git commit -m "feat: add Boxland.Release.migrate/0 and auto-migrate-on-boot in prod"
```

---

### Task 24: Multi-stage Dockerfile

**Files:**
- Create: `Dockerfile`
- Create: `.dockerignore`

- [ ] **Step 1: Write the Dockerfile**

Create `Dockerfile`:

```dockerfile
ARG ELIXIR_VERSION=1.17.2
ARG OTP_VERSION=27.0
ARG DEBIAN_VERSION=bookworm-20240701-slim

# === BUILDER ===
FROM hexpm/elixir:${ELIXIR_VERSION}-erlang-${OTP_VERSION}-debian-${DEBIAN_VERSION} AS builder

RUN apt-get update -y && apt-get install -y \
      build-essential git nodejs npm libvips-dev protobuf-compiler \
    && apt-get clean && rm -f /var/lib/apt/lists/*_*

WORKDIR /app
RUN mix local.hex --force && mix local.rebar --force
ENV MIX_ENV=prod

COPY mix.exs mix.lock ./
RUN mix deps.get --only prod
COPY config/config.exs config/prod.exs config/
RUN mix deps.compile

COPY priv priv
COPY lib lib
COPY assets assets
COPY schemas schemas
COPY lib/boxland_logic lib/boxland_logic

RUN cd assets && npm install --production=false && cd ..

RUN mix assets.deploy
RUN mix compile

COPY config/runtime.exs config/
RUN mix release

# === RUNTIME ===
FROM debian:${DEBIAN_VERSION} AS runtime

RUN apt-get update -y && apt-get install -y \
      libstdc++6 openssl libncurses5 locales ca-certificates libvips \
    && apt-get clean && rm -f /var/lib/apt/lists/*_*

RUN sed -i '/en_US.UTF-8/s/^# //g' /etc/locale.gen && locale-gen
ENV LANG=en_US.UTF-8 LANGUAGE=en_US:en LC_ALL=en_US.UTF-8

WORKDIR /app
RUN useradd --system --create-home --uid 1000 boxland
USER boxland

COPY --from=builder --chown=boxland /app/_build/prod/rel/boxland ./
ENV HOME=/app PORT=4000 PHX_SERVER=true RUN_MIGRATIONS_ON_BOOT=true

EXPOSE 4000
CMD ["bin/boxland", "start"]
```

- [ ] **Step 2: Write .dockerignore**

Create `.dockerignore`:

```
.git
.gitignore
_build
deps
.env*
.elixir_ls
*.beam
priv/static
docker-compose.yml
docs
README.md
LICENSE
node_modules
assets/node_modules
test
```

- [ ] **Step 3: Build the image locally to verify**

Run:
```bash
docker build -t boxland:dev-test .
```
Expected: builds successfully (takes 2–5 min). May fail if `protoc` isn't available — but the Dockerfile installs `protobuf-compiler`. Verify the build completes.

If you hit "could not find protoc-gen-elixir": the `mix proto.gen` task uses an escript installed in `~/.mix/escripts`. For the Dockerfile, we can skip running `mix proto.gen` at build time IF the generated files are committed to git (they are, per Task 20). The Dockerfile copies `lib/boxland_web/proto/` and `assets/js/proto/` as part of `lib/`/`assets/` and uses them as-is. No regen needed in the image.

- [ ] **Step 4: Commit**

```bash
git add Dockerfile .dockerignore
git commit -m "feat: add multi-stage Dockerfile (builder + runtime, libvips bundled)"
```

---

### Task 25: railway.toml

**Files:**
- Create: `railway.toml`

- [ ] **Step 1: Write railway.toml**

Create `railway.toml`:

```toml
[build]
builder = "DOCKERFILE"
dockerfilePath = "./Dockerfile"

[deploy]
startCommand = "bin/boxland start"
healthcheckPath = "/healthz"
healthcheckTimeout = 30
restartPolicyType = "ON_FAILURE"
restartPolicyMaxRetries = 3
```

- [ ] **Step 2: Commit**

```bash
git add railway.toml
git commit -m "chore: add Railway deploy config"
```

---

## Phase 11 — Wrap-up

### Task 26: README with onboarding

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Replace README contents**

Replace `README.md` with:

```markdown
# Boxland (Elixir)

A 2D MMORPG engine and design suite. This is the Elixir/Phoenix LiveView/Pixi rewrite of Boxland.

For the architecture and design rationale, see:
- `docs/superpowers/specs/2026-04-30-elixir-phoenix-pixi-foundation-design.md` (foundation spec)
- The Go reference repo at `/Users/cmonetti/boxland/` (legacy implementation, do not edit)

## Quick start (development)

Prerequisites:
- Elixir 1.17+ / OTP 27+
- Docker (for Postgres, Redis, MinIO)
- libvips (`brew install vips` on Mac)
- Node.js 20+ (for esbuild + ts-proto)
- `protoc` (`brew install protobuf` on Mac)
- `just` (`brew install just`)

Bring up local services and run the dev server:

```bash
cp .env.dev.example .env.dev
# Generate a secret: mix phx.gen.secret
# Edit .env.dev to set SECRET_KEY_BASE
set -a && source .env.dev && set +a

just dev-up           # docker compose up -d (postgres + redis + minio)
mix deps.get
mix ecto.create
mix ecto.migrate
just serve            # iex -S mix phx.server
```

Visit http://localhost:4000.

## Common commands

```bash
just                  # list all available recipes
just test             # run the test suite
just db-reset         # drop, create, and migrate the dev DB
just proto-gen        # regenerate Protobuf modules
just ci               # format check + credo + proto check + tests
```

## Project layout

```
lib/boxland/         — domain contexts (auth, library, maps, entities, levels, worlds, game, scripting)
lib/boxland_web/     — web layer (live, channels, components, controllers, proto)
lib/boxland_logic/   — built-in Lua action catalog
priv/repo/migrations — Ecto migration files
schemas/             — *.proto wire schemas (single source of truth)
assets/js/           — esbuild-bundled TypeScript (LiveView socket, hooks, Pixi modules)
assets/css/          — Tailwind sources
test/                — ExUnit + LiveView tests
```

## Production deploy

Pushed to `main` → Railway picks up the change → Docker build → BEAM release → auto-migrate on boot → healthcheck on `/healthz`.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: project README with onboarding instructions"
```

---

### Task 27: Final verification — full test suite + boot

**Files:** none

- [ ] **Step 1: Run the full test suite**

Run:
```bash
mix test
```
Expected: all tests pass. Should be ~30+ tests across the auth, library, maps, entities, levels, game, scripting, and web/health modules.

- [ ] **Step 2: Run formatter check**

Run:
```bash
mix format --check-formatted
```
Expected: no output (everything formatted). If errors, run `mix format` to fix.

- [ ] **Step 3: Verify proto codegen is up-to-date**

Run:
```bash
mix proto.gen --check
```
Expected: "Generated proto modules are up-to-date."

- [ ] **Step 4: Verify the dev server boots**

Run:
```bash
mix phx.server
```
Expected: starts cleanly. Visit http://localhost:4000/healthz — returns "ok". Visit http://localhost:4000/readyz — returns "ready".

Stop with Ctrl+C twice.

- [ ] **Step 5: (Optional) Verify Docker build still works**

Run:
```bash
docker build -t boxland:final-check .
```
Expected: builds.

- [ ] **Step 6: Final commit (if formatter changes anything)**

```bash
git status
# If anything changed:
git add -A
git commit -m "chore: final formatter pass"
```

---

## Done

At this point:

- Phoenix app boots locally via `just serve`
- All schemas + migrations in place
- Auth scaffolding (no UI) implemented and tested
- Luerl scripting host running with empty catalog
- Protobuf codegen pipeline working
- Docker image builds
- Railway deploy config present
- All tests passing

**The "empty deployed app with /healthz returning ok" outcome is met.** Surface #2 (TUI) and #3 (Designer auth UI) and beyond are now ready to be brainstormed and planned individually.

Push to `main` (after you've connected the Railway project to the repo) to trigger first deploy:

```bash
git remote add origin <your-railway-connected-repo-url>
git push -u origin main
```

Or, if you want to verify Railway can pull and deploy first, do a dry deploy through Railway's UI before pushing the public branch.
