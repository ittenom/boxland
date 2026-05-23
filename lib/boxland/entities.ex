defmodule Boxland.Entities do
  @moduledoc """
  Context for EntityType templates: CRUD, action/property helpers, and
  the runtime property-merge used by the ECA dispatcher.

  Property model:
  - `EntityType.properties` is the declared schema with defaults.
  - `LevelEntity.properties` is the per-instance override map.
  - `merge_properties/2` returns `%{key => value}` for runtime use.

  Action model:
  - `EntityType.actions` is `[%{"id", "name", "enabled", "trigger", "function"}, ...]`.
  - Actions are owned by the type. Use `add_action/2`, `update_action/3`,
    `remove_action/2` to modify the array in place.
  """

  import Ecto.Query

  alias Boxland.Entities.EntityType
  alias Boxland.Repo

  @doc "List all entity types for an owner, ordered by slug."
  def list_entity_types(owner_id) do
    EntityType
    |> where([t], t.owner_id == ^owner_id)
    |> order_by([t], asc: t.slug)
    |> Repo.all()
  end

  def get_entity_type!(owner_id, id) do
    EntityType
    |> where([t], t.owner_id == ^owner_id and t.id == ^id)
    |> Repo.one!()
  end

  def get_entity_type_by_slug(owner_id, slug) do
    Repo.get_by(EntityType, owner_id: owner_id, slug: slug)
  end

  def create_entity_type(owner_id, attrs) do
    %EntityType{}
    |> EntityType.changeset(Elixir.Map.put(attrs, "owner_id", owner_id))
    |> Repo.insert()
  end

  def update_entity_type(%EntityType{} = type, attrs) do
    type
    |> EntityType.changeset(attrs)
    |> Repo.update()
  end

  def delete_entity_type(%EntityType{} = type), do: Repo.delete(type)

  @doc """
  Merge declared property defaults with instance overrides. Instance
  values win. Keys present in overrides but not declared are kept
  (free-form additions allowed at runtime).
  """
  def merge_properties(%EntityType{properties: declared}, %{} = overrides) do
    declared
    |> Enum.into(%{}, fn %{"key" => k, "default" => v} -> {k, v} end)
    |> Elixir.Map.merge(overrides || %{})
  end

  def merge_properties(_, overrides), do: overrides || %{}

  @doc "Append an action to an entity type. `attrs` may omit `id`; a fresh one is generated."
  def add_action(%EntityType{} = type, attrs) do
    action =
      attrs
      |> Elixir.Map.put_new("id", new_id())
      |> Elixir.Map.put_new("enabled", true)
      |> Elixir.Map.put_new("name", "Action")

    update_entity_type(type, %{"actions" => type.actions ++ [action]})
  end

  def update_action(%EntityType{} = type, action_id, attrs) do
    updated =
      Enum.map(type.actions, fn
        %{"id" => ^action_id} = a -> Elixir.Map.merge(a, attrs)
        a -> a
      end)

    update_entity_type(type, %{"actions" => updated})
  end

  def remove_action(%EntityType{} = type, action_id) do
    update_entity_type(type, %{
      "actions" => Enum.reject(type.actions, &(&1["id"] == action_id))
    })
  end

  @doc "Append a declared property. Returns {:error, :duplicate_key} on collision."
  def add_property(%EntityType{} = type, %{"key" => key} = attrs) do
    if Enum.any?(type.properties, &(&1["key"] == key)) do
      {:error, :duplicate_key}
    else
      update_entity_type(type, %{"properties" => type.properties ++ [attrs]})
    end
  end

  def update_property(%EntityType{} = type, key, attrs) do
    updated =
      Enum.map(type.properties, fn
        %{"key" => ^key} = p -> Elixir.Map.merge(p, attrs)
        p -> p
      end)

    update_entity_type(type, %{"properties" => updated})
  end

  def remove_property(%EntityType{} = type, key) do
    update_entity_type(type, %{
      "properties" => Enum.reject(type.properties, &(&1["key"] == key))
    })
  end

  defp new_id, do: Ecto.UUID.generate()
end
