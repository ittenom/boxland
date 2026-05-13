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
    |> preload(:layers)
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

  def primary_layer(%Map{layers: layers}) do
    Enum.min_by(layers, & &1.z_index, fn -> nil end)
  end

  def update_layer_tiles(%Layer{} = layer, tiles) when is_map(tiles) do
    layer
    |> Layer.changeset(%{tiles: tiles})
    |> Repo.update()
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
end
