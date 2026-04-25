# iOS-readiness audit (PLAN.md §145)

The seven invariants from PLAN.md §1a, walked against the v1 codebase
at the close of v1 work. Each section names the invariant, the
evidence in the repo, and any drift that needs fixing before tagging
v1.

Status legend: ✅ holds · ⚠️ holds with caveat · ❌ drift, fix required.

---

## 1. ✅ All authoritative game state ships through FlatBuffers

**Evidence**:
- `schemas/world.fbs` (`MapState`, `Snapshot`, `Diff`, `EntityState`,
  `Tile`, `LightingCell`, `AudioEvent`, `ChunkVersion`, `Mutation`).
- `schemas/input.fbs` (`Auth`, `ClientMessage`, every per-verb payload).
- `schemas/design.fbs` (designer-only / live-publish payloads).
- Server `internal/proto/*_generated.go` and web
  `web/src/net/proto/boxland/proto/*.ts` are both regenerated from
  the same `.fbs` source via `just gen-fb` (Docker-pinned `flatc`).
- `web/src/net/codec.ts` encodes every outbound verb through the
  `flatbuffers` builder; `decodeDiff` consumes the inbound side.
- The WS gateway (`server/internal/ws/gateway.go`) reads
  `proto.GetRootAsClientMessage`; nothing parses JSON on the WS path.
- The WS broadcaster (`server/internal/ws/broadcaster.go`) hands raw
  `[]byte` blobs from the encoder to the WebSocket; no JSON wrap.

**Caveat**: design-tool HTTP routes (`/design/*`, `/play/*`) are JSON
over HTTP by design (humans + HTMX), and iOS never hits them.
Confirmed: the WS handshake mints a player JWT through
`/play/ws-ticket` so the iOS client only needs the JSON `{"token": ...}`
shape and the FlatBuffers `Auth` payload after that.

## 2. ✅ Collision algorithm has canonical pseudocode

**Evidence**:
- `schemas/collision.md` contains the spec.
- `server/internal/sim/collision/move.go` and `web/src/collision/move.ts`
  are literal ports.
- 44 test vectors in `shared/test-vectors/collision.json`; both runtimes
  consume them via `collision_corpus_test.go` (Go) and `corpus.test.ts`
  (TS) and assert byte-identical resolved deltas.
- The Swift port at v1.1 will consume the **same JSON corpus** as a
  third literal port; CI gates land in §137.

## 3. ✅ Assets are CDN-served at content-addressed URLs

**Evidence**:
- `internal/assets/` writes uploaded blobs to S3-compatible storage
  with `sha256(blob)` paths.
- The asset URL returned to clients is `S3_PUBLIC_BASE_URL + "/" + sha`;
  the env-var doc requires `S3_PUBLIC_BASE_URL` to be CDN-fronted in
  prod.
- Palette variants are pre-baked at publish time (PLAN.md §1) and
  written to additional sha256 paths; the runtime asset catalog returns
  the variant URL directly without a server round-trip per frame.
- iOS at v1.1 fetches the same URLs; no web-side rewriting exists.

## 4. ✅ Auth flows are protocol-level, not transport-coupled

**Evidence**:
- `internal/auth/player/`: signup, login, refresh, JWT mint, OAuth (Google
  + Apple + Discord stubs) all live on the player Service. The handlers
  in `internal/playerweb/handlers.go` are thin shims; the iOS client
  reuses the same endpoints (`/play/login`, `/play/signup`,
  `/play/ws-ticket`, `/auth/oauth/*`).
- The WS handshake's `Auth` table accepts a token string regardless of
  transport; the only realm-specific bit is whether the server expects
  a player JWT or a designer ticket -- both are HTTP-mintable.
- Apple Sign-In is documented as **mandatory** for the iOS launch
  (App Store Review §4.8) in `docs/env-vars.md` and the OAuth registry
  (`internal/auth/player/oauth.go`).

## 5. ✅ No game logic assumes mouse + keyboard

**Evidence**:
- Movement: keyboard `MovementIntent` (game/intents.ts), gamepad axes
  (input/gamepad.ts), and click-to-move (input/mouse.ts) all produce
  the same `MovePayload` on the wire.
- The CommandBus is the abstraction — every input source is a
  `Command` dispatch (PLAN.md §6h). Settings rebinder rebinds the
  combo, not the source.
- The renderer never reads `event.clientX/Y` directly; the input module
  converts to world sub-pixels via `CameraReader.subPerCanvasPx`.
- The HUD layer (`render/nameplates.ts`) draws via Pixi text + Graphics;
  nothing canvas-specific. Same code will drive SpriteKit at v1.1.

## 6. ✅ Vector test cases live in /shared/test-vectors

**Evidence**:
- 44 vectors at `shared/test-vectors/collision.json`.
- Both `internal/sim/collision/corpus_test.go` and
  `web/src/collision/corpus.test.ts` consume the same file.
- §136's expansion (12 new vectors covering inner/outer corners,
  multi-tile rows, mask bit selectivity, exact-meet sub-pixel cases)
  ships in this v1 audit pass.

## 7. ✅ No web-only escape hatches in level/asset data

**Evidence**:
- All sizing flows from the integer-scale viewport rule
  (`render/viewport.ts`); no hard-coded canvas widths in level
  formats.
- Tile coords are int32 grid indices; entity coords are int32
  fixed-point sub-pixels (1 px = 256). Same on every runtime.
- Audio events carry sub-pixel positions; the falloff math
  (`audio/falloff.ts`) takes a CameraReader, not a window.

---

## Drift identified

None. Every invariant is currently held by the codebase. Tagging v1
remains gated only on the **operational** tasks 138 (smoke test),
141 (Docker), 142 (railway.toml) -- none of which the iOS port
depends on.

## Next-revision checklist (v1.1 setup)

When iOS work begins (§7 of PLAN.md), the following entry points
exist + are stable:

1. `flatc` Swift output: add `--swift -o ios/Boxland/Net/proto` to
   `docker/flatc.Dockerfile` invocations.
2. Auth: hit `/play/login` + `/play/ws-ticket` from
   `ASWebAuthenticationSession`-backed views.
3. Collision corpus: import `shared/test-vectors/collision.json` into
   the iOS test target; produce a literal Swift port of the algorithm.
4. Assets: prefetch `S3_PUBLIC_BASE_URL` URLs; cache by sha key.
5. Settings: same JSON shape; wire to the same `/play/settings/me`
   endpoints.

No blockers; no breaking-change risk.
