# Boxland — production server image.
#
# Multi-stage:
#   1. flatc-stage  -- pinned flatc, generates Go bindings from /schemas/.
#   2. web-stage    -- Node, builds the Vite bundle into /web/dist/.
#   3. go-stage     -- Go toolchain, compiles the static server binary
#                       (embeds web bundle + Templ views + static assets).
#   4. final        -- distroless/static, ships only the boxland binary.
#
# Build context is the repo root: `docker build -f docker/Dockerfile .`

ARG GO_VERSION=1.23
ARG NODE_VERSION=20
ARG FLATC_VERSION=24.3.25

# ---------- 1. flatc bindings ----------
FROM debian:bookworm-slim AS flatc-stage
ARG FLATC_VERSION
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl unzip \
    && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL "https://github.com/google/flatbuffers/releases/download/v${FLATC_VERSION}/Linux.flatc.binary.g++-13.zip" \
        -o /tmp/flatc.zip \
    && unzip /tmp/flatc.zip -d /usr/local/bin/ \
    && chmod +x /usr/local/bin/flatc
WORKDIR /work
COPY schemas /work/schemas
RUN mkdir -p /work/server/internal/proto /work/web/src/net/proto \
    && flatc --go --gen-onefile --go-namespace boxland.proto \
        -o /work/server/internal/proto \
        /work/schemas/world.fbs /work/schemas/input.fbs /work/schemas/design.fbs \
    && flatc --ts -o /work/web/src/net/proto \
        /work/schemas/world.fbs /work/schemas/input.fbs /work/schemas/design.fbs

# ---------- 2. web bundle ----------
FROM node:${NODE_VERSION}-bookworm-slim AS web-stage
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm ci
COPY web/ ./
COPY --from=flatc-stage /work/web/src/net/proto ./src/net/proto
RUN npm run build

# ---------- 3. Go binary ----------
FROM golang:${GO_VERSION}-bookworm AS go-stage
WORKDIR /src
COPY server/go.mod server/go.sum* ./server/
RUN cd server && go mod download
COPY server/ ./server/
COPY --from=flatc-stage /work/server/internal/proto ./server/internal/proto
COPY --from=web-stage /web/dist ./server/static/web
# Future: copy compiled Templ views + CSS bundle into ./server/static once they exist.
RUN cd server && CGO_ENABLED=0 GOOS=linux go build \
        -trimpath -ldflags="-s -w -X main.Version=${BOXLAND_VERSION:-prod}" \
        -o /out/boxland ./cmd/boxland

# ---------- 4. final ----------
FROM gcr.io/distroless/static-debian12:nonroot AS final
COPY --from=go-stage /out/boxland /boxland
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/boxland"]
CMD ["serve"]
