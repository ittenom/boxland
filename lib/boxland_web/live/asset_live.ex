defmodule BoxlandWeb.AssetLive do
  use BoxlandWeb, :live_view

  alias Boxland.Library

  def mount(_params, _session, socket) do
    designer = socket.assigns.current_designer

    socket =
      socket
      |> assign(:assets, Library.list_assets(designer.id))
      |> assign(:selected_asset, nil)
      |> assign(:selected_tile, 0)
      |> assign(
        :collision_form,
        to_form(
          %{
            "x" => "0",
            "y" => "0",
            "width" => "32",
            "height" => "32",
            "points" => "0,0 31,0 31,31 0,31",
            "colors" => "#000000"
          },
          as: :collision
        )
      )
      |> assign(:form, to_form(%{}, as: :asset))
      |> allow_upload(:tileset, accept: ~w(.png), max_entries: 1, max_file_size: 8_000_000)

    {:ok, socket}
  end

  def handle_event("upload", %{"asset" => %{"name" => name}}, socket) do
    designer = socket.assigns.current_designer

    results =
      consume_uploaded_entries(socket, :tileset, fn %{path: path}, entry ->
        with {:ok, {width, height}} <- Library.parse_png_dimensions(path),
             {:ok, attrs} <- persist_upload(path, entry, name, width, height),
             {:ok, asset} <- Library.create_tileset(designer.id, attrs) do
          {:ok, asset}
        else
          {:error, reason} -> {:postpone, reason}
        end
      end)

    case results do
      [asset | _] ->
        {:noreply,
         socket
         |> put_flash(:info, "Tileset uploaded.")
         |> assign(:assets, Library.list_assets(designer.id))
         |> assign(:selected_asset, asset)}

      _ ->
        {:noreply,
         put_flash(socket, :error, "Upload a valid PNG tileset with dimensions divisible by 32.")}
    end
  end

  def handle_event("select_asset", %{"id" => id}, socket) do
    asset = Library.get_asset!(socket.assigns.current_designer.id, id)
    {:noreply, assign(socket, selected_asset: asset, selected_tile: 0)}
  end

  def handle_event("select_tile", %{"tile" => tile}, socket) do
    {:noreply, assign(socket, :selected_tile, String.to_integer(tile))}
  end

  def handle_event("set_collision", %{"tile" => tile, "mode" => mode}, socket) do
    asset = socket.assigns.selected_asset
    tile_index = String.to_integer(tile)

    {:ok, asset} =
      Library.put_tile_collision(asset, tile_index, Library.collision_from_params(mode, %{}))

    {:noreply,
     socket
     |> assign(:selected_asset, asset)
     |> assign(:assets, Library.list_assets(socket.assigns.current_designer.id))}
  end

  def handle_event("apply_collision", %{"collision" => %{"mode" => mode} = params}, socket) do
    {:ok, asset} =
      Library.put_tile_collision(
        socket.assigns.selected_asset,
        socket.assigns.selected_tile,
        Library.collision_from_params(mode, params)
      )

    {:noreply,
     socket
     |> assign(:selected_asset, asset)
     |> assign(:assets, Library.list_assets(socket.assigns.current_designer.id))}
  end

  def handle_event("toggle_pixel", %{"x" => x, "y" => y}, socket) do
    {:ok, asset} =
      Library.toggle_collision_pixel(
        socket.assigns.selected_asset,
        socket.assigns.selected_tile,
        String.to_integer(x),
        String.to_integer(y)
      )

    {:noreply,
     socket
     |> assign(:selected_asset, asset)
     |> assign(:assets, Library.list_assets(socket.assigns.current_designer.id))}
  end

  def render(assigns) do
    ~H"""
    <Layouts.app flash={@flash} current_scope={%{designer: @current_designer}}>
      <section class="space-y-6">
        <div class="flex items-center justify-between">
          <div>
            <p class="text-sm font-semibold text-primary">Assets</p>
            <h1 class="text-3xl font-semibold tracking-tight">Upload a 32x32 tileset</h1>
          </div>
          <.link navigate={~p"/app"} class="btn btn-ghost">Dashboard</.link>
        </div>

        <.form for={@form} id="tileset-upload-form" phx-submit="upload" class="card bg-base-200">
          <div class="card-body gap-4">
            <.input field={@form[:name]} type="text" label="Tileset name" required />
            <.live_file_input upload={@uploads.tileset} class="file-input file-input-bordered w-full" />
            <p class="text-sm text-base-content/60">
              PNG only. Width and height must be divisible by 32.
            </p>
            <.button class="btn btn-primary w-fit">Upload tileset</.button>
          </div>
        </.form>

        <div class="grid gap-6 lg:grid-cols-[18rem_1fr]">
          <aside class="space-y-2">
            <h2 class="font-semibold">Tilesets</h2>
            <button
              :for={asset <- @assets}
              id={"asset-#{asset.id}"}
              phx-click="select_asset"
              phx-value-id={asset.id}
              class="btn btn-ghost w-full justify-start"
            >
              {asset.name}
            </button>
          </aside>

          <div class="min-h-80">
            <.tileset_editor
              :if={@selected_asset}
              asset={@selected_asset}
              selected_tile={@selected_tile}
              collision_form={@collision_form}
            />
            <div :if={!@selected_asset} class="hero rounded-box bg-base-200 py-20">
              <div class="hero-content text-center">
                <p class="text-base-content/70">Select or upload a tileset to edit tile collision.</p>
              </div>
            </div>
          </div>
        </div>
      </section>
    </Layouts.app>
    """
  end

  attr :asset, Boxland.Library.Asset, required: true
  attr :selected_tile, :integer, required: true
  attr :collision_form, Phoenix.HTML.Form, required: true

  defp tileset_editor(assigns) do
    ~H"""
    <div class="space-y-4">
      <div>
        <h2 class="text-xl font-semibold">{@asset.name}</h2>
        <p class="text-sm text-base-content/60">
          {@asset.metadata["columns"]} columns x {@asset.metadata["rows"]} rows
        </p>
      </div>

      <div class="grid gap-6 xl:grid-cols-[1fr_24rem]">
        <div class="grid grid-cols-4 gap-3 sm:grid-cols-6 md:grid-cols-8 lg:grid-cols-10">
          <div
            :for={index <- 0..(@asset.metadata["tile_count"] - 1)}
            class={[
              "card p-2",
              @selected_tile == index && "bg-primary/10 ring-2 ring-primary",
              @selected_tile != index && "bg-base-200"
            ]}
          >
            <button
              class="mx-auto h-8 w-8 border border-base-300 bg-no-repeat"
              style={tile_style(@asset, index)}
              phx-click="select_tile"
              phx-value-tile={index}
            />
            <div class="mt-2 grid grid-cols-2 gap-1">
              <button
                class="btn btn-xs"
                phx-click="set_collision"
                phx-value-tile={index}
                phx-value-mode="none"
              >
                Pass
              </button>
              <button
                class="btn btn-xs"
                phx-click="set_collision"
                phx-value-tile={index}
                phx-value-mode="full"
              >
                Solid
              </button>
            </div>
            <p class="mt-1 truncate text-center text-xs text-base-content/60">
              {Library.tile_collision(@asset, index)["mode"]}
            </p>
          </div>
        </div>

        <aside class="card bg-base-200">
          <div class="card-body space-y-4">
            <h3 class="card-title">Tile {@selected_tile} collision</h3>
            <p class="text-sm text-base-content/60">
              Mode: {Library.tile_collision(@asset, @selected_tile)["mode"]}
            </p>

            <.form
              for={@collision_form}
              id="collision-form"
              phx-submit="apply_collision"
              class="space-y-3"
            >
              <.input
                field={@collision_form[:mode]}
                type="select"
                label="Mode"
                options={[
                  {"Passable", "none"},
                  {"Full box", "full"},
                  {"Offset rectangle", "rectangle"},
                  {"Polygon", "polygon"},
                  {"By tile color", "colors"}
                ]}
              />
              <div class="grid grid-cols-2 gap-2">
                <.input field={@collision_form[:x]} type="number" label="X" min="0" max="31" />
                <.input field={@collision_form[:y]} type="number" label="Y" min="0" max="31" />
                <.input field={@collision_form[:width]} type="number" label="Width" min="1" max="32" />
                <.input
                  field={@collision_form[:height]}
                  type="number"
                  label="Height"
                  min="1"
                  max="32"
                />
              </div>
              <.input field={@collision_form[:points]} type="text" label="Polygon points" />
              <.input field={@collision_form[:colors]} type="text" label="Collider colors" />
              <.button class="btn btn-primary btn-sm">Apply collision</.button>
            </.form>

            <div class="grid w-fit gap-0" style="grid-template-columns: repeat(32, 0.5rem);">
              <button
                :for={{solid?, x, y} <- mask_pixels(@asset, @selected_tile)}
                class={[
                  "h-2 w-2 border border-base-300",
                  solid? && "bg-error",
                  !solid? && "bg-base-100"
                ]}
                phx-click="toggle_pixel"
                phx-value-x={x}
                phx-value-y={y}
              />
            </div>
          </div>
        </aside>
      </div>
    </div>
    """
  end

  defp persist_upload(path, entry, name, width, height) do
    uploads_dir = Application.app_dir(:boxland, "priv/static/uploads")
    File.mkdir_p!(uploads_dir)

    hash = :crypto.hash(:sha256, File.read!(path))
    filename = "#{Base.url_encode64(hash, padding: false)}#{Path.extname(entry.client_name)}"
    dest = Path.join(uploads_dir, filename)
    File.cp!(path, dest)

    {:ok,
     %{
       name: if(name == "", do: Path.rootname(entry.client_name), else: name),
       sha256: hash,
       content_url: "/uploads/#{filename}",
       byte_size: entry.client_size,
       mime_type: entry.client_type,
       width: width,
       height: height
     }}
  end

  defp tile_style(asset, index) do
    columns = asset.metadata["columns"]
    x = rem(index, columns) * 32
    y = div(index, columns) * 32

    "background-image: url('#{asset.content_url}'); background-position: -#{x}px -#{y}px;"
  end

  defp mask_pixels(asset, tile_index) do
    asset
    |> Library.tile_collision(tile_index)
    |> Boxland.Library.CollisionMask.rows()
    |> Enum.with_index()
    |> Enum.flat_map(fn {row, y} ->
      row
      |> Enum.with_index()
      |> Enum.map(fn {solid?, x} -> {solid?, x, y} end)
    end)
  end
end
