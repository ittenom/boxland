defmodule Boxland.Common.Vec2 do
  @moduledoc false

  use Protobuf,
    full_name: "boxland.common.Vec2",
    protoc_gen_elixir_version: "0.16.0",
    syntax: :proto3

  field :x, 1, type: :sint32
  field :y, 2, type: :sint32
end
