defmodule BoxlandWeb.LevelRender do
  @moduledoc """
  Pure rendering helpers shared by the Level Editor's edit-mode canvas and
  its play-mode sprite layer (and previously duplicated verbatim across the
  editor and the now-removed Sandbox).

  Everything here is side-effect free: it turns level/map/asset data into
  CSS strings and cell indices. The LiveViews own interaction and state.
  """

  import Ecto.Query

  alias Boxland.Maps

  @cell_px 32

  def cell_px, do: @cell_px

  @doc "All `{x, y}` cells of a map, row-major."
  def cells(width, height), do: for(y <- 0..(height - 1), x <- 0..(width - 1), do: {x, y})

  @doc "Visible layers of a map, sorted bottom-to-top by z then id."
  def visible_layers(%Boxland.Maps.Map{layers: layers}) when is_list(layers) do
    layers
    |> Enum.filter(& &1.visible)
    |> Enum.sort_by(fn l -> {l.z_index, l.id} end)
  end

  def visible_layers(_), do: []

  @doc "Ecto query ordering layers bottom-to-top (for preloads)."
  def layer_order do
    from(l in Boxland.Maps.Layer, order_by: [asc: l.z_index, asc: l.id])
  end

  @doc "Lenient string→integer with a fallback."
  def safe_int(v, default) when is_binary(v) do
    case Integer.parse(v) do
      {n, _} -> n
      :error -> default
    end
  end

  def safe_int(v, _) when is_integer(v), do: v
  def safe_int(_, default), do: default

  @doc "CSS background for a single layer's tile at `(x, y)`, or hidden."
  def layer_cell_style(tilesets, layer, x, y) do
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

  @doc "CSS background-image/position for a tile index within a tileset asset."
  def tile_style(nil, _index), do: ""

  def tile_style(asset, index) do
    columns = asset.metadata["columns"] || asset.metadata["grid_cols"] || 1
    x = rem(index, columns) * @cell_px
    y = div(index, columns) * @cell_px

    "background-image: url('#{asset.content_url}'); background-position: -#{x}px -#{y}px;"
  end

  # === Spritesheet animations ===

  @doc """
  Playback data for a named animation on a spritesheet asset, or nil when
  the asset/animation is missing or the animation has no frames. Accepts
  both `%Asset{}` structs and string-keyed snapshot asset maps.
  """
  def animation_data(nil, _name), do: nil

  def animation_data(%{"metadata" => meta, "content_url" => url}, name),
    do: do_animation_data(meta, url, name)

  def animation_data(%{metadata: meta, content_url: url}, name),
    do: do_animation_data(meta, url, name)

  defp do_animation_data(meta, url, name) do
    case Enum.find(meta["animations"] || [], &(&1["name"] == name)) do
      %{"frames" => [_ | _] = frames} = animation ->
        %{
          url: url,
          cols: meta["grid_cols"] || 1,
          rows: meta["grid_rows"] || 1,
          frames: frames,
          fps: animation["fps"] || 8,
          loop: Map.get(animation, "loop", true)
        }

      _ ->
        nil
    end
  end

  @doc """
  HTML attributes (including `phx-hook="Sprite"`) that make an element play
  an animation. The caller must give the element a stable DOM id. Options:

    * `:sync` — "ambient" (default) or "tick"
    * `:tick` — current sim tick (required for tick sync)
    * `:ticks_per_frame` — sim ticks per animation frame (tick sync, default 2)
    * `:tile` — rendered frame size in px (default #{@cell_px})
  """
  def sprite_attrs(data, opts \\ [])
  def sprite_attrs(nil, _opts), do: []

  def sprite_attrs(data, opts) do
    base = [
      {"phx-hook", "Sprite"},
      {"data-sprite-url", data.url},
      {"data-sprite-cols", data.cols},
      {"data-sprite-rows", data.rows},
      {"data-sprite-tile", Keyword.get(opts, :tile, @cell_px)},
      {"data-sprite-frames", Enum.join(data.frames, ",")},
      {"data-sprite-fps", data.fps},
      {"data-sprite-loop", to_string(data.loop)},
      {"data-sprite-sync", Keyword.get(opts, :sync, "ambient")}
    ]

    case Keyword.get(opts, :tick) do
      nil ->
        base

      tick ->
        base ++
          [
            {"data-sprite-tick", tick},
            {"data-sprite-ticks-per-frame", Keyword.get(opts, :ticks_per_frame, 2)}
          ]
    end
  end

  @doc "True when a layer-tile cell references a spritesheet animation."
  def animated_cell?(%{"kind" => "animated", "animation" => _}), do: true
  def animated_cell?(_cell), do: false

  @doc "Animation playback data for an animated layer-tile cell."
  def tile_anim_data(%{"asset_id" => asset_id, "animation" => name}, assets_by_id),
    do: animation_data(Map.get(assets_by_id, asset_id), name)

  def tile_anim_data(_cell, _assets_by_id), do: nil

  @doc """
  Pick the animation name for an entity given its sim state ("moving" or
  "idle"): explicit binding → "default" binding → the visual_ref's own
  animation. Accepts EntityType structs or string-keyed snapshot type maps.
  """
  def resolved_animation(%{"animation_bindings" => bindings, "visual_ref" => ref}, state),
    do: do_resolved_animation(bindings, ref, state)

  def resolved_animation(%{animation_bindings: bindings, visual_ref: ref}, state),
    do: do_resolved_animation(bindings, ref, state)

  defp do_resolved_animation(bindings, ref, state) do
    bindings = bindings || %{}
    bindings[state] || bindings["default"] || (ref || %{})["animation"]
  end

  @doc """
  Spritesheet assets for every entity whose visual_ref is `"animated"`:
  `%{entity_id => asset}`. Parallels `entity_sprite_styles/2` (which keeps
  serving the static first-frame fallback for these entities).
  """
  def entity_anim_refs(level, assets_by_id) do
    for e <- level.entities,
        %{"kind" => "animated", "asset_id" => asset_id} <- [e.entity_type.visual_ref],
        asset = Map.get(assets_by_id, asset_id),
        asset != nil,
        into: %{} do
      {e.id, asset}
    end
  end

  @doc "First frame of an animation — the static fallback frame."
  def first_animation_frame(asset, name) do
    case animation_data(asset, name) do
      %{frames: [first | _]} -> first
      _ -> 0
    end
  end

  # === Graphics transforms ===

  @default_transform %{"mirror_x" => false, "mirror_y" => false, "rotation" => 0, "scale" => 1.0}

  @doc """
  CSS for an entity's render transform (`%{"mirror_x", "mirror_y",
  "rotation", "scale"}`). Mirrors fold into negative scale; the identity
  transform renders as an empty string. Apply this to a wrapper sized to
  the entity (not the positioned outer element — position translate and
  visual transform must live on separate elements).
  """
  def transform_style(nil), do: ""

  def transform_style(t) when is_map(t) do
    t = Map.merge(@default_transform, t)
    scale = if is_number(t["scale"]), do: t["scale"], else: 1.0
    sx = scale * if t["mirror_x"], do: -1, else: 1
    sy = scale * if t["mirror_y"], do: -1, else: 1
    rotation = if is_number(t["rotation"]), do: t["rotation"], else: 0

    if sx == 1 and sy == 1 and rotation == 0 do
      ""
    else
      "transform: scale(#{sx}, #{sy}) rotate(#{rotation}deg); transform-origin: center;"
    end
  end

  @doc """
  For each level entity, a per-offset map of sprite CSS styles. Single-cell
  entities have one entry at offset `{0, 0}`; group-bound entities have one
  entry per member tile, keyed by `(dx, dy)` from the entity's anchor cell.
  """
  def entity_sprite_styles(level, assets_by_id) do
    Map.new(level.entities, fn e -> {e.id, entity_sprite_offsets(e, assets_by_id, level)} end)
  end

  defp entity_sprite_offsets(e, assets_by_id, level) do
    case e.entity_type.visual_ref do
      %{"kind" => "group", "group_id" => gid} ->
        anchor_x = div(e.pos_x, @cell_px)
        anchor_y = div(e.pos_y, @cell_px)

        gid
        |> group_members_indexed(level)
        |> Enum.flat_map(fn {x, y, tile} ->
          case tile_sprite_style(tile, assets_by_id) do
            nil -> []
            style -> [{{x - anchor_x, y - anchor_y}, style}]
          end
        end)
        |> Map.new()

      %{"kind" => "tile"} = ref ->
        case tile_sprite_style(ref, assets_by_id) do
          nil -> %{}
          style -> %{{0, 0} => style}
        end

      %{"kind" => "sprite", "asset_id" => asset_id} ->
        case Map.get(assets_by_id, asset_id) do
          nil -> %{}
          asset -> %{{0, 0} => sprite_full_style(asset)}
        end

      %{"kind" => "animated", "asset_id" => asset_id} = ref ->
        # Static fallback (first frame); the Sprite hook animates over it.
        case Map.get(assets_by_id, asset_id) do
          nil -> %{}
          asset -> %{{0, 0} => tile_style(asset, first_animation_frame(asset, ref["animation"]))}
        end

      _ ->
        %{}
    end
  end

  defp tile_sprite_style(%{"asset_id" => asset_id, "tile_index" => index} = ref, assets_by_id) do
    rotation = Map.get(ref, "rotation", 0)

    case Map.get(assets_by_id, asset_id) do
      nil -> nil
      asset -> tile_style(asset, index) <> " transform: rotate(#{rotation}deg);"
    end
  end

  defp tile_sprite_style(_ref, _assets_by_id), do: nil

  def group_members_indexed(gid, level) do
    for layer <- level.map.layers || [],
        {k, tile} <- layer.tiles || %{},
        tile["group_id"] == gid do
      {x, y} = Maps.parse_key(k)
      {x, y, tile}
    end
  end

  defp sprite_full_style(asset) do
    "background-image: url('#{asset.content_url}');" <>
      " background-size: contain;" <>
      " background-position: center;" <>
      " background-repeat: no-repeat;" <>
      " image-rendering: pixelated;"
  end
end
