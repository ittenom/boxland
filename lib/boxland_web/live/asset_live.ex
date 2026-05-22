defmodule BoxlandWeb.AssetLive do
  use BoxlandWeb, :live_view

  alias Boxland.Library
  alias Boxland.Library.CollisionMask

  @tile_size 32
  @preview_zoom 8
  @preview_size @tile_size * @preview_zoom

  def mount(_params, _session, socket) do
    designer = socket.assigns.current_designer

    socket =
      socket
      |> assign(:tilesets, Library.list_tilesets(designer.id))
      |> assign(:selected_asset, nil)
      |> assign(:selected_tile, 0)
      |> assign(:upload_open?, false)
      |> assign(:rename_id, nil)
      |> assign(:form, to_form(%{"name" => ""}, as: :asset))
      |> auto_select_asset()
      |> allow_upload(:tileset,
        accept: ~w(.png image/png),
        max_entries: 1,
        max_file_size: 8_000_000
      )

    {:ok, socket}
  end

  def handle_event("open_upload", _params, socket) do
    {:noreply,
     socket
     |> assign(:upload_open?, true)
     |> assign(:form, to_form(%{"name" => ""}, as: :asset))}
  end

  def handle_event("close_upload", _params, socket) do
    {:noreply, assign(socket, :upload_open?, false)}
  end

  def handle_event("validate_upload", %{"asset" => params}, socket) do
    {:noreply, assign(socket, :form, to_form(params, as: :asset))}
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
         |> assign(:tilesets, Library.list_tilesets(designer.id))
         |> assign(:selected_asset, asset)
         |> assign(:selected_tile, first_tile_index(asset))
         |> assign(:upload_open?, false)
         |> assign(:form, to_form(%{"name" => ""}, as: :asset))}

      _ ->
        {:noreply, put_flash(socket, :error, upload_result_error(socket, errors))}
    end
  end

  def handle_event("select_asset", %{"id" => id}, socket) do
    asset = Library.get_asset!(socket.assigns.current_designer.id, id)

    {:noreply,
     socket
     |> assign(:selected_asset, asset)
     |> assign(:selected_tile, first_tile_index(asset))
     |> assign(:rename_id, nil)}
  end

  def handle_event("select_tile", %{"tile" => tile}, socket) do
    {:noreply, assign(socket, :selected_tile, String.to_integer(tile))}
  end

  def handle_event("quick_set", %{"mode" => mode}, socket) do
    apply_mask(socket, Library.collision_from_params(mode, %{}))
  end

  def handle_event("set_mode", %{"mode" => mode}, socket) do
    current = current_mask(socket)
    new_mask = mask_for_mode(mode, current)
    apply_mask(socket, new_mask)
  end

  def handle_event("update_rect", %{"rect" => params}, socket) do
    mask = Library.collision_from_params("rectangle", params)
    apply_mask(socket, mask)
  end

  def handle_event("update_polygon", %{"polygon" => %{"points" => raw}}, socket) do
    case parse_polygon_points(raw) do
      {:ok, points} ->
        apply_mask(socket, CollisionMask.polygon(points))

      :error ->
        {:noreply, socket}
    end
  end

  def handle_event("update_colors", %{"colors" => %{"colors" => raw}}, socket) do
    mask = Library.collision_from_params("colors", %{"colors" => raw})
    apply_mask(socket, mask)
  end

  def handle_event("paint_pixels", %{"pixels" => pixels, "value" => value}, socket)
      when is_list(pixels) and is_boolean(value) do
    {:ok, asset} =
      Library.paint_collision_pixels(
        socket.assigns.selected_asset,
        socket.assigns.selected_tile,
        Enum.map(pixels, fn p -> p end),
        value
      )

    {:noreply, replace_asset(socket, asset)}
  end

  def handle_event("fill_mask", _params, socket) do
    {:ok, asset} =
      Library.fill_collision_mask(socket.assigns.selected_asset, socket.assigns.selected_tile)

    {:noreply, replace_asset(socket, asset)}
  end

  def handle_event("clear_mask", _params, socket) do
    {:ok, asset} =
      Library.clear_collision_mask(socket.assigns.selected_asset, socket.assigns.selected_tile)

    {:noreply, replace_asset(socket, asset)}
  end

  def handle_event("invert_mask", _params, socket) do
    {:ok, asset} =
      Library.invert_collision_mask(socket.assigns.selected_asset, socket.assigns.selected_tile)

    {:noreply, replace_asset(socket, asset)}
  end

  def handle_event("rename_start", %{"id" => id}, socket) do
    {:noreply, assign(socket, :rename_id, id)}
  end

  def handle_event("rename_cancel", _params, socket) do
    {:noreply, assign(socket, :rename_id, nil)}
  end

  def handle_event("rename_save", %{"id" => id, "name" => name}, socket) do
    designer = socket.assigns.current_designer
    asset = Library.get_asset!(designer.id, id)

    case Library.rename_asset(asset, name) do
      {:ok, updated} ->
        selected =
          case socket.assigns.selected_asset do
            %{id: ^id} -> updated
            other -> other
          end

        {:noreply,
         socket
         |> assign(:tilesets, Library.list_tilesets(designer.id))
         |> assign(:selected_asset, selected)
         |> assign(:rename_id, nil)
         |> put_flash(:info, "Tileset renamed.")}

      {:error, _} ->
        {:noreply, put_flash(socket, :error, "Could not rename tileset.")}
    end
  end

  def handle_event("delete_asset", %{"id" => id}, socket) do
    designer = socket.assigns.current_designer
    asset = Library.get_asset!(designer.id, id)
    {:ok, _} = Library.delete_asset(asset)

    socket =
      socket
      |> assign(:tilesets, Library.list_tilesets(designer.id))
      |> put_flash(:info, "Tileset deleted.")

    selected_id = socket.assigns.selected_asset && socket.assigns.selected_asset.id

    socket =
      if selected_id == id do
        socket
        |> assign(:selected_asset, nil)
        |> auto_select_asset()
      else
        socket
      end

    {:noreply, socket}
  end

  def render(assigns) do
    ~H"""
    <Layouts.app flash={@flash} current_scope={%{designer: @current_designer}} width="wide">
      <section class="space-y-4">
        <header class="flex flex-wrap items-end justify-between gap-3">
          <div>
            <p class="text-sm font-semibold text-primary">Library</p>
            <h1 class="text-2xl font-semibold tracking-tight">Tilesets</h1>
          </div>
          <.link navigate={~p"/app"} class="btn btn-ghost btn-sm">Dashboard</.link>
        </header>

        <div class="grid gap-4 lg:grid-cols-[16rem_minmax(0,1fr)_22rem] xl:grid-cols-[18rem_minmax(0,1fr)_24rem]">
          <.library_panel
            tilesets={@tilesets}
            selected_asset={@selected_asset}
            rename_id={@rename_id}
          />
          <.workspace_panel selected_asset={@selected_asset} selected_tile={@selected_tile} />
          <.editor_panel
            selected_asset={@selected_asset}
            selected_tile={@selected_tile}
          />
        </div>
      </section>

      <.upload_modal :if={@upload_open?} form={@form} uploads={@uploads} />
    </Layouts.app>
    """
  end

  attr :tilesets, :list, required: true
  attr :selected_asset, :any, required: true
  attr :rename_id, :any, required: true

  defp library_panel(assigns) do
    ~H"""
    <aside class="rounded-box bg-base-200/60 p-3">
      <div class="mb-3 flex items-center justify-between">
        <h2 class="text-sm font-semibold uppercase tracking-wide text-base-content/70">
          {length(@tilesets)} tileset{if length(@tilesets) == 1, do: "", else: "s"}
        </h2>
        <button
          type="button"
          class="btn btn-primary btn-sm"
          phx-click="open_upload"
        >
          <.icon name="hero-plus" class="size-4" /> Upload
        </button>
      </div>

      <p :if={@tilesets == []} class="rounded-box bg-base-300/40 p-4 text-sm text-base-content/70">
        No tilesets yet. Upload a 32×32 tileset PNG to start editing collisions.
      </p>

      <ul :if={@tilesets != []} class="space-y-1">
        <li :for={asset <- @tilesets}>
          <.library_item
            asset={asset}
            selected?={@selected_asset && @selected_asset.id == asset.id}
            renaming?={@rename_id == asset.id}
          />
        </li>
      </ul>
    </aside>
    """
  end

  attr :asset, :map, required: true
  attr :selected?, :boolean, required: true
  attr :renaming?, :boolean, required: true

  defp library_item(assigns) do
    ~H"""
    <div class={[
      "group flex items-center gap-2 rounded-md p-2 transition",
      @selected? && "bg-primary/15 ring-1 ring-primary/40",
      !@selected? && "hover:bg-base-300/40"
    ]}>
      <span
        class="h-10 w-10 shrink-0 rounded border border-base-300 bg-base-100 bg-no-repeat"
        style={thumbnail_style(@asset)}
        aria-hidden="true"
      />

      <form
        :if={@renaming?}
        phx-submit="rename_save"
        phx-click-away="rename_cancel"
        phx-value-id={@asset.id}
        class="flex flex-1 items-center gap-1"
      >
        <input
          type="text"
          name="name"
          value={@asset.name}
          autofocus
          class="input input-xs w-full"
          phx-key="escape"
          phx-keydown="rename_cancel"
        />
        <button type="submit" class="btn btn-xs btn-primary">Save</button>
      </form>

      <button
        :if={!@renaming?}
        type="button"
        class="min-w-0 flex-1 text-left"
        phx-click="select_asset"
        phx-value-id={@asset.id}
        id={"asset-#{@asset.id}"}
      >
        <span class="block truncate text-sm font-medium">{@asset.name}</span>
        <span class="block text-xs text-base-content/60">
          {@asset.metadata["columns"]}×{@asset.metadata["rows"]} · {tile_count(@asset)} tiles
        </span>
      </button>

      <div
        :if={!@renaming?}
        class="dropdown dropdown-end shrink-0 opacity-0 transition group-hover:opacity-100 focus-within:opacity-100"
      >
        <div tabindex="0" role="button" class="btn btn-ghost btn-xs btn-square" aria-label="Actions">
          <.icon name="hero-ellipsis-vertical" class="size-4" />
        </div>
        <ul tabindex="0" class="menu dropdown-content z-10 mt-1 w-32 rounded-box bg-base-100 p-1 shadow">
          <li>
            <button type="button" phx-click="rename_start" phx-value-id={@asset.id}>
              Rename
            </button>
          </li>
          <li>
            <button
              type="button"
              class="text-error"
              phx-click="delete_asset"
              phx-value-id={@asset.id}
              data-confirm={"Delete \"#{@asset.name}\"? This can't be undone."}
            >
              Delete
            </button>
          </li>
        </ul>
      </div>
    </div>
    """
  end

  attr :selected_asset, :any, required: true
  attr :selected_tile, :integer, required: true

  defp workspace_panel(assigns) do
    ~H"""
    <section class="space-y-3">
      <div :if={@selected_asset} class="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h2 class="text-xl font-semibold leading-tight">{@selected_asset.name}</h2>
          <p class="text-sm text-base-content/60">
            {@selected_asset.metadata["columns"]} cols × {@selected_asset.metadata["rows"]} rows · tile {@selected_tile} selected
          </p>
        </div>
        <div class="flex items-center gap-2">
          <span class="text-xs uppercase tracking-wide text-base-content/60">Quick set</span>
          <button type="button" class="btn btn-sm btn-ghost" phx-click="quick_set" phx-value-mode="none">
            Passable
          </button>
          <button type="button" class="btn btn-sm btn-ghost" phx-click="quick_set" phx-value-mode="full">
            Solid
          </button>
        </div>
      </div>

      <div :if={@selected_asset} class="rounded-box bg-base-200/40 p-3">
        <div
          class="grid gap-2"
          style="grid-template-columns: repeat(auto-fill, minmax(56px, 1fr));"
        >
          <button
            :for={index <- tile_indexes(@selected_asset)}
            type="button"
            class={[
              "group relative aspect-square rounded border bg-base-100 transition",
              @selected_tile == index && "border-primary ring-2 ring-primary",
              @selected_tile != index && "border-base-300/60 hover:border-base-content/40"
            ]}
            phx-click="select_tile"
            phx-value-tile={index}
            aria-label={"Tile #{index}"}
            aria-pressed={@selected_tile == index}
          >
            <span
              class="absolute inset-0 bg-no-repeat"
              style={tile_card_style(@selected_asset, index)}
              aria-hidden="true"
            />
            <span
              :if={collision_badge(@selected_asset, index)}
              class={[
                "absolute right-1 top-1 inline-flex h-2.5 w-2.5 rounded-full ring-1 ring-base-100",
                collision_badge_class(@selected_asset, index)
              ]}
              title={"Collision: #{collision_badge(@selected_asset, index)}"}
            />
          </button>
        </div>
      </div>

      <div :if={!@selected_asset} class="hero rounded-box bg-base-200/60 py-20">
        <div class="hero-content text-center">
          <p class="text-base-content/70">Select a tileset on the left, or upload a new one.</p>
        </div>
      </div>
    </section>
    """
  end

  attr :selected_asset, :any, required: true
  attr :selected_tile, :integer, required: true

  defp editor_panel(assigns) do
    ~H"""
    <aside class="space-y-3 lg:sticky lg:top-4 lg:self-start">
      <div :if={@selected_asset} class="rounded-box bg-base-200/60 p-4 space-y-4">
        <header>
          <h3 class="text-base font-semibold">Tile {@selected_tile}</h3>
          <p class="text-xs text-base-content/60">Click a tile to select, then choose a collision mode.</p>
        </header>

        <.tile_preview asset={@selected_asset} selected_tile={@selected_tile} />

        <.mode_selector mode={current_mode(@selected_asset, @selected_tile)} />

        <.mode_controls
          asset={@selected_asset}
          selected_tile={@selected_tile}
          mode={current_mode(@selected_asset, @selected_tile)}
        />
      </div>

      <div :if={!@selected_asset} class="rounded-box bg-base-200/60 p-6 text-sm text-base-content/60">
        Pick a tile to start editing its collision shape.
      </div>
    </aside>
    """
  end

  attr :asset, :map, required: true
  attr :selected_tile, :integer, required: true

  defp tile_preview(assigns) do
    assigns =
      assigns
      |> assign(:mask, Library.tile_collision(assigns.asset, assigns.selected_tile))
      |> assign(:preview_size, @preview_size)
      |> assign(:tile_size, @tile_size)
      |> assign(:zoom, @preview_zoom)

    mode = assigns.mask["mode"]
    paintable? = mode == "manual"
    show_overlay? = mode not in ["none", "colors"]

    assigns =
      assigns
      |> assign(:paintable?, paintable?)
      |> assign(:show_overlay?, show_overlay?)

    ~H"""
    <div
      class="relative mx-auto overflow-hidden rounded border border-base-300 bg-base-100"
      style={"width: #{@preview_size}px; height: #{@preview_size}px;"}
    >
      <div
        class="absolute inset-0 bg-no-repeat"
        style={preview_style(@asset, @selected_tile)}
        aria-hidden="true"
      />

      <div
        :if={@show_overlay?}
        id={"mask-overlay-#{@asset.id}-#{@selected_tile}"}
        phx-hook={(@paintable? && "TileMaskPainter") || nil}
        data-tile={@selected_tile}
        class={[
          "absolute inset-0 grid touch-none",
          @paintable? && "cursor-crosshair"
        ]}
        style={"grid-template-columns: repeat(#{@tile_size}, 1fr); grid-template-rows: repeat(#{@tile_size}, 1fr);"}
      >
        <div
          :for={{solid?, x, y} <- mask_pixels(@asset, @selected_tile)}
          data-mask-cell
          data-x={x}
          data-y={y}
          data-solid={if solid?, do: "1", else: "0"}
          class={[
            "border-0",
            solid? && "bg-error/55",
            !solid? && "bg-transparent"
          ]}
        />
      </div>
    </div>
    """
  end

  attr :mode, :string, required: true

  defp mode_selector(assigns) do
    ~H"""
    <form phx-change="set_mode" class="flex items-center gap-2">
      <label for="collision-mode" class="text-xs uppercase tracking-wide text-base-content/60">
        Mode
      </label>
      <select id="collision-mode" name="mode" class="select select-sm flex-1">
        <option value="none" selected={@mode == "none"}>Passable</option>
        <option value="full" selected={@mode == "full"}>Solid (full tile)</option>
        <option value="rectangle" selected={@mode == "rectangle"}>Rectangle</option>
        <option value="polygon" selected={@mode == "polygon"}>Polygon</option>
        <option value="colors" selected={@mode == "colors"}>Color-driven</option>
        <option value="manual" selected={@mode == "manual"}>Manual paint</option>
      </select>
    </form>
    """
  end

  attr :asset, :map, required: true
  attr :selected_tile, :integer, required: true
  attr :mode, :string, required: true

  defp mode_controls(%{mode: "rectangle"} = assigns) do
    rect = current_mask(assigns.asset, assigns.selected_tile)["rect"] || %{}
    assigns = assign(assigns, :rect, rect)

    ~H"""
    <form phx-change="update_rect" class="space-y-2">
      <p class="text-xs text-base-content/60">
        Rectangle covers the area you set. Values are in 32-pixel tile coordinates.
      </p>
      <div class="grid grid-cols-2 gap-2">
        <label class="form-control">
          <span class="label-text text-xs">X</span>
          <input
            type="number"
            name="rect[x]"
            value={Map.get(@rect, "x", 0)}
            min="0"
            max="31"
            class="input input-sm w-full"
          />
        </label>
        <label class="form-control">
          <span class="label-text text-xs">Y</span>
          <input
            type="number"
            name="rect[y]"
            value={Map.get(@rect, "y", 0)}
            min="0"
            max="31"
            class="input input-sm w-full"
          />
        </label>
        <label class="form-control">
          <span class="label-text text-xs">Width</span>
          <input
            type="number"
            name="rect[width]"
            value={Map.get(@rect, "width", 32)}
            min="1"
            max="32"
            class="input input-sm w-full"
          />
        </label>
        <label class="form-control">
          <span class="label-text text-xs">Height</span>
          <input
            type="number"
            name="rect[height]"
            value={Map.get(@rect, "height", 32)}
            min="1"
            max="32"
            class="input input-sm w-full"
          />
        </label>
      </div>
    </form>
    """
  end

  defp mode_controls(%{mode: "polygon"} = assigns) do
    points = current_mask(assigns.asset, assigns.selected_tile)["points"] || []
    text = format_polygon_points(points)
    assigns = assign(assigns, :points_text, text)

    ~H"""
    <form phx-change="update_polygon" phx-submit="update_polygon" class="space-y-2">
      <p class="text-xs text-base-content/60">
        Points as <code>x,y</code> pairs separated by spaces. Example:
        <code>0,0 31,0 31,31 0,31</code>.
      </p>
      <textarea
        name="polygon[points]"
        rows="3"
        class="textarea textarea-sm w-full font-mono"
        spellcheck="false"
        phx-debounce="500"
      >{@points_text}</textarea>
    </form>
    """
  end

  defp mode_controls(%{mode: "colors"} = assigns) do
    colors = current_mask(assigns.asset, assigns.selected_tile)["colors"] || []
    assigns = assign(assigns, :colors_text, Enum.join(colors, ", "))
    assigns = assign(assigns, :colors_list, colors)

    ~H"""
    <form phx-change="update_colors" class="space-y-2">
      <p class="text-xs text-base-content/60">
        Collision derived at runtime from these tile colors. Comma-separated <code>#rrggbb</code> values.
      </p>
      <input
        type="text"
        name="colors[colors]"
        value={@colors_text}
        placeholder="#000000, #5a5a5a"
        class="input input-sm w-full font-mono"
        spellcheck="false"
        phx-debounce="500"
      />
      <div :if={@colors_list != []} class="flex flex-wrap gap-2">
        <span
          :for={color <- @colors_list}
          class="inline-flex items-center gap-1 rounded border border-base-300 px-2 py-1 text-xs font-mono"
        >
          <span class="inline-block h-3 w-3 rounded border border-base-300" style={"background: #{color};"} />
          {color}
        </span>
      </div>
    </form>
    """
  end

  defp mode_controls(%{mode: "manual"} = assigns) do
    ~H"""
    <div class="space-y-2">
      <p class="text-xs text-base-content/60">
        Drag on the preview above to paint solid pixels. Pixels under the cursor toggle from their current state.
      </p>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="btn btn-sm btn-ghost" phx-click="fill_mask">
          <.icon name="hero-square-2-stack" class="size-4" /> Fill
        </button>
        <button type="button" class="btn btn-sm btn-ghost" phx-click="clear_mask">
          <.icon name="hero-x-mark" class="size-4" /> Clear
        </button>
        <button type="button" class="btn btn-sm btn-ghost" phx-click="invert_mask">
          <.icon name="hero-arrows-right-left" class="size-4" /> Invert
        </button>
      </div>
    </div>
    """
  end

  defp mode_controls(%{mode: "none"} = assigns) do
    ~H"""
    <p class="text-xs text-base-content/60">
      Entities walk through this tile freely.
    </p>
    """
  end

  defp mode_controls(%{mode: "full"} = assigns) do
    ~H"""
    <p class="text-xs text-base-content/60">
      The whole 32×32 tile blocks movement.
    </p>
    """
  end

  defp mode_controls(assigns), do: ~H""

  attr :form, Phoenix.HTML.Form, required: true
  attr :uploads, :map, required: true

  defp upload_modal(assigns) do
    ~H"""
    <div
      id="upload-modal"
      class="fixed inset-0 z-40 flex items-center justify-center bg-base-300/70 p-4"
      phx-window-keydown="close_upload"
      phx-key="escape"
    >
      <div
        class="w-full max-w-lg rounded-box bg-base-100 p-6 shadow-xl"
        phx-click-away="close_upload"
      >
        <div class="mb-4 flex items-start justify-between">
          <div>
            <h2 class="text-lg font-semibold">Upload tileset</h2>
            <p class="text-sm text-base-content/60">PNG only. Width and height divisible by 32.</p>
          </div>
          <button
            type="button"
            class="btn btn-ghost btn-sm btn-square"
            phx-click="close_upload"
            aria-label="Close"
          >
            <.icon name="hero-x-mark" class="size-4" />
          </button>
        </div>

        <.form
          for={@form}
          id="tileset-upload-form"
          phx-change="validate_upload"
          phx-submit="upload"
          class="space-y-4"
        >
          <.input field={@form[:name]} type="text" label="Tileset name" />

          <label
            for={@uploads.tileset.ref}
            class="block cursor-pointer rounded-box border-2 border-dashed border-base-300 bg-base-200/50 p-6 text-center transition hover:border-primary hover:bg-base-200"
            phx-drop-target={@uploads.tileset.ref}
          >
            <.live_file_input upload={@uploads.tileset} class="sr-only" />
            <.icon name="hero-arrow-up-tray" class="size-6 mx-auto text-base-content/60" />
            <p class="mt-2 text-sm font-medium">Drop a PNG here, or click to choose</p>
            <p class="mt-1 text-xs text-base-content/60">Max 8 MB</p>
          </label>

          <div :for={entry <- @uploads.tileset.entries} class="space-y-1 text-sm">
            <div class="flex items-center justify-between">
              <span class="truncate">{entry.client_name}</span>
              <span class="text-base-content/60">{entry.progress}%</span>
            </div>
            <progress class="progress progress-primary w-full" value={entry.progress} max="100" />
          </div>

          <p :for={error <- upload_errors(@uploads.tileset)} class="text-sm text-error">
            {upload_error_text(error)}
          </p>

          <div class="flex justify-end gap-2">
            <button type="button" class="btn btn-ghost btn-sm" phx-click="close_upload">
              Cancel
            </button>
            <button type="submit" class="btn btn-primary btn-sm">Upload tileset</button>
          </div>
        </.form>
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

  defp tile_card_style(asset, index) do
    columns = asset.metadata["columns"]
    rows = asset.metadata["rows"]
    x = rem(index, columns)
    y = div(index, columns)

    "background-image: url('#{asset.content_url}');" <>
      "background-position: -#{x * 100}% -#{y * 100}%;" <>
      "background-size: #{columns * 100}% #{rows * 100}%;" <>
      "image-rendering: pixelated;"
  end

  defp preview_style(asset, index) do
    columns = asset.metadata["columns"]
    rows = asset.metadata["rows"]
    x = rem(index, columns)
    y = div(index, columns)

    "background-image: url('#{asset.content_url}');" <>
      "background-position: -#{x * @preview_size}px -#{y * @preview_size}px;" <>
      "background-size: #{columns * @preview_size}px #{rows * @preview_size}px;" <>
      "image-rendering: pixelated;"
  end

  defp thumbnail_style(asset) do
    case first_tile_index(asset) do
      nil ->
        ""

      index ->
        columns = asset.metadata["columns"] || 1
        rows = asset.metadata["rows"] || 1
        x = rem(index, columns)
        y = div(index, columns)

        "background-image: url('#{asset.content_url}');" <>
          "background-position: -#{x * 100}% -#{y * 100}%;" <>
          "background-size: #{columns * 100}% #{rows * 100}%;" <>
          "image-rendering: pixelated;"
    end
  end

  defp tile_indexes(asset) do
    Map.get(asset.metadata, "tile_indexes", Enum.to_list(0..(asset.metadata["tile_count"] - 1)))
  end

  defp tile_count(asset) do
    asset
    |> tile_indexes()
    |> length()
  end

  defp first_tile_index(nil), do: nil

  defp first_tile_index(asset) do
    case tile_indexes(asset) do
      [first | _] -> first
      _ -> 0
    end
  end

  defp mask_pixels(asset, tile_index) do
    asset
    |> Library.tile_collision(tile_index)
    |> CollisionMask.rows()
    |> Enum.with_index()
    |> Enum.flat_map(fn {row, y} ->
      row
      |> Enum.with_index()
      |> Enum.map(fn {solid?, x} -> {solid?, x, y} end)
    end)
  end

  defp current_mask(socket) do
    current_mask(socket.assigns.selected_asset, socket.assigns.selected_tile)
  end

  defp current_mask(nil, _tile), do: CollisionMask.none()

  defp current_mask(asset, tile_index) do
    Library.tile_collision(asset, tile_index)
  end

  defp current_mode(asset, tile) do
    case current_mask(asset, tile)["mode"] do
      nil -> "none"
      mode -> mode
    end
  end

  defp collision_badge(asset, index) do
    case current_mask(asset, index)["mode"] do
      "none" -> nil
      mode -> mode
    end
  end

  defp collision_badge_class(asset, index) do
    case current_mask(asset, index)["mode"] do
      "full" -> "bg-error"
      "manual" -> "bg-warning"
      "rectangle" -> "bg-info"
      "polygon" -> "bg-info"
      "colors" -> "bg-accent"
      _ -> "bg-base-content/40"
    end
  end

  defp mask_for_mode("rectangle", _current), do: CollisionMask.rectangle(0, 0, 32, 32)

  defp mask_for_mode("polygon", _current),
    do: CollisionMask.polygon([{0, 0}, {31, 0}, {31, 31}, {0, 31}])

  defp mask_for_mode("colors", _current), do: CollisionMask.colors([])
  defp mask_for_mode("manual", current), do: Map.put(current, "mode", "manual")
  defp mask_for_mode("full", _current), do: CollisionMask.full()
  defp mask_for_mode(_, _current), do: CollisionMask.none()

  defp apply_mask(socket, mask) do
    {:ok, asset} =
      Library.put_tile_collision(
        socket.assigns.selected_asset,
        socket.assigns.selected_tile,
        mask
      )

    {:noreply, replace_asset(socket, asset)}
  end

  defp replace_asset(socket, asset) do
    socket
    |> assign(:selected_asset, asset)
    |> assign(:tilesets, Library.list_tilesets(socket.assigns.current_designer.id))
  end

  defp auto_select_asset(socket) do
    case {socket.assigns.selected_asset, socket.assigns.tilesets} do
      {nil, [first | _]} ->
        socket
        |> assign(:selected_asset, first)
        |> assign(:selected_tile, first_tile_index(first))

      _ ->
        socket
    end
  end

  defp parse_polygon_points(raw) when is_binary(raw) do
    raw
    |> String.split(~r/\s+/, trim: true)
    |> Enum.reduce_while([], fn pair, acc ->
      case String.split(pair, ",", parts: 2) do
        [x, y] ->
          with {xi, ""} <- Integer.parse(String.trim(x)),
               {yi, ""} <- Integer.parse(String.trim(y)) do
            {:cont, [{xi, yi} | acc]}
          else
            _ -> {:halt, :error}
          end

        _ ->
          {:halt, :error}
      end
    end)
    |> case do
      :error -> :error
      list when is_list(list) -> {:ok, Enum.reverse(list)}
    end
  end

  defp parse_polygon_points(_), do: :error

  defp format_polygon_points(points) do
    points
    |> Enum.map_join(" ", fn
      %{"x" => x, "y" => y} -> "#{x},#{y}"
      {x, y} -> "#{x},#{y}"
    end)
  end
end
