defmodule Boxland.Worlds.World do
  @moduledoc """
  A set of levels. The graph between them is emergent — transition entities
  in each level reference target levels via their `level_transition` component.
  """
  use Ecto.Schema
  import Ecto.Changeset

  schema "worlds" do
    field :slug, :string
    field :name, :string

    belongs_to :owner, Boxland.Auth.Designer
    has_many :levels, Boxland.Levels.Level

    timestamps(type: :utc_datetime)
  end

  def changeset(world, attrs) do
    world
    |> cast(attrs, [:owner_id, :slug, :name])
    |> validate_required([:owner_id, :slug, :name])
    |> validate_format(:slug, ~r/^[a-z0-9][a-z0-9-]*$/)
    |> unique_constraint([:owner_id, :slug])
    |> foreign_key_constraint(:owner_id)
  end
end
