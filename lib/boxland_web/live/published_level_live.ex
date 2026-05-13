defmodule BoxlandWeb.PublishedLevelLive do
  use BoxlandWeb, :live_view

  alias Boxland.Levels

  def mount(%{"id" => id}, _session, socket) do
    version = Levels.latest_published_version!(id)
    snapshot = version.snapshot
    spawn = Enum.find(snapshot["entities"], &(&1["preset"] == "spawn"))
    player = if spawn, do: {div(spawn["pos_x"], 32), div(spawn["pos_y"], 32)}, else: {0, 0}

    {:ok,
     socket
     |> assign(:version, version)
     |> assign(:snapshot, snapshot)
     |> assign(:player, player)
     |> assign(:message, nil)}
  end

  def handle_event("move", %{"dx" => dx, "dy" => dy}, socket) do
    {x, y} = socket.assigns.player
    snapshot = socket.assigns.snapshot
    next = {x + String.to_integer(dx), y + String.to_integer(dy)}

    cond do
      out_of_bounds?(snapshot, next) or blocked?(snapshot, next) ->
        {:noreply, assign(socket, :message, "Blocked")}

      true ->
        {:noreply,
         socket |> assign(:player, next) |> assign(:message, inspect_tile(snapshot, next))}
    end
  end

  def render(assigns) do
    ~H"""
    <Layouts.app flash={@flash}>
      <section class="space-y-4">
        <div>
          <p class="text-sm font-semibold text-primary">Published Level v{@version.version}</p>
          <h1 class="text-3xl font-semibold tracking-tight">{@snapshot["level"]["name"]}</h1>
        </div>

        <p :if={@message} class="alert alert-info">{@message}</p>

        <div class="overflow-auto rounded-box bg-base-200 p-4">
          <div
            class="grid w-fit gap-px"
            style={"grid-template-columns: repeat(#{@snapshot["map"]["width"]}, 32px);"}
          >
            <div
              :for={{x, y} <- cells(@snapshot["map"]["width"], @snapshot["map"]["height"])}
              class="relative h-8 w-8 border border-base-300 bg-base-100 bg-no-repeat"
              style={cell_style(@snapshot, x, y)}
            >
              <span
                :if={@player == {x, y}}
                class="absolute inset-1 rounded-full bg-secondary text-center text-xs font-bold text-secondary-content"
              >
                @
              </span>
              <span
                :for={entity <- entities_at(@snapshot["entities"], x, y)}
                class="absolute bottom-0 right-0 rounded bg-primary px-1 text-[10px] text-primary-content"
              >
                {String.first(entity["preset"]) |> String.upcase()}
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

  defp cells(width, height), do: for(y <- 0..(height - 1), x <- 0..(width - 1), do: {x, y})

  defp out_of_bounds?(snapshot, {x, y}) do
    x < 0 or y < 0 or x >= snapshot["map"]["width"] or y >= snapshot["map"]["height"]
  end

  defp blocked?(snapshot, {x, y}) do
    entity_blocked?(snapshot["entities"], x, y) or tile_blocked?(snapshot, x, y)
  end

  defp entity_blocked?(entities, x, y) do
    entities
    |> Enum.any?(fn entity ->
      entity["preset"] == "collision" and div(entity["pos_x"], 32) == x and
        div(entity["pos_y"], 32) == y
    end)
  end

  defp tile_blocked?(snapshot, x, y) do
    assets = Map.new(snapshot["assets"], &{&1["id"], &1})

    snapshot["map"]["layers"]
    |> Enum.any?(fn layer ->
      case Map.get(layer["tiles"], "#{x},#{y}") do
        %{"asset_id" => asset_id, "tile_index" => tile_index} ->
          asset = assets[asset_id]

          asset &&
            asset["metadata"]
            |> Map.get("collisions", %{})
            |> Map.get(Integer.to_string(tile_index), Boxland.Library.CollisionMask.none())
            |> Boxland.Library.CollisionMask.to_booleans()
            |> Enum.any?()

        _ ->
          false
      end
    end)
  end

  defp inspect_tile(snapshot, {x, y}) do
    snapshot["entities"]
    |> entities_at(x, y)
    |> Enum.find_value(fn entity ->
      case entity["preset"] do
        "portal" -> "Portal reached"
        "sign" -> "Sign"
        "collectible" -> "Collectible"
        _ -> nil
      end
    end)
  end

  defp entities_at(entities, x, y) do
    Enum.filter(entities, &(div(&1["pos_x"], 32) == x and div(&1["pos_y"], 32) == y))
  end

  defp cell_style(snapshot, x, y) do
    layer = List.first(snapshot["map"]["layers"])
    assets = Map.new(snapshot["assets"], &{&1["id"], &1})

    case layer && Map.get(layer["tiles"], "#{x},#{y}") do
      %{"asset_id" => asset_id, "tile_index" => tile_index, "rotation" => rotation} ->
        asset = assets[asset_id]
        tile_style(asset, tile_index) <> " transform: rotate(#{rotation}deg);"

      _ ->
        ""
    end
  end

  defp tile_style(nil, _index), do: ""

  defp tile_style(asset, index) do
    columns = asset["metadata"]["columns"]
    x = rem(index, columns) * 32
    y = div(index, columns) * 32

    "background-image: url('#{asset["content_url"]}'); background-position: -#{x}px -#{y}px;"
  end
end
