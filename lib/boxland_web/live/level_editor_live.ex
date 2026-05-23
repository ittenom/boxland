defmodule BoxlandWeb.LevelEditorLive do
  use BoxlandWeb, :live_view

  alias Boxland.{Entities, Levels, Library, Maps, Repo}
  alias Boxland.Entities.EntityType
  alias Boxland.Levels.LevelEntity

  @cell_px 32
  @tools ~w(select place delete)

  def mount(%{"id" => id}, _session, socket) do
    designer = socket.assigns.current_designer
    level = Levels.get_level!(designer.id, id) |> preload_map_layers()

    {:ok,
     socket
     |> assign(:level, level)
     |> assign(:tilesets, Library.list_tilesets(designer.id))
     |> assign(:sprites, list_sprites(designer.id))
     |> assign(:groups, list_groups(level))
     |> assign(:tool, "select")
     |> assign(:palette_mode, "preset")
     |> assign(:preset, "spawn")
     |> assign(:selected_tile, nil)
     |> assign(:selected_sprite_id, nil)
     |> assign(:selected_group_id, nil)
     |> assign(:invisible_size, %{"w" => 1, "h" => 1})
     |> assign(:place_z, default_place_z(level))
     |> assign(:selected_layer_id, default_selected_layer_id(level))
     |> assign(:renaming_layer_id, nil)
     |> assign(:selection, nil)
     |> assign(:publish_error, nil)}
  end

  defp default_selected_layer_id(level) do
    case visible_layers(level.map) do
      [] -> nil
      [layer | _] -> layer.id
    end
  end

  # === Tool events ===

  def handle_event("tool", %{"tool" => tool}, socket) when tool in @tools do
    socket =
      socket
      |> assign(:tool, tool)
      |> maybe_clear_selection_for_tool(tool)

    {:noreply, socket}
  end

  # === Palette events ===

  def handle_event("palette_mode", %{"mode" => mode}, socket) do
    # Picking from the palette implies you want to place — switch tools.
    {:noreply, socket |> assign(:palette_mode, mode) |> assign(:tool, "place")}
  end

  def handle_event("preset", %{"preset" => preset}, socket) do
    {:noreply,
     socket
     |> assign(:preset, preset)
     |> assign(:palette_mode, "preset")
     |> assign(:tool, "place")}
  end

  def handle_event("pick_tile", %{"asset_id" => asset_id, "index" => index}, socket) do
    {:noreply,
     socket
     |> assign(:selected_tile, %{
       "asset_id" => String.to_integer(asset_id),
       "tile_index" => String.to_integer(index)
     })
     |> assign(:palette_mode, "tile")
     |> assign(:tool, "place")}
  end

  def handle_event("pick_sprite", %{"asset_id" => asset_id}, socket) do
    {:noreply,
     socket
     |> assign(:selected_sprite_id, String.to_integer(asset_id))
     |> assign(:palette_mode, "sprite")
     |> assign(:tool, "place")}
  end

  def handle_event("pick_group", %{"group_id" => gid}, socket) do
    {:noreply,
     socket
     |> assign(:selected_group_id, gid)
     |> assign(:palette_mode, "group")
     |> assign(:tool, "place")}
  end

  def handle_event("set_invisible_size", %{"w" => w, "h" => h}, socket) do
    {:noreply,
     assign(socket, :invisible_size, %{
       "w" => safe_int(w, 1),
       "h" => safe_int(h, 1)
     })}
  end

  def handle_event("set_place_z", %{"z" => z}, socket) do
    {:noreply, assign(socket, :place_z, safe_int(z, 0))}
  end

  # === Canvas events ===

  def handle_event("cell", %{"x" => x, "y" => y}, socket) do
    cell_x = String.to_integer(x)
    cell_y = String.to_integer(y)

    case socket.assigns.tool do
      "select" -> handle_cell_select(socket, cell_x, cell_y)
      "place" -> handle_cell_place(socket, cell_x, cell_y)
      "delete" -> handle_cell_delete(socket, cell_x, cell_y)
    end
  end

  def handle_event("select_entity", %{"id" => id}, socket) do
    {:noreply, assign(socket, :selection, {:entity, String.to_integer(id)})}
  end

  def handle_event("clear_selection", _params, socket) do
    {:noreply, assign(socket, :selection, nil)}
  end

  def handle_event("delete_entity", %{"id" => id}, socket) do
    designer = socket.assigns.current_designer
    level = socket.assigns.level
    _ = Levels.delete_entity(designer.id, level.id, String.to_integer(id))

    {:noreply,
     socket
     |> refresh_level()
     |> assign(:selection, nil)}
  end

  def handle_event("promote_selection", _params, socket) do
    designer = socket.assigns.current_designer
    level = socket.assigns.level

    case socket.assigns.selection do
      {:group, gid} ->
        case ensure_group_entity(designer, level, gid) do
          {:ok, entity} ->
            {:noreply,
             socket
             |> refresh_level()
             |> assign(:selection, {:entity, entity.id})}

          {:error, _} ->
            {:noreply, put_flash(socket, :error, "Could not promote group.")}
        end

      {:tile, layer_id, x, y} ->
        case promote_tile(designer, level, layer_id, x, y) do
          {:ok, entity} ->
            {:noreply,
             socket
             |> refresh_level()
             |> assign(:selection, {:entity, entity.id})}

          {:error, _} ->
            {:noreply, put_flash(socket, :error, "Could not promote tile.")}
        end

      _ ->
        {:noreply, socket}
    end
  end

  # === Layer events ===

  def handle_event("select_layer", %{"id" => id}, socket) do
    lid = String.to_integer(id)
    layer = Enum.find(socket.assigns.level.map.layers, &(&1.id == lid))

    socket =
      socket
      |> assign(:selected_layer_id, lid)
      |> then(fn s -> if layer, do: assign(s, :place_z, layer.z_index), else: s end)

    {:noreply, socket}
  end

  def handle_event("add_layer", _params, socket) do
    {:ok, layer} = Maps.create_layer(socket.assigns.level.map)

    {:noreply,
     socket
     |> refresh_level()
     |> assign(:selected_layer_id, layer.id)
     |> assign(:place_z, layer.z_index)}
  end

  def handle_event("duplicate_layer", %{"id" => id}, socket) do
    layer = Maps.get_layer_for_map!(socket.assigns.level.map.id, String.to_integer(id))
    {:ok, dup} = Maps.duplicate_layer(layer)

    {:noreply,
     socket
     |> refresh_level()
     |> assign(:selected_layer_id, dup.id)
     |> assign(:place_z, dup.z_index)}
  end

  def handle_event("delete_layer", %{"id" => id}, socket) do
    layer_id = String.to_integer(id)
    layer = Maps.get_layer_for_map!(socket.assigns.level.map.id, layer_id)

    case Maps.delete_layer(layer) do
      {:ok, _} ->
        socket = refresh_level(socket)

        new_selected =
          if socket.assigns.selected_layer_id == layer_id,
            do: default_selected_layer_id(socket.assigns.level),
            else: socket.assigns.selected_layer_id

        {:noreply,
         socket
         |> assign(:selected_layer_id, new_selected)
         |> assign(:place_z, default_place_z(socket.assigns.level))}

      {:error, :last_layer} ->
        {:noreply, put_flash(socket, :error, "A map needs at least one layer.")}
    end
  end

  def handle_event("rename_layer_start", %{"id" => id}, socket) do
    {:noreply, assign(socket, :renaming_layer_id, String.to_integer(id))}
  end

  def handle_event("rename_layer_cancel", _params, socket) do
    {:noreply, assign(socket, :renaming_layer_id, nil)}
  end

  def handle_event("rename_layer", %{"id" => id, "name" => name}, socket) do
    name = String.trim(name)

    if name == "" do
      {:noreply, assign(socket, :renaming_layer_id, nil)}
    else
      layer = Maps.get_layer_for_map!(socket.assigns.level.map.id, String.to_integer(id))

      case Maps.rename_layer(layer, name) do
        {:ok, _} ->
          {:noreply,
           socket
           |> assign(:renaming_layer_id, nil)
           |> refresh_level()}

        {:error, _} ->
          {:noreply,
           socket
           |> put_flash(:error, "That name is already in use.")
           |> assign(:renaming_layer_id, nil)}
      end
    end
  end

  def handle_event("toggle_visibility", %{"id" => id}, socket) do
    layer = Maps.get_layer_for_map!(socket.assigns.level.map.id, String.to_integer(id))
    {:ok, _} = Maps.toggle_layer_visibility(layer)
    {:noreply, refresh_level(socket)}
  end

  def handle_event("toggle_lock", %{"id" => id}, socket) do
    layer = Maps.get_layer_for_map!(socket.assigns.level.map.id, String.to_integer(id))
    {:ok, _} = Maps.toggle_layer_lock(layer)
    {:noreply, refresh_level(socket)}
  end

  def handle_event("set_opacity", %{"id" => id, "opacity" => opacity}, socket) do
    layer = Maps.get_layer_for_map!(socket.assigns.level.map.id, String.to_integer(id))
    {:ok, _} = Maps.set_layer_opacity(layer, String.to_integer(opacity))
    {:noreply, refresh_level(socket)}
  end

  def handle_event("move_layer_up", %{"id" => id}, socket) do
    move_layer(socket, String.to_integer(id), -1)
  end

  def handle_event("move_layer_down", %{"id" => id}, socket) do
    move_layer(socket, String.to_integer(id), +1)
  end

  # === Inspector events ===

  def handle_event("inspector_save", params, socket) do
    designer = socket.assigns.current_designer
    level = socket.assigns.level

    with %LevelEntity{} = entity <- selected_entity(socket),
         {:ok, _} <- Levels.update_entity(entity, inspector_attrs(params)) do
      {:noreply, refresh_level(socket)}
    else
      nil -> {:noreply, socket}
      {:error, _cs} -> {:noreply, put_flash(socket, :error, "Could not save entity.")}
    end
    |> tap(fn _ -> designer && level end)
  end

  def handle_event(
        "inspector_property_set",
        %{"key" => key, "value" => value} = _params,
        socket
      ) do
    case selected_entity(socket) do
      nil ->
        {:noreply, socket}

      entity ->
        coerced = coerce_property_value(entity.entity_type, key, value)
        new_props = Map.put(entity.properties || %{}, key, coerced)
        {:ok, _} = Levels.update_entity(entity, %{"properties" => new_props})
        {:noreply, refresh_level(socket)}
    end
  end

  def handle_event(
        "type_add_property",
        %{"key" => key, "type" => type, "default" => default},
        socket
      ) do
    case selected_entity(socket) do
      nil ->
        {:noreply, socket}

      entity ->
        coerced_default = coerce_value(type, default)

        Entities.add_property(entity.entity_type, %{
          "key" => key,
          "type" => type,
          "default" => coerced_default
        })
        |> case do
          {:ok, _} -> {:noreply, refresh_level(socket)}
          {:error, :duplicate_key} -> {:noreply, put_flash(socket, :error, "Key exists.")}
        end
    end
  end

  def handle_event("type_remove_property", %{"key" => key}, socket) do
    case selected_entity(socket) do
      nil ->
        {:noreply, socket}

      entity ->
        {:ok, _} = Entities.remove_property(entity.entity_type, key)
        {:noreply, refresh_level(socket)}
    end
  end

  def handle_event("type_add_action", _params, socket) do
    case selected_entity(socket) do
      nil ->
        {:noreply, socket}

      entity ->
        {:ok, _} =
          Entities.add_action(entity.entity_type, %{
            "name" => "Action",
            "trigger" => %{"kind" => "spawn"},
            "function" => %{"kind" => "despawn_self"}
          })

        {:noreply, refresh_level(socket)}
    end
  end

  def handle_event(
        "type_update_action",
        %{"action_id" => action_id, "field" => field, "value" => value},
        socket
      ) do
    case selected_entity(socket) do
      nil ->
        {:noreply, socket}

      entity ->
        action = Enum.find(entity.entity_type.actions, &(&1["id"] == action_id))

        if action do
          updated = put_in_action(action, String.split(field, "."), value)
          {:ok, _} = Entities.update_action(entity.entity_type, action_id, updated)
          {:noreply, refresh_level(socket)}
        else
          {:noreply, socket}
        end
    end
  end

  def handle_event("type_remove_action", %{"action_id" => action_id}, socket) do
    case selected_entity(socket) do
      nil ->
        {:noreply, socket}

      entity ->
        {:ok, _} = Entities.remove_action(entity.entity_type, action_id)
        {:noreply, refresh_level(socket)}
    end
  end

  def handle_event("publish", _params, socket) do
    designer = socket.assigns.current_designer
    level = socket.assigns.level

    case Levels.publish_level(designer.id, level.id) do
      {:ok, version} ->
        {:noreply,
         socket
         |> put_flash(:info, "Published version #{version.version}.")
         |> assign(:publish_error, nil)}

      {:error, reason} when is_binary(reason) ->
        {:noreply, assign(socket, :publish_error, reason)}

      {:error, changeset} ->
        {:noreply, assign(socket, :publish_error, inspect(changeset.errors))}
    end
  end

  # === Layer helpers ===

  defp move_layer(socket, layer_id, direction) do
    layers = display_layers(socket.assigns.level.map)
    index = Enum.find_index(layers, &(&1.id == layer_id))
    target = index && index + direction

    if is_nil(index) or target < 0 or target >= length(layers) do
      {:noreply, socket}
    else
      reordered =
        layers
        |> List.replace_at(index, Enum.at(layers, target))
        |> List.replace_at(target, Enum.at(layers, index))

      {:ok, _} = Maps.reorder_layers(socket.assigns.level.map.id, Enum.map(reordered, & &1.id))
      {:noreply, refresh_level(socket)}
    end
  end

  defp display_layers(map) do
    Enum.sort_by(map.layers, fn l -> {-l.z_index, l.id} end)
  end

  # === Cell-click dispatch helpers ===

  defp handle_cell_place(socket, x, y) do
    designer = socket.assigns.current_designer
    level = socket.assigns.level

    case place_from_palette(socket, designer, level, x, y) do
      {:ok, entity} ->
        {:noreply,
         socket
         |> refresh_level()
         |> assign(:selection, {:entity, entity.id})}

      {:error, reason} ->
        {:noreply, put_flash(socket, :error, format_place_error(reason))}
    end
  end

  defp handle_cell_select(socket, x, y) do
    level = socket.assigns.level
    selection = pick_selection(level, x, y)
    {:noreply, assign(socket, :selection, selection)}
  end

  defp pick_selection(level, x, y) do
    cond do
      (entity = topmost_entity_at(level, x, y)) -> {:entity, entity.id}
      (gid = group_id_at(level.map, x, y)) -> {:group, gid}
      (tile = tile_at_on_visible(level.map, x, y)) -> {:tile, elem(tile, 0).id, x, y}
      true -> nil
    end
  end

  defp handle_cell_delete(socket, x, y) do
    case topmost_entity_at(socket.assigns.level, x, y) do
      nil ->
        {:noreply, socket}

      entity ->
        designer = socket.assigns.current_designer
        _ = Levels.delete_entity(designer.id, socket.assigns.level.id, entity.id)

        {:noreply,
         socket
         |> refresh_level()
         |> assign(:selection, nil)}
    end
  end

  # === Placement helpers ===

  defp place_from_palette(socket, designer, level, x, y) do
    case socket.assigns.palette_mode do
      "preset" ->
        {:ok, _} =
          Levels.create_preset_entity(
            designer.id,
            level.id,
            socket.assigns.preset,
            x * @cell_px,
            y * @cell_px,
            %{},
            z_index_override: socket.assigns.place_z
          )

      "tile" ->
        place_tile(socket, designer, level, x, y)

      "sprite" ->
        place_sprite(socket, designer, level, x, y)

      "group" ->
        place_group(socket, designer, level)

      "invisible" ->
        place_invisible(socket, designer, level, x, y)

      _ ->
        {:error, :unknown_palette}
    end
  end

  defp place_tile(socket, designer, _level_id, x, y) do
    case socket.assigns.selected_tile do
      nil ->
        {:error, :no_tile_selected}

      %{"asset_id" => asset_id, "tile_index" => tile_index} ->
        {:ok, type} =
          Levels.ensure_entity_type_for(designer.id, {:tile, asset_id, tile_index})

        Levels.spawn_entity(designer.id, socket.assigns.level.id, %{
          "entity_type_id" => type.id,
          "pos_x" => x * @cell_px,
          "pos_y" => y * @cell_px,
          "z_index_override" => socket.assigns.place_z
        })
    end
  end

  defp place_sprite(socket, designer, _level_id, x, y) do
    case socket.assigns.selected_sprite_id do
      nil ->
        {:error, :no_sprite_selected}

      sprite_id ->
        {:ok, type} = Levels.ensure_entity_type_for(designer.id, {:sprite, sprite_id})

        Levels.spawn_entity(designer.id, socket.assigns.level.id, %{
          "entity_type_id" => type.id,
          "pos_x" => x * @cell_px,
          "pos_y" => y * @cell_px,
          "z_index_override" => socket.assigns.place_z
        })
    end
  end

  defp place_group(socket, designer, _level_id) do
    case socket.assigns.selected_group_id do
      nil ->
        {:error, :no_group_selected}

      gid ->
        {:ok, type} = Levels.ensure_entity_type_for(designer.id, {:group, gid})

        # Position will be snapped to bbox top-left by bind_group.
        {:ok, entity} =
          Levels.spawn_entity(designer.id, socket.assigns.level.id, %{
            "entity_type_id" => type.id,
            "pos_x" => 0,
            "pos_y" => 0,
            "z_index_override" => socket.assigns.place_z
          })

        Levels.bind_group(designer.id, socket.assigns.level.id, entity.id, gid)
    end
  end

  defp place_invisible(socket, designer, _level_id, x, y) do
    {:ok, type} = Levels.ensure_entity_type_for(designer.id, :invisible)
    size = socket.assigns.invisible_size

    Levels.spawn_entity(designer.id, socket.assigns.level.id, %{
      "entity_type_id" => type.id,
      "pos_x" => x * @cell_px,
      "pos_y" => y * @cell_px,
      "z_index_override" => socket.assigns.place_z,
      "instance_overrides" => %{"size" => size}
    })
  end

  defp format_place_error(:no_tile_selected), do: "Select a tile from the palette first."
  defp format_place_error(:no_sprite_selected), do: "Select a sprite from the palette first."
  defp format_place_error(:no_group_selected), do: "Select a group from the palette first."
  defp format_place_error(_), do: "Could not place entity."

  # === Inspector helpers ===

  defp inspector_attrs(params) do
    params
    |> Map.take(["tag", "pos_x", "pos_y", "z_index_override"])
    |> Map.new(fn
      {"pos_x", v} -> {"pos_x", safe_int(v, 0)}
      {"pos_y", v} -> {"pos_y", safe_int(v, 0)}
      {"z_index_override", ""} -> {"z_index_override", nil}
      {"z_index_override", v} -> {"z_index_override", safe_int(v, 0)}
      {"tag", v} -> {"tag", v}
      pair -> pair
    end)
  end

  defp coerce_property_value(%EntityType{properties: schema}, key, raw) do
    case Enum.find(schema, &(&1["key"] == key)) do
      %{"type" => type} -> coerce_value(type, raw)
      _ -> raw
    end
  end

  defp coerce_property_value(_, _, raw), do: raw

  defp coerce_value("number", v) when is_binary(v) do
    case Integer.parse(v) do
      {n, ""} ->
        n

      _ ->
        case Float.parse(v) do
          {f, ""} -> f
          _ -> 0
        end
    end
  end

  defp coerce_value("boolean", v) when is_binary(v), do: v in ["true", "1", "on"]
  defp coerce_value("boolean", v) when is_boolean(v), do: v
  defp coerce_value(_, v), do: v

  defp put_in_action(action, [last], value), do: Map.put(action, last, value)

  defp put_in_action(action, [head | rest], value) do
    Map.update(action, head, put_in_action(%{}, rest, value), fn child ->
      put_in_action(child || %{}, rest, value)
    end)
  end

  # === Data loading ===

  defp refresh_level(socket) do
    designer = socket.assigns.current_designer
    level = Levels.get_level!(designer.id, socket.assigns.level.id) |> preload_map_layers()

    socket
    |> assign(:level, level)
    |> assign(:groups, list_groups(level))
  end

  defp preload_map_layers(level) do
    Map.update!(level, :map, &Repo.preload(&1, layers: layer_order()))
  end

  defp layer_order do
    import Ecto.Query
    from(l in Boxland.Maps.Layer, order_by: [asc: l.z_index, asc: l.id])
  end

  defp list_sprites(owner_id) do
    import Ecto.Query

    Boxland.Library.Asset
    |> where([a], a.owner_id == ^owner_id and a.kind == "sprite")
    |> order_by([a], asc: a.name)
    |> Repo.all()
  end

  defp list_groups(level) do
    level.map.layers
    |> Enum.flat_map(fn layer ->
      layer.tiles
      |> Map.values()
      |> Enum.map(& &1["group_id"])
    end)
    |> Enum.reject(&is_nil/1)
    |> Enum.uniq()
  end

  defp topmost_entity_at(level, x, y) do
    level.entities
    |> Enum.filter(&entity_covers_cell?(&1, level.map.layers, x, y))
    |> Enum.sort_by(&-entity_z(&1))
    |> List.first()
  end

  defp entity_covers_cell?(entity, layers, x, y) do
    case group_cells_of(entity, layers) do
      [_ | _] = cells ->
        Enum.any?(cells, fn {gx, gy} -> gx == x and gy == y end)

      [] ->
        ex = div(entity.pos_x, @cell_px)
        ey = div(entity.pos_y, @cell_px)
        {w, h} = entity_footprint(entity)
        x >= ex and x < ex + w and y >= ey and y < ey + h
    end
  end

  defp entity_footprint(entity) do
    base =
      (entity.instance_overrides || %{})
      |> Map.get("size", entity.entity_type.size || %{"w" => 1, "h" => 1})

    {Map.get(base, "w", 1), Map.get(base, "h", 1)}
  end

  defp group_cells_of(%LevelEntity{group_id: nil}, _layers), do: []

  defp group_cells_of(%LevelEntity{group_id: gid}, layers) when is_list(layers) do
    for layer <- layers,
        {k, tile} <- layer.tiles,
        tile["group_id"] == gid do
      Maps.parse_key(k)
    end
  end

  defp group_cells_of(_, _), do: []

  defp entity_z(entity), do: entity.z_index_override || entity.entity_type.default_z_index

  defp group_id_at(map, x, y) do
    map.layers
    |> Enum.filter(& &1.visible)
    |> Enum.sort_by(&(-&1.z_index))
    |> Enum.find_value(fn layer ->
      case Maps.tile_at(layer.tiles, x, y) do
        %{"group_id" => gid} when is_binary(gid) -> gid
        _ -> nil
      end
    end)
  end

  defp tile_at_on_visible(map, x, y) do
    map.layers
    |> Enum.filter(& &1.visible)
    |> Enum.sort_by(&(-&1.z_index))
    |> Enum.find_value(fn layer ->
      case Maps.tile_at(layer.tiles, x, y) do
        %{"asset_id" => _, "tile_index" => _} = tile -> {layer, tile}
        _ -> nil
      end
    end)
  end

  defp ensure_group_entity(designer, level, group_id) do
    case Enum.find(level.entities, &(&1.group_id == group_id)) do
      nil ->
        {:ok, type} = Levels.ensure_entity_type_for(designer.id, {:group, group_id})

        {:ok, e} =
          Levels.spawn_entity(designer.id, level.id, %{
            "entity_type_id" => type.id,
            "pos_x" => 0,
            "pos_y" => 0
          })

        Levels.bind_group(designer.id, level.id, e.id, group_id)

      existing ->
        {:ok, existing}
    end
  end

  defp ensure_tile_entity(designer, level, {layer, tile}, x, y) do
    asset_id = tile["asset_id"]
    tile_index = tile["tile_index"]

    existing =
      Enum.find(level.entities, fn e ->
        ref = e.entity_type.visual_ref || %{}

        ref["kind"] == "tile" and
          ref["asset_id"] == asset_id and
          ref["tile_index"] == tile_index and
          div(e.pos_x, @cell_px) == x and
          div(e.pos_y, @cell_px) == y
      end)

    case existing do
      nil ->
        {:ok, type} =
          Levels.ensure_entity_type_for(designer.id, {:tile, asset_id, tile_index})

        Levels.spawn_entity(designer.id, level.id, %{
          "entity_type_id" => type.id,
          "pos_x" => x * @cell_px,
          "pos_y" => y * @cell_px,
          "z_index_override" => layer.z_index
        })

      e ->
        {:ok, e}
    end
  end

  defp maybe_clear_selection_for_tool(socket, "place"), do: assign(socket, :selection, nil)
  defp maybe_clear_selection_for_tool(socket, _), do: socket

  defp selected_entity(socket) do
    case socket.assigns.selection do
      {:entity, id} -> Enum.find(socket.assigns.level.entities, &(&1.id == id))
      _ -> nil
    end
  end

  defp promote_tile(designer, level, layer_id, x, y) do
    layer = Enum.find(level.map.layers, &(&1.id == layer_id))

    case layer && Maps.tile_at(layer.tiles, x, y) do
      %{"asset_id" => _, "tile_index" => _} = tile ->
        ensure_tile_entity(designer, level, {layer, tile}, x, y)

      _ ->
        {:error, :not_found}
    end
  end

  defp default_place_z(level) do
    case visible_layers(level.map) do
      [] -> 0
      [layer | _] -> layer.z_index
    end
  end

  defp visible_layers(%Boxland.Maps.Map{layers: layers}) when is_list(layers) do
    layers
    |> Enum.filter(& &1.visible)
    |> Enum.sort_by(fn l -> {l.z_index, l.id} end)
  end

  defp visible_layers(_), do: []

  defp safe_int(v, default) when is_binary(v) do
    case Integer.parse(v) do
      {n, _} -> n
      :error -> default
    end
  end

  defp safe_int(v, _) when is_integer(v), do: v
  defp safe_int(_, default), do: default

  # === Render ===

  def render(assigns) do
    layers = visible_layers(assigns.level.map)
    selection_entity = selected_entity_for_render(assigns)
    selection_highlight = selection_cells(assigns.selection, assigns.level)
    affected_layers = affected_layer_ids(assigns.selection, assigns.level)

    assigns =
      assigns
      |> assign(:layers, layers)
      |> assign(:selected, selection_entity)
      |> assign(:selection_highlight, selection_highlight)
      |> assign(:affected_layer_ids, affected_layers)

    ~H"""
    <Layouts.app flash={@flash} current_scope={%{designer: @current_designer}}>
      <section class="space-y-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <p class="text-sm font-semibold text-primary">Level Editor</p>
            <h1 class="text-3xl font-semibold tracking-tight">{@level.name}</h1>
          </div>
          <div class="flex gap-2">
            <.link navigate={~p"/app/levels"} class="btn btn-ghost">Levels</.link>
            <.link navigate={~p"/app/levels/#{@level.id}/sandbox"} class="btn btn-secondary">
              Sandbox
            </.link>
            <.link navigate={~p"/play/#{@level.id}"} class="btn btn-ghost">Live</.link>
            <button id="publish-level-button" phx-click="publish" class="btn btn-primary">
              Publish
            </button>
          </div>
        </div>

        <div :if={@publish_error} class="alert alert-error">{@publish_error}</div>

        <div id="level-toolbar" class="flex flex-wrap gap-2">
          <.tool_button icon="hero-cursor-arrow-rays" label="V" tool="select" active={@tool == "select"} />
          <.tool_button icon="hero-pencil" label="P" tool="place" active={@tool == "place"} />
          <.tool_button icon="hero-x-mark" label="X" tool="delete" active={@tool == "delete"} />
        </div>

        <div class="grid gap-4 lg:grid-cols-[14rem_1fr_20rem]">
          <.palette
            mode={@palette_mode}
            preset={@preset}
            tilesets={@tilesets}
            sprites={@sprites}
            groups={@groups}
            selected_tile={@selected_tile}
            selected_sprite_id={@selected_sprite_id}
            selected_group_id={@selected_group_id}
            invisible_size={@invisible_size}
            place_z={@place_z}
          />

          <.canvas
            level={@level}
            layers={@layers}
            tilesets={@tilesets}
            selected_entity_id={selected_entity_id_for_canvas(@selection)}
            highlight_cells={@selection_highlight}
          />

          <div class="space-y-3">
            <.inspector selection={@selection} entity={@selected} />
            <.layers_panel
              layers={display_layers(@level.map)}
              selected_layer_id={@selected_layer_id}
              renaming_layer_id={@renaming_layer_id}
              affected_layer_ids={@affected_layer_ids}
            />
          </div>
        </div>
      </section>
    </Layouts.app>
    """
  end

  defp selected_entity_id_for_canvas({:entity, id}), do: id
  defp selected_entity_id_for_canvas(_), do: nil

  defp selected_entity_for_render(%{selection: {:entity, id}, level: level}),
    do: Enum.find(level.entities, &(&1.id == id))

  defp selected_entity_for_render(_), do: nil

  defp selection_cells(nil, _level), do: MapSet.new()

  defp selection_cells({:entity, _id}, _level), do: MapSet.new()

  defp selection_cells({:group, gid}, level) do
    level.map.layers
    |> Enum.flat_map(fn layer ->
      layer.tiles
      |> Enum.filter(fn {_k, t} -> t["group_id"] == gid end)
      |> Enum.map(fn {k, _t} -> Maps.parse_key(k) end)
    end)
    |> MapSet.new()
  end

  defp selection_cells({:tile, _layer_id, x, y}, _level), do: MapSet.new([{x, y}])

  defp affected_layer_ids(nil, _level), do: MapSet.new()

  defp affected_layer_ids({:entity, id}, level) do
    case Enum.find(level.entities, &(&1.id == id)) do
      nil ->
        MapSet.new()

      entity ->
        cond do
          # Group-bound entity: every layer holding one of its tiles.
          entity.group_id ->
            level.map.layers
            |> Enum.filter(fn l ->
              Enum.any?(l.tiles, fn {_k, t} -> t["group_id"] == entity.group_id end)
            end)
            |> Enum.map(& &1.id)
            |> MapSet.new()

          # Otherwise the layer whose z matches the entity's z.
          true ->
            z = entity.z_index_override || entity.entity_type.default_z_index

            level.map.layers
            |> Enum.filter(&(&1.z_index == z))
            |> Enum.map(& &1.id)
            |> MapSet.new()
        end
    end
  end

  defp affected_layer_ids({:group, gid}, level) do
    level.map.layers
    |> Enum.filter(fn l -> Enum.any?(l.tiles, fn {_k, t} -> t["group_id"] == gid end) end)
    |> Enum.map(& &1.id)
    |> MapSet.new()
  end

  defp affected_layer_ids({:tile, layer_id, _x, _y}, _level), do: MapSet.new([layer_id])

  # === Components ===

  attr :mode, :string
  attr :preset, :string
  attr :tilesets, :list
  attr :sprites, :list
  attr :groups, :list
  attr :selected_tile, :any
  attr :selected_sprite_id, :any
  attr :selected_group_id, :any
  attr :invisible_size, :map
  attr :place_z, :integer

  defp palette(assigns) do
    ~H"""
    <aside id="level-palette" class="space-y-3">
      <nav class="tabs tabs-boxed bg-base-200 text-xs" role="tablist">
        <a
          :for={tab <- ~w(preset tile sprite group invisible)}
          id={"palette-tab-#{tab}"}
          phx-click="palette_mode"
          phx-value-mode={tab}
          class={["tab", @mode == tab && "tab-active"]}
        >
          {String.capitalize(tab)}
        </a>
      </nav>

      <form class="flex items-center gap-2" phx-change="set_place_z">
        <label for="place-z" class="text-[10px] font-semibold uppercase tracking-wide text-base-content/60">
          Place at z
        </label>
        <input
          id="place-z"
          type="number"
          name="z"
          value={@place_z}
          step="1"
          class="input input-xs input-bordered w-20"
        />
      </form>

      <div :if={@mode == "preset"} class="space-y-1">
        <button
          :for={{slug, name} <- Boxland.Levels.preset_entities()}
          id={"preset-#{slug}"}
          phx-click="preset"
          phx-value-preset={slug}
          class={["btn btn-sm w-full justify-start", @preset == slug && "btn-primary"]}
        >
          {name}
        </button>
      </div>

      <div :if={@mode == "tile"} class="space-y-2">
        <p :if={@tilesets == []} class="text-xs text-base-content/60">No tilesets uploaded.</p>

        <div :for={ts <- @tilesets} class="space-y-1">
          <div class="text-[10px] font-semibold uppercase tracking-wide text-base-content/60">
            {ts.name}
          </div>
          <div class="grid grid-cols-6 gap-1">
            <button
              :for={i <- 0..(tileset_tile_count(ts) - 1)}
              id={"palette-tile-#{ts.id}-#{i}"}
              phx-click="pick_tile"
              phx-value-asset_id={ts.id}
              phx-value-index={i}
              class={[
                "h-8 w-8 border border-base-300",
                @selected_tile == %{"asset_id" => ts.id, "tile_index" => i} && "ring-2 ring-primary"
              ]}
              style={tile_swatch_style(ts, i)}
              aria-label={"tile #{i} of #{ts.name}"}
            />
          </div>
        </div>
      </div>

      <div :if={@mode == "sprite"} class="space-y-1">
        <p :if={@sprites == []} class="text-xs text-base-content/60">No sprites uploaded.</p>
        <button
          :for={s <- @sprites}
          id={"palette-sprite-#{s.id}"}
          phx-click="pick_sprite"
          phx-value-asset_id={s.id}
          class={[
            "btn btn-sm w-full justify-start",
            @selected_sprite_id == s.id && "btn-primary"
          ]}
        >
          {s.name}
        </button>
      </div>

      <div :if={@mode == "group"} class="space-y-1">
        <p :if={@groups == []} class="text-xs text-base-content/60">
          No tile groups on this map. Use Mapmaker to create one.
        </p>
        <button
          :for={gid <- @groups}
          id={"palette-group-#{gid}"}
          phx-click="pick_group"
          phx-value-group_id={gid}
          class={[
            "btn btn-sm w-full justify-start font-mono",
            @selected_group_id == gid && "btn-primary"
          ]}
        >
          {String.slice(gid, 0, 10)}…
        </button>
      </div>

      <form
        :if={@mode == "invisible"}
        phx-change="set_invisible_size"
        class="space-y-2 rounded-box bg-base-200 p-2"
      >
        <label class="text-[10px] font-semibold uppercase tracking-wide text-base-content/60">
          Size (cells)
        </label>
        <div class="flex gap-2">
          <input
            id="invisible-w"
            type="number"
            name="w"
            value={@invisible_size["w"]}
            min="1"
            class="input input-xs input-bordered w-16"
          />
          <input
            id="invisible-h"
            type="number"
            name="h"
            value={@invisible_size["h"]}
            min="1"
            class="input input-xs input-bordered w-16"
          />
        </div>
        <p class="text-[10px] text-base-content/60">
          Click a cell to place an invisible {@invisible_size["w"]}×{@invisible_size["h"]} box.
        </p>
      </form>
    </aside>
    """
  end

  attr :layers, :list, required: true
  attr :selected_layer_id, :any, required: true
  attr :renaming_layer_id, :any, required: true
  attr :affected_layer_ids, :any, required: true

  defp layers_panel(assigns) do
    ~H"""
    <aside id="layers-panel" class="space-y-2 rounded-box bg-base-200 p-3">
      <div class="flex items-center justify-between">
        <h2 class="text-sm font-semibold uppercase tracking-wide text-base-content/70">Layers</h2>
        <button
          id="add-layer-button"
          phx-click="add_layer"
          class="btn btn-xs btn-primary"
          title="Add layer"
        >
          <.icon name="hero-plus" class="size-3" /> Add
        </button>
      </div>

      <ul class="space-y-1" role="listbox" aria-label="Layers">
        <li
          :for={{layer, position} <- Enum.with_index(@layers)}
          id={"layer-row-#{layer.id}"}
          role="option"
          aria-selected={to_string(layer.id == @selected_layer_id)}
          phx-click="select_layer"
          phx-value-id={layer.id}
          class={[
            "group flex flex-col gap-1 rounded-md border p-2 transition cursor-pointer",
            layer.id == @selected_layer_id && "border-primary bg-primary/10",
            layer.id != @selected_layer_id && "border-base-300 bg-base-100 hover:bg-base-100/70",
            MapSet.member?(@affected_layer_ids, layer.id) && "ring-2 ring-accent/60"
          ]}
        >
          <div class="flex items-center gap-1">
            <button
              type="button"
              phx-click="toggle_visibility"
              phx-value-id={layer.id}
              class="btn btn-ghost btn-xs px-1"
              title={if layer.visible, do: "Hide layer", else: "Show layer"}
              aria-label={if layer.visible, do: "Hide layer", else: "Show layer"}
            >
              <.icon
                name={if layer.visible, do: "hero-eye", else: "hero-eye-slash"}
                class={["size-4", !layer.visible && "text-base-content/40"]}
              />
            </button>
            <button
              type="button"
              phx-click="toggle_lock"
              phx-value-id={layer.id}
              class="btn btn-ghost btn-xs px-1"
              title={if layer.locked, do: "Unlock layer", else: "Lock layer"}
              aria-label={if layer.locked, do: "Unlock layer", else: "Lock layer"}
            >
              <.icon
                name={if layer.locked, do: "hero-lock-closed", else: "hero-lock-open"}
                class={["size-4", layer.locked && "text-warning"]}
              />
            </button>

            <%= if @renaming_layer_id == layer.id do %>
              <form
                phx-submit="rename_layer"
                phx-click-away="rename_layer_cancel"
                phx-value-id={layer.id}
                class="flex flex-1 items-center gap-1"
              >
                <input
                  type="text"
                  name="name"
                  value={layer.name}
                  autofocus
                  phx-keydown="rename_layer_cancel"
                  phx-key="Escape"
                  class="input input-xs input-bordered flex-1"
                />
              </form>
            <% else %>
              <span
                class={[
                  "flex-1 truncate text-sm",
                  !layer.visible && "text-base-content/40 line-through decoration-base-content/30"
                ]}
                phx-click="rename_layer_start"
                phx-value-id={layer.id}
                title="Rename"
              >
                {layer.name}
              </span>
            <% end %>

            <span class="font-mono text-[10px] text-base-content/50" title="z-index">
              z={layer.z_index}
            </span>
          </div>

          <form
            phx-change="set_opacity"
            phx-value-id={layer.id}
            class="flex items-center gap-1"
          >
            <.icon name="hero-adjustments-horizontal" class="size-3 text-base-content/50" />
            <input
              type="range"
              min="0"
              max="100"
              step="5"
              value={layer.opacity}
              name="opacity"
              phx-debounce="150"
              class="range range-xs flex-1"
              aria-label="Layer opacity"
            />
            <span class="w-8 text-right font-mono text-[10px] text-base-content/50">
              {layer.opacity}%
            </span>
          </form>

          <div class="flex items-center justify-end gap-0.5 opacity-0 group-hover:opacity-100 focus-within:opacity-100">
            <button
              type="button"
              phx-click="move_layer_up"
              phx-value-id={layer.id}
              class="btn btn-ghost btn-xs px-1"
              title="Move up (higher z)"
              disabled={position == 0}
              aria-label="Move layer up"
            >
              <.icon name="hero-chevron-up" class="size-3" />
            </button>
            <button
              type="button"
              phx-click="move_layer_down"
              phx-value-id={layer.id}
              class="btn btn-ghost btn-xs px-1"
              title="Move down (lower z)"
              disabled={position == length(@layers) - 1}
              aria-label="Move layer down"
            >
              <.icon name="hero-chevron-down" class="size-3" />
            </button>
            <button
              type="button"
              phx-click="duplicate_layer"
              phx-value-id={layer.id}
              class="btn btn-ghost btn-xs px-1"
              title="Duplicate"
              aria-label="Duplicate layer"
            >
              <.icon name="hero-document-duplicate" class="size-3" />
            </button>
            <button
              type="button"
              phx-click="delete_layer"
              phx-value-id={layer.id}
              data-confirm={"Delete layer \"#{layer.name}\"?"}
              class="btn btn-ghost btn-xs px-1 text-error"
              title="Delete"
              aria-label="Delete layer"
              disabled={length(@layers) <= 1}
            >
              <.icon name="hero-trash" class="size-3" />
            </button>
          </div>
        </li>
      </ul>
    </aside>
    """
  end

  attr :icon, :string, required: true
  attr :label, :string, required: true
  attr :tool, :string, required: true
  attr :active, :boolean, required: true

  defp tool_button(assigns) do
    ~H"""
    <button
      id={"level-tool-#{@tool}"}
      phx-click="tool"
      phx-value-tool={@tool}
      class={["btn btn-sm", @active && "btn-primary"]}
    >
      <.icon name={@icon} class="size-4" /> {@label}
    </button>
    """
  end

  attr :level, :any
  attr :layers, :list
  attr :tilesets, :list
  attr :selected_entity_id, :any
  attr :highlight_cells, :any, default: nil

  defp canvas(assigns) do
    ~H"""
    <div
      id="level-canvas"
      class="overflow-auto rounded-box bg-base-200 p-4"
    >
      <div
        class="relative grid w-fit gap-px"
        style={"grid-template-columns: repeat(#{@level.map.width}, 32px);"}
      >
        <button
          :for={{x, y} <- cells(@level.map.width, @level.map.height)}
          id={"level-cell-#{x}-#{y}"}
          phx-click="cell"
          phx-value-x={x}
          phx-value-y={y}
          class={[
            "relative h-8 w-8 border border-base-300 bg-base-100",
            cell_highlighted?(@highlight_cells, x, y) && "ring-2 ring-accent z-20"
          ]}
        >
          <span
            :for={layer <- @layers}
            class="pointer-events-none absolute inset-0 bg-no-repeat"
            style={layer_cell_style(@tilesets, layer, x, y)}
          />
        </button>

        <div
          :for={entity <- @level.entities}
          id={"level-entity-#{entity.id}"}
          class={[
            "pointer-events-none absolute z-10 flex items-center justify-center border text-[10px] font-bold",
            entity_color_class(entity),
            entity.id == @selected_entity_id && "ring-2 ring-accent"
          ]}
          style={entity_position_style(entity)}
          aria-label={"entity #{entity.id}"}
        >
          {entity_label(entity)}
        </div>
      </div>
    </div>
    """
  end

  attr :selection, :any, default: nil
  attr :entity, :any

  defp inspector(assigns) do
    ~H"""
    <aside id="level-inspector" class="space-y-3">
      <div :if={is_nil(@selection)} class="rounded-box bg-base-200 p-3 text-xs text-base-content/60">
        Click a tile, group, or entity on the canvas to inspect.
      </div>

      <div
        :if={match?({:group, _}, @selection)}
        id="inspector-group"
        class="rounded-box bg-base-200 p-3 space-y-2 text-xs"
      >
        <h2 class="text-sm font-semibold uppercase tracking-wide text-base-content/70">Group</h2>
        <div class="font-mono text-[11px] text-base-content/70">
          group_id: {elem(@selection, 1)}
        </div>
        <p class="text-base-content/60">
          This group isn't an entity yet. Promote it to assign properties and actions.
        </p>
        <button
          id="promote-selection"
          phx-click="promote_selection"
          class="btn btn-xs btn-primary w-full"
        >
          Promote to entity
        </button>
      </div>

      <div
        :if={match?({:tile, _, _, _}, @selection)}
        id="inspector-tile"
        class="rounded-box bg-base-200 p-3 space-y-2 text-xs"
      >
        <h2 class="text-sm font-semibold uppercase tracking-wide text-base-content/70">Tile</h2>
        <div class="font-mono text-[11px] text-base-content/70">
          ({elem(@selection, 2)}, {elem(@selection, 3)})
        </div>
        <p class="text-base-content/60">
          This tile isn't an entity yet. Promote it to assign properties and actions.
        </p>
        <button
          id="promote-selection"
          phx-click="promote_selection"
          class="btn btn-xs btn-primary w-full"
        >
          Promote to entity
        </button>
      </div>

      <div :if={@entity} class="space-y-3">
        <div class="rounded-box bg-base-200 p-3">
          <div class="mb-2 flex items-center justify-between">
            <h2 class="text-sm font-semibold uppercase tracking-wide text-base-content/70">
              Identity
            </h2>
            <button
              id="entity-delete"
              phx-click="delete_entity"
              phx-value-id={@entity.id}
              class="btn btn-xs btn-error"
            >
              <.icon name="hero-trash" class="size-3" />
            </button>
          </div>
          <form phx-change="inspector_save" class="space-y-2 text-xs">
            <label class="flex flex-col">
              <span class="text-base-content/60">Tag</span>
              <input
                id="entity-tag"
                type="text"
                name="tag"
                value={@entity.tag || ""}
                class="input input-xs input-bordered"
              />
            </label>
            <div class="font-mono text-[10px] text-base-content/60">
              Type: {@entity.entity_type.slug}
            </div>
          </form>
        </div>

        <div class="rounded-box bg-base-200 p-3">
          <h2 class="mb-2 text-sm font-semibold uppercase tracking-wide text-base-content/70">
            Position
          </h2>
          <form phx-change="inspector_save" class="grid grid-cols-3 gap-2 text-xs">
            <label class="flex flex-col">
              <span class="text-base-content/60">x (px)</span>
              <input
                id="entity-pos-x"
                type="number"
                name="pos_x"
                value={@entity.pos_x}
                class="input input-xs input-bordered"
              />
            </label>
            <label class="flex flex-col">
              <span class="text-base-content/60">y (px)</span>
              <input
                id="entity-pos-y"
                type="number"
                name="pos_y"
                value={@entity.pos_y}
                class="input input-xs input-bordered"
              />
            </label>
            <label class="flex flex-col">
              <span class="text-base-content/60">z</span>
              <input
                id="entity-z"
                type="number"
                name="z_index_override"
                value={@entity.z_index_override || ""}
                class="input input-xs input-bordered"
              />
            </label>
          </form>
        </div>

        <div class="rounded-box bg-base-200 p-3">
          <h2 class="mb-2 text-sm font-semibold uppercase tracking-wide text-base-content/70">
            Properties
          </h2>

          <div :if={@entity.entity_type.properties == []} class="text-xs text-base-content/60">
            No declared properties yet.
          </div>

          <form
            :for={prop <- @entity.entity_type.properties}
            phx-change="inspector_property_set"
            class="mb-1 flex items-center gap-2 text-xs"
          >
            <span class="font-mono text-base-content/70 w-24 truncate">{prop["key"]}</span>
            <input type="hidden" name="key" value={prop["key"]} />
            <input
              id={"entity-prop-#{prop["key"]}"}
              name="value"
              value={current_property_value(@entity, prop)}
              class="input input-xs input-bordered flex-1"
            />
            <button
              type="button"
              phx-click="type_remove_property"
              phx-value-key={prop["key"]}
              class="btn btn-xs btn-ghost"
              aria-label={"remove #{prop["key"]}"}
            >
              <.icon name="hero-x-mark" class="size-3" />
            </button>
          </form>

          <form phx-submit="type_add_property" class="mt-2 flex gap-1 text-xs">
            <input
              type="text"
              name="key"
              placeholder="key"
              class="input input-xs input-bordered flex-1"
            />
            <select name="type" class="select select-xs select-bordered">
              <option value="number">number</option>
              <option value="string">string</option>
              <option value="boolean">boolean</option>
            </select>
            <input
              type="text"
              name="default"
              placeholder="default"
              class="input input-xs input-bordered w-20"
            />
            <button id="entity-add-property" type="submit" class="btn btn-xs">+</button>
          </form>
        </div>

        <div class="rounded-box bg-base-200 p-3">
          <div class="mb-2 flex items-center justify-between">
            <h2 class="text-sm font-semibold uppercase tracking-wide text-base-content/70">
              Actions
            </h2>
            <button id="entity-add-action" phx-click="type_add_action" class="btn btn-xs">
              +
            </button>
          </div>

          <div :if={@entity.entity_type.actions == []} class="text-xs text-base-content/60">
            No actions on this entity type yet.
          </div>

          <div
            :for={action <- @entity.entity_type.actions}
            id={"action-#{action["id"]}"}
            class="mb-2 rounded bg-base-100 p-2 text-xs"
          >
            <div class="mb-1 flex items-center justify-between">
              <input
                phx-blur="type_update_action"
                phx-value-action_id={action["id"]}
                phx-value-field="name"
                name="value"
                value={action["name"]}
                class="input input-xs input-bordered flex-1 mr-1"
              />
              <button
                phx-click="type_remove_action"
                phx-value-action_id={action["id"]}
                class="btn btn-xs btn-ghost"
              >
                <.icon name="hero-x-mark" class="size-3" />
              </button>
            </div>

            <div class="grid grid-cols-2 gap-2">
              <div>
                <span class="text-base-content/60">Trigger</span>
                <form
                  phx-change="type_update_action"
                  phx-value-action_id={action["id"]}
                  phx-value-field="trigger.kind"
                >
                  <select name="value" class="select select-xs select-bordered w-full">
                    <option :for={k <- ~w(spawn despawn proximity property)} value={k} selected={action["trigger"]["kind"] == k}>
                      {k}
                    </option>
                  </select>
                </form>
              </div>

              <div>
                <span class="text-base-content/60">Function</span>
                <form
                  phx-change="type_update_action"
                  phx-value-action_id={action["id"]}
                  phx-value-field="function.kind"
                >
                  <select name="value" class="select select-xs select-bordered w-full">
                    <option
                      :for={k <- ~w(spawn_self despawn_self spawn_other despawn_other modify_property)}
                      value={k}
                      selected={action["function"]["kind"] == k}
                    >
                      {k}
                    </option>
                  </select>
                </form>
              </div>
            </div>
          </div>
        </div>
      </div>
    </aside>
    """
  end

  # === Render helpers ===

  defp cells(width, height), do: for(y <- 0..(height - 1), x <- 0..(width - 1), do: {x, y})

  defp cell_highlighted?(nil, _x, _y), do: false

  defp cell_highlighted?(%MapSet{} = set, x, y), do: MapSet.member?(set, {x, y})

  defp cell_highlighted?(_, _, _), do: false

  defp entity_color_class(entity) do
    case entity.entity_type.visual_ref do
      %{"kind" => "invisible"} -> "bg-accent/40 border-accent/70 text-accent-content"
      %{"kind" => "preset"} -> "bg-primary/80 border-primary text-primary-content"
      _ -> "bg-secondary/70 border-secondary text-secondary-content"
    end
  end

  defp entity_position_style(entity) do
    {w, h} = entity_size_cells(entity)
    px = entity.pos_x
    py = entity.pos_y
    "left: #{px}px; top: #{py}px; width: #{w * @cell_px - 2}px; height: #{h * @cell_px - 2}px;"
  end

  defp entity_size_cells(entity) do
    base =
      (entity.instance_overrides || %{})
      |> Map.get("size", entity.entity_type.size || %{"w" => 1, "h" => 1})

    {Map.get(base, "w", 1), Map.get(base, "h", 1)}
  end

  defp entity_label(entity) do
    case entity.entity_type.visual_ref do
      %{"kind" => "preset", "slug" => slug} -> String.upcase(String.first(slug))
      %{"kind" => "invisible"} -> "□"
      _ -> ""
    end
  end

  defp current_property_value(entity, %{"key" => key, "default" => default}) do
    Map.get(entity.properties || %{}, key, default)
  end

  defp tile_swatch_style(asset, index) do
    columns = asset.metadata["columns"] || 1
    x = rem(index, columns) * 32
    y = div(index, columns) * 32

    "background-image: url('#{asset.content_url}');" <>
      " background-position: -#{x}px -#{y}px;" <>
      " background-repeat: no-repeat;"
  end

  defp tileset_tile_count(%{metadata: meta}) do
    Map.get(meta, "tile_count") ||
      ((Map.get(meta, "columns") || 1) * (Map.get(meta, "rows") || 1))
  end

  defp layer_cell_style(tilesets, layer, x, y) do
    case Maps.tile_at(layer.tiles, x, y) do
      nil ->
        "display: none;"

      %{"asset_id" => asset_id, "tile_index" => tile_index, "rotation" => rotation} ->
        asset = Enum.find(tilesets, &(&1.id == asset_id))

        tile_style(asset, tile_index) <>
          " transform: rotate(#{rotation}deg);" <>
          " opacity: #{layer.opacity / 100};"
    end
  end

  defp tile_style(nil, _index), do: ""

  defp tile_style(asset, index) do
    columns = asset.metadata["columns"]
    x = rem(index, columns) * 32
    y = div(index, columns) * 32

    "background-image: url('#{asset.content_url}'); background-position: -#{x}px -#{y}px;"
  end
end
