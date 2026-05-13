defmodule BoxlandWeb.LevelEditorLive do
  use BoxlandWeb, :live_view

  alias Boxland.{Levels, Library}

  def mount(%{"id" => id}, _session, socket) do
    designer = socket.assigns.current_designer
    level = Levels.get_level!(designer.id, id)

    {:ok,
     socket
     |> assign(:level, level)
     |> assign(:tilesets, Library.list_tilesets(designer.id))
     |> assign(:preset, "spawn")
     |> assign(:publish_error, nil)}
  end

  def handle_event("preset", %{"preset" => preset}, socket) do
    {:noreply, assign(socket, :preset, preset)}
  end

  def handle_event("place", %{"x" => x, "y" => y}, socket) do
    designer = socket.assigns.current_designer
    level = socket.assigns.level
    x = String.to_integer(x) * 32
    y = String.to_integer(y) * 32

    {:ok, _entity} =
      Levels.create_preset_entity(designer.id, level.id, socket.assigns.preset, x, y)

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
    map = assigns.level.map
    layer = Boxland.Maps.primary_layer(Boxland.Repo.preload(map, :layers))
    assigns = assign(assigns, :layer, layer)

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
                class="relative h-8 w-8 border border-base-300 bg-base-100 bg-no-repeat"
                style={cell_style(@tilesets, @layer.tiles, x, y)}
              >
                <span
                  :for={entity <- entities_at(@level.entities, x, y)}
                  class="absolute inset-1 rounded bg-primary/80 text-[10px] font-bold text-primary-content"
                >
                  {preset_label(entity)}
                </span>
              </button>
            </div>
          </div>

          <aside class="space-y-3">
            <h2 class="font-semibold">Placed entities</h2>
            <div
              :for={entity <- @level.entities}
              class="flex items-center justify-between rounded-box bg-base-200 p-2"
            >
              <span>{preset_label(entity)} at {div(entity.pos_x, 32)}, {div(entity.pos_y, 32)}</span>
              <button phx-click="delete_entity" phx-value-id={entity.id} class="btn btn-xs btn-error">
                <.icon name="hero-x-mark" class="size-3" />
              </button>
            </div>
          </aside>
        </div>
      </section>
    </Layouts.app>
    """
  end

  defp entities_at(entities, x, y) do
    Enum.filter(entities, &(div(&1.pos_x, 32) == x and div(&1.pos_y, 32) == y))
  end

  defp cells(width, height), do: for(y <- 0..(height - 1), x <- 0..(width - 1), do: {x, y})

  defp preset_label(entity) do
    entity.entity_type.components
    |> Enum.find_value("?", fn
      %{"preset" => preset} -> String.first(preset) |> String.upcase()
      _ -> nil
    end)
  end

  defp cell_style(tilesets, tiles, x, y) do
    case Boxland.Maps.tile_at(tiles, x, y) do
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
