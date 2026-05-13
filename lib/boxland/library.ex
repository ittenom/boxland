defmodule Boxland.Library do
  @moduledoc "Asset library operations for designer-uploaded game files."

  import Ecto.Query

  alias Boxland.Library.Asset
  alias Boxland.Library.CollisionMask
  alias Boxland.Repo

  @tile_size 32

  def tile_size, do: @tile_size

  def list_assets(owner_id) do
    Asset
    |> where([a], a.owner_id == ^owner_id)
    |> order_by([a], desc: a.inserted_at)
    |> Repo.all()
  end

  def list_tilesets(owner_id) do
    Asset
    |> where([a], a.owner_id == ^owner_id and a.kind == "tileset")
    |> order_by([a], asc: a.name)
    |> Repo.all()
  end

  def get_asset!(owner_id, id) do
    Repo.get_by!(Asset, id: id, owner_id: owner_id)
  end

  def create_tileset(owner_id, attrs) do
    with {:ok, metadata} <- tileset_metadata(attrs) do
      %Asset{}
      |> Asset.changeset(%{
        owner_id: owner_id,
        kind: "tileset",
        name: attrs.name,
        sha256: attrs.sha256,
        content_url: attrs.content_url,
        byte_size: attrs.byte_size,
        mime_type: attrs.mime_type,
        metadata: metadata
      })
      |> Repo.insert()
    end
  end

  def put_tile_collision(%Asset{kind: "tileset"} = asset, tile_index, mask)
      when is_integer(tile_index) and is_map(mask) do
    collisions =
      asset.metadata
      |> Map.get("collisions", %{})
      |> Map.put(Integer.to_string(tile_index), mask)

    asset
    |> Asset.changeset(%{metadata: Map.put(asset.metadata, "collisions", collisions)})
    |> Repo.update()
  end

  def tile_collision(%Asset{} = asset, tile_index) do
    asset.metadata
    |> Map.get("collisions", %{})
    |> Map.get(Integer.to_string(tile_index), CollisionMask.none())
  end

  def collision_from_params("none", _params), do: CollisionMask.none()
  def collision_from_params("full", _params), do: CollisionMask.full()

  def collision_from_params("rectangle", params) do
    CollisionMask.rectangle(
      int_param(params, "x", 0),
      int_param(params, "y", 0),
      int_param(params, "width", @tile_size),
      int_param(params, "height", @tile_size)
    )
  end

  def collision_from_params("polygon", %{"points" => points}) do
    points =
      points
      |> String.split(~r/\s+/, trim: true)
      |> Enum.map(fn pair ->
        [x, y] = String.split(pair, ",", parts: 2)
        {String.to_integer(x), String.to_integer(y)}
      end)

    CollisionMask.polygon(points)
  end

  def collision_from_params("colors", %{"colors" => colors}) do
    colors
    |> String.split(",", trim: true)
    |> CollisionMask.colors()
  end

  def toggle_collision_pixel(%Asset{} = asset, tile_index, x, y) do
    put_tile_collision(
      asset,
      tile_index,
      CollisionMask.toggle_pixel(tile_collision(asset, tile_index), x, y)
    )
  end

  def parse_png_dimensions(path) do
    with {:ok, bytes} <- File.read(path),
         {:ok, width, height} <- parse_png_header(bytes) do
      {:ok, {width, height}}
    else
      {:error, reason} -> {:error, "could not read image: #{inspect(reason)}"}
      :not_png -> {:error, "file is not a PNG image"}
      :missing_ihdr -> {:error, "PNG is missing its IHDR header"}
    end
  end

  defp parse_png_header(
         <<137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, "IHDR", width::32, height::32,
           _rest::binary>>
       ) do
    {:ok, width, height}
  end

  defp parse_png_header(<<137, 80, 78, 71, 13, 10, 26, 10, _rest::binary>>), do: :missing_ihdr
  defp parse_png_header(_bytes), do: :not_png

  defp tileset_metadata(attrs) do
    rows = div(attrs.height, @tile_size)
    columns = div(attrs.width, @tile_size)

    cond do
      rem(attrs.width, @tile_size) != 0 or rem(attrs.height, @tile_size) != 0 ->
        {:error, "tileset dimensions must be divisible by #{@tile_size}px"}

      rows == 0 or columns == 0 ->
        {:error, "tileset must contain at least one tile"}

      true ->
        {:ok,
         %{
           "tile_size" => @tile_size,
           "width" => attrs.width,
           "height" => attrs.height,
           "rows" => rows,
           "columns" => columns,
           "tile_count" => rows * columns,
           "collisions" => %{}
         }}
    end
  end

  defp int_param(params, key, default) do
    params
    |> Map.get(key, default)
    |> case do
      value when is_integer(value) -> value
      value when is_binary(value) -> String.to_integer(value)
      _ -> default
    end
  end
end
