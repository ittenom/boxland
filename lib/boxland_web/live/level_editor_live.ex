defmodule BoxlandWeb.LevelEditorLive do
  use BoxlandWeb, :live_view

  import BoxlandWeb.LevelRender, only: [entity_sprite_styles: 2, group_members_indexed: 2]
  import BoxlandWeb.Components.Ide

  alias Boxland.{Entities, Levels, Library, Maps, Repo}
  alias Boxland.Entities.EntityType
  alias Boxland.Game.{Eca, Simulation}
  alias Boxland.Levels.LevelEntity

  @cell_px 32
  @tools ~w(select place delete path)

  @default_tick_rate_ms 250
  @min_tick_rate_ms 50
  @max_tick_rate_ms 2000

  def mount(%{"id" => id}, _session, socket) do
    designer = socket.assigns.current_designer
    level = Levels.get_level!(designer.id, id) |> preload_map_layers()
    tilesets = Library.list_tilesets(designer.id)
    sprites = list_sprites(designer.id)

    {:ok,
     socket
     |> assign(:level, level)
     |> assign(:tilesets, tilesets)
     |> assign(:sprites, sprites)
     |> assign(:assets_by_id, Elixir.Map.new(tilesets ++ sprites, &{&1.id, &1}))
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
     |> assign(:show_paths, true)
     |> assign(:show_grid, false)
     |> assign(:publish_error, nil)
     # === IDE shell (collapsible sections + context menu) ===
     |> assign(:closed_sections, MapSet.new())
     |> assign(:context_menu, nil)
     # === Play mode (deterministic simulation) ===
     |> assign(:mode, :edit)
     |> assign(:sim, nil)
     |> assign(:running, false)
     |> assign(:tick_rate_ms, @default_tick_rate_ms)
     |> assign(:min_tick_rate_ms, @min_tick_rate_ms)
     |> assign(:max_tick_rate_ms, @max_tick_rate_ms)
     |> assign(:play_selected_id, nil)
     |> assign(:play_message, nil)}
  end

  defp default_selected_layer_id(level) do
    case visible_layers(level.map) do
      [] -> nil
      [layer | _] -> layer.id
    end
  end

  # === Mode + play-mode events ===

  def handle_event("enter_play", _params, socket) do
    {:noreply,
     socket
     |> assign(:mode, :play)
     |> assign(:sim, build_sim(socket))
     |> assign(:running, false)
     |> assign(:play_selected_id, nil)
     |> assign(:play_message, nil)}
  end

  def handle_event("exit_play", _params, socket) do
    # Stopping discards the transient simulation; the persisted level is
    # untouched (Unreal-style play-in-editor).
    {:noreply,
     socket
     |> assign(:mode, :edit)
     |> assign(:running, false)
     |> assign(:sim, nil)}
  end

  def handle_event("toggle_play", _params, socket) do
    running? = not socket.assigns.running
    if running?, do: Process.send_after(self(), :tick, socket.assigns.tick_rate_ms)
    {:noreply, assign(socket, :running, running?)}
  end

  def handle_event("step", _params, socket) do
    if socket.assigns.running do
      {:noreply, socket}
    else
      {:noreply, assign(socket, :sim, Simulation.advance(socket.assigns.sim))}
    end
  end

  def handle_event("play_reset", _params, socket) do
    {:noreply,
     socket
     |> assign(:sim, Simulation.reset(socket.assigns.sim))
     |> assign(:running, false)
     |> assign(:play_message, "Reset")}
  end

  def handle_event("set_rate", %{"rate" => rate}, socket) do
    n =
      rate
      |> safe_int(@default_tick_rate_ms)
      |> max(@min_tick_rate_ms)
      |> min(@max_tick_rate_ms)

    {:noreply, assign(socket, :tick_rate_ms, n)}
  end

  def handle_event("scrub", %{"tick" => t}, socket) do
    {:noreply,
     socket
     |> assign(:sim, Simulation.at(socket.assigns.sim, safe_int(t, 0)))
     |> assign(:running, false)}
  end

  def handle_event("play_move", %{"dx" => dx, "dy" => dy}, socket) do
    move = {safe_int(dx, 0), safe_int(dy, 0)}
    sim = Simulation.queue_move(socket.assigns.sim, move)
    # When paused, advance one tick so the queued move is immediately visible
    # (still deterministic — the move is in the input log).
    sim = if socket.assigns.running, do: sim, else: Simulation.advance(sim)
    {:noreply, assign(socket, :sim, sim)}
  end

  def handle_event("play_key", %{"key" => key}, socket) do
    case key do
      "ArrowUp" -> handle_event("play_move", %{"dx" => "0", "dy" => "-1"}, socket)
      "ArrowDown" -> handle_event("play_move", %{"dx" => "0", "dy" => "1"}, socket)
      "ArrowLeft" -> handle_event("play_move", %{"dx" => "-1", "dy" => "0"}, socket)
      "ArrowRight" -> handle_event("play_move", %{"dx" => "1", "dy" => "0"}, socket)
      " " -> handle_event("toggle_play", %{}, socket)
      "." -> handle_event("step", %{}, socket)
      _ -> {:noreply, socket}
    end
  end

  def handle_event("play_select_entity", %{"id" => id}, socket) do
    {:noreply, assign(socket, :play_selected_id, parse_world_id(id))}
  end

  def handle_event("play_clear_selection", _params, socket) do
    {:noreply, assign(socket, :play_selected_id, nil)}
  end

  def handle_event("toggle_show_grid", _params, socket) do
    {:noreply, assign(socket, :show_grid, not socket.assigns.show_grid)}
  end

  # === IDE shell: tree selection, sections, context menu, drag ===

  def handle_event("select_layer", %{"id" => id}, socket) do
    lid = String.to_integer(id)
    layer = Enum.find(socket.assigns.level.map.layers, &(&1.id == lid))

    socket =
      socket
      |> assign(:selected_layer_id, lid)
      |> assign(:selection, {:layer, lid})
      |> then(fn s -> if layer, do: assign(s, :place_z, layer.z_index), else: s end)

    {:noreply, socket}
  end

  def handle_event("select_group", %{"id" => gid}, socket) do
    {:noreply, assign(socket, :selection, {:group, gid})}
  end

  def handle_event("toggle_section", %{"id" => id}, socket) do
    closed = socket.assigns.closed_sections

    closed =
      if MapSet.member?(closed, id), do: MapSet.delete(closed, id), else: MapSet.put(closed, id)

    {:noreply, assign(socket, :closed_sections, closed)}
  end

  def handle_event("open_context_menu", %{"kind" => kind, "id" => id, "x" => x, "y" => y}, socket) do
    # Right-click focuses the object, then the menu acts on the selection.
    socket = focus_for_context(socket, kind, id)

    {:noreply,
     assign(socket, :context_menu, %{kind: kind, id: id, x: trunc_num(x), y: trunc_num(y)})}
  end

  def handle_event("close_context_menu", _params, socket) do
    {:noreply, assign(socket, :context_menu, nil)}
  end

  def handle_event(
        "tree_reorder",
        %{"group" => "layers", "id" => id, "before_id" => before_id},
        socket
      ) do
    ids = reordered_layer_ids(socket.assigns.level.map, id, before_id)
    {:ok, _} = Maps.reorder_layers(socket.assigns.level.map.id, ids)
    {:noreply, refresh_level(socket)}
  end

  def handle_event(
        "tree_reorder",
        %{"group" => "waypoints", "id" => id, "before_id" => before_id},
        socket
      ) do
    case selected_entity(socket) do
      nil ->
        {:noreply, socket}

      entity ->
        wps = reorder_waypoints(entity.waypoints || [], id, before_id)
        {:ok, _} = Levels.update_entity(entity, %{"waypoints" => wps})
        {:noreply, refresh_level(socket)}
    end
  end

  def handle_event("tree_reorder", _params, socket), do: {:noreply, socket}

  def handle_event("waypoint_move", %{"index" => index, "x" => x, "y" => y}, socket) do
    case selected_entity(socket) do
      nil ->
        {:noreply, socket}

      entity ->
        idx = trunc_num(index)
        wps = entity.waypoints || []

        case Enum.at(wps, idx) do
          nil ->
            {:noreply, socket}

          wp ->
            new_wp = wp |> Elixir.Map.put("x", trunc_num(x)) |> Elixir.Map.put("y", trunc_num(y))

            {:ok, _} =
              Levels.update_entity(entity, %{"waypoints" => List.replace_at(wps, idx, new_wp)})

            {:noreply, refresh_level(socket)}
        end
    end
  end

  # (waypoint_remove is handled by the existing clause in the Waypoint events section)

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
      "path" -> handle_cell_path(socket, cell_x, cell_y)
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
    case selected_entity(socket) do
      nil ->
        {:noreply, socket}

      entity ->
        case Levels.update_entity(entity, Map.take(params, ["tag"])) do
          {:ok, _} -> {:noreply, refresh_level(socket)}
          {:error, _} -> {:noreply, put_flash(socket, :error, "Could not save entity.")}
        end
    end
  end

  def handle_event("inspector_position", params, socket) do
    case selected_entity(socket) do
      nil ->
        {:noreply, socket}

      entity ->
        designer = socket.assigns.current_designer
        level = socket.assigns.level

        cur_x = div(entity.pos_x, 32)
        cur_y = div(entity.pos_y, 32)
        cur_z = entity.z_index_override || entity.entity_type.default_z_index

        dx = safe_int(params["cell_x"], cur_x) - cur_x
        dy = safe_int(params["cell_y"], cur_y) - cur_y
        dz = safe_int(params["z_index_override"], cur_z) - cur_z

        if dx == 0 and dy == 0 and dz == 0 do
          {:noreply, socket}
        else
          case Levels.move_entity(designer.id, level.id, entity.id, dx, dy, dz) do
            {:ok, _} ->
              {:noreply, refresh_level(socket)}

            {:error, :no_target_layer} ->
              {:noreply, put_flash(socket, :error, "No layer at the target z.")}

            {:error, _} ->
              {:noreply, put_flash(socket, :error, "Could not move entity.")}
          end
        end
    end
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

  # === Waypoint events ===

  def handle_event("waypoint_add", _params, socket) do
    case selected_entity(socket) do
      nil ->
        {:noreply, socket}

      entity ->
        cx = div(entity.pos_x, 32)
        cy = div(entity.pos_y, 32)
        wps = (entity.waypoints || []) ++ [%{"x" => cx, "y" => cy}]
        {:ok, _} = Levels.update_entity(entity, %{"waypoints" => wps})
        {:noreply, refresh_level(socket)}
    end
  end

  def handle_event(
        "waypoint_set",
        %{"index" => index, "field" => field, "value" => value},
        socket
      )
      when field in ["x", "y"] do
    case selected_entity(socket) do
      nil ->
        {:noreply, socket}

      entity ->
        idx = String.to_integer(index)
        wps = entity.waypoints || []

        case Enum.at(wps, idx) do
          nil ->
            {:noreply, socket}

          wp ->
            new_wp = Map.put(wp, field, safe_int(value, 0))
            new_wps = List.replace_at(wps, idx, new_wp)
            {:ok, _} = Levels.update_entity(entity, %{"waypoints" => new_wps})
            {:noreply, refresh_level(socket)}
        end
    end
  end

  def handle_event("toggle_show_paths", _params, socket) do
    {:noreply, assign(socket, :show_paths, not socket.assigns.show_paths)}
  end

  def handle_event("movement_set", params, socket) do
    case selected_entity(socket) do
      nil ->
        {:noreply, socket}

      entity ->
        current = entity.movement || %{}

        next =
          current
          |> stash_movement_field(params, "mode")
          |> stash_movement_field(params, "ticks_per_step")
          |> stash_movement_field(params, "wait_at_waypoint")

        {:ok, _} = Levels.update_entity(entity, %{"movement" => next})
        {:noreply, refresh_level(socket)}
    end
  end

  def handle_event("waypoint_clear_all", _params, socket) do
    case selected_entity(socket) do
      nil ->
        {:noreply, socket}

      entity ->
        {:ok, _} = Levels.update_entity(entity, %{"waypoints" => []})
        {:noreply, refresh_level(socket)}
    end
  end

  def handle_event("waypoint_remove_at_cell", %{"x" => x, "y" => y}, socket) do
    case selected_entity(socket) do
      nil ->
        {:noreply, socket}

      entity ->
        cx = String.to_integer(x)
        cy = String.to_integer(y)
        wps = entity.waypoints || []

        new_wps =
          Enum.reject(wps, fn wp ->
            Map.get(wp, "x") == cx and Map.get(wp, "y") == cy
          end)

        if new_wps == wps do
          {:noreply, socket}
        else
          {:ok, _} = Levels.update_entity(entity, %{"waypoints" => new_wps})
          {:noreply, refresh_level(socket)}
        end
    end
  end

  def handle_event("waypoint_remove", %{"index" => index}, socket) do
    case selected_entity(socket) do
      nil ->
        {:noreply, socket}

      entity ->
        # `index` may be a string (inspector button) or a number (drag hook).
        idx = trunc_num(index)
        wps = entity.waypoints || []
        new_wps = List.delete_at(wps, idx)
        {:ok, _} = Levels.update_entity(entity, %{"waypoints" => new_wps})
        {:noreply, refresh_level(socket)}
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

  # === Simulation loop + helpers ===

  def handle_info(:tick, socket) do
    if socket.assigns.mode == :play and socket.assigns.running do
      Process.send_after(self(), :tick, socket.assigns.tick_rate_ms)
      {:noreply, assign(socket, :sim, Simulation.advance(socket.assigns.sim))}
    else
      {:noreply, socket}
    end
  end

  defp build_sim(socket) do
    level = socket.assigns.level
    {player, player_z} = spawn_state(level)
    blocked_by_z = Levels.blocked_cells_by_z(level, socket.assigns.tilesets)

    level.entities
    |> Eca.init_world(player,
      bounds: {level.map.width, level.map.height},
      blocked_by_z: blocked_by_z,
      player_z: player_z,
      seed: :erlang.phash2(level.id)
    )
    |> Simulation.new()
  end

  defp spawn_state(level) do
    case Enum.find(level.entities, &spawn_preset?/1) do
      nil ->
        {{0, 0}, 0}

      spawn ->
        {{div(spawn.pos_x, @cell_px), div(spawn.pos_y, @cell_px)},
         Levels.entity_effective_z(spawn)}
    end
  end

  defp spawn_preset?(entity),
    do: match?(%{"kind" => "preset", "slug" => "spawn"}, entity.entity_type.visual_ref)

  defp parse_world_id(id) when is_integer(id), do: id

  defp parse_world_id(id) when is_binary(id) do
    case Integer.parse(id) do
      {n, ""} -> n
      _ -> id
    end
  end

  defp parse_world_id(other), do: other

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

  # === IDE tree/context/reorder helpers ===

  defp trunc_num(n) when is_integer(n), do: n
  defp trunc_num(n) when is_float(n), do: trunc(n)
  defp trunc_num(n) when is_binary(n), do: safe_int(n, 0)
  defp trunc_num(_), do: 0

  # Right-clicking an object focuses it so context-menu items act on the selection.
  defp focus_for_context(socket, "layer", id) do
    lid = safe_int(id, 0)
    socket |> assign(:selected_layer_id, lid) |> assign(:selection, {:layer, lid})
  end

  defp focus_for_context(socket, "entity", id),
    do: assign(socket, :selection, {:entity, safe_int(id, 0)})

  defp focus_for_context(socket, "group", gid), do: assign(socket, :selection, {:group, gid})
  defp focus_for_context(socket, _kind, _id), do: socket

  # New display order (top→bottom) after dropping layer `id` before `before_id`
  # (nil = move to the end/bottom). Returns ids in display order for reorder_layers.
  defp reordered_layer_ids(map, id, before_id) do
    id = safe_int(id, 0)
    before = before_id && safe_int(before_id, nil)

    ordered = display_layers(map) |> Enum.map(& &1.id) |> Enum.reject(&(&1 == id))

    case before && Enum.find_index(ordered, &(&1 == before)) do
      nil -> ordered ++ [id]
      idx -> List.insert_at(ordered, idx, id)
    end
  end

  # Reorder a waypoint list. `id`/`before_id` are stringified indices from TreeDnD.
  defp reorder_waypoints(waypoints, id, before_id) do
    from = safe_int(id, 0)

    case Enum.at(waypoints, from) do
      nil ->
        waypoints

      wp ->
        rest = List.delete_at(waypoints, from)
        before = before_id && safe_int(before_id, nil)
        # before_id indexes into the ORIGINAL list; map it onto `rest`.
        insert_at =
          cond do
            is_nil(before) -> length(rest)
            before > from -> before - 1
            true -> before
          end

        List.insert_at(rest, insert_at, wp)
    end
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
      entity = topmost_entity_at(level, x, y) -> {:entity, entity.id}
      gid = group_id_at(level.map, x, y) -> {:group, gid}
      tile = tile_at_on_visible(level.map, x, y) -> {:tile, elem(tile, 0).id, x, y}
      true -> nil
    end
  end

  # In Path mode, clicks edit the selected entity's waypoint list:
  #   - on the entity's own anchor cell → no-op (entity already starts there)
  #   - on an existing waypoint cell    → remove that waypoint
  #   - otherwise                       → append a new waypoint at the cell
  # If nothing is selected, clicking an entity selects it instead.
  defp handle_cell_path(socket, x, y) do
    level = socket.assigns.level

    case selected_entity(socket) do
      nil ->
        case topmost_entity_at(level, x, y) do
          nil ->
            {:noreply,
             put_flash(socket, :info, "Select an entity first, then click cells to lay its path.")}

          entity ->
            {:noreply, assign(socket, :selection, {:entity, entity.id})}
        end

      entity ->
        anchor_x = div(entity.pos_x, @cell_px)
        anchor_y = div(entity.pos_y, @cell_px)
        wps = entity.waypoints || []

        cond do
          x == anchor_x and y == anchor_y ->
            {:noreply, socket}

          Enum.any?(wps, &(Map.get(&1, "x") == x and Map.get(&1, "y") == y)) ->
            new_wps =
              Enum.reject(wps, &(Map.get(&1, "x") == x and Map.get(&1, "y") == y))

            {:ok, _} = Levels.update_entity(entity, %{"waypoints" => new_wps})
            {:noreply, refresh_level(socket)}

          true ->
            new_wps = wps ++ [%{"x" => x, "y" => y}]
            updates = %{"waypoints" => new_wps} |> maybe_default_movement(entity)
            {:ok, _} = Levels.update_entity(entity, updates)
            {:noreply, refresh_level(socket)}
        end
    end
  end

  defp maybe_default_movement(updates, entity) do
    case entity.movement || %{} do
      m when map_size(m) == 0 ->
        Map.put(updates, "movement", %{
          "mode" => "loop",
          "ticks_per_step" => 1,
          "wait_at_waypoint" => 0
        })

      _ ->
        updates
    end
  end

  defp stash_movement_field(current, params, key) do
    case Map.get(params, key) do
      nil ->
        current

      value ->
        case key do
          "mode" ->
            if value in LevelEntity.movement_modes(),
              do: Map.put(current, "mode", value),
              else: current

          _ ->
            Map.put(current, key, safe_int(value, Map.get(current, key, 0)))
        end
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
    |> Enum.sort_by(&(-entity_z(&1)))
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

  def render(%{mode: :play} = assigns), do: render_play(assigns)

  def render(assigns) do
    layers = visible_layers(assigns.level.map)
    selection_entity = selected_entity_for_render(assigns)
    selection_highlight = selection_cells(assigns.selection, assigns.level)
    affected_layers = affected_layer_ids(assigns.selection, assigns.level)
    sprite_styles = entity_sprite_styles(assigns.level, assigns.assets_by_id)
    entity_cell_index = build_entity_cell_index(assigns.level, sprite_styles)
    waypoint_markers = build_waypoint_markers(selection_entity)

    {path_cells, path_directions, unreachable_leg} =
      if assigns.show_paths,
        do: compute_path_overlay(selection_entity, assigns.level, assigns.tilesets),
        else: {MapSet.new(), %{}, nil}

    promoted_gids = for(e <- assigns.level.entities, e.group_id, do: e.group_id) |> MapSet.new()
    unpromoted_groups = Enum.reject(assigns.groups, &MapSet.member?(promoted_gids, &1))
    selected_layer = selected_layer_struct(assigns)

    assigns =
      assigns
      |> assign(:layers, layers)
      |> assign(:selected, selection_entity)
      |> assign(:selected_layer, selected_layer)
      |> assign(:unpromoted_groups, unpromoted_groups)
      |> assign(:selection_highlight, selection_highlight)
      |> assign(:affected_layer_ids, affected_layers)
      |> assign(:entity_cell_index, entity_cell_index)
      |> assign(:waypoint_markers, waypoint_markers)
      |> assign(:path_cells, path_cells)
      |> assign(:path_directions, path_directions)
      |> assign(:unreachable_leg, unreachable_leg)

    ~H"""
    <div id="level-editor-root" phx-hook="ContextMenu">
      <.ide_shell flash={@flash}>
        <:activity>
          <.ide_rail_nav active={:levels} />
          <div class="flex-1"></div>
          <.rail_item icon="hero-play" label="Play (Space)" phx-click="enter_play" />
        </:activity>

        <:explorer>
          <.explorer_tree
            layers={display_layers(@level.map)}
            entities={@level.entities}
            groups={@unpromoted_groups}
            selection={@selection}
            selected_layer_id={@selected_layer_id}
            affected_layer_ids={@affected_layer_ids}
            closed_sections={@closed_sections}
            palette_mode={@palette_mode}
            preset={@preset}
            tilesets={@tilesets}
            sprites={@sprites}
            palette_groups={@groups}
            selected_tile={@selected_tile}
            selected_sprite_id={@selected_sprite_id}
            selected_group_id={@selected_group_id}
            invisible_size={@invisible_size}
            place_z={@place_z}
          />
        </:explorer>

        <:viewport>
          <.ide_toolbar id="level-toolbar">
            <h1 class="mr-2 text-sm font-semibold text-base-content">{@level.name}</h1>
            <.ide_tool_button
              id="level-tool-select"
              icon="hero-cursor-arrow-rays"
              label="Select"
              active={@tool == "select"}
              phx-click="tool"
              phx-value-tool="select"
              title="Select (V)"
            />
            <.ide_tool_button
              id="level-tool-place"
              icon="hero-pencil"
              label="Place"
              active={@tool == "place"}
              phx-click="tool"
              phx-value-tool="place"
              title="Place (P)"
            />
            <.ide_tool_button
              id="level-tool-delete"
              icon="hero-x-mark"
              label="Delete"
              active={@tool == "delete"}
              phx-click="tool"
              phx-value-tool="delete"
              title="Delete (X)"
            />
            <.ide_tool_button
              id="level-tool-path"
              icon="hero-map-pin"
              label="Path"
              active={@tool == "path"}
              phx-click="tool"
              phx-value-tool="path"
              title="Path"
            />
            <span class="mx-1 h-5 w-px bg-base-content/15"></span>
            <.ide_tool_button
              icon="hero-arrows-pointing-out"
              label="Paths"
              active={@show_paths}
              phx-click="toggle_show_paths"
              title="Toggle path overlay"
            />
            <.ide_tool_button
              icon="hero-squares-2x2"
              label="Grid"
              active={@show_grid}
              phx-click="toggle_show_grid"
              title="Toggle gridlines"
            />
            <div class="flex-1"></div>
            <.link navigate={~p"/play/#{@level.id}"} class="ide-toolbtn">Live</.link>
            <button
              id="publish-level-button"
              phx-click="publish"
              class="ide-toolbtn ide-toolbtn-active"
            >
              <.icon name="hero-rocket-launch" class="size-4" /> Publish
            </button>
          </.ide_toolbar>

          <div :if={@publish_error} class="alert alert-error m-3">{@publish_error}</div>

          <.canvas
            level={@level}
            layers={@layers}
            tilesets={@tilesets}
            tool={@tool}
            show_grid={@show_grid}
            selected_entity_id={selected_entity_id_for_canvas(@selection)}
            highlight_cells={@selection_highlight}
            entity_cell_index={@entity_cell_index}
            waypoint_markers={@waypoint_markers}
            path_cells={@path_cells}
            path_directions={@path_directions}
            unreachable_leg={@unreachable_leg}
          />
        </:viewport>

        <:inspector>
          <%= case @selection do %>
            <% {:layer, _} -> %>
              <.layer_inspector layer={@selected_layer} />
            <% _ -> %>
              <.inspector selection={@selection} entity={@selected} tool={@tool} />
          <% end %>
        </:inspector>

        <:status>
          <span class="font-mono">tool: {@tool}</span>
          <span class="font-mono">z: {@place_z}</span>
          <span class="flex-1"></span>
          <span class="text-base-content/50">
            Right-click for actions · drag waypoints on the canvas
          </span>
        </:status>
      </.ide_shell>

      <.context_menu open={@context_menu != nil} x={ctx(@context_menu, :x)} y={ctx(@context_menu, :y)}>
        <%= case ctx(@context_menu, :kind) do %>
          <% "layer" -> %>
            <.context_item
              icon="hero-pencil"
              phx-click="rename_layer_start"
              phx-value-id={ctx(@context_menu, :id)}
            >
              Rename
            </.context_item>
            <.context_item
              icon="hero-document-duplicate"
              phx-click="duplicate_layer"
              phx-value-id={ctx(@context_menu, :id)}
            >
              Duplicate
            </.context_item>
            <.context_item
              icon="hero-eye"
              phx-click="toggle_visibility"
              phx-value-id={ctx(@context_menu, :id)}
            >
              Toggle visibility
            </.context_item>
            <.context_item
              icon="hero-trash"
              danger
              phx-click="delete_layer"
              phx-value-id={ctx(@context_menu, :id)}
              data-confirm="Delete this layer?"
            >
              Delete
            </.context_item>
          <% "entity" -> %>
            <.context_item
              icon="hero-trash"
              danger
              phx-click="delete_entity"
              phx-value-id={ctx(@context_menu, :id)}
            >
              Delete entity
            </.context_item>
          <% "group" -> %>
            <.context_item icon="hero-arrow-up-circle" phx-click="promote_selection">
              Promote to entity
            </.context_item>
          <% _ -> %>
        <% end %>
      </.context_menu>
    </div>
    """
  end

  defp ctx(nil, _key), do: nil
  defp ctx(menu, key), do: Elixir.Map.get(menu, key)

  defp selected_layer_struct(%{selection: {:layer, id}, level: level}),
    do: Enum.find(level.map.layers, &(&1.id == id))

  defp selected_layer_struct(_), do: nil

  defp section_open?(closed_sections, key), do: not MapSet.member?(closed_sections, key)

  defp object_label(entity) do
    entity.tag || entity.entity_type.name || entity.entity_type.slug
  end

  defp object_icon(entity) do
    case entity.entity_type.visual_ref do
      %{"kind" => "preset"} -> "hero-bolt"
      %{"kind" => "invisible"} -> "hero-cube-transparent"
      %{"kind" => "group"} -> "hero-rectangle-group"
      %{"kind" => "sprite"} -> "hero-user"
      _ -> "hero-square-2-stack"
    end
  end

  # === IDE explorer + inspector components ===

  attr :layers, :list, required: true
  attr :entities, :list, required: true
  attr :groups, :list, required: true
  attr :selection, :any, required: true
  attr :selected_layer_id, :any, required: true
  attr :affected_layer_ids, :any, required: true
  attr :closed_sections, :any, required: true
  attr :palette_mode, :string, required: true
  attr :preset, :string, required: true
  attr :tilesets, :list, required: true
  attr :sprites, :list, required: true
  attr :palette_groups, :list, required: true
  attr :selected_tile, :any, required: true
  attr :selected_sprite_id, :any, required: true
  attr :selected_group_id, :any, required: true
  attr :invisible_size, :map, required: true
  attr :place_z, :integer, required: true

  defp explorer_tree(assigns) do
    ~H"""
    <.panel title="Explorer">
      <:actions>
        <button id="add-layer-button" phx-click="add_layer" class="ide-toolbtn !p-1" title="Add layer">
          <.icon name="hero-plus" class="size-3.5" />
        </button>
      </:actions>

      <.panel_section
        title="Layers"
        open={section_open?(@closed_sections, "layers")}
        phx-click="toggle_section"
        phx-value-id="layers"
      >
        <.tree id="layers-tree" phx-hook="TreeDnD" data-tree-group="layers">
          <.tree_node
            :for={layer <- @layers}
            id={"layer-row-#{layer.id}"}
            label={layer.name}
            icon="hero-square-3-stack-3d"
            draggable
            dnd_id={layer.id}
            context_kind="layer"
            context_id={layer.id}
            selected={layer.id == @selected_layer_id}
            affected={MapSet.member?(@affected_layer_ids, layer.id)}
            phx-click="select_layer"
            phx-value-id={layer.id}
          >
            <:trailing>
              <button
                phx-click="toggle_visibility"
                phx-value-id={layer.id}
                class="ide-toolbtn !p-0.5"
                title="Toggle visibility"
              >
                <.icon
                  name={if layer.visible, do: "hero-eye", else: "hero-eye-slash"}
                  class="size-3.5"
                />
              </button>
              <button
                phx-click="toggle_lock"
                phx-value-id={layer.id}
                class="ide-toolbtn !p-0.5"
                title="Toggle lock"
              >
                <.icon
                  name={if layer.locked, do: "hero-lock-closed", else: "hero-lock-open"}
                  class="size-3.5"
                />
              </button>
            </:trailing>
          </.tree_node>
        </.tree>
      </.panel_section>

      <.panel_section
        title="Objects"
        open={section_open?(@closed_sections, "objects")}
        phx-click="toggle_section"
        phx-value-id="objects"
      >
        <p :if={@entities == []} class="px-2 py-1 text-xs text-base-content/40">
          No entities placed.
        </p>
        <.tree id="objects-tree">
          <.tree_node
            :for={e <- @entities}
            id={"object-row-#{e.id}"}
            label={object_label(e)}
            icon={object_icon(e)}
            context_kind="entity"
            context_id={e.id}
            selected={@selection == {:entity, e.id}}
            phx-click="select_entity"
            phx-value-id={e.id}
          />
        </.tree>
      </.panel_section>

      <.panel_section
        :if={@groups != []}
        title="Groups"
        open={section_open?(@closed_sections, "groups")}
        phx-click="toggle_section"
        phx-value-id="groups"
      >
        <.tree id="groups-tree">
          <.tree_node
            :for={gid <- @groups}
            id={"group-row-#{gid}"}
            label={String.slice(gid, 0, 12)}
            icon="hero-rectangle-group"
            context_kind="group"
            context_id={gid}
            selected={@selection == {:group, gid}}
            phx-click="select_group"
            phx-value-id={gid}
          />
        </.tree>
      </.panel_section>

      <.panel_section
        title="Palette"
        open={section_open?(@closed_sections, "palette")}
        phx-click="toggle_section"
        phx-value-id="palette"
      >
        <.palette
          mode={@palette_mode}
          preset={@preset}
          tilesets={@tilesets}
          sprites={@sprites}
          groups={@palette_groups}
          selected_tile={@selected_tile}
          selected_sprite_id={@selected_sprite_id}
          selected_group_id={@selected_group_id}
          invisible_size={@invisible_size}
          place_z={@place_z}
        />
      </.panel_section>
    </.panel>
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

  defp selection_cells({:layer, _id}, _level), do: MapSet.new()

  defp affected_layer_ids({:layer, id}, _level), do: MapSet.new([id])

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

  # Build %{{x, y} => [%{entity:, anchor?:}]} mapping every cell touched by
  # any entity. Anchor cell carries the entity's label.
  # Returns %{{x, y} => [%{index: int, entity_id: id}, ...]} for the
  # currently-selected entity's waypoints. Empty when no entity is selected.
  defp build_waypoint_markers(nil), do: %{}

  defp build_waypoint_markers(entity) do
    (entity.waypoints || [])
    |> Enum.with_index(1)
    |> Enum.reduce(%{}, fn {wp, idx}, acc ->
      cell = {Elixir.Map.get(wp, "x", 0), Elixir.Map.get(wp, "y", 0)}

      Elixir.Map.update(acc, cell, [%{index: idx, entity_id: entity.id}], fn list ->
        [%{index: idx, entity_id: entity.id} | list]
      end)
    end)
  end

  # Returns {MapSet of cells in the entity's projected A* route,
  #          %{cell => :north|:south|:east|:west} for direction arrows,
  #          unreachable_leg_or_nil}.
  defp compute_path_overlay(nil, _level, _assets), do: {MapSet.new(), %{}, nil}

  defp compute_path_overlay(entity, level, assets) do
    waypoints = entity.waypoints || []

    if waypoints == [] do
      {MapSet.new(), %{}, nil}
    else
      start = {div(entity.pos_x, @cell_px), div(entity.pos_y, @cell_px)}
      z = Levels.entity_effective_z(entity)
      blocked = Levels.blocked_at(Levels.blocked_cells_by_z(level, assets), z)

      blocked? = fn cell ->
        cell != start and MapSet.member?(blocked, cell)
      end

      opts = [bounds: {level.map.width, level.map.height}, blocked?: blocked?]

      case Boxland.Pathfinding.preview_path(start, waypoints, opts) do
        :empty -> {MapSet.new(), %{}, nil}
        {:ok, cells} -> {MapSet.new(cells), path_direction_map(cells), nil}
        {:partial, cells, leg} -> {MapSet.new(cells), path_direction_map(cells), leg}
      end
    end
  end

  # Build %{cell => direction} for each cell in the projected route by
  # looking at the next cell. The last cell has no direction (it's the
  # final waypoint or a dead-end).
  defp path_direction_map(cells) do
    cells
    |> Enum.chunk_every(2, 1, :discard)
    |> Enum.into(%{}, fn [{x1, y1}, {x2, y2}] ->
      dir =
        cond do
          x2 > x1 -> :east
          x2 < x1 -> :west
          y2 > y1 -> :south
          y2 < y1 -> :north
          true -> :east
        end

      {{x1, y1}, dir}
    end)
  end

  # %{{x, y} => [%{entity:, anchor?:, sprite_style:}]}. `sprite_style` is the
  # entity's real sprite CSS for that cell offset (nil → fall back to the
  # colored marker letter, e.g. invisible/preset entities).
  defp build_entity_cell_index(level, sprite_styles) do
    Enum.reduce(level.entities, %{}, fn entity, acc ->
      {ax, ay} = anchor = {div(entity.pos_x, @cell_px), div(entity.pos_y, @cell_px)}
      offset_styles = Elixir.Map.get(sprite_styles, entity.id, %{})

      entity
      |> entity_occupied_cells(level)
      |> Enum.reduce(acc, fn {cx, cy} = cell, inner ->
        entry = %{
          entity: entity,
          anchor?: cell == anchor,
          sprite_style: Elixir.Map.get(offset_styles, {cx - ax, cy - ay})
        }

        Elixir.Map.update(inner, cell, [entry], &[entry | &1])
      end)
    end)
  end

  defp entity_occupied_cells(%LevelEntity{group_id: gid} = entity, level) when is_binary(gid) do
    cells =
      for layer <- level.map.layers,
          {k, tile} <- layer.tiles,
          tile["group_id"] == gid do
        Maps.parse_key(k)
      end

    case cells do
      [] -> single_cell_footprint(entity)
      _ -> cells
    end
  end

  defp entity_occupied_cells(entity, _level), do: single_cell_footprint(entity)

  defp single_cell_footprint(entity) do
    ax = div(entity.pos_x, @cell_px)
    ay = div(entity.pos_y, @cell_px)
    {w, h} = entity_footprint(entity)

    for dy <- 0..(h - 1), dx <- 0..(w - 1), do: {ax + dx, ay + dy}
  end

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
        <label
          for="place-z"
          class="text-[10px] font-semibold uppercase tracking-wide text-base-content/60"
        >
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

  attr :level, :any
  attr :layers, :list
  attr :tilesets, :list
  attr :tool, :string, default: "select"
  attr :show_grid, :boolean, default: false
  attr :selected_entity_id, :any
  attr :highlight_cells, :any, default: nil
  attr :entity_cell_index, :map, default: %{}
  attr :waypoint_markers, :map, default: %{}
  attr :path_cells, :any, default: nil
  attr :path_directions, :map, default: %{}
  attr :unreachable_leg, :any, default: nil

  defp canvas(assigns) do
    ~H"""
    <div id="level-canvas-wrap" class="space-y-2">
      <div
        :if={@tool == "path"}
        id="path-tool-banner"
        class="rounded-md border border-info/40 bg-info/10 px-3 py-1.5 text-xs text-info"
      >
        <strong>Path tool —</strong>
        with an entity selected, click cells to add waypoints; click a numbered waypoint to remove it.
      </div>

      <div
        id="level-canvas"
        class="overflow-auto rounded-box bg-base-200 p-4"
      >
        <div
          id="level-canvas-grid"
          phx-hook="WaypointDrag"
          class={[
            "relative grid w-fit",
            @show_grid && "gap-px bg-base-300",
            @tool == "path" && "cursor-crosshair",
            @tool == "delete" && "cursor-not-allowed",
            @tool == "place" && "cursor-pointer"
          ]}
          style={"grid-template-columns: repeat(#{@level.map.width}, 32px);"}
        >
          <button
            :for={{x, y} <- cells(@level.map.width, @level.map.height)}
            id={"level-cell-#{x}-#{y}"}
            phx-click="cell"
            phx-value-x={x}
            phx-value-y={y}
            data-cell-x={x}
            data-cell-y={y}
            class={[
              "relative h-8 w-8 bg-base-100",
              @show_grid && "border border-base-300",
              cell_highlighted?(@highlight_cells, x, y) && "ring-2 ring-accent z-20"
            ]}
          >
            <span
              :for={layer <- @layers}
              class="pointer-events-none absolute inset-0 bg-no-repeat"
              style={layer_cell_style(@tilesets, layer, x, y)}
            />

            <span
              :if={path_cell?(@path_cells, x, y)}
              id={"path-cell-#{x}-#{y}"}
              class="pointer-events-none absolute inset-0 z-10 flex items-center justify-center text-info/90"
              aria-hidden="true"
            >
              <%= case Elixir.Map.get(@path_directions, {x, y}) do %>
                <% :north -> %>
                  <span class="text-[14px] leading-none">↑</span>
                <% :south -> %>
                  <span class="text-[14px] leading-none">↓</span>
                <% :east -> %>
                  <span class="text-[14px] leading-none">→</span>
                <% :west -> %>
                  <span class="text-[14px] leading-none">←</span>
                <% _ -> %>
                  <span class="block h-2 w-2 rounded-full bg-info/80 shadow-[0_0_4px_rgba(59,130,246,0.6)]" />
              <% end %>
            </span>

            <span
              :for={covering <- Elixir.Map.get(@entity_cell_index, {x, y}, [])}
              id={"level-entity-#{covering.entity.id}-cell-#{x}-#{y}"}
              class={[
                "pointer-events-none absolute inset-0 flex items-center justify-center text-[10px] font-bold",
                covering.sprite_style && "bg-no-repeat",
                !covering.sprite_style && "opacity-40",
                !covering.sprite_style && entity_cell_color_class(covering.entity),
                covering.entity.id == @selected_entity_id && "entity-pulse"
              ]}
              style={covering.sprite_style}
              aria-label={"entity #{covering.entity.id}"}
            >
              <span :if={covering.anchor? and is_nil(covering.sprite_style)}>
                {entity_label(covering.entity)}
              </span>
            </span>

            <span
              :for={marker <- Elixir.Map.get(@waypoint_markers, {x, y}, [])}
              id={"waypoint-marker-#{marker.entity_id}-#{marker.index}"}
              data-waypoint-index={marker.index - 1}
              class={[
                "absolute -right-1 -top-1 z-30 flex h-5 w-5 cursor-grab touch-none items-center justify-center rounded-full text-[10px] font-bold shadow ring-2 ring-base-100",
                if(waypoint_unreachable?(@unreachable_leg, marker.index),
                  do: "bg-error text-error-content",
                  else: "bg-warning text-warning-content"
                )
              ]}
              aria-label={"waypoint #{marker.index}"}
              title={
                if(waypoint_unreachable?(@unreachable_leg, marker.index),
                  do: "drag to move · drag off-grid to remove (waypoint #{marker.index})",
                  else: "drag to move · drag off-grid to remove (waypoint #{marker.index})"
                )
              }
            >
              {marker.index}
            </span>
          </button>
        </div>
      </div>
    </div>
    """
  end

  defp path_cell?(nil, _x, _y), do: false
  defp path_cell?(%MapSet{} = cells, x, y), do: MapSet.member?(cells, {x, y})

  # The "unreachable_leg" is 0..N where leg k is the segment heading to
  # waypoint k+1 (1-indexed waypoint label). leg 0 = start → wp1, so wp1
  # is the first unreachable. legs > N mean the closing leg back to wp1.
  defp waypoint_unreachable?(nil, _index), do: false

  defp waypoint_unreachable?(leg, index) when is_integer(leg) and is_integer(index) do
    leg + 1 == index
  end

  defp entity_cell_color_class(entity) do
    case entity.entity_type.visual_ref do
      %{"kind" => "invisible"} -> "bg-accent text-accent-content"
      %{"kind" => "preset"} -> "bg-primary text-primary-content"
      _ -> "bg-secondary text-secondary-content"
    end
  end

  attr :selection, :any, default: nil
  attr :entity, :any
  attr :tool, :string, default: "select"

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
            Position (cell)
          </h2>
          <form phx-change="inspector_position" class="grid grid-cols-3 gap-2 text-xs">
            <label class="flex flex-col">
              <span class="text-base-content/60">x</span>
              <input
                id="entity-cell-x"
                type="number"
                name="cell_x"
                value={div(@entity.pos_x, 32)}
                phx-debounce="300"
                class="input input-xs input-bordered"
              />
            </label>
            <label class="flex flex-col">
              <span class="text-base-content/60">y</span>
              <input
                id="entity-cell-y"
                type="number"
                name="cell_y"
                value={div(@entity.pos_y, 32)}
                phx-debounce="300"
                class="input input-xs input-bordered"
              />
            </label>
            <label class="flex flex-col">
              <span class="text-base-content/60">z</span>
              <input
                id="entity-z"
                type="number"
                name="z_index_override"
                value={@entity.z_index_override || @entity.entity_type.default_z_index}
                phx-debounce="300"
                class="input input-xs input-bordered"
              />
            </label>
          </form>
        </div>

        <div class="rounded-box bg-base-200 p-3" id="inspector-path">
          <div class="mb-2 flex items-center justify-between">
            <h2 class="text-sm font-semibold uppercase tracking-wide text-base-content/70">
              Path
            </h2>
            <div class="flex gap-1">
              <button
                id="waypoint-clear"
                phx-click="waypoint_clear_all"
                data-confirm="Remove every waypoint on this entity?"
                class="btn btn-xs btn-ghost"
                title="Clear all waypoints"
                disabled={(@entity.waypoints || []) == []}
              >
                Clear
              </button>
            </div>
          </div>

          <% movement = LevelEntity.normalize_movement(@entity.movement) %>

          <form phx-change="movement_set" class="mb-2 grid grid-cols-3 gap-2 text-xs">
            <label class="col-span-3 flex flex-col">
              <span class="text-base-content/60">Mode</span>
              <select
                id="movement-mode"
                name="mode"
                class="select select-xs select-bordered"
              >
                <option
                  :for={{val, lbl} <- movement_mode_options()}
                  value={val}
                  selected={movement["mode"] == val}
                >
                  {lbl}
                </option>
              </select>
            </label>
            <label class="flex flex-col">
              <span class="text-base-content/60" title="One step every N ticks">Step ÷</span>
              <input
                id="movement-ticks-per-step"
                type="number"
                name="ticks_per_step"
                value={movement["ticks_per_step"]}
                min="1"
                max="100"
                class="input input-xs input-bordered"
              />
            </label>
            <label class="col-span-2 flex flex-col">
              <span class="text-base-content/60" title="Ticks paused after each arrival">
                Wait at WP
              </span>
              <input
                id="movement-wait"
                type="number"
                name="wait_at_waypoint"
                value={movement["wait_at_waypoint"]}
                min="0"
                max="1000"
                class="input input-xs input-bordered"
              />
            </label>
          </form>

          <div
            :if={(@entity.waypoints || []) == []}
            class="rounded bg-base-100 p-2 text-xs text-base-content/60"
          >
            <p class="mb-1 font-semibold">No waypoints yet.</p>
            <p>
              Switch to the
              <span class="rounded bg-primary/10 px-1 font-mono text-primary">Path</span>
              tool, then click cells on the map to lay this entity's route. Click an existing waypoint to remove it.
            </p>
          </div>

          <.tree
            :if={(@entity.waypoints || []) != []}
            id="waypoint-tree"
            phx-hook="TreeDnD"
            data-tree-group="waypoints"
          >
            <.tree_node
              :for={{wp, idx} <- Enum.with_index(@entity.waypoints || [])}
              id={"waypoint-row-#{idx}"}
              label={"#{idx + 1}.  (#{Map.get(wp, "x", 0)}, #{Map.get(wp, "y", 0)})"}
              icon="hero-map-pin"
              draggable
              dnd_id={idx}
            >
              <:trailing>
                <button
                  id={"waypoint-remove-#{idx}"}
                  phx-click="waypoint_remove"
                  phx-value-index={idx}
                  class="ide-toolbtn !p-0.5 text-error"
                  aria-label={"remove waypoint #{idx + 1}"}
                  title="Remove"
                >
                  <.icon name="hero-x-mark" class="size-3" />
                </button>
              </:trailing>
            </.tree_node>
          </.tree>

          <p
            :if={(@entity.waypoints || []) != []}
            class="mt-2 text-[10px] text-base-content/50"
          >
            Drag waypoints on the canvas to move them; drag handles here to reorder.
          </p>
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
                    <option
                      :for={k <- ~w(spawn despawn proximity property)}
                      value={k}
                      selected={action["trigger"]["kind"] == k}
                    >
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
                      :for={
                        k <-
                          ~w(spawn_self despawn_self spawn_other despawn_other modify_property)
                      }
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

  defp movement_mode_options do
    [
      {"loop", "Loop (wp1 → wpN → wp1)"},
      {"ping_pong", "Ping-pong (bounce)"},
      {"once", "Once (stop at end)"},
      {"random", "Random pick"},
      {"off", "Off (don't move)"}
    ]
  end

  defp cell_highlighted?(nil, _x, _y), do: false

  defp cell_highlighted?(%MapSet{} = set, x, y), do: MapSet.member?(set, {x, y})

  defp cell_highlighted?(_, _, _), do: false

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
      (Map.get(meta, "columns") || 1) * (Map.get(meta, "rows") || 1)
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

  # === Play mode (deterministic simulation preview) ===

  defp render_play(assigns) do
    sim = assigns.sim
    world = sim.current
    world_entities = alive_world_entities(world)
    sprite_styles = entity_sprite_styles(assigns.level, assigns.assets_by_id)
    design_tiles = build_entity_design_tile_index(assigns.level)
    selected = play_selected_entity(world, assigns.play_selected_id)

    assigns =
      assigns
      |> assign(:world, world)
      |> assign(:world_entities, world_entities)
      |> assign(:sprite_styles, sprite_styles)
      |> assign(:design_tiles, design_tiles)
      |> assign(:play_selected, selected)
      |> assign(:path_cells, play_path_cells(selected, world))
      |> assign(:layers, visible_layers(assigns.level.map))
      |> assign(:px_w, assigns.level.map.width * @cell_px)
      |> assign(:px_h, assigns.level.map.height * @cell_px)

    ~H"""
    <div id="play-root" phx-window-keydown="play_key">
      <.ide_shell flash={@flash}>
        <:activity>
          <.ide_rail_nav active={:levels} />
          <div class="flex-1"></div>
          <.rail_item icon="hero-pencil-square" label="Edit" phx-click="exit_play" />
        </:activity>

        <:explorer>
          <.panel title="Scene">
            <p :if={@world_entities == []} class="px-2 py-1 text-xs text-base-content/40">
              No live entities.
            </p>
            <.tree id="play-objects-tree">
              <.tree_node
                :for={e <- @world_entities}
                id={"play-object-#{sim_id(e.id)}"}
                label={e.tag || e.type_slug}
                icon="hero-cube"
                selected={@play_selected_id == e.id}
                phx-click="play_select_entity"
                phx-value-id={sim_id(e.id)}
              />
            </.tree>
          </.panel>
        </:explorer>

        <:viewport>
          <.ide_toolbar>
            <.ide_tool_button
              id="play-toggle"
              icon={if @running, do: "hero-pause", else: "hero-play"}
              label={if @running, do: "Pause", else: "Play"}
              active={@running}
              phx-click="toggle_play"
              title="Play/Pause (Space)"
            />
            <.ide_tool_button
              id="play-step"
              icon="hero-forward"
              label="Step"
              phx-click="step"
              disabled={@running}
              title="Step (.)"
            />
            <.ide_tool_button
              id="play-reset"
              icon="hero-arrow-path"
              label="Reset"
              phx-click="play_reset"
              title="Reset"
            />
            <span class="mx-1 h-5 w-px bg-base-content/15"></span>
            <label class="flex items-center gap-2 text-xs text-base-content/60">
              <span>Rate</span>
              <form phx-change="set_rate" class="contents">
                <input
                  id="play-rate"
                  type="range"
                  name="rate"
                  min={@min_tick_rate_ms}
                  max={@max_tick_rate_ms}
                  step="50"
                  value={@tick_rate_ms}
                  class="range range-xs w-28"
                />
              </form>
              <span class="w-12 text-right font-mono">{@tick_rate_ms}ms</span>
            </label>
            <div class="flex-1"></div>
            <button
              id="publish-level-button"
              phx-click="publish"
              class="ide-toolbtn ide-toolbtn-active"
            >
              <.icon name="hero-rocket-launch" class="size-4" /> Publish
            </button>
          </.ide_toolbar>

          <div
            id="play-timeline"
            class="flex items-center gap-3 border-b border-base-content/10 px-3 py-2"
          >
            <span class="text-[10px] font-semibold uppercase tracking-wide text-base-content/50">
              Tick
            </span>
            <form phx-change="scrub" class="contents">
              <input
                id="play-scrubber"
                type="range"
                name="tick"
                min="0"
                max={max(@sim.max_tick, 1)}
                step="1"
                value={@sim.tick}
                class="range range-xs flex-1"
                aria-label="Scrub timeline"
              />
            </form>
            <span class="w-20 text-right font-mono text-xs">{@sim.tick} / {@sim.max_tick}</span>
          </div>

          <p :if={@play_message} class="alert alert-info m-3 py-2 text-sm">{@play_message}</p>

          <div id="play-canvas" class="min-h-0 flex-1 overflow-auto p-4">
            <div class="relative" style={"width: #{@px_w}px; height: #{@px_h}px;"}>
              <%!-- Static map background (z-ordered layers, entity-owned tiles suppressed) --%>
              <div
                class="absolute inset-0 grid"
                style={"grid-template-columns: repeat(#{@level.map.width}, 32px);"}
              >
                <div
                  :for={{x, y} <- cells(@level.map.width, @level.map.height)}
                  class="relative h-8 w-8"
                >
                  <span
                    :for={layer <- @layers}
                    :if={not tile_owned_by_entity?(layer, @design_tiles, x, y)}
                    class="pointer-events-none absolute inset-0 bg-no-repeat"
                    style={layer_cell_style(@tilesets, layer, x, y)}
                  />
                </div>
              </div>

              <%!-- Keyed sprite layer: only changed sprites diff per tick --%>
              <div class="absolute inset-0">
                <span
                  :for={{x, y} <- @path_cells}
                  class="pointer-events-none absolute z-10 flex h-8 w-8 items-center justify-center"
                  style={"transform: translate(#{x * 32}px, #{y * 32}px);"}
                  aria-hidden="true"
                >
                  <span class="block h-2 w-2 rounded-full bg-info/80 shadow-[0_0_4px_rgba(59,130,246,0.6)]" />
                </span>

                <div
                  :for={e <- @world_entities}
                  id={"sim-entity-#{sim_id(e.id)}"}
                  style={sim_entity_style(e)}
                >
                  <span
                    :for={{{dx, dy}, style} <- Elixir.Map.get(@sprite_styles, e.id, %{})}
                    class="pointer-events-none absolute h-8 w-8 bg-no-repeat"
                    style={"left: #{dx * 32}px; top: #{dy * 32}px; #{style}"}
                  />
                  <button
                    phx-click="play_select_entity"
                    phx-value-id={sim_id(e.id)}
                    class={[
                      "absolute left-0 top-0 flex h-8 w-8 items-center justify-center text-[10px] font-bold",
                      not world_has_sprite?(@sprite_styles, e.id) && world_entity_color(e),
                      @play_selected_id == e.id && "ring-2 ring-accent"
                    ]}
                    title={world_entity_title(e)}
                    aria-label={"entity #{sim_id(e.id)}"}
                  >
                    <span :if={not world_has_sprite?(@sprite_styles, e.id)}>
                      {world_entity_label(e)}
                    </span>
                  </button>
                </div>

                <div
                  id="play-player"
                  class="pointer-events-none absolute z-40 flex items-center justify-center"
                  style={"transform: translate(#{@world.player.cell_x * 32}px, #{@world.player.cell_y * 32}px); width: 32px; height: 32px;"}
                >
                  <span class="flex h-6 w-6 items-center justify-center rounded-full bg-secondary text-xs font-bold text-secondary-content">
                    @
                  </span>
                </div>
              </div>
            </div>
          </div>
        </:viewport>

        <:inspector>
          <.play_inspector entity={@play_selected} world={@world} />

          <div class="px-3 py-3">
            <p class="mb-2 text-[10px] font-semibold uppercase tracking-wide text-base-content/50">
              Move player
            </p>
            <div class="grid w-32 grid-cols-3 gap-1">
              <span></span>
              <button
                phx-click="play_move"
                phx-value-dx="0"
                phx-value-dy="-1"
                class="ide-toolbtn justify-center"
              >
                ↑
              </button>
              <span></span>
              <button
                phx-click="play_move"
                phx-value-dx="-1"
                phx-value-dy="0"
                class="ide-toolbtn justify-center"
              >
                ←
              </button>
              <button
                phx-click="play_move"
                phx-value-dx="0"
                phx-value-dy="1"
                class="ide-toolbtn justify-center"
              >
                ↓
              </button>
              <button
                phx-click="play_move"
                phx-value-dx="1"
                phx-value-dy="0"
                class="ide-toolbtn justify-center"
              >
                →
              </button>
            </div>
          </div>
        </:inspector>

        <:status>
          <span class="font-mono">tick: {@sim.tick}/{@sim.max_tick}</span>
          <span class="font-mono">alive: {length(@world_entities)}</span>
          <span class="font-mono">player z: {@world.player.z}</span>
          <span class="flex-1"></span>
          <span class="text-base-content/50">
            Arrows move · Space play/pause · . step · drag timeline to scrub
          </span>
        </:status>
      </.ide_shell>
    </div>
    """
  end

  attr :entity, :any, default: nil
  attr :world, :any, required: true

  defp play_inspector(assigns) do
    ~H"""
    <div id="play-inspector" class="rounded-box bg-base-200 p-3 space-y-2 text-xs">
      <h2 class="text-sm font-semibold uppercase tracking-wide text-base-content/70">Inspector</h2>

      <div :if={is_nil(@entity)} class="text-base-content/60">
        Click an entity on the canvas to inspect its live state.
      </div>

      <div :if={@entity} class="space-y-2">
        <div class="font-mono text-[11px]">
          <div>id: {sim_id(@entity.id)}</div>
          <div>type: {@entity.type_slug}</div>
          <div :if={@entity.tag}>tag: {@entity.tag}</div>
          <div>cell: ({@entity.cell_x}, {@entity.cell_y}) z={@entity.z}</div>
          <div>alive: {@entity.alive}</div>
        </div>

        <div :if={(@entity.waypoints || []) != []}>
          <p class="font-semibold text-base-content/80">Path · {movement_label(@entity)}</p>
          <ol class="ml-4 list-decimal font-mono text-[11px]">
            <li
              :for={{wp, idx} <- Enum.with_index(@entity.waypoints || [])}
              class={[idx == current_waypoint_index(@entity) && "text-info font-bold"]}
            >
              ({Elixir.Map.get(wp, "x", 0)}, {Elixir.Map.get(wp, "y", 0)})
            </li>
          </ol>
        </div>

        <div :if={visible_props(@entity.properties) != %{}}>
          <p class="font-semibold text-base-content/80">Properties</p>
          <ul class="ml-2 font-mono text-[11px]">
            <li :for={{k, v} <- visible_props(@entity.properties)}>{k} = {inspect(v)}</li>
          </ul>
        </div>

        <div>
          <p class="font-semibold text-base-content/80">Last tick</p>
          <p :if={fired_actions_for(@world, @entity.id) == []} class="text-base-content/60">
            (no actions fired)
          </p>
          <ul :if={fired_actions_for(@world, @entity.id) != []} class="ml-2 font-mono text-[11px]">
            <li :for={action_id <- fired_actions_for(@world, @entity.id)}>
              {action_label(@entity, action_id)}
            </li>
          </ul>
        </div>
      </div>
    </div>
    """
  end

  # === Play-mode helpers ===

  defp alive_world_entities(world) do
    world.entities
    |> Elixir.Map.values()
    |> Enum.filter(& &1.alive)
    |> Enum.sort_by(& &1.z)
  end

  defp play_selected_entity(_world, nil), do: nil

  defp play_selected_entity(world, id) do
    case Elixir.Map.get(world.entities, id) do
      %{alive: true} = e -> e
      _ -> nil
    end
  end

  defp play_path_cells(nil, _world), do: MapSet.new()

  defp play_path_cells(entity, world) do
    case entity.waypoints || [] do
      [] ->
        MapSet.new()

      waypoints ->
        start = {entity.cell_x, entity.cell_y}
        blocked = dig_blocked(world, entity.z)
        blocked? = fn cell -> cell != start and MapSet.member?(blocked, cell) end
        opts = [bounds: world[:bounds] || {1_000_000, 1_000_000}, blocked?: blocked?]

        case Boxland.Pathfinding.preview_path(start, waypoints, opts) do
          :empty -> MapSet.new()
          {:ok, cells} -> MapSet.new(cells)
          {:partial, cells, _leg} -> MapSet.new(cells)
        end
    end
  end

  defp dig_blocked(world, z) do
    world
    |> Elixir.Map.get(:blocked_by_z, %{})
    |> Elixir.Map.get(z, MapSet.new())
  end

  # Maps each level entity's *design* cell(s) to its visual_ref so the play
  # canvas suppresses the painted layer tile a moving entity sits on.
  defp build_entity_design_tile_index(level) do
    Enum.reduce(level.entities, %{}, fn e, acc ->
      case e.entity_type.visual_ref do
        %{"kind" => "group", "group_id" => gid} = ref ->
          Enum.reduce(group_members_indexed(gid, level), acc, fn {x, y, _tile}, acc ->
            Elixir.Map.put(acc, {x, y}, ref)
          end)

        nil ->
          acc

        ref ->
          Elixir.Map.put(acc, {div(e.pos_x, @cell_px), div(e.pos_y, @cell_px)}, ref)
      end
    end)
  end

  defp tile_owned_by_entity?(layer, design_tiles, x, y) do
    case Elixir.Map.get(design_tiles, {x, y}) do
      %{"kind" => "tile", "asset_id" => aid, "tile_index" => idx} ->
        match?(%{"asset_id" => ^aid, "tile_index" => ^idx}, Maps.tile_at(layer.tiles, x, y))

      %{"kind" => "group", "group_id" => gid} ->
        match?(%{"group_id" => ^gid}, Maps.tile_at(layer.tiles, x, y))

      _ ->
        false
    end
  end

  defp sim_entity_style(e) do
    "position: absolute; transform: translate(#{e.cell_x * @cell_px}px, #{e.cell_y * @cell_px}px); z-index: #{e.z || 0};"
  end

  defp sim_id({:spawned, n}), do: "spawned-#{n}"
  defp sim_id(id) when is_integer(id), do: Integer.to_string(id)
  defp sim_id(id), do: to_string(id)

  defp world_has_sprite?(sprite_styles, id) do
    case Elixir.Map.get(sprite_styles, id) do
      m when is_map(m) and map_size(m) > 0 -> true
      _ -> false
    end
  end

  defp world_entity_color(entity) do
    case entity.type_slug do
      "preset-spawn" -> "bg-secondary/70 text-secondary-content opacity-90"
      "preset-collision" -> "bg-error/40 text-error-content opacity-90"
      "preset-portal" -> "bg-info/70 text-info-content opacity-90"
      "preset-sign" -> "bg-warning/70 text-warning-content opacity-90"
      "preset-collectible" -> "bg-success/70 text-success-content opacity-90"
      _ -> "bg-primary/70 text-primary-content opacity-90"
    end
  end

  defp world_entity_label(entity) do
    case entity.type_slug do
      "preset-" <> slug -> slug |> String.first() |> String.upcase()
      "invisible-box" -> "□"
      slug -> slug |> String.first() |> String.upcase()
    end
  end

  defp world_entity_title(entity) do
    if entity.tag, do: "#{entity.tag} (#{entity.type_slug})", else: entity.type_slug
  end

  defp movement_label(entity) do
    movement = Elixir.Map.get(entity, :movement) || %{}
    mode = Elixir.Map.get(movement, "mode", "loop")
    ticks = Elixir.Map.get(movement, "ticks_per_step", 1)
    wait = Elixir.Map.get(movement, "wait_at_waypoint", 0)

    extras = Enum.filter([ticks > 1 && "step ÷ #{ticks}", wait > 0 && "wait #{wait}"], & &1)

    case extras do
      [] -> mode
      _ -> "#{mode} · #{Enum.join(extras, ", ")}"
    end
  end

  defp visible_props(props) when is_map(props) do
    props
    |> Enum.reject(fn {k, _} -> is_binary(k) and String.starts_with?(k, "_") end)
    |> Enum.into(%{})
  end

  defp visible_props(_), do: %{}

  defp current_waypoint_index(entity) do
    case entity.waypoints || [] do
      [] -> nil
      wps -> rem(Elixir.Map.get(entity.properties || %{}, "_waypoint_index", 0), length(wps))
    end
  end

  defp fired_actions_for(world, entity_id) do
    case Elixir.Map.get(world, :fired) do
      %MapSet{} = set ->
        set
        |> Enum.filter(fn
          {^entity_id, _action_id} -> true
          _ -> false
        end)
        |> Enum.map(fn {_id, action_id} -> action_id end)

      _ ->
        []
    end
  end

  defp action_label(entity, action_id) do
    case Enum.find(entity.actions || [], &(&1["id"] == action_id)) do
      %{"name" => name, "function" => %{"kind" => kind}} -> "#{name} (#{kind})"
      %{"function" => %{"kind" => kind}} -> kind
      _ -> inspect(action_id)
    end
  end
end
