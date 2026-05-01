ARG ELIXIR_VERSION=1.19.5
ARG OTP_VERSION=28.5
ARG DEBIAN_VERSION=bookworm-20260421-slim

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

RUN cd assets && npm install --production=false && cd ..

RUN mix compile
RUN mix assets.deploy

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
