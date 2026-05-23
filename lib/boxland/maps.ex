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
