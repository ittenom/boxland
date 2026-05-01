defmodule Boxland.Maps.Map do
  @moduledoc "A non-interactive grid of tiles, organized in z-indexed layers."
  use Ecto.Schema
  import Ecto.Changeset

  schema "maps" do
    field :slug, :string
    field :name, :string
    field :width, :integer
    field :height, :integer

    belongs_to :owner, Boxland.Auth.Designer
    has_many :layers, Boxland.Maps.Layer

    timestamps(type: :utc_datetime)
  end

  def changeset(map, attrs) do
    map
    |> cast(attrs, [:owner_id, :slug, :name, :width, :height])
    |> validate_required([:owner_id, :slug, :name, :width, :height])
    |> validate_number(:width, greater_than: 0)
    |> validate_number(:height, greater_than: 0)
    |> validate_format(:slug, ~r/^[a-z0-9][a-z0-9-]*$/)
    |> unique_constraint([:owner_id, :slug])
    |> foreign_key_constraint(:owner_id)
  end
end
