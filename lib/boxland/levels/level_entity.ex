defmodule Boxland.Levels.LevelEntity do
  @moduledoc """
  An entity placement in a level (the instance of an entity_type at a
  specific coordinate, with optional per-instance overrides).

  `tag` is a designer-assigned label used as a reference target by ECA
  Actions ("send damage to tag:goons"). Multiple instances may share a
  tag.

  `properties` are instance overrides for declared properties on the
  entity type. Merged at runtime — instance values win.

  `group_id` binds the entity to a tile group on its level's map. When
  the entity moves, every tile in the group translates with it (across
  x/y, and across layers for z).
  """
  use Ecto.Schema
  import Ecto.Changeset

  schema "level_entities" do
    field :pos_x, :integer
    field :pos_y, :integer
    field :z_index_override, :integer
    field :tag, :string
    field :group_id, :string
    field :properties, :map, default: %{}
    field :instance_overrides, :map, default: %{}
    field :script_state, :map, default: %{}

    belongs_to :level, Boxland.Levels.Level
    belongs_to :entity_type, Boxland.Entities.EntityType

    timestamps(type: :utc_datetime)
  end

  def changeset(level_entity, attrs) do
    level_entity
    |> cast(attrs, [
      :level_id,
      :entity_type_id,
      :pos_x,
      :pos_y,
      :z_index_override,
      :tag,
      :group_id,
      :properties,
      :instance_overrides,
      :script_state
    ])
    |> validate_required([:level_id, :entity_type_id, :pos_x, :pos_y])
    |> foreign_key_constraint(:level_id)
    |> foreign_key_constraint(:entity_type_id)
  end
end
