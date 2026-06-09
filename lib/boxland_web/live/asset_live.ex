defmodule BoxlandWeb.AssetLive do
  use BoxlandWeb, :live_view

  import BoxlandWeb.Components.Ide

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
      |> assign(:spritesheets, Library.list_spritesheets(designer.id))
      |> assign(:selected_asset, nil)
      |> assign(:selected_tile, 0)
      |> assign(:selected_animation, nil)
      |> assign(:upload_open?, false)
      |> assign(:rename_id, nil)
      |> assign(:eyedropper_unavailable?, false)
      |> assign(:form, to_form(%{"name" => "", "kind" => "tileset"}, as: :asset))
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
     |> assign(:form, to_form(%{"name" => "", "kind" => "tileset"}, as: :asset))}
  end

  def handle_event("close_upload", _params, socket) do
    {:noreply, assign(socket, :upload_open?, false)}
  end

  def handle_event("validate_upload", %{"asset" => params}, socket) do
    {:noreply, assign(socket, :form, to_form(params, as: :asset))}
  end

  def handle_event("upload", %{"asset" => %{"name" => name} = params}, socket) do
    designer = socket.assigns.current_designer
    kind = if params["kind"] == "spritesheet", do: "spritesheet", else: "tileset"

    {assets, errors} =
      consume_uploaded_entries(socket, :tileset, fn %{path: path}, entry ->
        with {:ok, {width, height}} <- Library.parse_png_dimensions(path),
             :ok <- validate_tileset_dimensions(width, height),
             {:ok, tile_indexes} <- Library.visible_tile_indexes(path, width, height),
             {:ok, attrs} <- persist_upload(path, entry, name, width, height, tile_indexes),
             {:ok, asset} <- create_asset(kind, designer.id, attrs) do
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
         |> put_flash(:info, "#{String.capitalize(kind)} uploaded.")
         |> reload_assets()
         |> assign(:selected_asset, asset)
         |> assign(:selected_tile, first_tile_index(asset))
         |> assign(:selected_animation, nil)
         |> assign(:upload_open?, false)
         |> assign(:form, to_form(%{"name" => "", "kind" => "tileset"}, as: :asset))}

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
     |> assign(:selected_animation, first_animation_name(asset))
     |> assign(:rename_id, nil)
     |> assign(:eyedropper_unavailable?, false)}
  end

  def handle_event("select_tile", %{"tile" => tile}, socket) do
    {:noreply, assign(socket, :selected_tile, String.to_integer(tile))}
  end

  # === Spritesheet animation editing ===

  def handle_event("anim_new", %{"animation" => %{"name" => raw_name}}, socket) do
    asset = socket.assigns.selected_asset
    name = String.trim(raw_name)
    animation = %{"name" => name, "frames" => [], "fps" => 8, "loop" => true}

    cond do
      name == "" ->
        {:noreply, put_flash(socket, :error, "Animation name can't be blank.")}

      Library.animation(asset, name) ->
        {:noreply, put_flash(socket, :error, "An animation named \"#{name}\" already exists.")}

      true ->
        case Library.put_animations(asset, animations(asset) ++ [animation]) do
          {:ok, updated} ->
            {:noreply, socket |> replace_asset(updated) |> assign(:selected_animation, name)}

          {:error, message} ->
            {:noreply, put_flash(socket, :error, animation_error(message))}
        end
    end
  end

  def handle_event("anim_select", %{"name" => name}, socket) do
    {:noreply, assign(socket, :selected_animation, name)}
  end

  def handle_event("anim_delete", %{"name" => name}, socket) do
    asset = socket.assigns.selected_asset
    remaining = Enum.reject(animations(asset), &(&1["name"] == name))

    case Library.put_animations(asset, remaining) do
      {:ok, updated} ->
        selected =
          if socket.assigns.selected_animation == name,
            do: first_animation_name(updated),
            else: socket.assigns.selected_animation

        {:noreply, socket |> replace_asset(updated) |> assign(:selected_animation, selected)}

      {:error, message} ->
        {:noreply, put_flash(socket, :error, animation_error(message))}
    end
  end

  def handle_event("anim_settings", %{"animation" => params}, socket) do
    update_selected_animation(socket, fn animation ->
      animation
      |> Map.put("fps", parse_fps(params["fps"], animation["fps"]))
      |> Map.put("loop", params["loop"] == "true")
    end)
  end

  def handle_event("anim_toggle_frame", %{"frame" => frame}, socket) do
    frame = String.to_integer(frame)

    update_selected_animation(socket, fn animation ->
      frames = animation["frames"]

      frames =
        if frame in frames,
          do: Enum.reject(frames, &(&1 == frame)),
          else: frames ++ [frame]

      Map.put(animation, "frames", frames)
    end)
  end

  def handle_event("anim_clear_frames", _params, socket) do
    update_selected_animation(socket, &Map.put(&1, "frames", []))
  end

  def handle_event("quick_set", %{"mode" => mode}, socket) do
    apply_mask(socket, Library.collision_from_params(mode, %{}))
  end

  def handle_event("set_mode", %{"mode" => mode}, socket) do
    current = current_mask(socket)
    new_mask = mask_for_mode(mode, current)
    apply_mask(socket, new_mask)
  end

  def handle_event(
        "set_rect",
        %{"x" => x, "y" => y, "width" => width, "height" => height},
        socket
      )
      when is_integer(x) and is_integer(y) and is_integer(width) and is_integer(height) do
    size = CollisionMask.size()
    x = clamp(x, 0, size - 1)
    y = clamp(y, 0, size - 1)
    width = clamp(width, 1, size - x)
    height = clamp(height, 1, size - y)

    apply_mask(socket, CollisionMask.rectangle(x, y, width, height))
  end

  def handle_event("polygon_add_point", %{"x" => x, "y" => y}, socket)
      when is_integer(x) and is_integer(y) do
    apply_mask(socket, CollisionMask.polygon(polygon_points(socket) ++ [clamp_point(x, y)]))
  end

  def handle_event("polygon_move_point", %{"index" => index, "x" => x, "y" => y}, socket)
      when is_integer(index) and is_integer(x) and is_integer(y) do
    points = polygon_points(socket)

    if index >= 0 and index < length(points) do
      points = List.replace_at(points, index, clamp_point(x, y))
      apply_mask(socket, CollisionMask.polygon(points))
    else
      {:noreply, socket}
    end
  end

  def handle_event("polygon_undo_point", _params, socket) do
    case polygon_points(socket) do
      [] -> {:noreply, socket}
      points -> apply_mask(socket, CollisionMask.polygon(Enum.drop(points, -1)))
    end
  end

  def handle_event("polygon_clear_points", _params, socket) do
    apply_mask(socket, CollisionMask.polygon([]))
  end

  def handle_event("pick_color", %{"color" => color}, socket) do
    case normalize_color(color) do
      {:ok, color} ->
        colors = mask_colors(socket)

        colors =
          if color in colors,
            do: List.delete(colors, color),
            else: colors ++ [color]

        apply_mask(socket, CollisionMask.colors(colors))

      :error ->
        {:noreply, socket}
    end
  end

  def handle_event("add_color", %{"color" => color}, socket) do
    case normalize_color(color) do
      {:ok, color} ->
        colors = mask_colors(socket)

        if color in colors do
          {:noreply, socket}
        else
          apply_mask(socket, CollisionMask.colors(colors ++ [color]))
        end

      :error ->
        {:noreply, socket}
    end
  end

  def handle_event("remove_color", %{"color" => color}, socket) do
    case normalize_color(color) do
      {:ok, color} ->
        apply_mask(socket, CollisionMask.colors(List.delete(mask_colors(socket), color)))

      :error ->
        {:noreply, socket}
    end
  end

  def handle_event("eyedropper_unavailable", _params, socket) do
    {:noreply, assign(socket, :eyedropper_unavailable?, true)}
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
         |> reload_assets()
         |> assign(:selected_asset, selected)
         |> assign(:rename_id, nil)
         |> put_flash(:info, "Asset renamed.")}

      {:error, _} ->
        {:noreply, put_flash(socket, :error, "Could not rename asset.")}
    end
  end

  def handle_event("delete_asset", %{"id" => id}, socket) do
    designer = socket.assigns.current_designer
    asset = Library.get_asset!(designer.id, id)
    {:ok, _} = Library.delete_asset(asset)

    socket =
      socket
      |> reload_assets()
      |> put_flash(:info, "Asset deleted.")

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
    <div id="asset-root">
      <.ide_shell flash={@flash}>
        <:activity>
          <.ide_rail_nav active={:assets} />
        </:activity>

        <:explorer>
          <.panel title="Library">
            <:actions>
              <button
                id="asset-upload-button"
                phx-click="open_upload"
                class="ide-toolbtn !p-1"
                title="Upload tileset"
              >
                <.icon name="hero-arrow-up-tray" class="size-3.5" />
              </button>
            </:actions>
            <.library_panel
              tilesets={@tilesets}
              spritesheets={@spritesheets}
              selected_asset={@selected_asset}
              rename_id={@rename_id}
            />
          </.panel>
        </:explorer>

        <:viewport>
          <.ide_toolbar id="asset-toolbar">
            <h1 class="mr-2 text-sm font-semibold text-base-content">
              {(@selected_asset && @selected_asset.name) || "Assets"}
            </h1>
            <div class="flex-1"></div>
            <button
              id="asset-upload-button-2"
              phx-click="open_upload"
              class="ide-toolbtn ide-toolbtn-active"
            >
              <.icon name="hero-arrow-up-tray" class="size-4" /> Upload
            </button>
          </.ide_toolbar>

          <div class="min-h-0 flex-1 overflow-auto p-4">
            <.workspace_panel
              selected_asset={@selected_asset}
              selected_tile={@selected_tile}
              selected_animation={@selected_animation}
            />
          </div>
        </:viewport>

        <:inspector>
          <.editor_panel
            selected_asset={@selected_asset}
            selected_tile={@selected_tile}
            selected_animation={@selected_animation}
            eyedropper_unavailable?={@eyedropper_unavailable?}
          />
        </:inspector>

        <:status>
          <span class="font-mono">
            {length(@tilesets)} tileset{if length(@tilesets) == 1, do: "", else: "s"} · {length(
              @spritesheets
            )} spritesheet{if length(@spritesheets) == 1, do: "", else: "s"}
          </span>
          <span :if={@selected_asset} class="font-mono">tile: {@selected_tile}</span>
          <span class="flex-1"></span>
          <span class="text-base-content/50">
            Upload PNG tilesets &amp; spritesheets · collisions · animations
          </span>
        </:status>
      </.ide_shell>

      <.upload_modal :if={@upload_open?} form={@form} uploads={@uploads} />
    </div>
    """
  end

  attr :tilesets, :list, required: true
  attr :spritesheets, :list, required: true
  attr :selected_asset, :any, required: true
  attr :rename_id, :any, required: true

  defp library_panel(assigns) do
    ~H"""
    <div class="space-y-3 px-1.5">
      <p
        :if={@tilesets == [] and @spritesheets == []}
        class="rounded-box bg-base-300/40 p-4 text-xs text-base-content/60"
      >
        No assets yet. Upload a 32×32 tileset or spritesheet PNG to get started.
      </p>

      <div :if={@tilesets != []}>
        <p class="px-1 pb-1 text-[10px] font-semibold uppercase tracking-wide text-base-content/50">
          Tilesets
        </p>
        <ul class="space-y-1">
          <li :for={asset <- @tilesets}>
            <.library_item
              asset={asset}
              selected?={@selected_asset && @selected_asset.id == asset.id}
              renaming?={@rename_id == asset.id}
            />
          </li>
        </ul>
      </div>

      <div :if={@spritesheets != []}>
        <p class="px-1 pb-1 text-[10px] font-semibold uppercase tracking-wide text-base-content/50">
          Spritesheets
        </p>
        <ul class="space-y-1">
          <li :for={asset <- @spritesheets}>
            <.library_item
              asset={asset}
              selected?={@selected_asset && @selected_asset.id == asset.id}
              renaming?={@rename_id == asset.id}
            />
          </li>
        </ul>
      </div>
    </div>
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
        <span :if={@asset.kind == "tileset"} class="block text-xs text-base-content/60">
          {meta_cols(@asset)}×{meta_rows(@asset)} · {tile_count(@asset)} tiles
        </span>
        <span :if={@asset.kind == "spritesheet"} class="block text-xs text-base-content/60">
          {meta_cols(@asset)}×{meta_rows(@asset)} · {length(animations(@asset))} animation{if length(
                                                                                                animations(
                                                                                                  @asset
                                                                                                )
                                                                                              ) == 1,
                                                                                              do: "",
                                                                                              else:
                                                                                                "s"}
        </span>
      </button>

      <div
        :if={!@renaming?}
        class="dropdown dropdown-end shrink-0 opacity-0 transition group-hover:opacity-100 focus-within:opacity-100"
      >
        <div tabindex="0" role="button" class="btn btn-ghost btn-xs btn-square" aria-label="Actions">
          <.icon name="hero-ellipsis-vertical" class="size-4" />
        </div>
        <ul
          tabindex="0"
          class="menu dropdown-content z-10 mt-1 w-32 rounded-box bg-base-100 p-1 shadow"
        >
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
  attr :selected_animation, :any, default: nil

  defp workspace_panel(%{selected_asset: %{kind: "spritesheet"}} = assigns) do
    assigns =
      assign(
        assigns,
        :animation,
        Library.animation(assigns.selected_asset, assigns.selected_animation)
      )

    ~H"""
    <section class="space-y-3">
      <div class="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h2 class="text-xl font-semibold leading-tight">{@selected_asset.name}</h2>
          <p class="text-sm text-base-content/60">
            {meta_cols(@selected_asset)} cols × {meta_rows(@selected_asset)} rows · {@selected_asset.metadata[
              "frame_count"
            ]} frames
          </p>
        </div>
        <p :if={@animation} class="text-sm text-base-content/60">
          Editing <span class="font-semibold text-base-content">{@animation["name"]}</span>
          — click frames to add or remove them, in playback order.
        </p>
        <p :if={!@animation} class="text-sm text-base-content/60">
          Create an animation on the right, then click frames to build it.
        </p>
      </div>

      <div class="rounded-box bg-base-200/40 p-3">
        <div class="grid gap-2" style="grid-template-columns: repeat(auto-fill, minmax(56px, 1fr));">
          <button
            :for={index <- tile_indexes(@selected_asset)}
            type="button"
            class={[
              "group relative aspect-square rounded border bg-base-100 transition",
              @animation && index in @animation["frames"] && "border-primary ring-2 ring-primary",
              !(@animation && index in @animation["frames"]) &&
                "border-base-300/60 hover:border-base-content/40"
            ]}
            disabled={is_nil(@animation)}
            phx-click="anim_toggle_frame"
            phx-value-frame={index}
            aria-label={"Frame #{index}"}
            aria-pressed={@animation && index in @animation["frames"]}
          >
            <span
              class="absolute inset-0 bg-no-repeat"
              style={tile_card_style(@selected_asset, index)}
              aria-hidden="true"
            />
            <span
              :if={@animation && index in @animation["frames"]}
              class="absolute right-1 top-1 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-primary px-1 text-[10px] font-bold text-primary-content ring-1 ring-base-100"
              title="Playback order"
            >
              {Enum.find_index(@animation["frames"], &(&1 == index)) + 1}
            </span>
          </button>
        </div>
      </div>
    </section>
    """
  end

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
          <button
            type="button"
            class="btn btn-sm btn-ghost"
            phx-click="quick_set"
            phx-value-mode="none"
          >
            Passable
          </button>
          <button
            type="button"
            class="btn btn-sm btn-ghost"
            phx-click="quick_set"
            phx-value-mode="full"
          >
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
  attr :selected_animation, :any, default: nil
  attr :eyedropper_unavailable?, :boolean, default: false

  defp editor_panel(%{selected_asset: %{kind: "spritesheet"}} = assigns) do
    assigns =
      assigns
      |> assign(:animation, Library.animation(assigns.selected_asset, assigns.selected_animation))
      |> assign(:preview_box, @preview_size)

    ~H"""
    <aside class="space-y-3 lg:sticky lg:top-4 lg:self-start">
      <div class="rounded-box bg-base-200/60 p-4 space-y-4">
        <header>
          <h3 class="text-base font-semibold">Animations</h3>
          <p class="text-xs text-base-content/60">
            Named frame sequences other tools can play (map tiles, entities).
          </p>
        </header>

        <ul :if={animations(@selected_asset) != []} class="space-y-1">
          <li
            :for={animation <- animations(@selected_asset)}
            class={[
              "group flex items-center gap-2 rounded-md px-2 py-1.5 transition",
              @selected_animation == animation["name"] && "bg-primary/15 ring-1 ring-primary/40",
              @selected_animation != animation["name"] && "hover:bg-base-300/40"
            ]}
          >
            <button
              type="button"
              class="min-w-0 flex-1 text-left"
              phx-click="anim_select"
              phx-value-name={animation["name"]}
            >
              <span class="block truncate text-sm font-medium">{animation["name"]}</span>
              <span class="block text-xs text-base-content/60">
                {length(animation["frames"])} frame{if length(animation["frames"]) == 1,
                  do: "",
                  else: "s"} · {animation["fps"]} fps · {if animation["loop"],
                  do: "loop",
                  else: "once"}
              </span>
            </button>
            <button
              type="button"
              class="btn btn-ghost btn-xs btn-square opacity-0 transition group-hover:opacity-100"
              phx-click="anim_delete"
              phx-value-name={animation["name"]}
              data-confirm={"Delete animation \"#{animation["name"]}\"?"}
              aria-label={"Delete #{animation["name"]}"}
            >
              <.icon name="hero-trash" class="size-3.5" />
            </button>
          </li>
        </ul>

        <form phx-submit="anim_new" class="flex items-center gap-2">
          <input
            type="text"
            name="animation[name]"
            placeholder="New animation name"
            class="input input-sm flex-1"
            autocomplete="off"
          />
          <button type="submit" class="btn btn-sm btn-primary">Add</button>
        </form>
      </div>

      <div :if={@animation} class="rounded-box bg-base-200/60 p-4 space-y-4">
        <header>
          <h3 class="text-base font-semibold">{@animation["name"]}</h3>
          <p class="text-xs text-base-content/60">
            Click frames in the grid to add or remove them.
          </p>
        </header>

        <div
          :if={@animation["frames"] != []}
          id={"anim-preview-#{@selected_asset.id}-#{@animation["name"]}"}
          phx-hook="Sprite"
          data-sprite-url={@selected_asset.content_url}
          data-sprite-cols={meta_cols(@selected_asset)}
          data-sprite-rows={meta_rows(@selected_asset)}
          data-sprite-tile={@preview_box}
          data-sprite-frames={Enum.join(@animation["frames"], ",")}
          data-sprite-fps={@animation["fps"]}
          data-sprite-loop={to_string(@animation["loop"])}
          data-sprite-sync="ambient"
          class="mx-auto rounded border border-base-300 bg-base-100 bg-no-repeat"
          style={"width: #{@preview_box}px; height: #{@preview_box}px; image-rendering: pixelated;"}
        />
        <p
          :if={@animation["frames"] == []}
          class="rounded-box bg-base-300/40 p-4 text-center text-xs text-base-content/60"
        >
          No frames yet — click frames in the grid to add them.
        </p>

        <form phx-change="anim_settings" class="grid grid-cols-2 items-end gap-2">
          <label class="form-control">
            <span class="label-text text-xs">FPS</span>
            <input
              type="number"
              name="animation[fps]"
              value={@animation["fps"]}
              min="1"
              max="60"
              class="input input-sm w-full"
            />
          </label>
          <label class="label cursor-pointer justify-start gap-2 pb-1.5">
            <input type="hidden" name="animation[loop]" value="false" />
            <input
              type="checkbox"
              name="animation[loop]"
              value="true"
              checked={@animation["loop"]}
              class="checkbox checkbox-sm"
            />
            <span class="label-text text-xs">Loop</span>
          </label>
        </form>

        <button type="button" class="btn btn-sm btn-ghost" phx-click="anim_clear_frames">
          <.icon name="hero-x-mark" class="size-4" /> Clear frames
        </button>
      </div>
    </aside>
    """
  end

  defp editor_panel(assigns) do
    ~H"""
    <aside class="space-y-3 lg:sticky lg:top-4 lg:self-start">
      <div :if={@selected_asset} class="rounded-box bg-base-200/60 p-4 space-y-4">
        <header>
          <h3 class="text-base font-semibold">Tile {@selected_tile}</h3>
          <p class="text-xs text-base-content/60">
            Click a tile to select, then choose a collision mode.
          </p>
        </header>

        <.tile_preview asset={@selected_asset} selected_tile={@selected_tile} />

        <.mode_selector mode={current_mode(@selected_asset, @selected_tile)} />

        <.mode_controls
          asset={@selected_asset}
          selected_tile={@selected_tile}
          mode={current_mode(@selected_asset, @selected_tile)}
          eyedropper_unavailable?={@eyedropper_unavailable?}
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

    overlay_hook =
      case mode do
        "manual" -> "TileMaskPainter"
        "rectangle" -> "TileRectDrag"
        "polygon" -> "TilePolygonDraw"
        "colors" -> "TileColorPicker"
        _ -> nil
      end

    assigns =
      assigns
      |> assign(:mode, mode)
      |> assign(:overlay_hook, overlay_hook)
      |> assign(:show_overlay?, mode != "none")
      |> assign(:show_cells?, mode not in ["none", "colors"])
      |> assign(:polygon_points, mask_points(assigns.mask))

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
        id={"mask-overlay-#{@asset.id}-#{@selected_tile}-#{@mode}"}
        phx-hook={@overlay_hook}
        data-tile={@selected_tile}
        data-grid={@tile_size}
        data-cols={meta_cols(@asset)}
        data-image-url={@asset.content_url}
        class={[
          "absolute inset-0 grid touch-none",
          @overlay_hook && "cursor-crosshair"
        ]}
        style={"grid-template-columns: repeat(#{@tile_size}, 1fr); grid-template-rows: repeat(#{@tile_size}, 1fr);"}
      >
        <div
          :for={{solid?, x, y} <- (@show_cells? && mask_pixels(@asset, @selected_tile)) || []}
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

        <svg
          :if={@mode == "polygon"}
          viewBox={"0 0 #{@tile_size} #{@tile_size}"}
          class="absolute inset-0 h-full w-full"
          style="pointer-events: none;"
          aria-hidden="true"
        >
          <polygon
            :if={@polygon_points != []}
            data-polygon-outline
            points={Enum.map_join(@polygon_points, " ", fn {x, y} -> "#{x},#{y}" end)}
            class="fill-info/25 stroke-info"
            stroke-width="0.3"
            stroke-linejoin="round"
          />
          <circle
            :for={{{x, y}, index} <- Enum.with_index(@polygon_points)}
            data-vertex
            data-index={index}
            cx={x}
            cy={y}
            r="1"
            class="fill-info stroke-base-100"
            stroke-width="0.3"
            style="pointer-events: auto; cursor: grab;"
          />
        </svg>
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
  attr :eyedropper_unavailable?, :boolean, default: false

  defp mode_controls(%{mode: "rectangle"} = assigns) do
    rect = current_mask(assigns.asset, assigns.selected_tile)["rect"] || %{}
    assigns = assign(assigns, :rect, rect)

    ~H"""
    <div class="space-y-2">
      <p class="text-xs text-base-content/60">
        Drag on the preview above to draw the collision rectangle. Coordinates are in 32-pixel tile space.
      </p>
      <p class="rounded bg-base-300/40 px-2 py-1 font-mono text-xs text-base-content/80">
        x {Map.get(@rect, "x", 0)} · y {Map.get(@rect, "y", 0)} · w {Map.get(@rect, "width", 32)} · h {Map.get(
          @rect,
          "height",
          32
        )}
      </p>
    </div>
    """
  end

  defp mode_controls(%{mode: "polygon"} = assigns) do
    points = mask_points(current_mask(assigns.asset, assigns.selected_tile))
    assigns = assign(assigns, :points, points)

    ~H"""
    <div class="space-y-2">
      <p class="text-xs text-base-content/60">
        Click the preview above to add a vertex; drag a vertex to move it.
        Pixels inside the outline become solid.
      </p>
      <p class="rounded bg-base-300/40 px-2 py-1 font-mono text-xs text-base-content/80">
        {length(@points)} point{if length(@points) == 1, do: "", else: "s"}
      </p>
      <div class="flex flex-wrap gap-2">
        <button
          type="button"
          class="btn btn-sm btn-ghost"
          phx-click="polygon_undo_point"
          disabled={@points == []}
        >
          <.icon name="hero-arrow-uturn-left" class="size-4" /> Undo point
        </button>
        <button
          type="button"
          class="btn btn-sm btn-ghost"
          phx-click="polygon_clear_points"
          disabled={@points == []}
        >
          <.icon name="hero-x-mark" class="size-4" /> Clear
        </button>
      </div>
    </div>
    """
  end

  defp mode_controls(%{mode: "colors"} = assigns) do
    colors = current_mask(assigns.asset, assigns.selected_tile)["colors"] || []
    assigns = assign(assigns, :colors_list, colors)

    ~H"""
    <div class="space-y-2">
      <p class="text-xs text-base-content/60">
        Collision derived at runtime from these tile colors. Click the preview above to
        sample a color from the tile; click a sampled color again to remove it.
      </p>
      <p :if={@eyedropper_unavailable?} class="text-xs text-warning">
        Pixel sampling isn't available for this image — add colors with the picker below.
      </p>
      <div :if={@colors_list != []} class="flex flex-wrap gap-2">
        <span
          :for={color <- @colors_list}
          class="inline-flex items-center gap-1 rounded border border-base-300 px-2 py-1 text-xs font-mono"
        >
          <span
            class="inline-block h-3 w-3 rounded border border-base-300"
            style={"background: #{color};"}
          />
          {color}
          <button
            type="button"
            class="btn btn-ghost btn-xs btn-square -mr-1 h-4 min-h-4 w-4"
            phx-click="remove_color"
            phx-value-color={color}
            aria-label={"Remove #{color}"}
          >
            <.icon name="hero-x-mark" class="size-3" />
          </button>
        </span>
      </div>
      <p :if={@colors_list == []} class="text-xs text-base-content/60">
        No colors yet — click the preview to sample one.
      </p>
      <form phx-submit="add_color" class="flex items-center gap-2">
        <input
          type="color"
          name="color"
          value="#000000"
          class="h-8 w-10 cursor-pointer rounded border border-base-300 bg-base-100 p-0.5"
          aria-label="Pick a color to add"
        />
        <button type="submit" class="btn btn-sm btn-ghost">
          <.icon name="hero-plus" class="size-4" /> Add color
        </button>
      </form>
    </div>
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
            <h2 class="text-lg font-semibold">Upload asset</h2>
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
          <.input field={@form[:name]} type="text" label="Asset name" />

          <.input
            field={@form[:kind]}
            type="select"
            label="Kind"
            options={[
              {"Tileset (map tiles + collision)", "tileset"},
              {"Spritesheet (animations)", "spritesheet"}
            ]}
          />

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
            <button type="submit" class="btn btn-primary btn-sm">Upload</button>
          </div>
        </.form>
      </div>
    </div>
    """
  end

  defp create_asset("spritesheet", owner_id, attrs) do
    Library.create_spritesheet(owner_id, Map.put(attrs, :frame_indexes, attrs.tile_indexes))
  end

  defp create_asset(_kind, owner_id, attrs), do: Library.create_tileset(owner_id, attrs)

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
      "" -> "Could not save asset."
      message -> "Could not save asset: #{message}."
    end
  end

  defp upload_error(reason) when is_binary(reason), do: "Could not upload asset: #{reason}."
  defp upload_error(reason), do: "Could not upload asset: #{inspect(reason)}."

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
    sprite_style(asset, index)
  end

  # Tilesets store the grid as columns/rows; spritesheets as grid_cols/grid_rows.
  defp meta_cols(asset), do: asset.metadata["columns"] || asset.metadata["grid_cols"] || 1
  defp meta_rows(asset), do: asset.metadata["rows"] || asset.metadata["grid_rows"] || 1

  defp preview_style(asset, index) do
    columns = meta_cols(asset)
    rows = meta_rows(asset)
    x = rem(index, columns)
    y = div(index, columns)

    "background-image: url('#{asset.content_url}');" <>
      "background-position: -#{x * @preview_size}px -#{y * @preview_size}px;" <>
      "background-size: #{columns * @preview_size}px #{rows * @preview_size}px;" <>
      "image-rendering: pixelated;"
  end

  defp thumbnail_style(asset) do
    case first_tile_index(asset) do
      nil -> ""
      index -> sprite_style(asset, index)
    end
  end

  # CSS sprite positioning where the container is one tile wide and the image
  # is scaled to (cols * container_size). Percentage-based background-position
  # divides by (cols - 1) because CSS interprets % as a fraction of the unused
  # space (image size - container size), not container size.
  defp sprite_style(asset, index) do
    columns = meta_cols(asset)
    rows = meta_rows(asset)
    x = rem(index, columns)
    y = div(index, columns)
    pos_x = if columns > 1, do: x / (columns - 1) * 100, else: 0
    pos_y = if rows > 1, do: y / (rows - 1) * 100, else: 0

    "background-image: url('#{asset.content_url}');" <>
      "background-position: #{format_percent(pos_x)}% #{format_percent(pos_y)}%;" <>
      "background-size: #{columns * 100}% #{rows * 100}%;" <>
      "image-rendering: pixelated;"
  end

  defp format_percent(value) do
    :erlang.float_to_binary(value * 1.0, decimals: 4)
  end

  defp tile_indexes(%{kind: "spritesheet"} = asset) do
    Map.get(
      asset.metadata,
      "frame_indexes",
      Enum.to_list(0..((asset.metadata["frame_count"] || 1) - 1))
    )
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

  defp clamp(value, lower, upper), do: value |> max(lower) |> min(upper)

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
    |> reload_assets()
  end

  defp reload_assets(socket) do
    designer = socket.assigns.current_designer

    socket
    |> assign(:tilesets, Library.list_tilesets(designer.id))
    |> assign(:spritesheets, Library.list_spritesheets(designer.id))
  end

  defp auto_select_asset(socket) do
    case {socket.assigns.selected_asset, socket.assigns.tilesets ++ socket.assigns.spritesheets} do
      {nil, [first | _]} ->
        socket
        |> assign(:selected_asset, first)
        |> assign(:selected_tile, first_tile_index(first))
        |> assign(:selected_animation, first_animation_name(first))

      _ ->
        socket
    end
  end

  defp animations(asset), do: Map.get(asset.metadata, "animations", [])

  defp first_animation_name(%{kind: "spritesheet"} = asset) do
    case animations(asset) do
      [first | _] -> first["name"]
      _ -> nil
    end
  end

  defp first_animation_name(_asset), do: nil

  defp update_selected_animation(socket, fun) do
    asset = socket.assigns.selected_asset
    name = socket.assigns.selected_animation

    updated_animations =
      Enum.map(animations(asset), fn animation ->
        if animation["name"] == name, do: fun.(animation), else: animation
      end)

    case Library.put_animations(asset, updated_animations) do
      {:ok, updated} -> {:noreply, replace_asset(socket, updated)}
      {:error, message} -> {:noreply, put_flash(socket, :error, animation_error(message))}
    end
  end

  defp animation_error(message) when is_binary(message),
    do: "Could not save animation: #{message}."

  defp animation_error(other), do: "Could not save animation: #{inspect(other)}."

  defp parse_fps(raw, fallback) do
    case Integer.parse(to_string(raw)) do
      {n, _} -> n
      :error -> fallback
    end
  end

  defp polygon_points(socket), do: mask_points(current_mask(socket))

  defp mask_points(mask) do
    (mask["points"] || [])
    |> Enum.flat_map(fn
      %{"x" => x, "y" => y} when is_integer(x) and is_integer(y) -> [{x, y}]
      _ -> []
    end)
  end

  defp clamp_point(x, y) do
    size = CollisionMask.size()
    {clamp(x, 0, size - 1), clamp(y, 0, size - 1)}
  end

  defp mask_colors(socket), do: current_mask(socket)["colors"] || []

  defp normalize_color(color) when is_binary(color) do
    if Regex.match?(~r/^#[0-9a-fA-F]{6}$/, color),
      do: {:ok, String.upcase(color)},
      else: :error
  end

  defp normalize_color(_color), do: :error
end
