defmodule Boxland.Maps.Layer do
  @moduledoc "A single z-indexed layer of tile placements within a Map."
  use Ecto.Schema
  import Ecto.Changeset

  schema "map_layers" do
    field :name, :string
    field :z_index, :integer
    field :tiles, :map, default: %{}

    belongs_to :map, Boxland.Maps.Map

    timestamps(type: :utc_datetime)
  end

  def changeset(layer, attrs) do
    layer
    |> cast(attrs, [:map_id, :name, :z_index, :tiles])
    |> validate_required([:map_id, :name, :z_index])
    |> unique_constraint([:map_id, :name])
    |> foreign_key_constraint(:map_id)
  end
end
