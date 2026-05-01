defmodule Boxland.Levels.Level do
  @moduledoc "A Map + entity placements + HUD config + instancing policy."
  use Ecto.Schema
  import Ecto.Changeset

  @valid_instancing ~w(shared per_party per_user)

  schema "levels" do
    field :slug, :string
    field :name, :string
    field :hud_config, :map, default: %{}
    field :instancing, :string, default: "shared"

    belongs_to :owner, Boxland.Auth.Designer
    belongs_to :map, Boxland.Maps.Map
    belongs_to :world, Boxland.Worlds.World
    has_many :entities, Boxland.Levels.LevelEntity

    timestamps(type: :utc_datetime)
  end

  def changeset(level, attrs) do
    level
    |> cast(attrs, [:owner_id, :slug, :name, :map_id, :world_id, :hud_config, :instancing])
    |> validate_required([:owner_id, :slug, :name, :map_id])
    |> validate_format(:slug, ~r/^[a-z0-9][a-z0-9-]*$/)
    |> validate_inclusion(:instancing, @valid_instancing)
    |> unique_constraint([:owner_id, :slug])
    |> foreign_key_constraint(:owner_id)
    |> foreign_key_constraint(:map_id)
    |> foreign_key_constraint(:world_id)
  end
end
