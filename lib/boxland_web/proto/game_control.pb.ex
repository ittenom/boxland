defmodule Boxland.Game.JoinAck do
  @moduledoc false

  use Protobuf,
    full_name: "boxland.game.JoinAck",
    protoc_gen_elixir_version: "0.16.0",
    syntax: :proto3

  field :server_time_ms, 1, type: :uint64, json_name: "serverTimeMs"
end
