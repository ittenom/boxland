defmodule Boxland.Levels do
  @moduledoc "Level Editor, Sandbox, and publishing operations."

  import Ecto.Query

  alias Boxland.Entities
  alias Boxland.Entities.EntityType
  alias Boxland.Library.CollisionMask
  alias Boxland.Levels.{Level, LevelEntity, PublishedLevelVersion}
  alias Boxland.Maps
  alias Boxland.Maps.Map, as: BMap
  alias Boxland.Repo

  @cell_px 32

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

  def create_preset_entity(owner_id, level_id, preset_slug, x, y, overrides \\ %{}, opts \\ []) do
    entity_type = ensure_preset_entity_type!(owner_id, preset_slug)
    z_override = Keyword.get(opts, :z_index_override)

    %LevelEntity{}
    |> LevelEntity.changeset(%{
      level_id: level_id,
      entity_type_id: entity_type.id,
      pos_x: x,
      pos_y: y,
      z_index_override: z_override,
      instance_overrides: overrides,
      script_state: %{"alive" => true}
    })
    |> Repo.insert()
  end

  @doc """
  Create a fresh LevelEntity. `attrs` may include `entity_type_id`,
  `pos_x`, `pos_y` (pixels), `z_index_override`, `tag`, `group_id`,
  `properties`. `script_state.alive` defaults to true.
  """
  def spawn_entity(owner_id, level_id, attrs) do
    _ = get_level!(owner_id, level_id)

    attrs =
      attrs
      |> stringify_keys()
      |> Elixir.Map.put("level_id", level_id)
      |> Elixir.Map.put_new("script_state", %{"alive" => true})

    %LevelEntity{}
    |> LevelEntity.changeset(attrs)
    |> Repo.insert()
  end

  @doc """
  Soft-delete: mark `script_state.alive = false`. The instance row stays
  put so referencing actions/snapshots still see it.
  """
  def despawn_entity(owner_id, level_id, entity_id) do
    case get_entity(owner_id, level_id, entity_id) do
      nil ->
        {:error, :not_found}

      entity ->
        entity
        |> LevelEntity.changeset(%{
          script_state: Elixir.Map.put(entity.script_state || %{}, "alive", false)
        })
        |> Repo.update()
    end
  end

  def delete_entity(owner_id, level_id, entity_id) do
    case get_entity(owner_id, level_id, entity_id) do
      nil -> {:error, :not_found}
      entity -> Repo.delete(entity)
    end
  end

  def get_entity(owner_id, level_id, entity_id) do
    level = get_level!(owner_id, level_id)
    Enum.find(level.entities, &(&1.id == entity_id))
  end

  @doc """
  Update an entity's mutable fields (`tag`, `properties`,
  `z_index_override`, `pos_x`, `pos_y`, `group_id`). Returns
  `{:ok, updated}` or a changeset error.
  """
  def update_entity(%LevelEntity{} = entity, attrs) do
    entity
    |> LevelEntity.changeset(stringify_keys(attrs))
    |> Repo.update()
  end

  @doc """
  Bind an existing tile group to an entity. Sets `entity.group_id` and
  snaps `pos_x/pos_y` to the group's bbox top-left.
  """
  def bind_group(owner_id, level_id, entity_id, group_id) when is_binary(group_id) do
    level = get_level!(owner_id, level_id)
    map = Repo.preload(level.map, :layers)

    case Enum.find(level.entities, &(&1.id == entity_id)) do
      nil ->
        {:error, :not_found}

      entity ->
        members = Maps.find_group_members(map, group_id)

        case members do
          [] ->
            {:error, :empty_group}

          _ ->
            {min_x, min_y} = bbox_top_left(members)

            update_entity(entity, %{
              "group_id" => group_id,
              "pos_x" => min_x * @cell_px,
              "pos_y" => min_y * @cell_px
            })
        end
    end
  end

  @doc """
  Move an entity by `(dx, dy, dz)` cells. If the entity owns a tile
  group (`group_id`), every tile in that group translates with it:
  `(x, y)` shifts by `(dx, dy)` within its layer, and the tile moves
  to the layer at `source_layer.z_index + dz`. If no layer exists at
  the target z for a given tile, the move fails with
  `{:error, :no_target_layer}`.

  Returns `{:ok, updated_entity}` on success.
  """
  def move_entity(owner_id, level_id, entity_id, dx, dy, dz \\ 0)
      when is_integer(dx) and is_integer(dy) and is_integer(dz) do
    level = get_level!(owner_id, level_id)
    map = Repo.preload(level.map, :layers)
    entity = Enum.find(level.entities, &(&1.id == entity_id))

    cond do
      is_nil(entity) ->
        {:error, :not_found}

      dx == 0 and dy == 0 and dz == 0 ->
        {:ok, entity}

      true ->
        Repo.transaction(fn ->
          with {:ok, _} <- maybe_translate_group(map, entity, dx, dy, dz),
               {:ok, updated} <- apply_entity_offset(entity, dx, dy, dz) do
            updated
          else
            {:error, reason} -> Repo.rollback(reason)
          end
        end)
    end
  end

  defp maybe_translate_group(_map, %LevelEntity{group_id: nil}, _dx, _dy, _dz), do: {:ok, :noop}

  defp maybe_translate_group(%BMap{} = map, %LevelEntity{group_id: gid}, dx, dy, dz) do
    members = Maps.find_group_members(map, gid)

    if members == [] do
      {:ok, :noop}
    else
      translate_group_members(map, members, dx, dy, dz, gid)
    end
  end

  defp translate_group_members(map, members, dx, dy, dz, gid) do
    layers_by_id = Elixir.Map.new(map.layers, &{&1.id, &1})
    layers_by_z = Enum.group_by(map.layers, & &1.z_index)

    target_records =
      Enum.reduce_while(members, {:ok, []}, fn {lid, x, y, tile}, {:ok, acc} ->
        source_layer = layers_by_id[lid]
        target_z = source_layer.z_index + dz

        case pick_layer_at_z(layers_by_z, target_z, lid) do
          nil ->
            {:halt, {:error, :no_target_layer}}

          target ->
            {:cont,
             {:ok,
              [
                %{
                  layer_id: target.id,
                  dx: x + dx,
                  dy: y + dy,
                  tile: Elixir.Map.put(tile, "group_id", gid)
                }
                | acc
              ]}}
        end
      end)

    with {:ok, recs} <- target_records,
         {:ok, del_updates} <- Maps.delete_cells_across_layers(map, members),
         map_after_delete <- apply_layer_updates_to_struct(map, del_updates),
         {:ok, _place_updates} <-
           Maps.place_block(map_after_delete, recs, {0, 0}, hd(map.layers).id) do
      {:ok, :moved}
    end
  end

  defp apply_layer_updates_to_struct(map, updates) do
    new_layers =
      Enum.map(map.layers, fn l ->
        case Elixir.Map.get(updates, l.id) do
          {_prev, updated} -> updated
          _ -> l
        end
      end)

    %{map | layers: new_layers}
  end

  defp pick_layer_at_z(layers_by_z, z, prefer_id) do
    case layers_by_z[z] do
      nil -> nil
      [single] -> single
      list -> Enum.find(list, &(&1.id == prefer_id)) || hd(Enum.sort_by(list, & &1.id))
    end
  end

  defp apply_entity_offset(entity, dx, dy, dz) do
    new_z =
      case entity.z_index_override do
        nil -> nil
        z -> z + dz
      end

    update_entity(entity, %{
      "pos_x" => entity.pos_x + dx * @cell_px,
      "pos_y" => entity.pos_y + dy * @cell_px,
      "z_index_override" => new_z
    })
  end

  defp bbox_top_left(members) do
    {Enum.min_by(members, fn {_l, x, _y, _t} -> x end) |> elem(1),
     Enum.min_by(members, fn {_l, _x, y, _t} -> y end) |> elem(2)}
  end

  defp stringify_keys(map) when is_map(map) do
    Elixir.Map.new(map, fn
      {k, v} when is_atom(k) -> {Atom.to_string(k), v}
      {k, v} -> {k, v}
    end)
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

  @doc """
  Compute impassable cells keyed by z-index. Tile cells contribute at
  their layer's `z_index`; collision-preset entity placements contribute
  at their effective z (`z_index_override || entity_type.default_z_index`).

  Returns `%{z_index => MapSet<{x, y}>}`. Used by the editor path preview
  and Sandbox runtime so both see the same z-aware blockers as ECA's
  `step_along_path`.
  """
  def blocked_cells_by_z(level, assets) do
    assets_by_id = Elixir.Map.new(assets, &{&1.id, &1})

    tile_acc =
      Enum.reduce(level.map.layers, %{}, fn layer, acc ->
        cells =
          layer.tiles
          |> Enum.filter(fn {_k, tile} -> tile_cell_blocked?(tile, assets_by_id) end)
          |> Enum.map(fn {k, _t} -> Maps.parse_key(k) end)
          |> MapSet.new()

        if MapSet.size(cells) == 0 do
          acc
        else
          Elixir.Map.update(acc, layer.z_index, cells, &MapSet.union(&1, cells))
        end
      end)

    Enum.reduce(level.entities, tile_acc, fn entity, acc ->
      if preset_slug(entity) == "collision" do
        z = entity_effective_z(entity)
        cell = {div(entity.pos_x, @cell_px), div(entity.pos_y, @cell_px)}
        Elixir.Map.update(acc, z, MapSet.new([cell]), &MapSet.put(&1, cell))
      else
        acc
      end
    end)
  end

  @doc """
  Look up the blocked-cell MapSet for a specific z, returning an empty
  MapSet when no entry exists.
  """
  def blocked_at(%{} = blocked_by_z, z) do
    Elixir.Map.get(blocked_by_z, z, MapSet.new())
  end

  @doc """
  Effective z for a LevelEntity (instance override wins over entity-type default).
  """
  def entity_effective_z(%LevelEntity{} = entity) do
    entity.z_index_override || entity.entity_type.default_z_index || 0
  end

  defp tile_cell_blocked?(%{"asset_id" => asset_id, "tile_index" => tile_index}, assets_by_id) do
    asset = assets_by_id[asset_id]

    asset &&
      asset.metadata
      |> Elixir.Map.get("collisions", %{})
      |> Elixir.Map.get(Integer.to_string(tile_index), CollisionMask.none())
      |> CollisionMask.to_booleans()
      |> Enum.any?()
  end

  defp tile_cell_blocked?(_, _), do: false

  @doc """
  Ensure-and-return an EntityType matching the given visual source.

  Sources:
    {:preset, slug}            -> reuses existing preset type
    {:tile, asset_id, idx}     -> one type per (asset, tile)
    {:sprite, asset_id}        -> one type per sprite asset
    {:group, group_id}         -> one type per tile group
    :invisible                 -> single shared "invisible-box" type

  Type slugs are deterministic, so repeated calls reuse the row.
  """
  def ensure_entity_type_for(owner_id, source) do
    {slug, name, visual_ref} = type_descriptor_for(source)

    case Repo.get_by(EntityType, owner_id: owner_id, slug: slug) do
      nil ->
        %EntityType{}
        |> EntityType.changeset(%{
          owner_id: owner_id,
          slug: slug,
          name: name,
          visual_ref: visual_ref,
          default_collision_mask: "none"
        })
        |> Repo.insert()

      existing ->
        {:ok, existing}
    end
  end

  defp type_descriptor_for({:preset, slug}) do
    {name, _} = Enum.find(@preset_entities, fn {s, _} -> s == slug end) || {slug, slug}
    {"preset-#{slug}", name, %{"kind" => "preset", "slug" => slug}}
  end

  defp type_descriptor_for({:tile, asset_id, tile_index}) do
    {"tile-#{asset_id}-#{tile_index}", "Tile #{asset_id}/#{tile_index}",
     %{"kind" => "tile", "asset_id" => asset_id, "tile_index" => tile_index, "rotation" => 0}}
  end

  defp type_descriptor_for({:sprite, asset_id}) do
    {"sprite-#{asset_id}", "Sprite #{asset_id}", %{"kind" => "sprite", "asset_id" => asset_id}}
  end

  defp type_descriptor_for({:group, group_id}) do
    # group_id is URL-safe base64 (mixed case + _/-), which doesn't satisfy
    # EntityType's slug regex. Use a hex digest so the slug stays deterministic
    # but lowercase.
    hash =
      :crypto.hash(:sha256, group_id) |> Base.encode16(case: :lower) |> binary_part(0, 12)

    {"group-#{hash}", "Group #{group_id}", %{"kind" => "group", "group_id" => group_id}}
  end

  defp type_descriptor_for(:invisible) do
    {"invisible-box", "Invisible Box", %{"kind" => "invisible"}}
  end

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
        visual_ref: %{"kind" => "preset", "slug" => slug},
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
          map.layers
          |> Enum.sort_by(fn l -> {l.z_index, l.id} end)
          |> Enum.map(fn layer ->
            %{
              "id" => layer.id,
              "name" => layer.name,
              "z_index" => layer.z_index,
              "visible" => layer.visible,
              "locked" => layer.locked,
              "opacity" => layer.opacity,
              "tiles" => layer.tiles
            }
          end)
      },
      "assets" => assets,
      "entity_types" =>
        level.entities
        |> Enum.map(& &1.entity_type)
        |> Enum.uniq_by(& &1.id)
        |> Enum.map(fn et ->
          %{
            "id" => et.id,
            "slug" => et.slug,
            "name" => et.name,
            "visual_ref" => et.visual_ref,
            "size" => et.size,
            "properties" => et.properties,
            "actions" => et.actions,
            "default_z_index" => et.default_z_index,
            "default_collision_mask" => et.default_collision_mask
          }
        end),
      "entities" =>
        Enum.map(level.entities, fn entity ->
          %{
            "id" => entity.id,
            "entity_type_id" => entity.entity_type_id,
            "preset" => preset_slug(entity),
            "tag" => entity.tag,
            "group_id" => entity.group_id,
            "pos_x" => entity.pos_x,
            "pos_y" => entity.pos_y,
            "z_index" => entity.z_index_override || entity.entity_type.default_z_index,
            "properties" => Entities.merge_properties(entity.entity_type, entity.properties),
            "instance_overrides" => entity.instance_overrides,
            "alive" => Elixir.Map.get(entity.script_state || %{}, "alive", true)
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

  defp preset_slug(%LevelEntity{entity_type: %EntityType{} = et}) do
    case et.visual_ref do
      %{"kind" => "preset", "slug" => slug} -> slug
      _ -> components_preset(et.components)
    end
  end

  defp components_preset(components) when is_list(components) do
    Enum.find_value(components, fn
      %{"preset" => preset} -> preset
      _ -> nil
    end)
  end

  defp components_preset(_), do: nil
end
