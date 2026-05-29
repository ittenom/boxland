defmodule BoxlandWeb.MapmakerLive do
  use BoxlandWeb, :live_view

  import BoxlandWeb.Components.Ide

  alias Boxland.{Library, Maps, Repo}

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
     |> assign(:clipboard, nil)
     |> assign(:move, nil)
     |> assign(:cursor_cell, nil)
     |> assign(:undo_stack, [])
     |> assign(:redo_stack, [])
     |> assign(:closed_sections, MapSet.new())
     |> assign(:context_menu, nil)}
  end

  def handle_event("tool", %{"tool" => tool}, socket) when tool in @tools do
    {:noreply, assign(socket, :tool, tool)}
  end

  def handle_event("undo", _params, socket), do: undo(socket)
  def handle_event("redo", _params, socket), do: redo(socket)

  def handle_event("toggle_section", %{"id" => id}, socket) do
    closed = socket.assigns.closed_sections

    closed =
      if MapSet.member?(closed, id), do: MapSet.delete(closed, id), else: MapSet.put(closed, id)

    {:noreply, assign(socket, :closed_sections, closed)}
  end

  def handle_event("open_context_menu", %{"kind" => kind, "id" => id, "x" => x, "y" => y}, socket) do
    socket =
      if kind == "layer", do: assign(socket, :selected_layer_id, safe_int(id, nil)), else: socket

    {:noreply, assign(socket, :context_menu, %{kind: kind, id: id, x: trunc(x), y: trunc(y)})}
  end

  def handle_event("close_context_menu", _params, socket) do
    {:noreply, assign(socket, :context_menu, nil)}
  end

  def handle_event(
        "tree_reorder",
        %{"group" => "layers", "id" => id, "before_id" => before_id},
        socket
      ) do
    ids = reordered_layer_ids(socket.assigns.map, id, before_id)
    {:ok, _} = Maps.reorder_layers(socket.assigns.map.id, ids)
    {:noreply, refresh_map(socket)}
  end

  def handle_event("tree_reorder", _params, socket), do: {:noreply, socket}

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
        cond do
          socket.assigns.move != nil ->
            cancel_move(socket)

          socket.assigns.tool == "clone" ->
            {:noreply, assign(socket, tool: "select_area", cursor_cell: nil)}

          true ->
            {:noreply, assign(socket, :selection, nil)}
        end

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
    {:noreply,
     socket
     |> assign(:selected_asset_id, String.to_integer(asset_id))
     |> assign(:selected_tile, 0)
     |> assign(:tool, "place")
     |> assign(:selection, nil)
     |> assign(:move, nil)
     |> assign(:cursor_cell, nil)}
  end

  def handle_event("select_tile", %{"tile" => tile}, socket) do
    {:noreply,
     socket
     |> assign(:selected_tile, String.to_integer(tile))
     |> assign(:tool, "place")
     |> assign(:selection, nil)
     |> assign(:move, nil)
     |> assign(:cursor_cell, nil)}
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
        block = Maps.effective_block(socket.assigns.map, selection_cells(selection), layer.id)

        case Maps.delete_cells_across_layers(socket.assigns.map, block) do
          {:ok, updates} ->
            {:noreply, socket |> apply_layer_updates(updates) |> push_undo_for(updates)}

          {:error, _} ->
            {:noreply, socket}
        end
    end
  end

  def handle_event("selection_rotate", _params, socket) do
    selection = socket.assigns.selection
    layer = active_layer(socket.assigns.map, socket.assigns.selected_layer_id)
    map = socket.assigns.map

    cond do
      is_nil(selection) ->
        {:noreply, socket}

      is_nil(layer) ->
        {:noreply, socket}

      not layer_editable?(layer) ->
        {:noreply, put_flash(socket, :error, "Layer is locked or hidden.")}

      true ->
        block = Maps.effective_block(map, selection_cells(selection), layer.id)
        bbox = block_bbox(block, selection)

        case Maps.rotate_block_across_layers(map, block, bbox, map.width, map.height) do
          {:ok, {updates, new_bbox}} when map_size(updates) > 0 ->
            {:noreply,
             socket
             |> apply_layer_updates(updates)
             |> push_undo_for(updates)
             |> assign(:selection, new_bbox)}

          {:ok, _} ->
            {:noreply, socket}

          {:error, :out_of_bounds} ->
            {:noreply, put_flash(socket, :error, "Rotation would extend past the map edge.")}

          {:error, :layer_not_editable} ->
            {:noreply,
             put_flash(socket, :error, "One of the affected layers is locked or hidden.")}

          {:error, _} ->
            {:noreply, socket}
        end
    end
  end

  def handle_event("selection_move_up", _params, socket) do
    move_selection(socket, :up)
  end

  def handle_event("selection_move_down", _params, socket) do
    move_selection(socket, :down)
  end

  def handle_event("selection_group", _params, socket) do
    selection = socket.assigns.selection

    if is_nil(selection) do
      {:noreply, socket}
    else
      cells = selection_cells(selection)
      {:ok, _gid} = Maps.group_cells(socket.assigns.map, cells)
      {:noreply, socket |> refresh_map() |> put_flash(:info, "Tiles grouped.")}
    end
  end

  def handle_event("selection_ungroup", _params, socket) do
    selection = socket.assigns.selection

    if is_nil(selection) do
      {:noreply, socket}
    else
      cells = selection_cells(selection)

      case Maps.ungroup_cells(socket.assigns.map, cells) do
        {:ok, []} ->
          {:noreply, socket}

        {:ok, _gids} ->
          {:noreply, socket |> refresh_map() |> put_flash(:info, "Tiles ungrouped.")}
      end
    end
  end

  def handle_event("selection_move_begin", _params, socket) do
    begin_move(socket)
  end

  def handle_event("selection_move_cancel", _params, socket) do
    cancel_move(socket)
  end

  def handle_event("cursor_at", %{"x" => x, "y" => y}, socket) do
    if socket.assigns.tool in ["move", "clone"] do
      {:noreply, assign(socket, :cursor_cell, {to_int(x), to_int(y)})}
    else
      {:noreply, socket}
    end
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
    highlighted = highlighted_cells(assigns.map, assigns.selection)
    affected = affected_layer_ids(assigns.map, assigns.selection, assigns.selected_layer_id)

    assigns =
      assigns
      |> assign(:highlighted_cells, highlighted)
      |> assign(:affected_layer_ids, affected)

    ~H"""
    <div id="mapmaker-root" phx-hook="ContextMenu" phx-window-keydown="hotkey">
      <.ide_shell flash={@flash}>
        <:activity>
          <.ide_rail_nav active={:maps} />
          <div class="flex-1"></div>
          <.rail_item icon="hero-cube" label="Make level" navigate={~p"/app/levels"} />
        </:activity>

        <:explorer>
          <.panel title="Explorer">
            <:actions>
              <button
                id="add-layer-button"
                phx-click="add_layer"
                class="ide-toolbtn !p-1"
                title="Add layer"
              >
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
                  :for={layer <- display_layers(@map)}
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
              title="Tilesets"
              open={section_open?(@closed_sections, "tilesets")}
              phx-click="toggle_section"
              phx-value-id="tilesets"
            >
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
            </.panel_section>
          </.panel>
        </:explorer>

        <:viewport>
          <.ide_toolbar id="map-toolbar">
            <h1 class="mr-2 text-sm font-semibold text-base-content">{@map.name}</h1>
            <.ide_tool_button
              id="map-tool-place"
              icon="hero-pencil"
              label="Place"
              active={@tool == "place"}
              phx-click="tool"
              phx-value-tool="place"
              title="Place (P)"
            />
            <.ide_tool_button
              id="map-tool-delete"
              icon="hero-x-mark"
              label="Delete"
              active={@tool == "delete"}
              phx-click="tool"
              phx-value-tool="delete"
              title="Delete (X)"
            />
            <.ide_tool_button
              id="map-tool-rotate"
              icon="hero-arrow-path"
              label="Rotate"
              active={@tool == "rotate"}
              phx-click="tool"
              phx-value-tool="rotate"
              title="Rotate (R)"
            />
            <.ide_tool_button
              id="map-tool-select_area"
              icon="hero-square-2-stack"
              label="Area"
              active={@tool == "select_area"}
              phx-click="tool"
              phx-value-tool="select_area"
              title="Select area (S)"
            />
            <.ide_tool_button
              id="map-tool-select"
              icon="hero-cursor-arrow-rays"
              label="Select"
              active={@tool == "select"}
              phx-click="tool"
              phx-value-tool="select"
              title="Select (V)"
            />
            <span class="mx-1 h-5 w-px bg-base-content/15"></span>
            <.ide_tool_button
              id="map-copy-button"
              icon="hero-clipboard-document"
              label="Copy"
              active={@tool == "clone"}
              phx-click="copy_selection"
              title="Copy selection"
            />
            <.ide_tool_button
              id="map-paste-button"
              icon="hero-clipboard-document-check"
              label="Paste"
              active={@tool == "clone"}
              phx-click="paste_selection"
              title="Paste copied selection"
            />
            <.ide_tool_button
              id="map-undo-button"
              icon="hero-arrow-uturn-left"
              phx-click="undo"
              title="Undo (⌘Z)"
            />
            <.ide_tool_button
              id="map-redo-button"
              icon="hero-arrow-uturn-right"
              phx-click="redo"
              title="Redo (⇧⌘Z)"
            />
            <div class="flex-1"></div>
            <.link navigate={~p"/app/levels"} class="ide-toolbtn ide-toolbtn-active">
              <.icon name="hero-cube" class="size-4" /> Make level
            </.link>
          </.ide_toolbar>

          <div
            id="mapmaker-canvas"
            phx-hook="MapmakerCanvas"
            data-tool={
              if active_layer(@map, @selected_layer_id) |> layer_editable?(),
                do: @tool,
                else: "select"
            }
            class="min-h-0 flex-1 overflow-auto p-3"
          >
            <div
              :if={@move}
              class="mb-2 flex items-center justify-between rounded-md bg-primary/15 px-3 py-1.5 text-xs text-primary-content"
            >
              <span class="text-primary">
                Moving {length(@move.records)} tile{if length(@move.records) == 1, do: "", else: "s"} —
                click to drop, Esc to cancel.
              </span>
              <button type="button" phx-click="selection_move_cancel" class="btn btn-ghost btn-xs">
                Cancel
              </button>
            </div>
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
                  "relative h-8 w-8 touch-none bg-base-100 transition-[filter] hover:brightness-105",
                  cell_highlighted?(@highlighted_cells, x, y) &&
                    "border-primary ring-1 ring-primary z-10"
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

              <.ghost_preview
                :if={@move && @cursor_cell}
                id="move-ghost"
                records={@move.records}
                w={@move.w}
                h={@move.h}
                cursor_cell={@cursor_cell}
                tilesets={@tilesets}
              />

              <.ghost_preview
                :if={@tool == "clone" && @clipboard && @clipboard.records != [] && @cursor_cell}
                id="clone-ghost"
                records={@clipboard.records}
                w={@clipboard.w}
                h={@clipboard.h}
                cursor_cell={@cursor_cell}
                tilesets={@tilesets}
                ring_class="ring-secondary/70"
              />
            </div>
          </div>
        </:viewport>

        <:inspector>
          <.layer_inspector layer={active_layer(@map, @selected_layer_id)} />
        </:inspector>

        <:status>
          <span class="font-mono">tool: {@tool}</span>
          <span :if={@selection} class="font-mono">
            sel: {@selection.x2 - @selection.x1 + 1}×{@selection.y2 - @selection.y1 + 1}
          </span>
          <span class="flex-1"></span>
          <span class="text-base-content/50">
            Right-click a layer for actions · drag layers to reorder
          </span>
        </:status>
      </.ide_shell>

      <.context_menu open={@context_menu != nil} x={ctx(@context_menu, :x)} y={ctx(@context_menu, :y)}>
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
          icon="hero-lock-closed"
          phx-click="toggle_lock"
          phx-value-id={ctx(@context_menu, :id)}
        >
          Toggle lock
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
      </.context_menu>
    </div>
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
    can_up =
      assigns.selected_layer && neighbor_layer(assigns.map, assigns.selected_layer, :up) != nil

    can_down =
      assigns.selected_layer && neighbor_layer(assigns.map, assigns.selected_layer, :down) != nil

    w = assigns.selection.x2 - assigns.selection.x1 + 1
    h = assigns.selection.y2 - assigns.selection.y1 + 1

    has_groups =
      assigns.selection
      |> then(&Maps.group_ids_at_cells(assigns.map, selection_cells_for_menu(&1)))
      |> Kernel.!=([])

    assigns =
      assign(assigns,
        can_up: can_up,
        can_down: can_down,
        dims: "#{w}×#{h}",
        has_groups: has_groups
      )

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
        :if={!@has_groups}
        type="button"
        id="selection-group"
        phx-click="selection_group"
        class="btn btn-ghost btn-xs px-1"
        title="Group tiles"
        aria-label="Group tiles"
      >
        <.icon name="hero-link" class="size-3.5" />
      </button>
      <button
        :if={@has_groups}
        type="button"
        id="selection-ungroup"
        phx-click="selection_ungroup"
        class="btn btn-ghost btn-xs px-1 text-warning"
        title="Ungroup tiles"
        aria-label="Ungroup tiles"
      >
        <.icon name="hero-link-slash" class="size-3.5" />
      </button>
      <span class="mx-0.5 h-4 w-px bg-base-300" />
      <button
        type="button"
        id="selection-move"
        phx-click="selection_move_begin"
        class="btn btn-ghost btn-xs px-1"
        title="Pick up tiles to move"
        aria-label="Pick up tiles to move"
      >
        <.icon name="hero-arrows-pointing-out" class="size-3.5" />
      </button>
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

  attr :id, :string, required: true
  attr :records, :list, required: true
  attr :w, :integer, required: true
  attr :h, :integer, required: true
  attr :cursor_cell, :any, required: true
  attr :tilesets, :list, required: true
  attr :ring_class, :string, default: "ring-primary/70"

  defp ghost_preview(assigns) do
    {cx, cy} = assigns.cursor_cell
    assigns = assign(assigns, cx: cx, cy: cy)

    ~H"""
    <div
      id={@id}
      class={["pointer-events-none absolute rounded ring-2", @ring_class]}
      style={"top: #{@cy * 32}px; left: #{@cx * 32}px; width: #{@w * 32}px; height: #{@h * 32}px;"}
    >
      <span
        :for={rec <- @records}
        class="absolute h-8 w-8 bg-no-repeat"
        style={ghost_record_style(@tilesets, rec)}
      />
    </div>
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

      socket.assigns.tool == "move" and socket.assigns.move != nil ->
        place_move(socket, x, y)

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
    case socket.assigns.tool do
      "clone" ->
        apply_clone(socket, layer, x, y)

      _ ->
        apply_single_cell(socket, layer, x, y)
    end
  end

  defp apply_single_cell(socket, layer, x, y) do
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

  defp apply_clone(socket, fallback_layer, anchor_x, anchor_y) do
    clipboard = socket.assigns.clipboard

    if is_nil(clipboard) or clipboard.records == [] do
      {:noreply, socket}
    else
      map = socket.assigns.map

      case Maps.place_block(
             map,
             clipboard.records,
             {anchor_x, anchor_y},
             fallback_layer.id
           ) do
        {:ok, updates} when map_size(updates) > 0 ->
          {:noreply, socket |> apply_layer_updates(updates) |> push_undo_for(updates)}

        {:ok, _} ->
          {:noreply, socket}

        {:error, :layer_not_editable} ->
          {:noreply, put_flash(socket, :error, "Destination layer is locked or hidden.")}

        {:error, _} ->
          {:noreply, socket}
      end
    end
  end

  # Apply a map of layer_id => {previous_tiles, updated_layer} to socket assigns.
  defp apply_layer_updates(socket, updates) do
    Enum.reduce(updates, socket, fn {_lid, {_prev, updated}}, acc ->
      replace_layer(acc, updated)
    end)
  end

  # Push undo entries for each layer that actually changed.
  defp push_undo_for(socket, updates) do
    entries =
      for {lid, {prev, updated}} <- updates, prev != updated.tiles do
        {lid, prev}
      end

    case entries do
      [] ->
        socket

      _ ->
        socket
        |> assign(:undo_stack, entries ++ socket.assigns.undo_stack)
        |> assign(:redo_stack, [])
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
        records = collect_block_records(socket.assigns.map, selection, layer.id)

        if records == [] do
          assign(socket, :tool, "clone")
        else
          {origin_x, origin_y} = block_origin(records, selection)

          clipboard = %{
            records:
              Enum.map(records, fn {lid, x, y, tile} ->
                %{layer_id: lid, dx: x - origin_x, dy: y - origin_y, tile: tile}
              end),
            w: selection.x2 - selection.x1 + 1,
            h: selection.y2 - selection.y1 + 1
          }

          assign(socket, clipboard: clipboard, tool: "clone")
        end
    end
  end

  defp clone_selection(socket), do: assign(socket, :tool, "clone")

  # Collect the records (across layers) the user actually means: rect cells on
  # active layer + every group member of groups touched by the rect.
  defp collect_block_records(map, selection, active_layer_id) do
    Maps.effective_block(map, selection_cells(selection), active_layer_id)
  end

  # When the rect itself defines the bounding box, anchor at the rect's
  # top-left so group members that sit outside the rect still drop in the
  # right place relative to it.
  defp block_origin(_records, %{x1: x1, y1: y1}), do: {x1, y1}

  defp update_tile(tiles, x, y, fun) do
    key = Maps.key(x, y)

    case Map.fetch(tiles, key) do
      {:ok, tile} -> Map.put(tiles, key, fun.(tile))
      :error -> tiles
    end
  end

  defp begin_move(socket) do
    selection = socket.assigns.selection
    layer = active_layer(socket.assigns.map, socket.assigns.selected_layer_id)

    with %{} <- selection,
         %{} <- layer,
         true <- layer_editable?(layer) do
      block = Maps.effective_block(socket.assigns.map, selection_cells(selection), layer.id)

      if block == [] do
        {:noreply, socket}
      else
        {origin_x, origin_y} = {selection.x1, selection.y1}

        records =
          Enum.map(block, fn {lid, x, y, tile} ->
            %{
              layer_id: lid,
              source_x: x,
              source_y: y,
              dx: x - origin_x,
              dy: y - origin_y,
              tile: tile
            }
          end)

        move = %{
          records: records,
          source_origin: {origin_x, origin_y},
          w: selection.x2 - selection.x1 + 1,
          h: selection.y2 - selection.y1 + 1
        }

        {:noreply,
         socket
         |> assign(:move, move)
         |> assign(:tool, "move")
         |> assign(:selection, nil)
         |> assign(:cursor_cell, nil)}
      end
    else
      _ -> {:noreply, socket}
    end
  end

  defp cancel_move(socket) do
    {:noreply,
     socket
     |> assign(:move, nil)
     |> assign(:cursor_cell, nil)
     |> assign(:tool, "select_area")}
  end

  defp place_move(socket, dest_x, dest_y) do
    move = socket.assigns.move
    map = socket.assigns.map

    new_x2 = dest_x + move.w - 1
    new_y2 = dest_y + move.h - 1

    cond do
      dest_x < 0 or dest_y < 0 or new_x2 >= map.width or new_y2 >= map.height ->
        {:noreply, put_flash(socket, :error, "Tiles would land outside the map.")}

      true ->
        active = active_layer(map, socket.assigns.selected_layer_id)
        fallback_id = (active && active.id) || hd(move.records).layer_id

        # Source cells, by their original layer, for the lift step
        source_block =
          Enum.map(move.records, fn r -> {r.layer_id, r.source_x, r.source_y, r.tile} end)

        # If the whole lifted set lived on one layer and the user has switched
        # to a different active layer, retarget. Multi-layer (group) sources
        # preserve their per-tile layer.
        source_layers = move.records |> Enum.map(& &1.layer_id) |> Enum.uniq()

        place_records =
          if (length(source_layers) == 1 and active) && hd(source_layers) != active.id do
            Enum.map(move.records, fn r ->
              %{layer_id: active.id, dx: r.dx, dy: r.dy, tile: r.tile}
            end)
          else
            Enum.map(move.records, fn r ->
              %{layer_id: r.layer_id, dx: r.dx, dy: r.dy, tile: r.tile}
            end)
          end

        Repo.transaction(fn ->
          with {:ok, del_updates} <- Maps.delete_cells_across_layers(map, source_block),
               # Re-read the map so place_block sees the freshly cleared layers
               map_after_delete <- apply_layer_updates_to_struct(map, del_updates),
               {:ok, place_updates} <-
                 Maps.place_block(map_after_delete, place_records, {dest_x, dest_y}, fallback_id) do
            merge_updates(del_updates, place_updates)
          else
            {:error, :layer_not_editable} -> Repo.rollback(:layer_not_editable)
            {:error, reason} -> Repo.rollback(reason)
          end
        end)
        |> case do
          {:ok, combined} ->
            {:noreply,
             socket
             |> apply_layer_updates(combined)
             |> push_undo_for(combined)
             |> assign(:move, nil)
             |> assign(:cursor_cell, nil)
             |> assign(:tool, "select_area")
             |> assign(:selection, %{x1: dest_x, y1: dest_y, x2: new_x2, y2: new_y2})}

          {:error, :layer_not_editable} ->
            {:noreply, put_flash(socket, :error, "Destination layer is locked or hidden.")}

          {:error, _} ->
            {:noreply, put_flash(socket, :error, "Could not move tiles.")}
        end
    end
  end

  # Build an in-memory %Map{} mirroring updates that haven't been re-fetched
  # yet — used to thread place_block's view of the world after the lift.
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

  defp merge_updates(a, b) do
    Elixir.Map.merge(a, b, fn _lid, {prev_a, _}, {_, upd_b} -> {prev_a, upd_b} end)
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

  defp section_open?(closed_sections, key), do: not MapSet.member?(closed_sections, key)

  defp ctx(nil, _key), do: nil
  defp ctx(menu, key), do: Map.get(menu, key)

  defp safe_int(v, _default) when is_integer(v), do: v

  defp safe_int(v, default) when is_binary(v) do
    case Integer.parse(v) do
      {n, _} -> n
      :error -> default
    end
  end

  defp safe_int(_, default), do: default

  # New display order (top→bottom) after dropping layer `id` before `before_id`
  # (nil = move to the bottom). Returns ids in display order for reorder_layers.
  defp reordered_layer_ids(map, id, before_id) do
    id = safe_int(id, 0)
    before = before_id && safe_int(before_id, nil)
    ordered = display_layers(map) |> Enum.map(& &1.id) |> Enum.reject(&(&1 == id))

    case before && Enum.find_index(ordered, &(&1 == before)) do
      nil -> ordered ++ [id]
      idx -> List.insert_at(ordered, idx, id)
    end
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

  defp block_bbox(records, selection) do
    xs = Enum.map(records, fn {_, x, _, _} -> x end)
    ys = Enum.map(records, fn {_, _, y, _} -> y end)

    %{
      x1: Enum.min([selection.x1 | xs]),
      y1: Enum.min([selection.y1 | ys]),
      x2: Enum.max([selection.x2 | xs]),
      y2: Enum.max([selection.y2 | ys])
    }
  end

  defp ghost_record_style(tilesets, %{dx: dx, dy: dy, tile: tile}) do
    asset = Enum.find(tilesets, &(&1.id == tile["asset_id"]))

    tile_style(asset, tile["tile_index"]) <>
      " left: #{dx * 32}px;" <>
      " top: #{dy * 32}px;" <>
      " transform: rotate(#{tile["rotation"]}deg);" <>
      " opacity: 0.55;"
  end

  # Selection cells helper that's safe to call from component code.
  defp selection_cells_for_menu(%{x1: x1, y1: y1, x2: x2, y2: y2}) do
    for y <- y1..y2, x <- x1..x2, do: {x, y}
  end

  defp highlighted_cells(_map, nil), do: MapSet.new()

  defp highlighted_cells(map, selection) do
    rect = selection_cells(selection)
    rect_set = MapSet.new(rect)

    group_ids = Maps.group_ids_at_cells(map, rect)

    group_cells =
      group_ids
      |> Enum.flat_map(&Maps.find_group_members(map, &1))
      |> Enum.map(fn {_lid, x, y, _t} -> {x, y} end)
      |> MapSet.new()

    MapSet.union(rect_set, group_cells)
  end

  defp cell_highlighted?(set, x, y), do: MapSet.member?(set, {x, y})

  defp affected_layer_ids(_map, nil, _active), do: MapSet.new()
  defp affected_layer_ids(_map, _sel, nil), do: MapSet.new()

  defp affected_layer_ids(map, selection, active_layer_id) do
    map
    |> Maps.effective_block(selection_cells(selection), active_layer_id)
    |> Enum.map(fn {lid, _x, _y, _t} -> lid end)
    |> MapSet.new()
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

    new_layers =
      Enum.map(map.layers, fn l -> if l.id == updated_layer.id, do: updated_layer, else: l end)

    assign(socket, :map, %{map | layers: new_layers})
  end
end
