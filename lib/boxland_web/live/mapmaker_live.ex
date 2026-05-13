defmodule BoxlandWeb.MapmakerLive do
  use BoxlandWeb, :live_view

  alias Boxland.{Library, Maps}

  @tools ~w(place delete rotate select_area clone select)

  def mount(%{"id" => id}, _session, socket) do
    designer = socket.assigns.current_designer
    map = Maps.get_map!(designer.id, id)
    layer = Maps.primary_layer(map)
    tilesets = Library.list_tilesets(designer.id)

    {:ok,
     socket
     |> assign(:map, map)
     |> assign(:layer, layer)
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

  def render(assigns) do
    ~H"""
    <Layouts.app flash={@flash} current_scope={%{designer: @current_designer}}>
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
          <button
            id="map-undo-button"
            phx-click="hotkey"
            phx-value-key="z"
            phx-value-ctrlKey="true"
            class="btn btn-sm"
          >
            <.icon name="hero-arrow-uturn-left" class="size-4" />
          </button>
          <button
            id="map-redo-button"
            phx-click="hotkey"
            phx-value-key="z"
            phx-value-ctrlKey="true"
            phx-value-shiftKey="true"
            class="btn btn-sm"
          >
            <.icon name="hero-arrow-uturn-right" class="size-4" />
          </button>
        </div>

        <div class="grid gap-4 lg:grid-cols-[18rem_1fr]">
          <aside class="space-y-4">
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
            data-tool={@tool}
            class="overflow-auto rounded-box bg-base-200 p-2"
          >
            <div
              class="grid w-fit gap-0"
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
                  "h-8 w-8 touch-none border border-base-300/60 bg-base-100 bg-no-repeat transition-[filter] hover:brightness-105",
                  selected_cell?(@selection, x, y) && "border-primary ring-1 ring-primary",
                  !selected_cell?(@selection, x, y) && "border-base-300/60"
                ]}
                style={cell_style(@tilesets, @layer.tiles, x, y)}
              />
            </div>
          </div>
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

  defp paint_tile(%{assigns: %{tool: "place", selected_asset_id: asset_id}} = socket, x, y)
       when not is_nil(asset_id) do
    tiles = socket.assigns.layer.tiles

    new_tiles =
      Maps.put_tile(tiles, x, y, %{
        asset_id: asset_id,
        tile_index: socket.assigns.selected_tile,
        rotation: 0
      })

    if new_tiles == tiles do
      {:noreply, socket}
    else
      {:ok, layer} = Maps.update_layer_tiles(socket.assigns.layer, new_tiles)

      {:noreply,
       socket
       |> assign(:layer, layer)
       |> assign(:undo_stack, [tiles | socket.assigns.undo_stack])
       |> assign(:redo_stack, [])}
    end
  end

  defp paint_tile(socket, _x, _y), do: {:noreply, socket}

  defp apply_tool(socket, x, y) do
    tiles = socket.assigns.layer.tiles

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

        "select_area" ->
          select_area(socket, x, y)

        "select" ->
          tiles

        "clone" ->
          paste_clipboard(tiles, socket.assigns.clipboard, x, y)
      end

    cond do
      match?({:selection, _}, new_tiles) ->
        {:selection, selection} = new_tiles
        {:noreply, assign(socket, :selection, selection)}

      new_tiles == tiles ->
        {:noreply, assign(socket, :selection, normalize_selection({x, y}))}

      true ->
        {:ok, layer} = Maps.update_layer_tiles(socket.assigns.layer, new_tiles)

        {:noreply,
         socket
         |> assign(:layer, layer)
         |> assign(:undo_stack, [tiles | socket.assigns.undo_stack])
         |> assign(:redo_stack, [])}
    end
  end

  defp undo(%{assigns: %{undo_stack: [previous | rest]}} = socket) do
    current = socket.assigns.layer.tiles
    {:ok, layer} = Maps.update_layer_tiles(socket.assigns.layer, previous)

    {:noreply,
     socket
     |> assign(:layer, layer)
     |> assign(:undo_stack, rest)
     |> assign(:redo_stack, [current | socket.assigns.redo_stack])}
  end

  defp undo(socket), do: {:noreply, socket}

  defp redo(%{assigns: %{redo_stack: [next | rest]}} = socket) do
    current = socket.assigns.layer.tiles
    {:ok, layer} = Maps.update_layer_tiles(socket.assigns.layer, next)

    {:noreply,
     socket
     |> assign(:layer, layer)
     |> assign(:redo_stack, rest)
     |> assign(:undo_stack, [current | socket.assigns.undo_stack])}
  end

  defp redo(socket), do: {:noreply, socket}

  defp clone_selection(%{assigns: %{selection: selection, layer: layer}} = socket)
       when not is_nil(selection) do
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

  defp selected_asset(tilesets, id), do: Enum.find(tilesets, &(&1.id == id))

  defp cells(width, height), do: for(y <- 0..(height - 1), x <- 0..(width - 1), do: {x, y})

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

  defp cell_style(tilesets, tiles, x, y) do
    case Maps.tile_at(tiles, x, y) do
      nil ->
        ""

      %{"asset_id" => asset_id, "tile_index" => tile_index, "rotation" => rotation} ->
        asset = selected_asset(tilesets, asset_id)
        tile_style(asset, tile_index) <> " transform: rotate(#{rotation}deg);"
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
end
