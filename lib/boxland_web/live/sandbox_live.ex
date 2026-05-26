defmodule BoxlandWeb.SandboxLive do
  use BoxlandWeb, :live_view

  alias Boxland.{Levels, Library, Maps, Repo}
  alias Boxland.Game.Eca

  @default_tick_rate_ms 250
  @min_tick_rate_ms 50
  @max_tick_rate_ms 2000

  def mount(%{"id" => id}, _session, socket) do
    designer = socket.assigns.current_designer

    level =
      designer.id
      |> Levels.get_level!(id)
      |> Elixir.Map.update!(:map, &Repo.preload(&1, layers: layer_order()))

    tilesets = Library.list_tilesets(designer.id)
    sprites = list_sprites(designer.id)
    assets_by_id = Elixir.Map.new(tilesets ++ sprites, &{&1.id, &1})

    {player, player_z} = spawn_state(level)
    blocked_by_z = Levels.blocked_cells_by_z(level, tilesets)
    world = build_world(level, player, player_z, blocked_by_z)

    {:ok,
     socket
     |> assign(:level, level)
     |> assign(:tilesets, tilesets)
     |> assign(:assets_by_id, assets_by_id)
     |> assign(:player, player)
     |> assign(:player_z, player_z)
     |> assign(:world, world)
     |> assign(:tick_count, 0)
     |> assign(:running, false)
     |> assign(:tick_rate_ms, @default_tick_rate_ms)
     |> assign(:selected_entity_id, nil)
     |> assign(:blocked_by_z, blocked_by_z)
     |> assign(:message, nil)}
  end

  # === Simulation loop ===

  def handle_info(:tick, socket) do
    if socket.assigns.running do
      socket = run_tick(socket)
      Process.send_after(self(), :tick, socket.assigns.tick_rate_ms)
      {:noreply, socket}
    else
      {:noreply, socket}
    end
  end

  def handle_event("toggle_play", _params, socket) do
    running? = not socket.assigns.running
    socket = assign(socket, :running, running?)

    if running? do
      Process.send_after(self(), :tick, socket.assigns.tick_rate_ms)
    end

    {:noreply, socket}
  end

  def handle_event("step", _params, socket) do
    if socket.assigns.running do
      {:noreply, socket}
    else
      {:noreply, run_tick(socket)}
    end
  end

  def handle_event("reset", _params, socket) do
    level = socket.assigns.level
    tilesets = socket.assigns.tilesets

    {player, player_z} = spawn_state(level)
    blocked_by_z = Levels.blocked_cells_by_z(level, tilesets)
    world = build_world(level, player, player_z, blocked_by_z)

    {:noreply,
     socket
     |> assign(:player, player)
     |> assign(:player_z, player_z)
     |> assign(:world, world)
     |> assign(:tick_count, 0)
     |> assign(:running, false)
     |> assign(:blocked_by_z, blocked_by_z)
     |> assign(:message, "Reset")}
  end

  def handle_event("set_rate", %{"rate" => rate}, socket) do
    n =
      rate
      |> safe_int(@default_tick_rate_ms)
      |> max(@min_tick_rate_ms)
      |> min(@max_tick_rate_ms)

    {:noreply, assign(socket, :tick_rate_ms, n)}
  end

  # === Player movement (no longer ticks the world; the loop owns time) ===

  def handle_event("move", %{"dx" => dx, "dy" => dy}, socket) do
    {x, y} = socket.assigns.player
    level = socket.assigns.level
    next = {x + String.to_integer(dx), y + String.to_integer(dy)}

    blocked = Levels.blocked_at(socket.assigns.blocked_by_z, socket.assigns.player_z)

    cond do
      out_of_bounds?(level, next) or MapSet.member?(blocked, next) ->
        {:noreply, assign(socket, :message, "Blocked")}

      true ->
        world = Eca.set_player(socket.assigns.world, next)

        {:noreply,
         socket
         |> assign(:player, next)
         |> assign(:world, world)
         |> assign(:message, inspect_tile(level, next))}
    end
  end

  def handle_event("key", %{"key" => key}, socket) do
    case key do
      "ArrowUp" -> handle_event("move", %{"dx" => "0", "dy" => "-1"}, socket)
      "ArrowDown" -> handle_event("move", %{"dx" => "0", "dy" => "1"}, socket)
      "ArrowLeft" -> handle_event("move", %{"dx" => "-1", "dy" => "0"}, socket)
      "ArrowRight" -> handle_event("move", %{"dx" => "1", "dy" => "0"}, socket)
      " " -> handle_event("toggle_play", %{}, socket)
      "." -> handle_event("step", %{}, socket)
      _ -> {:noreply, socket}
    end
  end

  # === Selection ===

  def handle_event("select_entity", %{"id" => id}, socket) do
    {:noreply, assign(socket, :selected_entity_id, parse_id(id))}
  end

  def handle_event("clear_selection", _params, socket) do
    {:noreply, assign(socket, :selected_entity_id, nil)}
  end

  # === Render ===

  def render(assigns) do
    layers = visible_layers(assigns.level.map)
    world_entities = alive_world_entities(assigns.world)
    entity_sprite_styles = compute_entity_sprite_styles(assigns.level, assigns.assets_by_id)
    entity_cell_index = build_world_entity_cell_index(world_entities, entity_sprite_styles)
    selected = selected_world_entity(assigns)
    path_cells = path_cells_for(selected, assigns)
    entity_design_tiles = build_entity_design_tile_index(assigns.level)

    assigns =
      assigns
      |> assign(:layers, layers)
      |> assign(:world_entities, world_entities)
      |> assign(:entity_cell_index, entity_cell_index)
      |> assign(:selected, selected)
      |> assign(:path_cells, path_cells)
      |> assign(:entity_sprite_styles, entity_sprite_styles)
      |> assign(:entity_design_tiles, entity_design_tiles)
      |> assign(:min_tick_rate_ms, @min_tick_rate_ms)
      |> assign(:max_tick_rate_ms, @max_tick_rate_ms)

    ~H"""
    <Layouts.app flash={@flash} current_scope={%{designer: @current_designer}}>
      <section id="sandbox-root" phx-window-keydown="key" class="space-y-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <p class="text-sm font-semibold text-primary">Sandbox</p>
            <h1 class="text-3xl font-semibold tracking-tight">{@level.name}</h1>
          </div>
          <.link navigate={~p"/app/levels/#{@level.id}"} class="btn btn-ghost">Back to editor</.link>
        </div>

        <div
          id="sandbox-controls"
          class="flex flex-wrap items-center gap-3 rounded-box bg-base-200 p-3"
        >
          <button
            id="sandbox-play"
            phx-click="toggle_play"
            class={["btn btn-sm", @running && "btn-primary"]}
            title={if @running, do: "Pause (Space)", else: "Play (Space)"}
          >
            <.icon name={if @running, do: "hero-pause", else: "hero-play"} class="size-4" />
            {if @running, do: "Pause", else: "Play"}
          </button>

          <button
            id="sandbox-step"
            phx-click="step"
            class="btn btn-sm"
            disabled={@running}
            title="Step one tick (.)"
          >
            <.icon name="hero-forward" class="size-4" /> Step
          </button>

          <button id="sandbox-reset" phx-click="reset" class="btn btn-sm btn-ghost">
            <.icon name="hero-arrow-path" class="size-4" /> Reset
          </button>

          <label class="flex items-center gap-2 text-xs text-base-content/70">
            <span>Rate</span>
            <form phx-change="set_rate" class="contents">
              <input
                id="sandbox-rate"
                type="range"
                name="rate"
                min={@min_tick_rate_ms}
                max={@max_tick_rate_ms}
                step="50"
                value={@tick_rate_ms}
                class="range range-xs w-40"
              />
            </form>
            <span class="font-mono w-12 text-right">{@tick_rate_ms}ms</span>
          </label>

          <div class="ml-auto flex items-center gap-3 text-xs">
            <span class="font-mono" title="Player z (collisions only apply at this z)">
              player z: {@player_z}
            </span>
            <span class="font-mono">tick: {@tick_count}</span>
            <span class="font-mono">alive: {length(@world_entities)}</span>
          </div>
        </div>

        <p :if={@message} class="alert alert-info py-2 text-sm">{@message}</p>

        <div class="grid gap-4 lg:grid-cols-[1fr_18rem]">
          <div class="overflow-auto rounded-box bg-base-200 p-4">
            <div
              class="relative grid w-fit gap-px"
              style={"grid-template-columns: repeat(#{@level.map.width}, 32px);"}
            >
              <div
                :for={{x, y} <- cells(@level.map.width, @level.map.height)}
                id={"sandbox-cell-#{x}-#{y}"}
                class="relative h-8 w-8 border border-base-300 bg-base-100"
              >
                <%= for item <- cell_stack(@layers, @tilesets, @entity_cell_index, @entity_design_tiles, x, y) do %>
                  <%= case item do %>
                    <% {:layer, layer} -> %>
                      <span
                        class="pointer-events-none absolute inset-0 bg-no-repeat"
                        style={layer_cell_style(@tilesets, layer, x, y)}
                      />
                    <% {:entity_sprite, entity, offset} -> %>
                      <span
                        :if={entity_sprite_at(@entity_sprite_styles, entity.id, offset)}
                        id={sprite_dom_id(entity.id, offset)}
                        class="pointer-events-none absolute inset-0 bg-no-repeat"
                        style={entity_sprite_at(@entity_sprite_styles, entity.id, offset)}
                      />
                  <% end %>
                <% end %>

                <span
                  :if={MapSet.member?(@path_cells, {x, y})}
                  class="pointer-events-none absolute inset-0 z-10 flex items-center justify-center"
                  aria-hidden="true"
                >
                  <span class="block h-2 w-2 rounded-full bg-info/80 shadow-[0_0_4px_rgba(59,130,246,0.6)]" />
                </span>

                <button
                  :for={covering <- anchor_entities_in_cell(@entity_cell_index, x, y)}
                  id={"sandbox-entity-#{covering.entity.id}"}
                  data-cell={"#{x}-#{y}"}
                  phx-click="select_entity"
                  phx-value-id={covering.entity.id}
                  class={[
                    "absolute inset-0 z-20 flex items-center justify-center text-[10px] font-bold",
                    entity_sprite_empty?(@entity_sprite_styles, covering.entity.id) &&
                      ["opacity-90", world_entity_color(covering.entity)],
                    @selected_entity_id == covering.entity.id && "ring-2 ring-accent z-30"
                  ]}
                  aria-label={"entity #{covering.entity.id}"}
                  title={world_entity_title(covering.entity)}
                >
                  <span :if={entity_sprite_empty?(@entity_sprite_styles, covering.entity.id)}>
                    {world_entity_label(covering.entity)}
                  </span>
                </button>

                <span
                  :if={@player == {x, y}}
                  class="pointer-events-none absolute inset-1 z-40 flex items-center justify-center rounded-full bg-secondary text-xs font-bold text-secondary-content"
                >
                  @
                </span>
              </div>
            </div>
          </div>

          <aside id="sandbox-side" class="space-y-3">
            <.entity_inspector entity={@selected} world={@world} tick_count={@tick_count} />

            <div class="rounded-box bg-base-200 p-3 text-xs text-base-content/70">
              <p class="mb-1 font-semibold text-base-content/80">Controls</p>
              <p>Arrows = move player</p>
              <p>Space = play/pause &nbsp; . = step</p>
              <p>Click an entity to inspect.</p>
            </div>

            <div class="grid w-32 grid-cols-3 gap-2">
              <span></span>
              <button phx-click="move" phx-value-dx="0" phx-value-dy="-1" class="btn btn-sm">
                ↑
              </button>
              <span></span>
              <button phx-click="move" phx-value-dx="-1" phx-value-dy="0" class="btn btn-sm">
                ←
              </button>
              <button phx-click="move" phx-value-dx="0" phx-value-dy="1" class="btn btn-sm">↓</button>
              <button phx-click="move" phx-value-dx="1" phx-value-dy="0" class="btn btn-sm">→</button>
            </div>
          </aside>
        </div>
      </section>
    </Layouts.app>
    """
  end

  attr :entity, :any, default: nil
  attr :world, :any, required: true
  attr :tick_count, :integer, required: true

  defp entity_inspector(assigns) do
    ~H"""
    <div id="sandbox-inspector" class="rounded-box bg-base-200 p-3 space-y-2 text-xs">
      <h2 class="text-sm font-semibold uppercase tracking-wide text-base-content/70">Inspector</h2>

      <div :if={is_nil(@entity)} class="text-base-content/60">
        Click an entity on the canvas to inspect its live state.
      </div>

      <div :if={@entity} class="space-y-2">
        <div class="font-mono text-[11px]">
          <div>id: {@entity.id}</div>
          <div>type: {@entity.type_slug}</div>
          <div :if={@entity.tag}>tag: {@entity.tag}</div>
          <div>cell: ({@entity.cell_x}, {@entity.cell_y}) z={@entity.z}</div>
          <div>alive: {@entity.alive}</div>
        </div>

        <div :if={(@entity.waypoints || []) != []}>
          <p class="font-semibold text-base-content/80">
            Path · {movement_label(@entity)}
          </p>
          <ol class="ml-4 list-decimal font-mono text-[11px]">
            <li
              :for={{wp, idx} <- Enum.with_index(@entity.waypoints || [])}
              class={[
                idx == current_waypoint_index(@entity) && "text-info font-bold"
              ]}
            >
              ({Elixir.Map.get(wp, "x", 0)}, {Elixir.Map.get(wp, "y", 0)})
            </li>
          </ol>
        </div>

        <div :if={visible_props(@entity.properties) != %{}}>
          <p class="font-semibold text-base-content/80">Properties</p>
          <ul class="ml-2 font-mono text-[11px]">
            <li :for={{k, v} <- visible_props(@entity.properties)}>
              {k} = {inspect(v)}
            </li>
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

  # === Tick execution ===

  defp run_tick(socket) do
    prev = socket.assigns.world
    world = Eca.tick(prev)
    message = eca_message(world, prev) || socket.assigns.message

    socket
    |> assign(:world, world)
    |> assign(:tick_count, socket.assigns.tick_count + 1)
    |> assign(:message, message)
  end

  defp eca_message(new_world, old_world) do
    transitioned =
      new_world.entities
      |> Enum.find(fn {id, e} ->
        old = Elixir.Map.get(old_world.entities, id)
        (old && old.alive) and not e.alive
      end)

    case transitioned do
      {_id, e} -> "Entity #{e.tag || e.type_slug} despawned"
      nil -> nil
    end
  end

  defp build_world(level, player, player_z, blocked_by_z) do
    bounds = {level.map.width, level.map.height}

    level.entities
    |> Eca.init_world(player,
      bounds: bounds,
      blocked_by_z: blocked_by_z,
      player_z: player_z
    )
    |> Eca.tick()
  end

  defp spawn_state(level) do
    case Enum.find(level.entities, &(preset(&1) == "spawn")) do
      nil -> {{0, 0}, 0}
      spawn -> {{div(spawn.pos_x, 32), div(spawn.pos_y, 32)}, Levels.entity_effective_z(spawn)}
    end
  end

  # === World queries ===

  defp alive_world_entities(world) do
    world.entities
    |> Elixir.Map.values()
    |> Enum.filter(& &1.alive)
    |> Enum.sort_by(& &1.z)
  end

  # Builds a cell-keyed index of which entities are visible on each cell.
  # A group-bound entity covers every cell its sprite footprint reaches
  # (anchor plus member-tile offsets), so we register an entry at each
  # `(cell_x + dx, cell_y + dy)` and tag only the anchor with `anchor?: true`.
  defp build_world_entity_cell_index(world_entities, sprite_styles) do
    Enum.reduce(world_entities, %{}, fn entity, acc ->
      offsets =
        case sprite_styles |> Elixir.Map.get(entity.id, %{}) |> Elixir.Map.keys() do
          [] -> [{0, 0}]
          keys -> keys
        end

      Enum.reduce(offsets, acc, fn {dx, dy} = offset, acc ->
        cell = {entity.cell_x + dx, entity.cell_y + dy}
        entry = %{entity: entity, anchor?: offset == {0, 0}, offset: offset}
        Elixir.Map.update(acc, cell, [entry], &[entry | &1])
      end)
    end)
  end

  # Maps each level entity's *design* cell(s) to its visual_ref. For a
  # group-bound entity this expands to every cell the group occupies on
  # the map, so the sandbox can suppress every painted layer tile that
  # belongs to that group — not just the anchor — and the moving entity
  # leaves no body parts behind.
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
          cell = {div(e.pos_x, 32), div(e.pos_y, 32)}
          Elixir.Map.put(acc, cell, ref)
      end
    end)
  end

  # Per-cell list of items to render under the interactive overlays:
  # interleaves layer tiles with entity sprites by z-index so a moving
  # entity stays inside its own layer rather than floating above the
  # whole canvas. A layer tile that matches an entity's design-position
  # visual_ref is suppressed — the entity's sprite replaces it.
  defp cell_stack(layers, tilesets, entity_cell_index, design_tiles, x, y) do
    cell_entries = Elixir.Map.get(entity_cell_index, {x, y}, [])

    layer_items =
      Enum.map(layers, fn layer ->
        {layer.z_index, :layer_order, {:layer, layer}}
      end)
      |> Enum.reject(fn {_z, _ord, {:layer, layer}} ->
        layer_tile_owned_by_entity?(layer, tilesets, design_tiles, x, y)
      end)

    entity_items =
      Enum.map(cell_entries, fn %{entity: entity, offset: offset} ->
        {entity.z || 0, :entity_order, {:entity_sprite, entity, offset}}
      end)

    (layer_items ++ entity_items)
    |> Enum.sort_by(fn {z, ord, _} -> {z, ord_rank(ord)} end)
    |> Enum.map(fn {_z, _ord, item} -> item end)
  end

  defp ord_rank(:layer_order), do: 0
  defp ord_rank(:entity_order), do: 1

  defp layer_tile_owned_by_entity?(layer, _tilesets, design_tiles, x, y) do
    case Elixir.Map.get(design_tiles, {x, y}) do
      %{"kind" => "tile", "asset_id" => aid, "tile_index" => idx} ->
        case Maps.tile_at(layer.tiles, x, y) do
          %{"asset_id" => ^aid, "tile_index" => ^idx} -> true
          _ -> false
        end

      %{"kind" => "group", "group_id" => gid} ->
        case Maps.tile_at(layer.tiles, x, y) do
          %{"group_id" => ^gid} -> true
          _ -> false
        end

      _ ->
        false
    end
  end

  defp selected_world_entity(%{selected_entity_id: nil}), do: nil

  defp selected_world_entity(%{selected_entity_id: id, world: world}) do
    Elixir.Map.get(world.entities, id)
  end

  defp path_cells_for(nil, _assigns), do: MapSet.new()

  defp path_cells_for(%{alive: false}, _assigns), do: MapSet.new()

  defp path_cells_for(entity, assigns) do
    waypoints = entity.waypoints || []

    if waypoints == [] do
      MapSet.new()
    else
      start = {entity.cell_x, entity.cell_y}
      blocked = Levels.blocked_at(assigns.blocked_by_z, entity.z)

      blocked? = fn cell -> cell != start and MapSet.member?(blocked, cell) end

      opts = [
        bounds: {assigns.level.map.width, assigns.level.map.height},
        blocked?: blocked?
      ]

      case Boxland.Pathfinding.preview_path(start, waypoints, opts) do
        :empty -> MapSet.new()
        {:ok, cells} -> MapSet.new(cells)
        {:partial, cells, _leg} -> MapSet.new(cells)
      end
    end
  end

  defp movement_label(entity) do
    movement = Elixir.Map.get(entity, :movement) || %{}
    mode = Elixir.Map.get(movement, "mode", "loop")
    ticks = Elixir.Map.get(movement, "ticks_per_step", 1)
    wait = Elixir.Map.get(movement, "wait_at_waypoint", 0)

    extras =
      [
        ticks > 1 && "step ÷ #{ticks}",
        wait > 0 && "wait #{wait}"
      ]
      |> Enum.filter(& &1)

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
    waypoints = entity.waypoints || []

    cond do
      waypoints == [] ->
        nil

      true ->
        rem(Elixir.Map.get(entity.properties || %{}, "_waypoint_index", 0), length(waypoints))
    end
  end

  defp fired_actions_for(world, entity_id) do
    case Elixir.Map.get(world, :fired) do
      nil ->
        []

      %MapSet{} = set ->
        set
        |> Enum.filter(fn
          {^entity_id, _action_id} -> true
          _ -> false
        end)
        |> Enum.map(fn {_id, action_id} -> action_id end)
    end
  end

  defp action_label(entity, action_id) do
    actions = entity.actions || []

    case Enum.find(actions, &(&1["id"] == action_id)) do
      %{"name" => name, "function" => %{"kind" => kind}} -> "#{name} (#{kind})"
      %{"function" => %{"kind" => kind}} -> kind
      _ -> inspect(action_id)
    end
  end

  # === Visual helpers ===

  defp world_entity_color(entity) do
    case entity.type_slug do
      "preset-spawn" -> "bg-secondary/70 text-secondary-content"
      "preset-collision" -> "bg-error/40 text-error-content"
      "preset-portal" -> "bg-info/70 text-info-content"
      "preset-sign" -> "bg-warning/70 text-warning-content"
      "preset-collectible" -> "bg-success/70 text-success-content"
      _ -> "bg-primary/70 text-primary-content"
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
    base = entity.tag || entity.type_slug

    cond do
      entity.tag -> "#{entity.tag} (#{entity.type_slug})"
      true -> base
    end
  end

  defp visible_layers(%Boxland.Maps.Map{layers: layers}) when is_list(layers) do
    layers
    |> Enum.filter(& &1.visible)
    |> Enum.sort_by(fn l -> {l.z_index, l.id} end)
  end

  defp visible_layers(_), do: []

  defp layer_order do
    import Ecto.Query
    from(l in Boxland.Maps.Layer, order_by: [asc: l.z_index, asc: l.id])
  end

  defp out_of_bounds?(level, {x, y}),
    do: x < 0 or y < 0 or x >= level.map.width or y >= level.map.height

  defp cells(width, height), do: for(y <- 0..(height - 1), x <- 0..(width - 1), do: {x, y})

  defp inspect_tile(level, {x, y}) do
    level.entities
    |> Enum.filter(&(div(&1.pos_x, 32) == x and div(&1.pos_y, 32) == y))
    |> Enum.find_value(fn entity ->
      case preset(entity) do
        "portal" -> "Portal reached"
        "sign" -> "Sign"
        "collectible" -> "Collectible"
        _ -> nil
      end
    end)
  end

  defp preset(entity) do
    Enum.find_value(entity.entity_type.components || [], fn
      %{"preset" => preset} -> preset
      _ -> nil
    end) ||
      case entity.entity_type.visual_ref do
        %{"kind" => "preset", "slug" => slug} -> slug
        _ -> nil
      end
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

  # For each level entity, returns a per-offset map of sprite CSS styles.
  # Single-cell entities have one entry at offset {0, 0}; group-bound
  # entities have one entry per member tile, keyed by `(dx, dy)` relative
  # to the entity's design (anchor) cell.
  defp compute_entity_sprite_styles(level, assets_by_id) do
    Elixir.Map.new(level.entities, fn e ->
      {e.id, entity_sprite_offsets(e, assets_by_id, level)}
    end)
  end

  defp entity_sprite_offsets(e, assets_by_id, level) do
    case e.entity_type.visual_ref do
      %{"kind" => "group", "group_id" => gid} ->
        anchor_x = div(e.pos_x, 32)
        anchor_y = div(e.pos_y, 32)

        gid
        |> group_members_indexed(level)
        |> Enum.flat_map(fn {x, y, tile} ->
          case tile_sprite_style(tile, assets_by_id) do
            nil -> []
            style -> [{{x - anchor_x, y - anchor_y}, style}]
          end
        end)
        |> Elixir.Map.new()

      %{"kind" => "tile"} = ref ->
        case tile_sprite_style(ref, assets_by_id) do
          nil -> %{}
          style -> %{{0, 0} => style}
        end

      %{"kind" => "sprite", "asset_id" => asset_id} ->
        case Elixir.Map.get(assets_by_id, asset_id) do
          nil -> %{}
          asset -> %{{0, 0} => sprite_full_style(asset)}
        end

      _ ->
        %{}
    end
  end

  defp tile_sprite_style(%{"asset_id" => asset_id, "tile_index" => index} = ref, assets_by_id) do
    rotation = Elixir.Map.get(ref, "rotation", 0)

    case Elixir.Map.get(assets_by_id, asset_id) do
      nil -> nil
      asset -> tile_style(asset, index) <> " transform: rotate(#{rotation}deg);"
    end
  end

  defp tile_sprite_style(_ref, _assets_by_id), do: nil

  defp group_members_indexed(gid, level) do
    for layer <- level.map.layers || [],
        {k, tile} <- layer.tiles || %{},
        tile["group_id"] == gid do
      {x, y} = Maps.parse_key(k)
      {x, y, tile}
    end
  end

  defp anchor_entities_in_cell(entity_cell_index, x, y) do
    entity_cell_index
    |> Elixir.Map.get({x, y}, [])
    |> Enum.filter(& &1.anchor?)
  end

  defp entity_sprite_at(styles, entity_id, offset) do
    styles
    |> Elixir.Map.get(entity_id, %{})
    |> Elixir.Map.get(offset)
  end

  defp entity_sprite_empty?(styles, entity_id) do
    case Elixir.Map.get(styles, entity_id) do
      nil -> true
      m when map_size(m) == 0 -> true
      _ -> false
    end
  end

  defp sprite_dom_id(entity_id, {0, 0}), do: "sandbox-entity-sprite-#{entity_id}"
  defp sprite_dom_id(entity_id, {dx, dy}), do: "sandbox-entity-sprite-#{entity_id}-#{dx}-#{dy}"

  defp sprite_full_style(asset) do
    "background-image: url('#{asset.content_url}');" <>
      " background-size: contain;" <>
      " background-position: center;" <>
      " image-rendering: pixelated;"
  end

  defp list_sprites(owner_id) do
    import Ecto.Query

    Boxland.Library.Asset
    |> where([a], a.owner_id == ^owner_id and a.kind == "sprite")
    |> Repo.all()
  end

  defp safe_int(v, default) when is_binary(v) do
    case Integer.parse(v) do
      {n, _} -> n
      :error -> default
    end
  end

  defp safe_int(v, _) when is_integer(v), do: v
  defp safe_int(_, default), do: default

  defp parse_id(id) when is_integer(id), do: id

  defp parse_id(id) when is_binary(id) do
    case Integer.parse(id) do
      {n, ""} -> n
      _ -> id
    end
  end

  defp parse_id(other), do: other
end
