defmodule BoxlandWeb.LevelEditorLive do
  use BoxlandWeb, :live_view

  alias Boxland.{Levels, Library, Maps}

  def mount(%{"id" => id}, _session, socket) do
    designer = socket.assigns.current_designer
    level = Levels.get_level!(designer.id, id)

    {:ok,
     socket
     |> assign(:level, level)
     |> assign(:tilesets, Library.list_tilesets(designer.id))
     |> assign(:preset, "spawn")
     |> assign(:place_z, default_place_z(level))
     |> assign(:publish_error, nil)}
  end

  def handle_event("preset", %{"preset" => preset}, socket) do
    {:noreply, assign(socket, :preset, preset)}
  end

  def handle_event("set_place_z", %{"z" => z}, socket) do
    {:noreply, assign(socket, :place_z, String.to_integer(z))}
  end

  def handle_event("place", %{"x" => x, "y" => y}, socket) do
    designer = socket.assigns.current_designer
    level = socket.assigns.level
    x = String.to_integer(x) * 32
    y = String.to_integer(y) * 32

    {:ok, _entity} =
      Levels.create_preset_entity(designer.id, level.id, socket.assigns.preset, x, y, %{},
        z_index_override: socket.assigns.place_z
      )

    {:noreply, assign(socket, :level, Levels.get_level!(designer.id, level.id))}
  end

  def handle_event("delete_entity", %{"id" => id}, socket) do
    designer = socket.assigns.current_designer
    level = socket.assigns.level
    _ = Levels.delete_entity(designer.id, level.id, String.to_integer(id))
    {:noreply, assign(socket, :level, Levels.get_level!(designer.id, level.id))}
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

  def render(assigns) do
    layers = visible_layers(assigns.level.map)
    assigns = assign(assigns, :layers, layers)

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

        <div class="flex flex-wrap items-center gap-3">
          <div class="flex flex-wrap gap-2">
            <button
              :for={{slug, name} <- Levels.preset_entities()}
              id={"preset-#{slug}"}
              phx-click="preset"
              phx-value-preset={slug}
              class={["btn btn-sm", @preset == slug && "btn-primary"]}
            >
              {name}
            </button>
          </div>

          <form class="flex items-center gap-2" phx-change="set_place_z">
            <label for="place-z" class="text-xs font-semibold uppercase tracking-wide text-base-content/60">
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
        </div>

        <div class="grid gap-4 lg:grid-cols-[1fr_18rem]">
          <div class="overflow-auto rounded-box bg-base-200 p-4">
            <div
              class="relative grid w-fit gap-px"
              style={"grid-template-columns: repeat(#{@level.map.width}, 32px);"}
            >
              <button
                :for={{x, y} <- cells(@level.map.width, @level.map.height)}
                id={"level-cell-#{x}-#{y}"}
                phx-click="place"
                phx-value-x={x}
                phx-value-y={y}
                class="relative h-8 w-8 border border-base-300 bg-base-100"
              >
                <span
                  :for={layer <- @layers}
                  class="pointer-events-none absolute inset-0 bg-no-repeat"
                  style={layer_cell_style(@tilesets, layer, x, y)}
                />
                <span
                  :for={entity <- entities_at(@level.entities, x, y)}
                  class="pointer-events-none absolute inset-1 rounded bg-primary/80 text-[10px] font-bold text-primary-content"
                  title={"z=#{entity_z(entity)}"}
                >
                  {preset_label(entity)}
                </span>
              </button>
            </div>
          </div>

          <aside class="space-y-4">
            <div class="rounded-box bg-base-200 p-3">
              <h2 class="mb-2 text-sm font-semibold uppercase tracking-wide text-base-content/70">
                Map layers
              </h2>
              <p :if={@layers == []} class="text-xs text-base-content/60">
                No visible layers.
              </p>
              <ul class="space-y-1 text-sm">
                <li
                  :for={layer <- Enum.sort_by(@layers, fn l -> -l.z_index end)}
                  class="flex items-center justify-between rounded bg-base-100 px-2 py-1"
                >
                  <span class="truncate">{layer.name}</span>
                  <span class="font-mono text-[10px] text-base-content/50">z={layer.z_index}</span>
                </li>
              </ul>
            </div>

            <div class="space-y-2">
              <h2 class="text-sm font-semibold uppercase tracking-wide text-base-content/70">
                Placed entities
              </h2>
              <div
                :for={entity <- @level.entities}
                class="flex items-center justify-between gap-2 rounded-box bg-base-200 p-2"
              >
                <div class="min-w-0 flex-1">
                  <div class="truncate text-sm font-medium">{preset_label_full(entity)}</div>
                  <div class="font-mono text-[10px] text-base-content/60">
                    ({div(entity.pos_x, 32)}, {div(entity.pos_y, 32)}) z={entity_z(entity)}
                  </div>
                </div>
                <button
                  phx-click="delete_entity"
                  phx-value-id={entity.id}
                  class="btn btn-xs btn-error"
                  aria-label="Delete entity"
                >
                  <.icon name="hero-x-mark" class="size-3" />
                </button>
              </div>
            </div>
          </aside>
        </div>
      </section>
    </Layouts.app>
    """
  end

  defp visible_layers(%Boxland.Maps.Map{layers: layers}) when is_list(layers) do
    layers
    |> Enum.filter(& &1.visible)
    |> Enum.sort_by(fn l -> {l.z_index, l.id} end)
  end

  defp visible_layers(map) do
    map
    |> Boxland.Repo.preload(layers: from_layer_order())
    |> Elixir.Map.get(:layers, [])
    |> Enum.filter(& &1.visible)
    |> Enum.sort_by(fn l -> {l.z_index, l.id} end)
  end

  defp from_layer_order do
    import Ecto.Query
    from(l in Boxland.Maps.Layer, order_by: [asc: l.z_index, asc: l.id])
  end

  defp default_place_z(level) do
    case visible_layers(level.map) do
      [] -> 0
      [layer | _] -> layer.z_index
    end
  end

  defp entities_at(entities, x, y) do
    Enum.filter(entities, &(div(&1.pos_x, 32) == x and div(&1.pos_y, 32) == y))
  end

  defp entity_z(entity) do
    entity.z_index_override || entity.entity_type.default_z_index
  end

  defp cells(width, height), do: for(y <- 0..(height - 1), x <- 0..(width - 1), do: {x, y})

  defp preset_label(entity) do
    entity.entity_type.components
    |> Enum.find_value("?", fn
      %{"preset" => preset} -> String.first(preset) |> String.upcase()
      _ -> nil
    end)
  end

  defp preset_label_full(entity) do
    entity.entity_type.components
    |> Enum.find_value(entity.entity_type.name, fn
      %{"preset" => preset} -> preset |> String.capitalize()
      _ -> nil
    end)
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
