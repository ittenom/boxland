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
