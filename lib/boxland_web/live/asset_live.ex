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
      |> allow_upload(:tileset,
        accept: ~w(.png image/png),
        max_entries: 1,
        max_file_size: 8_000_000
      )

    {:ok, socket}
  end

  def handle_event("upload", %{"asset" => %{"name" => name}}, socket) do
    designer = socket.assigns.current_designer

    {assets, errors} =
      consume_uploaded_entries(socket, :tileset, fn %{path: path}, entry ->
        with {:ok, {width, height}} <- Library.parse_png_dimensions(path),
             :ok <- validate_tileset_dimensions(width, height),
             {:ok, tile_indexes} <- Library.visible_tile_indexes(path, width, height),
             {:ok, attrs} <- persist_upload(path, entry, name, width, height, tile_indexes),
             {:ok, asset} <- Library.create_tileset(designer.id, attrs) do
          {:ok, asset}
        else
          {:error, reason} -> {:ok, {:error, upload_error(reason)}}
        end
      end)
      |> Enum.split_with(&match?(%Boxland.Library.Asset{}, &1))

    case assets do
      [asset | _] ->
        {:noreply,
         socket
         |> put_flash(:info, "Tileset uploaded.")
         |> assign(:assets, Library.list_assets(designer.id))
         |> assign(:selected_asset, asset)
         |> assign(:selected_tile, first_tile_index(asset))}

      _ ->
        {:noreply,
         put_flash(
           socket,
           :error,
           upload_result_error(socket, errors)
         )}
    end
  end

  def handle_event("validate_upload", %{"asset" => asset_params}, socket) do
    {:noreply, assign(socket, :form, to_form(asset_params, as: :asset))}
  end

  def handle_event("select_asset", %{"id" => id}, socket) do
    asset = Library.get_asset!(socket.assigns.current_designer.id, id)
    {:noreply, assign(socket, selected_asset: asset, selected_tile: first_tile_index(asset))}
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

        <.form
          for={@form}
          id="tileset-upload-form"
          phx-change="validate_upload"
          phx-submit="upload"
          class="card bg-base-200"
        >
          <div class="card-body gap-4">
            <.input field={@form[:name]} type="text" label="Tileset name" required />
            <.live_file_input upload={@uploads.tileset} class="file-input file-input-bordered w-full" />
            <p :for={error <- upload_errors(@uploads.tileset)} class="text-sm text-error">
              {upload_error_text(error)}
            </p>
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
            :for={index <- tile_indexes(@asset)}
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

  defp persist_upload(path, entry, name, width, height, tile_indexes) do
    body = File.read!(path)
    hash = :crypto.hash(:sha256, body)
    key = "uploads/#{Base.url_encode64(hash, padding: false)}#{Path.extname(entry.client_name)}"

    with {:ok, content_url} <- Boxland.Storage.put_object(key, body, entry.client_type) do
      {:ok,
       %{
         name: if(name == "", do: Path.rootname(entry.client_name), else: name),
         sha256: hash,
         content_url: content_url,
         byte_size: entry.client_size,
         mime_type: entry.client_type,
         width: width,
         height: height,
         tile_indexes:
           tile_indexes ||
             Enum.to_list(
               0..(div(width, Library.tile_size()) * div(height, Library.tile_size()) - 1)
             )
       }}
    end
  end

  defp validate_tileset_dimensions(width, height) do
    cond do
      rem(width, Library.tile_size()) != 0 or rem(height, Library.tile_size()) != 0 ->
        {:error, "image dimensions must be divisible by #{Library.tile_size()}px"}

      width == 0 or height == 0 ->
        {:error, "image must contain at least one tile"}

      true ->
        :ok
    end
  end

  defp upload_error(%Ecto.Changeset{} = changeset) do
    changeset.errors
    |> Enum.map(fn {field, {message, opts}} ->
      label = field |> Atom.to_string() |> String.replace("_", " ")
      "#{label} #{BoxlandWeb.CoreComponents.translate_error({message, opts})}"
    end)
    |> Enum.join(", ")
    |> case do
      "" -> "Could not save tileset."
      message -> "Could not save tileset: #{message}."
    end
  end

  defp upload_error(reason) when is_binary(reason), do: "Could not upload tileset: #{reason}."
  defp upload_error(reason), do: "Could not upload tileset: #{inspect(reason)}."

  defp upload_result_error(_socket, [{:error, message} | _]), do: message

  defp upload_result_error(socket, _errors) do
    socket.assigns.uploads.tileset
    |> upload_errors()
    |> List.first()
    |> case do
      nil -> "Choose a PNG tileset before uploading."
      error -> upload_error_text(error)
    end
  end

  defp upload_error_text(:too_large), do: "Tileset must be 8 MB or smaller."
  defp upload_error_text(:too_many_files), do: "Upload one tileset at a time."
  defp upload_error_text(:not_accepted), do: "Choose a PNG image."
  defp upload_error_text(error), do: "Upload failed: #{inspect(error)}."

  defp tile_style(asset, index) do
    columns = asset.metadata["columns"]
    x = rem(index, columns) * 32
    y = div(index, columns) * 32

    "background-image: url('#{asset.content_url}'); background-position: -#{x}px -#{y}px;"
  end

  defp tile_indexes(asset) do
    Map.get(asset.metadata, "tile_indexes", Enum.to_list(0..(asset.metadata["tile_count"] - 1)))
  end

  defp first_tile_index(asset), do: asset |> tile_indexes() |> List.first() || 0

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
