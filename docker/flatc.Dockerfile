# Pinned flatc image for cross-platform schema generation.
# All developers and CI invoke flatc through this image to prevent version drift.
# Bump FLATC_VERSION here and update CI; do not invoke a host-installed flatc.

FROM debian:bookworm-slim AS build

ARG FLATC_VERSION=24.3.25

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        unzip \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://github.com/google/flatbuffers/releases/download/v${FLATC_VERSION}/Linux.flatc.binary.g++-13.zip" \
        -o /tmp/flatc.zip \
    && unzip /tmp/flatc.zip -d /usr/local/bin/ \
    && chmod +x /usr/local/bin/flatc \
    && rm /tmp/flatc.zip \
    && flatc --version

FROM debian:bookworm-slim
COPY --from=build /usr/local/bin/flatc /usr/local/bin/flatc
WORKDIR /work
ENTRYPOINT ["/usr/local/bin/flatc"]
