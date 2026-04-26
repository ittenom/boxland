# Boxland environment variables

The canonical list lives in [`.env.example`](../.env.example) at the
repo root; this doc explains the **why** behind each variable and the
expected production values.

PLAN.md §143.

---

## Server

| Var | Default | Purpose |
|---|---|---|
| `BOXLAND_ENV` | `dev` | Selects environment-aware defaults. `dev`/`staging`/`prod`. Production sets `Secure` cookies, `pretty=false` slog, longer rate-limit windows. |
| `BOXLAND_HTTP_ADDR` | `:8080` | Listen address for the Go HTTP server. The WS gateway shares this listener (mounted at `/ws`). |
| `BOXLAND_LOG_FORMAT` | `pretty` | `pretty` for human-readable dev console; `json` for log aggregators in prod. |
| `BOXLAND_LOG_LEVEL` | `info` | One of `debug`/`info`/`warn`/`error`. The slog observability checklist (PLAN.md §139) assumes `info`. |

## Postgres

| Var | Default | Purpose |
|---|---|---|
| `DATABASE_URL` | `localhost:5433` | Single connection string consumed by `pgxpool`. Dev uses port `5433` to avoid clashing with a locally-installed Postgres on `5432`. |

## Redis

| Var | Default | Purpose |
|---|---|---|
| `REDIS_URL` | `localhost:6380/0` | Backs sessions, pub/sub, the WAL stream. Dev port `6380` for the same conflict-avoidance reason. |

## Object storage

| Var | Default | Purpose |
|---|---|---|
| `S3_ENDPOINT` | `http://localhost:9000` | MinIO in dev; `https://<account>.r2.cloudflarestorage.com` in prod. |
| `S3_REGION` | `us-east-1` | Required by the SDK; ignored by MinIO/R2. |
| `S3_BUCKET` | `boxland-assets` | One bucket holds every content-addressed asset. |
| `S3_ACCESS_KEY_ID` | `boxland` | Credentials used by the asset pipeline writers. |
| `S3_SECRET_ACCESS_KEY` | `boxland_dev_secret` | (As above.) Rotate every 90d in prod. |
| `S3_USE_PATH_STYLE` | `true` | `true` for MinIO/R2; `false` for AWS S3. |
| `S3_PUBLIC_BASE_URL` | `http://localhost:9000/boxland-assets` | CDN-fronted URL prefix returned to clients. PLAN.md §1: "content-addressed CDN URLs" — never put bare S3 hostnames in front of players. |

## SMTP

| Var | Default | Purpose |
|---|---|---|
| `SMTP_HOST` | `localhost` | Mailpit in dev. Prod: SES/Postmark/Mailgun. |
| `SMTP_PORT` | `1025` | Mailpit's default. Prod typically `587` (STARTTLS). |
| `SMTP_USERNAME` | empty | Empty for Mailpit; populated in prod. |
| `SMTP_PASSWORD` | empty | (As above.) Use a vault/secret manager. |
| `SMTP_FROM` | `noreply@boxland.local` | Visible to recipients; align with your domain. |

## Auth secrets

All three MUST be regenerated per environment. `openssl rand -base64 64`
gives you a 512-bit value comfortably above the HMAC-SHA-256 minimum.

| Var | Purpose |
|---|---|
| `SESSION_COOKIE_SECRET` | Designer + player session-cookie HMAC. |
| `JWT_SIGNING_SECRET` | HS256 access-token signer. The WS Auth handshake's player path verifies with this. |
| `DESIGNER_WS_TICKET_SECRET` | One-shot WS-ticket HMAC; ticket TTL is ~30s, IP-bound, per PLAN.md §1. |

## OAuth providers (feature-flagged)

Each provider's `*_ENABLED=true` toggle is checked at boot; missing
client/secret with the flag on is a fatal config error so misdeployments
fail loudly.

PLAN.md §1 / §4c: Apple Sign-In is **mandatory** when shipping iOS
(v1.1) if any third-party SSO is offered, per App Store policy.

## Game tuning

These knobs rarely change; the defaults match PLAN.md §1.

| Var | Default | Purpose |
|---|---|---|
| `TICK_HZ` | `10` | Authoritative simulation rate (PLAN.md "10 Hz"). |
| `WAL_FLUSH_TICKS` | `20` | How many ticks of mutations to batch before Postgres flush; ≈ 2 s of WAL volume. |

## Updates

Controls for the in-app update notification + `boxland update`
flow. See [docs/updates.md](./updates.md) for the full story.

| Var | Default | Purpose |
|---|---|---|
| `BOXLAND_DISABLE_UPDATE_CHECK` | unset | Set to `true` (or `1`/`yes`/`on`) to suppress the GitHub probe entirely. The TLI banner, designer chrome pill, and `/design/api/version` all stop reporting an update. Useful for offline workshops, air-gapped servers, and CI. |
| `BOXLAND_GITHUB_TOKEN` | unset | GitHub personal access token (read-only `public_repo` scope is enough). Lifts the anonymous rate limit (60/hr/IP) to 5,000/hr/user — useful when many users share an IP behind NAT, or when checking against a private fork mirror. |
| `AOI_RADIUS_TILES` | `24` | Default subscription radius. PLAN.md §4h "16-tile chunks" + AOI radius. |
| `RECONNECT_GAP_TICK_LIMIT` | `600` | Reconnects within this many ticks resume via `AckTick` + diff replay; longer gaps force a full `Snapshot`. |

---

## Adding a new variable

1. Add a line to `.env.example` with a sane dev default + a one-line
   comment explaining its purpose.
2. Add a row to this table grouped by the existing sections.
3. Wire it in `internal/config/config.go`; tests in
   `internal/config/config_test.go` exercise the env-loader path.
