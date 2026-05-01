defmodule Boxland.Game.Snapshot do
  @moduledoc false

  use Protobuf,
    full_name: "boxland.game.Snapshot",
    protoc_gen_elixir_version: "0.16.0",
    syntax: :proto3

  field :tick, 1, type: :uint32
  field :server_time_ms, 2, type: :uint64, json_name: "serverTimeMs"
end
