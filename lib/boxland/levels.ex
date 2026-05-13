defmodule Boxland.Levels do
  @moduledoc "Level Editor, Sandbox, and publishing operations."

  import Ecto.Query

  alias Boxland.Entities.EntityType
  alias Boxland.Library.CollisionMask
  alias Boxland.Levels.{Level, LevelEntity, PublishedLevelVersion}
  alias Boxland.Repo

  @preset_entities [
    {"spawn", "Spawn Point"},
    {"collision", "Collision Volume"},
    {"portal", "Portal"},
    {"sign", "Sign"},
    {"collectible", "Collectible"}
  ]

  def preset_entities, do: @preset_entities

  def list_levels(owner_id) do
    Level
    |> where([l], l.owner_id == ^owner_id)
    |> order_by([l], asc: l.name)
    |> preload(:map)
    |> Repo.all()
  end

  def get_level!(owner_id, id) do
    Level
    |> where([l], l.owner_id == ^owner_id and l.id == ^id)
    |> preload([:map, entities: :entity_type])
    |> Repo.one!()
  end

  def create_level(owner_id, attrs) do
    %Level{}
    |> Level.changeset(Elixir.Map.put(attrs, "owner_id", owner_id))
    |> Repo.insert()
  end

  def change_level(%Level{} = level, attrs \\ %{}), do: Level.changeset(level, attrs)

  def create_preset_entity(owner_id, level_id, preset_slug, x, y, overrides \\ %{}) do
    entity_type = ensure_preset_entity_type!(owner_id, preset_slug)

    %LevelEntity{}
    |> LevelEntity.changeset(%{
      level_id: level_id,
      entity_type_id: entity_type.id,
      pos_x: x,
      pos_y: y,
      instance_overrides: overrides,
      script_state: %{}
    })
    |> Repo.insert()
  end

  def delete_entity(owner_id, level_id, entity_id) do
    level = get_level!(owner_id, level_id)

    level.entities
    |> Enum.find(&(&1.id == entity_id))
    |> case do
      nil -> {:error, :not_found}
      entity -> Repo.delete(entity)
    end
  end

  def latest_published_version(level_id) do
    PublishedLevelVersion
    |> where([v], v.level_id == ^level_id)
    |> order_by([v], desc: v.version)
    |> limit(1)
    |> Repo.one()
  end

  def latest_published_version!(level_id) do
    case latest_published_version(level_id) do
      nil -> raise Ecto.NoResultsError, queryable: PublishedLevelVersion
      version -> version
    end
  end

  def publish_level(owner_id, level_id) do
    level = get_level!(owner_id, level_id)

    with :ok <- validate_publishable(level) do
      version = next_version(level.id)

      %PublishedLevelVersion{}
      |> PublishedLevelVersion.changeset(%{
        level_id: level.id,
        version: version,
        snapshot: snapshot(level)
      })
      |> Repo.insert()
    end
  end

  def blocked?(level, assets, cell_x, cell_y) do
    entity_blocked?(level, cell_x, cell_y) or
      tile_blocked?(level.map.layers, assets, cell_x, cell_y)
  end

  def blocked?(level, cell_x, cell_y), do: entity_blocked?(level, cell_x, cell_y)

  defp ensure_preset_entity_type!(owner_id, preset_slug) do
    {slug, name} =
      Enum.find(@preset_entities, fn {slug, _name} -> slug == preset_slug end) ||
        raise ArgumentError, "unknown preset entity #{inspect(preset_slug)}"

    Repo.get_by(EntityType, owner_id: owner_id, slug: "preset-#{slug}") ||
      %EntityType{}
      |> EntityType.changeset(%{
        owner_id: owner_id,
        slug: "preset-#{slug}",
        name: name,
        components: [%{"preset" => slug}],
        default_collision_mask: if(slug == "collision", do: "full", else: "none")
      })
      |> Repo.insert!()
  end

  defp validate_publishable(%Level{} = level) do
    if Enum.any?(level.entities, &(preset_slug(&1) == "spawn")) do
      :ok
    else
      {:error, "Add a spawn point before publishing."}
    end
  end

  defp next_version(level_id) do
    PublishedLevelVersion
    |> where([v], v.level_id == ^level_id)
    |> select([v], max(v.version))
    |> Repo.one()
    |> case do
      nil -> 1
      version -> version + 1
    end
  end

  defp snapshot(level) do
    map = Repo.preload(level.map, :layers)
    asset_ids = tile_asset_ids(map.layers)
    assets = assets_snapshot(asset_ids)

    %{
      "level" => %{
        "id" => level.id,
        "slug" => level.slug,
        "name" => level.name,
        "hud_config" => level.hud_config,
        "instancing" => level.instancing
      },
      "map" => %{
        "id" => map.id,
        "slug" => map.slug,
        "name" => map.name,
        "width" => map.width,
        "height" => map.height,
        "layers" =>
          Enum.map(map.layers, fn layer ->
            %{
              "name" => layer.name,
              "z_index" => layer.z_index,
              "tiles" => layer.tiles
            }
          end)
      },
      "assets" => assets,
      "entities" =>
        Enum.map(level.entities, fn entity ->
          %{
            "id" => entity.id,
            "preset" => preset_slug(entity),
            "pos_x" => entity.pos_x,
            "pos_y" => entity.pos_y,
            "instance_overrides" => entity.instance_overrides
          }
        end)
    }
  end

  defp tile_asset_ids(layers) do
    layers
    |> Enum.flat_map(fn layer ->
      layer.tiles
      |> Map.values()
      |> Enum.map(& &1["asset_id"])
    end)
    |> Enum.reject(&is_nil/1)
    |> Enum.uniq()
  end

  defp assets_snapshot([]), do: []

  defp assets_snapshot(asset_ids) do
    Boxland.Library.Asset
    |> where([a], a.id in ^asset_ids)
    |> Repo.all()
    |> Enum.map(fn asset ->
      %{
        "id" => asset.id,
        "kind" => asset.kind,
        "name" => asset.name,
        "content_url" => asset.content_url,
        "metadata" => asset.metadata
      }
    end)
  end

  defp entity_blocked?(level, cell_x, cell_y) do
    level.entities
    |> Enum.any?(fn entity ->
      entity_cell_x = div(entity.pos_x, 32)
      entity_cell_y = div(entity.pos_y, 32)
      preset = preset_slug(entity)
      preset == "collision" and entity_cell_x == cell_x and entity_cell_y == cell_y
    end)
  end

  defp tile_blocked?(layers, assets, cell_x, cell_y) do
    assets_by_id = Map.new(assets, &{&1.id, &1})

    layers
    |> Enum.any?(fn layer ->
      case Map.get(layer.tiles, "#{cell_x},#{cell_y}") do
        %{"asset_id" => asset_id, "tile_index" => tile_index} ->
          asset = assets_by_id[asset_id]

          asset &&
            asset.metadata
            |> Map.get("collisions", %{})
            |> Map.get(Integer.to_string(tile_index), CollisionMask.none())
            |> CollisionMask.to_booleans()
            |> Enum.any?()

        _ ->
          false
      end
    end)
  end

  defp preset_slug(%LevelEntity{entity_type: %EntityType{components: components}}) do
    components
    |> Enum.find_value(fn
      %{"preset" => preset} -> preset
      _ -> nil
    end)
  end
end
