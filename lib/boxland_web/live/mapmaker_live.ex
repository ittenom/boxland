defmodule BoxlandWeb.MapmakerLive do
  use BoxlandWeb, :live_view

  alias Boxland.{Library, Maps}

  @tools ~w(place delete rotate select_area clone select)

  def mount(%{"id" => id}, _session, socket) do
    designer = socket.assigns.current_designer
    map = Maps.get_map!(designer.id, id)
    primary = Maps.primary_layer(map)
    tilesets = Library.list_tilesets(designer.id)

    {:ok,
     socket
     |> assign(:map, map)
     |> assign(:selected_layer_id, primary && primary.id)
     |> assign(:renaming_layer_id, nil)
     |> assign(:tilesets, tilesets)
     |> assign(:selected_asset_id, tilesets |> List.first() |> then(&(&1 && &1.id)))
     |> assign(:selected_tile, 0)
     |> assign(:tool, "place")
     |> assign(:selection, nil)
     |> assign(:clipboard, %{})
     |> assign(:undo_stack, [])
     |> assign(:redo_stack, [])}
  end

  def handle_event("tool", %{"tool" => tool}, socket) when tool in @tools do
    {:noreply, assign(socket, :tool, tool)}
  end

  def handle_event("undo", _params, socket), do: undo(socket)
  def handle_event("redo", _params, socket), do: redo(socket)

  def handle_event("hotkey", %{"key" => key} = params, socket) do
    cond do
      key in ["p", "P"] ->
        {:noreply, assign(socket, :tool, "place")}

      key in ["x", "X"] ->
        {:noreply, assign(socket, :tool, "delete")}

      key in ["r", "R"] ->
        {:noreply, assign(socket, :tool, "rotate")}

      key in ["s", "S"] ->
        {:noreply, assign(socket, :tool, "select_area")}

      key in ["c", "C"] ->
        {:noreply, clone_selection(socket)}

      key in ["v", "V"] ->
        {:noreply, assign(socket, :tool, "select")}

      key == "Escape" ->
        {:noreply, assign(socket, :selection, nil)}

      key in ["z", "Z"] and truthy?(params["metaKey"] || params["ctrlKey"]) and
          truthy?(params["shiftKey"]) ->
        redo(socket)

      key in ["z", "Z"] and truthy?(params["metaKey"] || params["ctrlKey"]) ->
        undo(socket)

      true ->
        {:noreply, socket}
    end
  end

  def handle_event("select_asset", %{"asset_id" => asset_id}, socket) do
    {:noreply, assign(socket, selected_asset_id: String.to_integer(asset_id), selected_tile: 0)}
  end

  def handle_event("select_tile", %{"tile" => tile}, socket) do
    {:noreply, assign(socket, :selected_tile, String.to_integer(tile))}
  end

  def handle_event("copy_selection", _params, socket) do
    {:noreply, clone_selection(socket)}
  end

  def handle_event("paste_selection", _params, socket) do
    {:noreply, assign(socket, :tool, "clone")}
  end

  def handle_event("cell", %{"x" => x, "y" => y}, socket) do
    x = String.to_integer(x)
    y = String.to_integer(y)
    apply_tool(socket, x, y)
  end

  def handle_event("paint_cell", %{"x" => x, "y" => y}, socket) do
    x = String.to_integer(x)
    y = String.to_integer(y)
    paint_tile(socket, x, y)
  end

  def handle_event("select_area_drag", %{"x1" => x1, "y1" => y1, "x2" => x2, "y2" => y2}, socket) do
    selection = %{
      x1: to_int(x1),
      y1: to_int(y1),
      x2: to_int(x2),
      y2: to_int(y2)
    }

    {:noreply, assign(socket, :selection, selection)}
  end

  def handle_event("clear_selection", _params, socket) do
    {:noreply, assign(socket, :selection, nil)}
  end

  def handle_event("selection_copy", _params, socket) do
    {:noreply, clone_selection(socket)}
  end

  def handle_event("selection_delete", _params, socket) do
    apply_to_selection(socket, fn layer, cells ->
      Maps.delete_tiles_in_cells(layer, cells)
    end)
  end

  def handle_event("selection_rotate", _params, socket) do
    apply_to_selection(socket, fn layer, cells ->
      Maps.rotate_tiles_in_cells(layer, cells)
    end)
  end

  def handle_event("selection_move_up", _params, socket) do
    move_selection(socket, :up)
  end

  def handle_event("selection_move_down", _params, socket) do
    move_selection(socket, :down)
  end

  # === Layer events ===

  def handle_event("select_layer", %{"id" => id}, socket) do
    {:noreply, assign(socket, :selected_layer_id, String.to_integer(id))}
  end

  def handle_event("add_layer", _params, socket) do
    map = socket.assigns.map
    {:ok, layer} = Maps.create_layer(map)

    {:noreply, refresh_map(socket, select: layer.id)}
  end

  def handle_event("duplicate_layer", %{"id" => id}, socket) do
    layer = Maps.get_layer_for_map!(socket.assigns.map.id, String.to_integer(id))
    {:ok, dup} = Maps.duplicate_layer(layer)
    {:noreply, refresh_map(socket, select: dup.id)}
  end

  def handle_event("delete_layer", %{"id" => id}, socket) do
    map = socket.assigns.map
    layer = Maps.get_layer_for_map!(map.id, String.to_integer(id))

    case Maps.delete_layer(layer) do
      {:ok, _} ->
        {:noreply, refresh_map(socket, prefer_other_than: layer.id)}

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
    map = socket.assigns.map

    if name == "" do
      {:noreply, assign(socket, :renaming_layer_id, nil)}
    else
      layer = Maps.get_layer_for_map!(map.id, String.to_integer(id))

      case Maps.rename_layer(layer, name) do
        {:ok, _} ->
          {:noreply,
           socket
           |> assign(:renaming_layer_id, nil)
           |> refresh_map()}

        {:error, _} ->
          {:noreply,
           socket
           |> put_flash(:error, "That name is already in use.")
           |> assign(:renaming_layer_id, nil)}
      end
    end
  end

  def handle_event("toggle_visibility", %{"id" => id}, socket) do
    layer = Maps.get_layer_for_map!(socket.assigns.map.id, String.to_integer(id))
    {:ok, _} = Maps.toggle_layer_visibility(layer)
    {:noreply, refresh_map(socket)}
  end

  def handle_event("toggle_lock", %{"id" => id}, socket) do
    layer = Maps.get_layer_for_map!(socket.assigns.map.id, String.to_integer(id))
    {:ok, _} = Maps.toggle_layer_lock(layer)
    {:noreply, refresh_map(socket)}
  end

  def handle_event("set_opacity", %{"id" => id, "opacity" => opacity}, socket) do
    layer = Maps.get_layer_for_map!(socket.assigns.map.id, String.to_integer(id))
    {:ok, _} = Maps.set_layer_opacity(layer, String.to_integer(opacity))
    {:noreply, refresh_map(socket)}
  end

  def handle_event("move_layer_up", %{"id" => id}, socket) do
    move_layer(socket, String.to_integer(id), -1)
  end

  def handle_event("move_layer_down", %{"id" => id}, socket) do
    move_layer(socket, String.to_integer(id), +1)
  end

  def render(assigns) do
    ~H"""
    <Layouts.app flash={@flash} width="wide" current_scope={%{designer: @current_designer}}>
      <section phx-window-keydown="hotkey" class="space-y-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <p class="text-sm font-semibold text-primary">Mapmaker</p>
            <h1 class="text-3xl font-semibold tracking-tight">{@map.name}</h1>
          </div>
          <div class="flex gap-2">
            <.link navigate={~p"/app/maps"} class="btn btn-ghost">Maps</.link>
            <.link navigate={~p"/app/levels"} class="btn btn-primary">Make level</.link>
          </div>
        </div>

        <div class="flex flex-wrap gap-2">
          <.tool_button icon="hero-pencil" label="P" tool="place" active={@tool == "place"} />
          <.tool_button icon="hero-x-mark" label="X" tool="delete" active={@tool == "delete"} />
          <.tool_button icon="hero-arrow-path" label="R" tool="rotate" active={@tool == "rotate"} />
          <.tool_button
            icon="hero-square-2-stack"
            label="S"
            tool="select_area"
            active={@tool == "select_area"}
          />
          <.tool_button
            icon="hero-cursor-arrow-rays"
            label="V"
            tool="select"
            active={@tool == "select"}
          />
          <button
            id="map-copy-button"
            phx-click="copy_selection"
            class={["btn btn-sm", @tool == "clone" && "btn-primary"]}
            title="Copy selection"
          >
            <.icon name="hero-clipboard-document" class="size-4" /> Copy
          </button>
          <button
            id="map-paste-button"
            phx-click="paste_selection"
            class={["btn btn-sm", @tool == "clone" && "btn-primary"]}
            title="Paste copied selection"
          >
            <.icon name="hero-clipboard-document-check" class="size-4" /> Paste
          </button>
          <button id="map-undo-button" phx-click="undo" class="btn btn-sm" title="Undo (⌘Z)">
            <.icon name="hero-arrow-uturn-left" class="size-4" />
          </button>
          <button id="map-redo-button" phx-click="redo" class="btn btn-sm" title="Redo (⇧⌘Z)">
            <.icon name="hero-arrow-uturn-right" class="size-4" />
          </button>
        </div>

        <div class="grid gap-4 lg:grid-cols-[16rem_1fr_18rem]">
          <aside class="space-y-3">
            <.input
              id="tileset-select"
              name="asset_id"
              type="select"
              label="Tileset"
              value={@selected_asset_id}
              options={Enum.map(@tilesets, &{&1.name, &1.id})}
              phx-change="select_asset"
            />
            <.tile_palette
              asset={selected_asset(@tilesets, @selected_asset_id)}
              selected_tile={@selected_tile}
            />
          </aside>

          <div
            id="mapmaker-canvas"
            phx-hook="MapmakerCanvas"
            data-tool={if active_layer(@map, @selected_layer_id) |> layer_editable?(), do: @tool, else: "select"}
            class="overflow-auto rounded-box bg-base-200 p-2"
          >
            <div
              class="relative grid w-fit gap-0"
              style={"grid-template-columns: repeat(#{@map.width}, 32px);"}
            >
              <button
                :for={{x, y} <- cells(@map.width, @map.height)}
                id={"map-cell-#{x}-#{y}"}
                data-map-cell
                data-x={x}
                data-y={y}
                phx-click="cell"
                phx-value-x={x}
                phx-value-y={y}
                class={[
                  "relative h-8 w-8 touch-none border border-base-300/60 bg-base-100 transition-[filter] hover:brightness-105",
                  selected_cell?(@selection, x, y) && "border-primary ring-1 ring-primary",
                  !selected_cell?(@selection, x, y) && "border-base-300/60"
                ]}
              >
                <span
                  :for={layer <- visible_layers(@map)}
                  class="pointer-events-none absolute inset-0 bg-no-repeat"
                  style={layer_cell_style(@tilesets, layer, x, y)}
                />
              </button>

              <.selection_menu
                :if={@selection && @tool in ["select", "select_area"]}
                selection={@selection}
                map={@map}
                selected_layer={active_layer(@map, @selected_layer_id)}
              />
            </div>
          </div>

          <.layers_panel
            layers={display_layers(@map)}
            selected_layer_id={@selected_layer_id}
            renaming_layer_id={@renaming_layer_id}
          />
        </div>
      </section>
    </Layouts.app>
    """
  end

  attr :icon, :string, required: true
  attr :label, :string, required: true
  attr :tool, :string, required: true
  attr :active, :boolean, required: true

  defp tool_button(assigns) do
    ~H"""
    <button
      id={"map-tool-#{@tool}"}
      phx-click="tool"
      phx-value-tool={@tool}
      class={["btn btn-sm", @active && "btn-primary"]}
    >
      <.icon name={@icon} class="size-4" /> {@label}
    </button>
    """
  end

  attr :asset, :any, required: true
  attr :selected_tile, :integer, required: true

  defp tile_palette(%{asset: nil} = assigns) do
    ~H"""
    <div class="rounded-box bg-base-200 p-4 text-sm text-base-content/60">
      Upload a tileset first.
    </div>
    """
  end

  defp tile_palette(assigns) do
    ~H"""
    <div class="grid grid-cols-6 gap-1">
      <button
        :for={index <- tile_indexes(@asset)}
        id={"tile-palette-#{index}"}
        phx-click="select_tile"
        phx-value-tile={index}
        class={[
          "h-8 w-8 border bg-no-repeat",
          @selected_tile == index && "border-primary",
          @selected_tile != index && "border-base-300"
        ]}
        style={tile_style(@asset, index)}
      />
    </div>
    """
  end

  attr :selection, :map, required: true
  attr :map, :map, required: true
  attr :selected_layer, :any, required: true

  defp selection_menu(assigns) do
    can_up = assigns.selected_layer && neighbor_layer(assigns.map, assigns.selected_layer, :up) != nil
    can_down = assigns.selected_layer && neighbor_layer(assigns.map, assigns.selected_layer, :down) != nil
    w = assigns.selection.x2 - assigns.selection.x1 + 1
    h = assigns.selection.y2 - assigns.selection.y1 + 1

    assigns = assign(assigns, can_up: can_up, can_down: can_down, dims: "#{w}×#{h}")

    ~H"""
    <div
      id="selection-menu"
      class="absolute z-10 flex items-center gap-0.5 rounded-full border border-base-300 bg-base-100 px-1 py-0.5 shadow-md"
      style={"top: #{(@selection.y2 + 1) * 32 + 6}px; left: #{@selection.x1 * 32}px;"}
    >
      <span class="px-2 font-mono text-[10px] text-base-content/60">
        {@dims}
      </span>
      <button
        type="button"
        id="selection-move-up"
        phx-click="selection_move_up"
        disabled={!@can_up}
        class="btn btn-ghost btn-xs px-1"
        title="Move to layer above"
        aria-label="Move tiles to layer above"
      >
        <.icon name="hero-arrow-up" class="size-3.5" />
      </button>
      <button
        type="button"
        id="selection-move-down"
        phx-click="selection_move_down"
        disabled={!@can_down}
        class="btn btn-ghost btn-xs px-1"
        title="Move to layer below"
        aria-label="Move tiles to layer below"
      >
        <.icon name="hero-arrow-down" class="size-3.5" />
      </button>
      <span class="mx-0.5 h-4 w-px bg-base-300" />
      <button
        type="button"
        id="selection-copy"
        phx-click="selection_copy"
        class="btn btn-ghost btn-xs px-1"
        title="Copy tiles"
        aria-label="Copy tiles"
      >
        <.icon name="hero-clipboard-document" class="size-3.5" />
      </button>
      <button
        type="button"
        id="selection-rotate"
        phx-click="selection_rotate"
        class="btn btn-ghost btn-xs px-1"
        title="Rotate tiles 90°"
        aria-label="Rotate tiles"
      >
        <.icon name="hero-arrow-path" class="size-3.5" />
      </button>
      <button
        type="button"
        id="selection-delete"
        phx-click="selection_delete"
        class="btn btn-ghost btn-xs px-1 text-error"
        title="Delete tiles"
        aria-label="Delete tiles"
      >
        <.icon name="hero-trash" class="size-3.5" />
      </button>
    </div>
    """
  end

  attr :layers, :list, required: true
  attr :selected_layer_id, :any, required: true
  attr :renaming_layer_id, :any, required: true

  defp layers_panel(assigns) do
    ~H"""
    <aside class="space-y-2 rounded-box bg-base-200 p-3" id="layers-panel">
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
            layer.id != @selected_layer_id && "border-base-300 bg-base-100 hover:bg-base-100/70"
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

      <p :if={@layers == []} class="rounded-box bg-base-100 p-3 text-xs text-base-content/60">
        No layers yet. Click Add to create one.
      </p>
    </aside>
    """
  end

  # === Tool helpers (all act on the selected layer only) ===

  defp paint_tile(%{assigns: %{tool: "place", selected_asset_id: asset_id}} = socket, x, y)
       when not is_nil(asset_id) do
    with %{} = layer <- active_layer(socket.assigns.map, socket.assigns.selected_layer_id),
         true <- layer_editable?(layer) do
      tiles = layer.tiles

      new_tiles =
        Maps.put_tile(tiles, x, y, %{
          asset_id: asset_id,
          tile_index: socket.assigns.selected_tile,
          rotation: 0
        })

      if new_tiles == tiles do
        {:noreply, socket}
      else
        {:ok, updated_layer} = Maps.update_layer_tiles(layer, new_tiles)

        {:noreply,
         socket
         |> replace_layer(updated_layer)
         |> assign(:undo_stack, [{layer.id, tiles} | socket.assigns.undo_stack])
         |> assign(:redo_stack, [])}
      end
    else
      _ -> {:noreply, socket}
    end
  end

  defp paint_tile(socket, _x, _y), do: {:noreply, socket}

  defp apply_tool(socket, x, y) do
    layer = active_layer(socket.assigns.map, socket.assigns.selected_layer_id)

    cond do
      is_nil(layer) ->
        {:noreply, socket}

      socket.assigns.tool == "select" ->
        {:noreply, assign(socket, :selection, normalize_selection({x, y}))}

      socket.assigns.tool == "select_area" ->
        result = select_area(socket, x, y)
        {:selection, selection} = result
        {:noreply, assign(socket, :selection, selection)}

      not layer_editable?(layer) ->
        {:noreply, put_flash(socket, :error, "Layer is locked or hidden.")}

      true ->
        do_apply_tool(socket, layer, x, y)
    end
  end

  defp do_apply_tool(socket, layer, x, y) do
    tiles = layer.tiles

    new_tiles =
      case socket.assigns.tool do
        "place" ->
          Maps.put_tile(tiles, x, y, %{
            asset_id: socket.assigns.selected_asset_id,
            tile_index: socket.assigns.selected_tile,
            rotation: 0
          })

        "delete" ->
          Maps.delete_tile(tiles, x, y)

        "rotate" ->
          update_tile(tiles, x, y, fn tile ->
            Map.update(tile, "rotation", 90, &rem(&1 + 90, 360))
          end)

        "clone" ->
          paste_clipboard(tiles, socket.assigns.clipboard, x, y)
      end

    if new_tiles == tiles do
      {:noreply, assign(socket, :selection, normalize_selection({x, y}))}
    else
      {:ok, updated_layer} = Maps.update_layer_tiles(layer, new_tiles)

      {:noreply,
       socket
       |> replace_layer(updated_layer)
       |> assign(:undo_stack, [{layer.id, tiles} | socket.assigns.undo_stack])
       |> assign(:redo_stack, [])}
    end
  end

  defp undo(%{assigns: %{undo_stack: [{layer_id, previous} | rest]}} = socket) do
    layer = Maps.get_layer_for_map!(socket.assigns.map.id, layer_id)
    current = layer.tiles
    {:ok, updated} = Maps.update_layer_tiles(layer, previous)

    {:noreply,
     socket
     |> replace_layer(updated)
     |> assign(:undo_stack, rest)
     |> assign(:redo_stack, [{layer_id, current} | socket.assigns.redo_stack])}
  end

  defp undo(socket), do: {:noreply, socket}

  defp redo(%{assigns: %{redo_stack: [{layer_id, next} | rest]}} = socket) do
    layer = Maps.get_layer_for_map!(socket.assigns.map.id, layer_id)
    current = layer.tiles
    {:ok, updated} = Maps.update_layer_tiles(layer, next)

    {:noreply,
     socket
     |> replace_layer(updated)
     |> assign(:redo_stack, rest)
     |> assign(:undo_stack, [{layer_id, current} | socket.assigns.undo_stack])}
  end

  defp redo(socket), do: {:noreply, socket}

  defp clone_selection(%{assigns: %{selection: selection}} = socket)
       when not is_nil(selection) do
    case active_layer(socket.assigns.map, socket.assigns.selected_layer_id) do
      nil ->
        assign(socket, :tool, "clone")

      layer ->
        clipboard =
          selection
          |> selection_cells()
          |> Enum.reduce(%{}, fn {x, y}, acc ->
            case Maps.tile_at(layer.tiles, x, y) do
              nil -> acc
              tile -> Map.put(acc, Maps.key(x - selection.x1, y - selection.y1), tile)
            end
          end)

        assign(socket, clipboard: clipboard, tool: "clone")
    end
  end

  defp clone_selection(socket), do: assign(socket, :tool, "clone")

  defp paste_clipboard(tiles, clipboard, x, y) do
    Enum.reduce(clipboard, tiles, fn {offset, tile}, acc ->
      [dx, dy] = offset |> String.split(",", parts: 2) |> Enum.map(&String.to_integer/1)
      Map.put(acc, Maps.key(x + dx, y + dy), tile)
    end)
  end

  defp update_tile(tiles, x, y, fun) do
    key = Maps.key(x, y)

    case Map.fetch(tiles, key) do
      {:ok, tile} -> Map.put(tiles, key, fun.(tile))
      :error -> tiles
    end
  end

  defp apply_to_selection(socket, fun) do
    selection = socket.assigns.selection
    layer = active_layer(socket.assigns.map, socket.assigns.selected_layer_id)

    cond do
      is_nil(selection) ->
        {:noreply, socket}

      is_nil(layer) ->
        {:noreply, socket}

      not layer_editable?(layer) ->
        {:noreply, put_flash(socket, :error, "Layer is locked or hidden.")}

      true ->
        previous = layer.tiles
        {:ok, updated} = fun.(layer, selection_cells(selection))

        if updated.tiles == previous do
          {:noreply, socket}
        else
          {:noreply,
           socket
           |> replace_layer(updated)
           |> assign(:undo_stack, [{layer.id, previous} | socket.assigns.undo_stack])
           |> assign(:redo_stack, [])}
        end
    end
  end

  defp move_selection(socket, direction) do
    selection = socket.assigns.selection
    from_layer = active_layer(socket.assigns.map, socket.assigns.selected_layer_id)

    with %{} <- selection,
         %{} = from <- from_layer,
         true <- layer_editable?(from),
         %{} = to <- neighbor_layer(socket.assigns.map, from, direction),
         true <- layer_editable?(to) do
      previous_from = from.tiles
      previous_to = to.tiles
      cells = selection_cells(selection)

      case Maps.move_tiles_between_layers(from, to, cells) do
        {:ok, {updated_from, updated_to}} ->
          if updated_from.tiles == previous_from and updated_to.tiles == previous_to do
            {:noreply, socket}
          else
            {:noreply,
             socket
             |> replace_layer(updated_from)
             |> replace_layer(updated_to)
             |> assign(:selected_layer_id, updated_to.id)
             |> assign(:undo_stack, [
               {from.id, previous_from},
               {to.id, previous_to}
               | socket.assigns.undo_stack
             ])
             |> assign(:redo_stack, [])}
          end

        {:error, _} ->
          {:noreply, put_flash(socket, :error, "Could not move tiles.")}
      end
    else
      _ ->
        message =
          case direction do
            :up -> "No layer above to move tiles into."
            :down -> "No layer below to move tiles into."
          end

        {:noreply, put_flash(socket, :error, message)}
    end
  end

  defp neighbor_layer(map, %{z_index: z}, :up) do
    map.layers
    |> Enum.filter(&(&1.z_index > z))
    |> Enum.min_by(& &1.z_index, fn -> nil end)
  end

  defp neighbor_layer(map, %{z_index: z}, :down) do
    map.layers
    |> Enum.filter(&(&1.z_index < z))
    |> Enum.max_by(& &1.z_index, fn -> nil end)
  end

  defp move_layer(socket, layer_id, direction) do
    layers = display_layers(socket.assigns.map)
    index = Enum.find_index(layers, &(&1.id == layer_id))
    target = index && index + direction

    if is_nil(index) or target < 0 or target >= length(layers) do
      {:noreply, socket}
    else
      reordered =
        layers
        |> List.replace_at(index, Enum.at(layers, target))
        |> List.replace_at(target, Enum.at(layers, index))

      {:ok, _} = Maps.reorder_layers(socket.assigns.map.id, Enum.map(reordered, & &1.id))
      {:noreply, refresh_map(socket)}
    end
  end

  # === Read helpers ===

  defp selected_asset(tilesets, id), do: Enum.find(tilesets, &(&1.id == id))

  defp cells(width, height), do: for(y <- 0..(height - 1), x <- 0..(width - 1), do: {x, y})

  defp active_layer(map, layer_id) do
    Enum.find(map.layers, &(&1.id == layer_id))
  end

  defp layer_editable?(nil), do: false
  defp layer_editable?(layer), do: layer.visible and not layer.locked

  @doc false
  # Display order: highest z first (top of stack).
  defp display_layers(map) do
    Enum.sort_by(map.layers, fn l -> {-l.z_index, l.id} end)
  end

  # Render order: lowest z first (bottom of stack drawn first, so higher z paints on top).
  defp visible_layers(map) do
    map.layers
    |> Enum.filter(& &1.visible)
    |> Enum.sort_by(fn l -> {l.z_index, l.id} end)
  end

  defp select_area(%{assigns: %{selection: nil}}, x, y),
    do: {:selection, normalize_selection({x, y})}

  defp select_area(%{assigns: %{selection: %{x1: x, y1: y}}}, x, y),
    do: {:selection, normalize_selection({x, y})}

  defp select_area(%{assigns: %{selection: selection}}, x, y),
    do: {:selection, normalize_selection({selection.x1, selection.y1}, {x, y})}

  defp normalize_selection({x, y}), do: %{x1: x, y1: y, x2: x, y2: y}

  defp normalize_selection({x1, y1}, {x2, y2}) do
    %{x1: min(x1, x2), y1: min(y1, y2), x2: max(x1, x2), y2: max(y1, y2)}
  end

  defp selected_cell?(nil, _x, _y), do: false

  defp selected_cell?(selection, x, y) do
    x >= selection.x1 and x <= selection.x2 and y >= selection.y1 and y <= selection.y2
  end

  defp selection_cells(selection) do
    for y <- selection.y1..selection.y2, x <- selection.x1..selection.x2, do: {x, y}
  end

  defp layer_cell_style(tilesets, layer, x, y) do
    case Maps.tile_at(layer.tiles, x, y) do
      nil ->
        "display: none;"

      %{"asset_id" => asset_id, "tile_index" => tile_index, "rotation" => rotation} ->
        asset = selected_asset(tilesets, asset_id)

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

  defp tile_indexes(asset) do
    Map.get(asset.metadata, "tile_indexes", Enum.to_list(0..(asset.metadata["tile_count"] - 1)))
  end

  defp truthy?(value), do: value in [true, "true"]

  defp to_int(v) when is_integer(v), do: v
  defp to_int(v) when is_binary(v), do: String.to_integer(v)

  defp refresh_map(socket, opts \\ []) do
    designer = socket.assigns.current_designer
    map = Maps.get_map!(designer.id, socket.assigns.map.id)

    selected =
      cond do
        Keyword.has_key?(opts, :select) ->
          Keyword.get(opts, :select)

        Keyword.has_key?(opts, :prefer_other_than) ->
          excluded = Keyword.get(opts, :prefer_other_than)

          map.layers
          |> Enum.reject(&(&1.id == excluded))
          |> List.first()
          |> then(&(&1 && &1.id))

        true ->
          # Keep current selection if still present; else pick the first.
          if Enum.any?(map.layers, &(&1.id == socket.assigns.selected_layer_id)) do
            socket.assigns.selected_layer_id
          else
            map.layers |> List.first() |> then(&(&1 && &1.id))
          end
      end

    socket
    |> assign(:map, map)
    |> assign(:selected_layer_id, selected)
  end

  defp replace_layer(socket, updated_layer) do
    map = socket.assigns.map
    new_layers = Enum.map(map.layers, fn l -> if l.id == updated_layer.id, do: updated_layer, else: l end)
    assign(socket, :map, %{map | layers: new_layers})
  end
end
