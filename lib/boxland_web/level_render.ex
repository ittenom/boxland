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
    columns = asset.metadata["columns"] || 1
    x = rem(index, columns) * @cell_px
    y = div(index, columns) * @cell_px

    "background-image: url('#{asset.content_url}'); background-position: -#{x}px -#{y}px;"
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
