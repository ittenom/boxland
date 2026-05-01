defmodule Boxland.Entities.EntityType do
  @moduledoc """
  A definition (template) of an interactive game object. Per-level
  placements are LevelEntities. Behavior is composed via components +
  Lua scripts attached to lifecycle hooks.
  """
  use Ecto.Schema
  import Ecto.Changeset

  schema "entity_types" do
    field :slug, :string
    field :name, :string
    field :visual_ref, :map, default: %{}
    field :animation_bindings, :map, default: %{}
    field :components, {:array, :map}, default: []
    field :scripts, {:array, :map}, default: []
    field :default_collision_mask, :string, default: "land"
    field :default_z_index, :integer, default: 25

    belongs_to :owner, Boxland.Auth.Designer

    timestamps(type: :utc_datetime)
  end

  def changeset(entity_type, attrs) do
    entity_type
    |> cast(attrs, [
      :owner_id,
      :slug,
      :name,
      :visual_ref,
      :animation_bindings,
      :components,
      :scripts,
      :default_collision_mask,
      :default_z_index
    ])
    |> validate_required([:owner_id, :slug, :name])
    |> validate_format(:slug, ~r/^[a-z0-9][a-z0-9-]*$/)
    |> unique_constraint([:owner_id, :slug])
    |> foreign_key_constraint(:owner_id)
  end
end
