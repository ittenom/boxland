# Boxland Railway Setup

Boxland is a Phoenix application packaged for Railway with the root
`Dockerfile` and `railway.toml`.

This guide covers setting up a Railway project. With some config changes, you can deploy locally or on the platform of your choice.

## What Railway Uses From This Repo

`railway.toml` defines the web service deployment behavior:

```toml
[build]
builder = "DOCKERFILE"
dockerfilePath = "./Dockerfile"

[deploy]
preDeployCommand = "bin/boxland eval 'Boxland.Release.migrate()'"
startCommand = "bin/boxland start"
healthcheckPath = "/healthz"
healthcheckTimeout = 30
restartPolicyType = "ON_FAILURE"
restartPolicyMaxRetries = 3
```

That means Railway will:

1. Build the app with the repo `Dockerfile`.
2. Run database migrations before the deployment goes live.
3. Start the release with `bin/boxland start`.
4. Wait for `/healthz` to return `200`.

The Docker build is optimized for Railway by default:

- `assets/package.json` and `assets/package-lock.json` are copied before the
  rest of `assets/`, so `npm ci` can stay cached when only frontend source files
  change.
- `BOXLAND_WITH_TUI=false` is the default Docker build arg, so Railway does not
  fetch or compile the terminal UI dependency.

To build a local Docker image that includes the terminal UI, override the build
arg:

```bash
docker build --build-arg BOXLAND_WITH_TUI=true -t boxland:dev-test .
```

## Create The Railway Project

1. Open Railway and create a new project.
2. Add a PostgreSQL database service.
3. Add a web service from this GitHub repository.
4. Confirm the web service is using the repo root as its source.
5. Let Railway use the checked-in `railway.toml` for build and deploy settings.

## Configure Web Service Variables

Open the web service's `Variables` tab and add:

```bash
DATABASE_URL=${{Postgres.DATABASE_URL}}
ECTO_IPV6=true
LANG=en_US.UTF-8
LC_CTYPE=en_US.UTF-8
SECRET_KEY_BASE=<generated secret>
```

Generate `SECRET_KEY_BASE` locally with:

```bash
mix phx.gen.secret
```

If your Postgres service has a different name in Railway, update the reference
namespace. For example, if the service is named `Database`, use:

```bash
DATABASE_URL=${{Database.DATABASE_URL}}
```

## Optional Variables

Restrict designer account creation to one email domain:

```bash
DESIGNER_EMAIL_DOMAIN=example.com
```

Leave `DESIGNER_EMAIL_DOMAIN` unset or blank to allow any valid email domain.

Override the public Phoenix host:

```bash
PHX_HOST=boxland.example.com
```

If `PHX_HOST` is unset, the app uses Railway's `RAILWAY_PUBLIC_DOMAIN`.

## Deploy

1. Review and deploy the staged Railway changes.
2. Watch the web service deployment logs.
3. Confirm the pre-deploy migration command succeeds.
4. Confirm the deployment becomes active after `/healthz` passes.
5. Open the generated Railway domain for the web service.

## Troubleshooting

If the app cannot connect to Postgres:

- Confirm `DATABASE_URL` references the correct Railway Postgres service name.
- Confirm `ECTO_IPV6=true` is set on the web service.
- Redeploy the web service after saving variable changes.

If Phoenix fails at startup:

- Confirm `SECRET_KEY_BASE` is set.
- Confirm `PHX_HOST` is either unset or set to the exact public host.
- Check that Railway assigned a public domain to the web service.

If migrations fail:

- Check the pre-deploy logs for the web service.
- Confirm the Postgres service is deployed and healthy.
- Confirm the web service can read `DATABASE_URL`.


## Local Reference

For local development, this repo expects Docker-backed services:

```bash
cp .env.dev.example .env.dev
set -a && source .env.dev && set +a

just dev-up
mix deps.get
mix ecto.create
mix ecto.migrate
just serve
```

Visit `http://localhost:4000`.
