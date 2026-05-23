defmodule Boxland.Entities.EntityType do
  @moduledoc """
  A definition (template) of an interactive game object. Per-level
  placements are LevelEntities. Behavior is composed via components +
  Lua scripts attached to lifecycle hooks.

  Declared `properties` are key/type/default schemas; per-instance
  values live on `LevelEntity.properties` and are merged at runtime.

  `actions` is the Event-Condition-Action vocabulary: each entry is
  `%{"id", "name", "enabled", "trigger" => %{kind, params},
     "function" => %{kind, params}}`.

  `size` is the entity's bounding-box footprint in cells (w × h).
  Used for invisible entities and for proximity bbox math.
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
    field :properties, {:array, :map}, default: []
    field :actions, {:array, :map}, default: []
    field :size, :map, default: %{"w" => 1, "h" => 1}
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
      :properties,
      :actions,
      :size,
      :default_collision_mask,
      :default_z_index
    ])
    |> validate_required([:owner_id, :slug, :name])
    |> validate_format(:slug, ~r/^[a-z0-9][a-z0-9-]*$/)
    |> validate_size()
    |> validate_properties()
    |> validate_actions()
    |> unique_constraint([:owner_id, :slug])
    |> foreign_key_constraint(:owner_id)
  end

  defp validate_size(changeset) do
    case get_field(changeset, :size) do
      %{"w" => w, "h" => h} when is_integer(w) and is_integer(h) and w > 0 and h > 0 ->
        changeset

      nil ->
        changeset

      _ ->
        add_error(changeset, :size, ~s(must be %{"w" => pos_int, "h" => pos_int}))
    end
  end

  defp validate_properties(changeset) do
    props = get_field(changeset, :properties) || []

    if Enum.all?(props, &valid_property?/1) do
      changeset
    else
      add_error(changeset, :properties, "each entry needs key, type, default")
    end
  end

  defp valid_property?(%{"key" => key, "type" => type, "default" => _})
       when is_binary(key) and type in ["number", "boolean", "string"],
       do: true

  defp valid_property?(_), do: false

  defp validate_actions(changeset) do
    actions = get_field(changeset, :actions) || []

    if Enum.all?(actions, &valid_action?/1) do
      changeset
    else
      add_error(changeset, :actions, "each action needs id, trigger, function")
    end
  end

  defp valid_action?(%{"id" => id, "trigger" => %{"kind" => _}, "function" => %{"kind" => _}})
       when is_binary(id),
       do: true

  defp valid_action?(_), do: false
end
