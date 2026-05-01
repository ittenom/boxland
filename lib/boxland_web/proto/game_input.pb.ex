defmodule Boxland.Game.Input.Move do
  @moduledoc false

  use Protobuf,
    full_name: "boxland.game.Input.Move",
    protoc_gen_elixir_version: "0.16.0",
    syntax: :proto3

  field :dx, 1, type: :sint32
  field :dy, 2, type: :sint32
end

defmodule Boxland.Game.Input do
  @moduledoc false

  use Protobuf,
    full_name: "boxland.game.Input",
    protoc_gen_elixir_version: "0.16.0",
    syntax: :proto3

  oneof :verb, 0

  field :client_time_ms, 1, type: :uint64, json_name: "clientTimeMs"
  field :move, 10, type: Boxland.Game.Input.Move, oneof: 0
end
