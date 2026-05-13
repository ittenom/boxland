defmodule BoxlandWeb.SandboxLive do
  use BoxlandWeb, :live_view

  alias Boxland.{Levels, Library, Maps, Repo}

  def mount(%{"id" => id}, _session, socket) do
    designer = socket.assigns.current_designer

    level =
      designer.id
      |> Levels.get_level!(id)
      |> Map.update!(:map, &Repo.preload(&1, :layers))

    spawn = Enum.find(level.entities, &(preset(&1) == "spawn"))
    player = if spawn, do: {div(spawn.pos_x, 32), div(spawn.pos_y, 32)}, else: {0, 0}

    {:ok,
     socket
     |> assign(:level, level)
     |> assign(:tilesets, Library.list_tilesets(designer.id))
     |> assign(:player, player)
     |> assign(:message, nil)}
  end

  def handle_event("move", %{"dx" => dx, "dy" => dy}, socket) do
    {x, y} = socket.assigns.player
    level = socket.assigns.level
    next = {x + String.to_integer(dx), y + String.to_integer(dy)}

    cond do
      out_of_bounds?(level, next) or
          Levels.blocked?(level, socket.assigns.tilesets, elem(next, 0), elem(next, 1)) ->
        {:noreply, assign(socket, :message, "Blocked")}

      true ->
        {:noreply, socket |> assign(:player, next) |> assign(:message, inspect_tile(level, next))}
    end
  end

  def render(assigns) do
    assigns = assign(assigns, :layer, Maps.primary_layer(assigns.level.map))

    ~H"""
    <Layouts.app flash={@flash} current_scope={%{designer: @current_designer}}>
      <section phx-window-keydown="key" class="space-y-4">
        <div class="flex items-center justify-between">
          <div>
            <p class="text-sm font-semibold text-primary">Sandbox</p>
            <h1 class="text-3xl font-semibold tracking-tight">{@level.name}</h1>
          </div>
          <.link navigate={~p"/app/levels/#{@level.id}"} class="btn btn-ghost">Back to editor</.link>
        </div>

        <p :if={@message} class="alert alert-info">{@message}</p>

        <div class="overflow-auto rounded-box bg-base-200 p-4">
          <div
            class="grid w-fit gap-px"
            style={"grid-template-columns: repeat(#{@level.map.width}, 32px);"}
          >
            <div
              :for={{x, y} <- cells(@level.map.width, @level.map.height)}
              class="relative h-8 w-8 border border-base-300 bg-base-100 bg-no-repeat"
              style={cell_style(@tilesets, @layer.tiles, x, y)}
            >
              <span
                :if={@player == {x, y}}
                class="absolute inset-1 rounded-full bg-secondary text-center text-xs font-bold text-secondary-content"
              >
                @
              </span>
              <span
                :for={entity <- entities_at(@level.entities, x, y)}
                class="absolute bottom-0 right-0 rounded bg-primary px-1 text-[10px] text-primary-content"
              >
                {preset_label(entity)}
              </span>
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

  defp out_of_bounds?(level, {x, y}),
    do: x < 0 or y < 0 or x >= level.map.width or y >= level.map.height

  defp cells(width, height), do: for(y <- 0..(height - 1), x <- 0..(width - 1), do: {x, y})

  defp inspect_tile(level, {x, y}) do
    level.entities
    |> entities_at(x, y)
    |> Enum.find_value(fn entity ->
      case preset(entity) do
        "portal" -> "Portal reached"
        "sign" -> "Sign"
        "collectible" -> "Collectible"
        _ -> nil
      end
    end)
  end

  defp entities_at(entities, x, y) do
    Enum.filter(entities, &(div(&1.pos_x, 32) == x and div(&1.pos_y, 32) == y))
  end

  defp preset(entity) do
    Enum.find_value(entity.entity_type.components, fn
      %{"preset" => preset} -> preset
      _ -> nil
    end)
  end

  defp preset_label(entity), do: preset(entity) |> String.first() |> String.upcase()

  defp cell_style(tilesets, tiles, x, y) do
    case Maps.tile_at(tiles, x, y) do
      nil ->
        ""

      %{"asset_id" => asset_id, "tile_index" => tile_index, "rotation" => rotation} ->
        asset = Enum.find(tilesets, &(&1.id == asset_id))
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
end
