defmodule Boxland.Game.GameEvent do
  @moduledoc false

  use Protobuf,
    full_name: "boxland.game.GameEvent",
    protoc_gen_elixir_version: "0.16.0",
    syntax: :proto3

  field :event_type, 1, type: :uint32, json_name: "eventType"
end
