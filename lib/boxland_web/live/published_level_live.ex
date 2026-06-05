defmodule BoxlandWeb.PublishedLevelLive do
  @moduledoc """
  Plays a published level snapshot with the live simulation: entity
  waypoint movement, ECA triggers (including transforms), and animated
  sprites all run against the immutable `PublishedLevelVersion.snapshot`.

  Each viewer runs their own deterministic per-LiveView simulation loop.
  TODO(multiplayer): lift the world into a shared GenServer per
  {level, instance_key} broadcasting ticks over PubSub (see
  `Boxland.Game.LevelState` for the intended persistence seam) so all
  players see one authoritative world.
  """
  use BoxlandWeb, :live_view

  alias Boxland.Game.{Eca, Simulation}
  alias Boxland.Levels
  alias BoxlandWeb.LevelRender

  @cell_px 32
  @tick_rate_ms 250

  def mount(%{"id" => id}, _session, socket) do
    version = Levels.latest_published_version!(id)
    snapshot = version.snapshot

    assets_by_id = Map.new(snapshot["assets"] || [], &{&1["id"], &1})
    types_by_slug = Map.new(snapshot["entity_types"] || [], &{&1["slug"], &1})

    {player, player_z} = spawn_state(snapshot)

    world =
      Eca.init_world_from_snapshot(snapshot, player,
        bounds: {snapshot["map"]["width"], snapshot["map"]["height"]},
        blocked_by_z: blocked_by_z(snapshot, assets_by_id),
        player_z: player_z,
        seed: :erlang.phash2({version.level_id, version.version})
      )

    if connected?(socket), do: Process.send_after(self(), :tick, @tick_rate_ms)

    {:ok,
     socket
     |> assign(:version, version)
     |> assign(:snapshot, snapshot)
     |> assign(:assets_by_id, assets_by_id)
     |> assign(:types_by_slug, types_by_slug)
     |> assign(:sprite_offsets, build_sprite_offsets(snapshot, assets_by_id))
     |> assign(:design_tiles, build_design_tile_index(snapshot))
     |> assign(:sim, Simulation.new(world))}
  end

  def handle_info(:tick, socket) do
    Process.send_after(self(), :tick, @tick_rate_ms)
    {:noreply, update(socket, :sim, &Simulation.advance/1)}
  end

  def handle_event("move", %{"dx" => dx, "dy" => dy}, socket) do
    move = {String.to_integer(dx), String.to_integer(dy)}
    {:noreply, update(socket, :sim, &Simulation.queue_move(&1, move))}
  end

  def render(assigns) do
    world = assigns.sim.current

    assigns =
      assigns
      |> assign(:world, world)
      |> assign(:world_entities, Enum.filter(Map.values(world.entities), & &1.alive))
      |> assign(:layers, visible_snapshot_layers(assigns.snapshot))
      |> assign(:message, message_at(world))
      |> assign(:px_w, assigns.snapshot["map"]["width"] * @cell_px)
      |> assign(:px_h, assigns.snapshot["map"]["height"] * @cell_px)

    ~H"""
    <Layouts.app flash={@flash}>
      <section class="space-y-4">
        <div>
          <p class="text-sm font-semibold text-primary">Published Level v{@version.version}</p>
          <h1 class="text-3xl font-semibold tracking-tight">{@snapshot["level"]["name"]}</h1>
        </div>

        <p :if={@message} class="alert alert-info">{@message}</p>

        <div class="overflow-auto rounded-box bg-base-200 p-4">
          <div class="relative" style={"width: #{@px_w}px; height: #{@px_h}px;"}>
            <%!-- Static map background (entity-owned design tiles suppressed) --%>
            <div
              class="absolute inset-0 grid"
              style={"grid-template-columns: repeat(#{@snapshot["map"]["width"]}, 32px);"}
            >
              <div
                :for={{x, y} <- cells(@snapshot["map"]["width"], @snapshot["map"]["height"])}
                class="relative h-8 w-8 border border-base-300 bg-base-100"
              >
                <span
                  :for={layer <- @layers}
                  :if={not tile_owned_by_entity?(layer, @design_tiles, x, y)}
                  class="pointer-events-none absolute inset-0 bg-no-repeat"
                  style={layer_cell_style(@assets_by_id, layer, x, y)}
                  {cell_anim_attrs(@assets_by_id, layer, x, y)}
                />
              </div>
            </div>

            <%!-- Live entities: position translate outside, render transform inside --%>
            <div class="absolute inset-0">
              <div
                :for={e <- @world_entities}
                id={"pub-entity-#{entity_dom_id(e.id)}"}
                style={entity_position_style(e)}
              >
                <div class="pointer-events-none absolute left-0 top-0" style={entity_visual_style(e)}>
                  <span
                    :for={{{dx, dy}, style} <- Map.get(@sprite_offsets, e.id, %{})}
                    id={"pub-sprite-#{entity_dom_id(e.id)}-#{dx}x#{dy}"}
                    class="pointer-events-none absolute h-8 w-8 bg-no-repeat"
                    style={"left: #{dx * 32}px; top: #{dy * 32}px; #{style}"}
                    {entity_anim_attrs(@types_by_slug, @assets_by_id, e, @sim.tick)}
                  />
                </div>
                <span
                  :if={badge_label(@types_by_slug, @sprite_offsets, e)}
                  class="absolute bottom-0 right-0 rounded bg-primary px-1 text-[10px] text-primary-content"
                >
                  {badge_label(@types_by_slug, @sprite_offsets, e)}
                </span>
              </div>

              <div
                id="pub-player"
                class="pointer-events-none absolute z-40 flex items-center justify-center"
                style={"transform: translate(#{@world.player.cell_x * 32}px, #{@world.player.cell_y * 32}px); width: 32px; height: 32px;"}
              >
                <span class="flex h-6 w-6 items-center justify-center rounded-full bg-secondary text-center text-xs font-bold text-secondary-content">
                  @
                </span>
              </div>
            </div>
          </div>
        </div>

        <div class="grid w-32 grid-cols-3 gap-2">
          <span></span>
          <button phx-click="move" phx-value-dx="0" phx-value-dy="-1" class="btn btn-sm">↑</button>
          <span></span>
          <button phx-click="move" phx-value-dx="-1" phx-value-dy="0" class="btn btn-sm">←</button>
          <button phx-click="move" phx-value-dx="0" phx-value-dy="1" class="btn btn-sm">↓</button>
          <button phx-click="move" phx-value-dx="1" phx-value-dy="0" class="btn btn-sm">→</button>
        </div>
      </section>
    </Layouts.app>
    """
  end

  # === World construction ===

  defp spawn_state(snapshot) do
    case Enum.find(snapshot["entities"] || [], &(&1["preset"] == "spawn")) do
      nil -> {{0, 0}, 0}
      spawn -> {{div(spawn["pos_x"], 32), div(spawn["pos_y"], 32)}, spawn["z_index"] || 0}
    end
  end

  # Snapshot equivalent of Levels.blocked_cells_by_z: per-layer-z tile
  # collision masks plus collision-preset entities at their effective z.
  defp blocked_by_z(snapshot, assets_by_id) do
    tile_acc =
      Enum.reduce(snapshot["map"]["layers"] || [], %{}, fn layer, acc ->
        cells =
          for {key, tile} <- layer["tiles"] || %{},
              tile_cell_blocked?(tile, assets_by_id),
              into: MapSet.new() do
            Boxland.Maps.parse_key(key)
          end

        if MapSet.size(cells) == 0 do
          acc
        else
          Map.update(acc, layer["z_index"] || 0, cells, &MapSet.union(&1, cells))
        end
      end)

    Enum.reduce(snapshot["entities"] || [], tile_acc, fn entity, acc ->
      if entity["preset"] == "collision" do
        z = entity["z_index"] || 0
        cell = {div(entity["pos_x"], 32), div(entity["pos_y"], 32)}
        Map.update(acc, z, MapSet.new([cell]), &MapSet.put(&1, cell))
      else
        acc
      end
    end)
  end

  defp tile_cell_blocked?(%{"asset_id" => asset_id, "tile_index" => tile_index}, assets_by_id) do
    case assets_by_id[asset_id] do
      nil ->
        false

      asset ->
        asset["metadata"]
        |> Map.get("collisions", %{})
        |> Map.get(Integer.to_string(tile_index), Boxland.Library.CollisionMask.none())
        |> Boxland.Library.CollisionMask.to_booleans()
        |> Enum.any?()
    end
  end

  defp tile_cell_blocked?(_tile, _assets), do: false

  # === Entity visuals (precomputed at mount from the design snapshot) ===

  # `%{entity_id => %{offset => css}}`, mirroring LevelRender.entity_sprite_styles
  # but over string-keyed snapshot data.
  defp build_sprite_offsets(snapshot, assets_by_id) do
    types_by_id = Map.new(snapshot["entity_types"] || [], &{&1["id"], &1})

    Map.new(snapshot["entities"] || [], fn entity ->
      type = types_by_id[entity["entity_type_id"]] || %{}
      {entity["id"], entity_offsets(type["visual_ref"], entity, snapshot, assets_by_id)}
    end)
  end

  defp entity_offsets(
         %{"kind" => "tile", "asset_id" => aid, "tile_index" => idx} = ref,
         _e,
         _s,
         assets
       ) do
    case assets[aid] do
      nil ->
        %{}

      asset ->
        %{{0, 0} => tile_style(asset, idx) <> " transform: rotate(#{ref["rotation"] || 0}deg);"}
    end
  end

  defp entity_offsets(%{"kind" => "sprite", "asset_id" => aid}, _e, _s, assets) do
    case assets[aid] do
      nil ->
        %{}

      asset ->
        %{
          {0, 0} =>
            "background-image: url('#{asset["content_url"]}'); background-size: contain;" <>
              " background-position: center; background-repeat: no-repeat; image-rendering: pixelated;"
        }
    end
  end

  defp entity_offsets(%{"kind" => "animated", "asset_id" => aid} = ref, _e, _s, assets) do
    case assets[aid] do
      nil ->
        %{}

      asset ->
        # Static first-frame fallback; the Sprite hook animates over it.
        %{{0, 0} => tile_style(asset, LevelRender.first_animation_frame(asset, ref["animation"]))}
    end
  end

  defp entity_offsets(%{"kind" => "group", "group_id" => gid}, entity, snapshot, assets) do
    anchor_x = div(entity["pos_x"], @cell_px)
    anchor_y = div(entity["pos_y"], @cell_px)

    for layer <- snapshot["map"]["layers"] || [],
        {key, tile} <- layer["tiles"] || %{},
        tile["group_id"] == gid,
        asset = assets[tile["asset_id"]],
        asset != nil,
        into: %{} do
      {x, y} = Boxland.Maps.parse_key(key)

      {{x - anchor_x, y - anchor_y},
       tile_style(asset, tile["tile_index"]) <>
         " transform: rotate(#{tile["rotation"] || 0}deg);"}
    end
  end

  defp entity_offsets(_ref, _e, _s, _assets), do: %{}

  # Design cells owned by tile/group entities, so the painted layer tile
  # is suppressed under the (possibly moving) entity sprite.
  defp build_design_tile_index(snapshot) do
    types_by_id = Map.new(snapshot["entity_types"] || [], &{&1["id"], &1})

    Enum.reduce(snapshot["entities"] || [], %{}, fn entity, acc ->
      case (types_by_id[entity["entity_type_id"]] || %{})["visual_ref"] do
        %{"kind" => "group", "group_id" => gid} = ref ->
          group_cells =
            for layer <- snapshot["map"]["layers"] || [],
                {key, tile} <- layer["tiles"] || %{},
                tile["group_id"] == gid,
                do: Boxland.Maps.parse_key(key)

          Enum.reduce(group_cells, acc, &Map.put(&2, &1, ref))

        %{"kind" => "tile"} = ref ->
          Map.put(acc, {div(entity["pos_x"], @cell_px), div(entity["pos_y"], @cell_px)}, ref)

        _ ->
          acc
      end
    end)
  end

  defp tile_owned_by_entity?(layer, design_tiles, x, y) do
    case Map.get(design_tiles, {x, y}) do
      %{"kind" => "tile", "asset_id" => aid, "tile_index" => idx} ->
        match?(
          %{"asset_id" => ^aid, "tile_index" => ^idx},
          Map.get(layer["tiles"] || %{}, "#{x},#{y}")
        )

      %{"kind" => "group", "group_id" => gid} ->
        match?(%{"group_id" => ^gid}, Map.get(layer["tiles"] || %{}, "#{x},#{y}"))

      _ ->
        false
    end
  end

  # === Per-tick render helpers ===

  defp entity_position_style(e) do
    "position: absolute; transform: translate(#{e.cell_x * @cell_px}px, #{e.cell_y * @cell_px}px); z-index: #{e.z || 0};"
  end

  defp entity_visual_style(e) do
    size = e[:size] || %{"w" => 1, "h" => 1}
    w = (size["w"] || 1) * @cell_px
    h = (size["h"] || 1) * @cell_px

    "width: #{w}px; height: #{h}px; " <> LevelRender.transform_style(e[:transform])
  end

  # Tick-synced animation attributes via the type's animation bindings.
  defp entity_anim_attrs(types_by_slug, assets_by_id, e, tick) do
    with %{"visual_ref" => %{"kind" => "animated", "asset_id" => aid}} = type <-
           types_by_slug[e.type_slug],
         asset when not is_nil(asset) <- assets_by_id[aid] do
      state = if e[:moving], do: "moving", else: "idle"
      name = LevelRender.resolved_animation(snapshot_type_bindings(type), state)

      case LevelRender.animation_data(asset, name) do
        nil -> []
        data -> LevelRender.sprite_attrs(data, sync: "tick", tick: tick)
      end
    else
      _ -> []
    end
  end

  # Old snapshots predate the animation_bindings key.
  defp snapshot_type_bindings(type) do
    %{
      "animation_bindings" => type["animation_bindings"] || %{},
      "visual_ref" => type["visual_ref"]
    }
  end

  # Letter badge for entities without any sprite (presets, invisible).
  defp badge_label(types_by_slug, sprite_offsets, e) do
    offsets = Map.get(sprite_offsets, e.id, %{})

    if map_size(offsets) == 0 do
      case types_by_slug[e.type_slug] do
        %{"visual_ref" => %{"kind" => "preset", "slug" => slug}} ->
          slug |> String.first() |> String.upcase()

        _ ->
          nil
      end
    else
      nil
    end
  end

  defp message_at(world) do
    cell = {world.player.cell_x, world.player.cell_y}

    world.entities
    |> Map.values()
    |> Enum.filter(&(&1.alive and {&1.cell_x, &1.cell_y} == cell))
    |> Enum.find_value(fn e ->
      case e.type_slug do
        "preset-portal" -> "Portal reached"
        "preset-sign" -> "Sign"
        "preset-collectible" -> "Collectible"
        _ -> nil
      end
    end)
  end

  # === Map layers ===

  defp cells(width, height), do: for(y <- 0..(height - 1), x <- 0..(width - 1), do: {x, y})

  defp visible_snapshot_layers(snapshot) do
    (snapshot["map"]["layers"] || [])
    |> Enum.filter(&Map.get(&1, "visible", true))
    |> Enum.sort_by(fn l -> {l["z_index"] || 0, l["id"]} end)
  end

  defp layer_cell_style(assets_by_id, layer, x, y) do
    case Map.get(layer["tiles"] || %{}, "#{x},#{y}") do
      %{"asset_id" => asset_id, "tile_index" => tile_index} = tile ->
        opacity = Map.get(layer, "opacity", 100) / 100

        tile_style(assets_by_id[asset_id], tile_index) <>
          " transform: rotate(#{tile["rotation"] || 0}deg); opacity: #{opacity};"

      _ ->
        "display: none;"
    end
  end

  defp cell_anim_attrs(assets_by_id, layer, x, y) do
    cell = Map.get(layer["tiles"] || %{}, "#{x},#{y}")

    if LevelRender.animated_cell?(cell) do
      case LevelRender.tile_anim_data(cell, assets_by_id) do
        nil -> []
        data -> [{"id", "pub-anim-#{layer["id"]}-#{x}-#{y}"} | LevelRender.sprite_attrs(data)]
      end
    else
      []
    end
  end

  defp tile_style(nil, _index), do: ""

  defp tile_style(asset, index) do
    columns = asset["metadata"]["columns"] || asset["metadata"]["grid_cols"] || 1
    x = rem(index, columns) * @cell_px
    y = div(index, columns) * @cell_px

    "background-image: url('#{asset["content_url"]}'); background-position: -#{x}px -#{y}px;"
  end

  defp entity_dom_id({:spawned, n}), do: "spawned-#{n}"
  defp entity_dom_id(id), do: to_string(id)
end
