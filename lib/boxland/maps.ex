defmodule Boxland.Maps do
  @moduledoc "Mapmaker operations for grid maps and tile layers."

  import Ecto.Query

  alias Boxland.Maps.{Layer, Map}
  alias Boxland.Repo

  def list_maps(owner_id) do
    Map
    |> where([m], m.owner_id == ^owner_id)
    |> order_by([m], asc: m.name)
    |> Repo.all()
  end

  def get_map!(owner_id, id) do
    Map
    |> where([m], m.owner_id == ^owner_id and m.id == ^id)
    |> preload(layers: ^layer_order())
    |> Repo.one!()
  end

  def create_map(owner_id, attrs) do
    Repo.transaction(fn ->
      map =
        %Map{}
        |> Map.changeset(Elixir.Map.put(attrs, "owner_id", owner_id))
        |> Repo.insert!()

      %Layer{}
      |> Layer.changeset(%{map_id: map.id, name: "ground", z_index: 0, tiles: %{}})
      |> Repo.insert!()

      get_map!(owner_id, map.id)
    end)
  end

  def change_map(%Map{} = map, attrs \\ %{}), do: Map.changeset(map, attrs)

  @doc "Lowest-z layer, used as the default selected layer."
  def primary_layer(%Map{layers: layers}) when is_list(layers) do
    Enum.min_by(layers, & &1.z_index, fn -> nil end)
  end

  def list_layers(map_id) do
    Layer
    |> where([l], l.map_id == ^map_id)
    |> order_by([l], asc: l.z_index, asc: l.id)
    |> Repo.all()
  end

  def get_layer!(layer_id), do: Repo.get!(Layer, layer_id)

  def get_layer_for_map!(map_id, layer_id) do
    Layer
    |> where([l], l.map_id == ^map_id and l.id == ^layer_id)
    |> Repo.one!()
  end

  def create_layer(%Map{id: map_id}, attrs \\ %{}) do
    name = Elixir.Map.get(attrs, :name) || Elixir.Map.get(attrs, "name") || next_layer_name(map_id)
    z_index = Elixir.Map.get(attrs, :z_index) || Elixir.Map.get(attrs, "z_index") || next_z_index(map_id)

    %Layer{}
    |> Layer.changeset(%{map_id: map_id, name: name, z_index: z_index, tiles: %{}})
    |> Repo.insert()
  end

  def duplicate_layer(%Layer{} = source) do
    name = next_layer_name(source.map_id, base: "#{source.name} copy")

    %Layer{}
    |> Layer.changeset(%{
      map_id: source.map_id,
      name: name,
      z_index: next_z_index(source.map_id),
      tiles: source.tiles,
      visible: source.visible,
      locked: source.locked,
      opacity: source.opacity
    })
    |> Repo.insert()
  end

  def delete_layer(%Layer{} = layer) do
    case Repo.aggregate(from(l in Layer, where: l.map_id == ^layer.map_id), :count, :id) do
      n when n <= 1 -> {:error, :last_layer}
      _ -> Repo.delete(layer)
    end
  end

  def rename_layer(%Layer{} = layer, name) do
    layer
    |> Layer.changeset(%{name: name})
    |> Repo.update()
  end

  def toggle_layer_visibility(%Layer{} = layer) do
    layer
    |> Layer.changeset(%{visible: !layer.visible})
    |> Repo.update()
  end

  def toggle_layer_lock(%Layer{} = layer) do
    layer
    |> Layer.changeset(%{locked: !layer.locked})
    |> Repo.update()
  end

  def set_layer_opacity(%Layer{} = layer, opacity) when is_integer(opacity) do
    layer
    |> Layer.changeset(%{opacity: opacity})
    |> Repo.update()
  end

  @doc """
  Reorder layers. Accepts a list of layer ids from highest-z (top of stack)
  to lowest-z (bottom of stack) — the natural order of an Illustrator-style
  layers panel.
  """
  def reorder_layers(map_id, ordered_ids_top_to_bottom) when is_list(ordered_ids_top_to_bottom) do
    count = length(ordered_ids_top_to_bottom)

    Repo.transaction(fn ->
      ordered_ids_top_to_bottom
      |> Enum.with_index()
      |> Enum.each(fn {id, idx} ->
        z = count - idx - 1

        Layer
        |> where([l], l.id == ^id and l.map_id == ^map_id)
        |> Repo.update_all(set: [z_index: z, updated_at: DateTime.utc_now()])
      end)
    end)
  end

  def update_layer_tiles(%Layer{} = layer, tiles) when is_map(tiles) do
    layer
    |> Layer.changeset(%{tiles: tiles})
    |> Repo.update()
  end

  @doc "Remove every tile at the given (x,y) cells on this layer."
  def delete_tiles_in_cells(%Layer{} = layer, cells) when is_list(cells) do
    new_tiles =
      Enum.reduce(cells, layer.tiles, fn {x, y}, acc -> Elixir.Map.delete(acc, key(x, y)) end)

    update_layer_tiles(layer, new_tiles)
  end

  @doc "Increment rotation by 90° on each existing tile in the given cells."
  def rotate_tiles_in_cells(%Layer{} = layer, cells) when is_list(cells) do
    new_tiles =
      Enum.reduce(cells, layer.tiles, fn {x, y}, acc ->
        case Elixir.Map.fetch(acc, key(x, y)) do
          {:ok, tile} ->
            Elixir.Map.put(
              acc,
              key(x, y),
              Elixir.Map.update(tile, "rotation", 90, &rem(&1 + 90, 360))
            )

          :error ->
            acc
        end
      end)

    update_layer_tiles(layer, new_tiles)
  end

  @doc """
  Move a rectangle of tiles from one layer (and origin) to another. `source_cells`
  enumerates the absolute (x, y) cells to lift; `source_origin` is the top-left
  the user picked them up at; `dest_origin` is where the top-left should land.

  Same-layer and cross-layer moves are both supported. Tiles already at the
  destination get clobbered. Atomic. Returns
  `{:ok, {updated_source_layer, updated_dest_layer}}`.
  """
  def relocate_block(
        %Layer{} = from_layer,
        source_cells,
        {sx, sy},
        %Layer{} = to_layer,
        {dx_o, dy_o}
      )
      when is_list(source_cells) do
    moving =
      Enum.reduce(source_cells, %{}, fn {x, y}, acc ->
        case Elixir.Map.fetch(from_layer.tiles, key(x, y)) do
          {:ok, tile} -> Elixir.Map.put(acc, {x - sx, y - sy}, tile)
          :error -> acc
        end
      end)

    from_remaining =
      Enum.reduce(source_cells, from_layer.tiles, fn {x, y}, acc ->
        Elixir.Map.delete(acc, key(x, y))
      end)

    Repo.transaction(fn ->
      if from_layer.id == to_layer.id do
        final =
          Enum.reduce(moving, from_remaining, fn {{dx, dy}, tile}, acc ->
            Elixir.Map.put(acc, key(dx_o + dx, dy_o + dy), tile)
          end)

        case update_layer_tiles(from_layer, final) do
          {:ok, updated} -> {updated, updated}
          {:error, reason} -> Repo.rollback(reason)
        end
      else
        to_final =
          Enum.reduce(moving, to_layer.tiles, fn {{dx, dy}, tile}, acc ->
            Elixir.Map.put(acc, key(dx_o + dx, dy_o + dy), tile)
          end)

        with {:ok, updated_from} <- update_layer_tiles(from_layer, from_remaining),
             {:ok, updated_to} <- update_layer_tiles(to_layer, to_final) do
          {updated_from, updated_to}
        else
          {:error, reason} -> Repo.rollback(reason)
        end
      end
    end)
  end

  @doc """
  Rotate a rectangular block of tiles 90° clockwise as a group: tile positions
  follow the rotation and each tile's `rotation` field is incremented by 90°.
  The block is anchored at its top-left corner — a `w×h` selection becomes
  `h×w` with the same `x1,y1`.

  Returns `{:ok, {updated_layer, new_selection}}` or `{:error, :out_of_bounds}`
  if the rotated extent would leave the map.
  """
  def rotate_block(%Layer{} = layer, selection, map_width, map_height)
      when is_integer(map_width) and is_integer(map_height) do
    %{x1: x1, y1: y1, x2: x2, y2: y2} = selection
    w = x2 - x1 + 1
    h = y2 - y1 + 1
    new_x2 = x1 + h - 1
    new_y2 = y1 + w - 1

    cond do
      new_x2 >= map_width or new_y2 >= map_height ->
        {:error, :out_of_bounds}

      x1 < 0 or y1 < 0 ->
        {:error, :out_of_bounds}

      true ->
        cells = for y <- y1..y2, x <- x1..x2, do: {x, y}

        # Pull existing tiles out of the source rectangle.
        moving =
          Enum.reduce(cells, %{}, fn {x, y}, acc ->
            case Elixir.Map.fetch(layer.tiles, key(x, y)) do
              {:ok, tile} -> Elixir.Map.put(acc, {x, y}, tile)
              :error -> acc
            end
          end)

        # Clear the source rectangle.
        cleared =
          Enum.reduce(cells, layer.tiles, fn {x, y}, acc ->
            Elixir.Map.delete(acc, key(x, y))
          end)

        # Re-place each moving tile at its rotated coordinate.
        placed =
          Enum.reduce(moving, cleared, fn {{x, y}, tile}, acc ->
            dx = x - x1
            dy = y - y1
            new_x = x1 + (h - 1 - dy)
            new_y = y1 + dx

            rotated =
              Elixir.Map.update(tile, "rotation", 90, &rem(&1 + 90, 360))

            Elixir.Map.put(acc, key(new_x, new_y), rotated)
          end)

        case update_layer_tiles(layer, placed) do
          {:ok, updated} ->
            {:ok, {updated, %{x1: x1, y1: y1, x2: new_x2, y2: new_y2}}}

          err ->
            err
        end
    end
  end

  @doc """
  Move every existing tile in `cells` from `from` to `to`. Atomic; returns
  `{:ok, {updated_from, updated_to}}`.
  """
  def move_tiles_between_layers(%Layer{} = from, %Layer{} = to, cells) when is_list(cells) do
    {moved, remaining} =
      Enum.reduce(cells, {%{}, from.tiles}, fn {x, y}, {moved, remaining} ->
        k = key(x, y)

        case Elixir.Map.fetch(remaining, k) do
          {:ok, tile} -> {Elixir.Map.put(moved, k, tile), Elixir.Map.delete(remaining, k)}
          :error -> {moved, remaining}
        end
      end)

    new_to_tiles = Elixir.Map.merge(to.tiles, moved)

    Repo.transaction(fn ->
      with {:ok, updated_from} <- update_layer_tiles(from, remaining),
           {:ok, updated_to} <- update_layer_tiles(to, new_to_tiles) do
        {updated_from, updated_to}
      else
        {:error, reason} -> Repo.rollback(reason)
      end
    end)
  end

  def put_tile(tiles, x, y, tile) do
    Elixir.Map.put(tiles, key(x, y), stringify_tile(tile))
  end

  def delete_tile(tiles, x, y), do: Elixir.Map.delete(tiles, key(x, y))

  def tile_at(tiles, x, y), do: Elixir.Map.get(tiles, key(x, y))

  def key(x, y), do: "#{x},#{y}"

  defp stringify_tile(tile) do
    %{
      "asset_id" => tile.asset_id,
      "tile_index" => tile.tile_index,
      "rotation" => tile.rotation
    }
  end

  defp next_z_index(map_id) do
    Layer
    |> where([l], l.map_id == ^map_id)
    |> select([l], max(l.z_index))
    |> Repo.one()
    |> case do
      nil -> 0
      max -> max + 1
    end
  end

  defp next_layer_name(map_id, opts \\ []) do
    base = Keyword.get(opts, :base, "layer")

    existing =
      Layer
      |> where([l], l.map_id == ^map_id)
      |> select([l], l.name)
      |> Repo.all()
      |> MapSet.new()

    if not MapSet.member?(existing, base) do
      base
    else
      Stream.iterate(2, &(&1 + 1))
      |> Enum.find(fn n -> not MapSet.member?(existing, "#{base} #{n}") end)
      |> then(&"#{base} #{&1}")
    end
  end

  defp layer_order, do: from(l in Layer, order_by: [asc: l.z_index, asc: l.id])
end
