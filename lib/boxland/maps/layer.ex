defmodule Boxland.Maps.Layer do
  @moduledoc "A single z-indexed layer of tile placements within a Map."
  use Ecto.Schema
  import Ecto.Changeset

  schema "map_layers" do
    field :name, :string
    field :z_index, :integer
    field :tiles, :map, default: %{}
    field :visible, :boolean, default: true
    field :locked, :boolean, default: false
    field :opacity, :integer, default: 100

    belongs_to :map, Boxland.Maps.Map

    timestamps(type: :utc_datetime)
  end

  def changeset(layer, attrs) do
    layer
    |> cast(attrs, [:map_id, :name, :z_index, :tiles, :visible, :locked, :opacity])
    |> validate_required([:map_id, :name, :z_index])
    |> validate_number(:opacity, greater_than_or_equal_to: 0, less_than_or_equal_to: 100)
    |> unique_constraint([:map_id, :name])
    |> foreign_key_constraint(:map_id)
  end
end
