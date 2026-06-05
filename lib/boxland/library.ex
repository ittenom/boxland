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

  def list_spritesheets(owner_id) do
    Asset
    |> where([a], a.owner_id == ^owner_id and a.kind == "spritesheet")
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

  def create_spritesheet(owner_id, attrs) do
    with {:ok, metadata} <- spritesheet_metadata(attrs) do
      %Asset{}
      |> Asset.changeset(%{
        owner_id: owner_id,
        kind: "spritesheet",
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

  @max_animation_fps 60

  @doc """
  Replace the named animations on a spritesheet. Each animation is
  `%{"name", "frames", "fps", "loop"}`; frames index into the sheet's
  `grid_cols x grid_rows` grid in playback order (repeats allowed).
  """
  def put_animations(%Asset{kind: "spritesheet"} = asset, animations) when is_list(animations) do
    with :ok <- validate_animations(animations, asset.metadata["frame_count"] || 0) do
      asset
      |> Asset.changeset(%{metadata: Map.put(asset.metadata, "animations", animations)})
      |> Repo.update()
    end
  end

  def animation(%Asset{kind: "spritesheet"} = asset, name) do
    asset.metadata
    |> Map.get("animations", [])
    |> Enum.find(&(&1["name"] == name))
  end

  def animation(_asset, _name), do: nil

  defp validate_animations(animations, frame_count) do
    names = Enum.map(animations, & &1["name"])

    cond do
      Enum.any?(animations, &(not valid_animation?(&1, frame_count))) ->
        {:error,
         "each animation needs a name, in-range frames, fps 1-#{@max_animation_fps}, and a loop flag"}

      length(Enum.uniq(names)) != length(names) ->
        {:error, "animation names must be unique"}

      true ->
        :ok
    end
  end

  defp valid_animation?(
         %{"name" => name, "frames" => frames, "fps" => fps, "loop" => loop},
         frame_count
       ) do
    # Frames may be empty mid-edit (the designer adds them one click at a
    # time); renderers skip animations with no frames.
    is_binary(name) and String.trim(name) != "" and
      is_list(frames) and
      Enum.all?(frames, &(is_integer(&1) and &1 >= 0 and &1 < frame_count)) and
      is_integer(fps) and fps >= 1 and fps <= @max_animation_fps and
      is_boolean(loop)
  end

  defp valid_animation?(_animation, _frame_count), do: false

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

  def paint_collision_pixels(%Asset{} = asset, tile_index, pixels, value)
      when is_list(pixels) and is_boolean(value) do
    put_tile_collision(
      asset,
      tile_index,
      CollisionMask.set_pixels(tile_collision(asset, tile_index), pixels, value)
    )
  end

  def fill_collision_mask(%Asset{} = asset, tile_index) do
    put_tile_collision(asset, tile_index, CollisionMask.full())
  end

  def clear_collision_mask(%Asset{} = asset, tile_index) do
    put_tile_collision(asset, tile_index, CollisionMask.none())
  end

  def invert_collision_mask(%Asset{} = asset, tile_index) do
    put_tile_collision(
      asset,
      tile_index,
      CollisionMask.invert(tile_collision(asset, tile_index))
    )
  end

  def rename_asset(%Asset{} = asset, name) when is_binary(name) do
    asset
    |> Asset.changeset(%{name: String.trim(name)})
    |> Repo.update()
  end

  def delete_asset(%Asset{} = asset) do
    Repo.delete(asset)
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

  def visible_tile_indexes(path, width, height) do
    columns = div(width, @tile_size)
    rows = div(height, @tile_size)
    all_indexes = Enum.to_list(0..(rows * columns - 1))

    case png_alpha_rows(path) do
      {:ok, alpha_rows} ->
        visible =
          alpha_rows
          |> Enum.with_index()
          |> Enum.reduce(MapSet.new(), fn {row, y}, indexes ->
            row
            |> Enum.with_index()
            |> Enum.reduce(indexes, fn {alpha, x}, acc ->
              if alpha > 0 do
                MapSet.put(acc, div(y, @tile_size) * columns + div(x, @tile_size))
              else
                acc
              end
            end)
          end)
          |> MapSet.to_list()
          |> Enum.sort()

        {:ok, visible}

      :opaque ->
        {:ok, all_indexes}

      {:error, _reason} ->
        {:ok, all_indexes}
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
    tile_count = rows * columns
    tile_indexes = Map.get(attrs, :tile_indexes, Enum.to_list(0..(tile_count - 1)))

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
           "tile_count" => tile_count,
           "tile_indexes" => tile_indexes,
           "collisions" => %{}
         }}
    end
  end

  defp spritesheet_metadata(attrs) do
    rows = div(attrs.height, @tile_size)
    columns = div(attrs.width, @tile_size)
    frame_count = rows * columns
    frame_indexes = Map.get(attrs, :frame_indexes, Enum.to_list(0..(frame_count - 1)))

    cond do
      rem(attrs.width, @tile_size) != 0 or rem(attrs.height, @tile_size) != 0 ->
        {:error, "spritesheet dimensions must be divisible by #{@tile_size}px"}

      rows == 0 or columns == 0 ->
        {:error, "spritesheet must contain at least one frame"}

      true ->
        {:ok,
         %{
           "tile_size" => @tile_size,
           "width" => attrs.width,
           "height" => attrs.height,
           "grid_rows" => rows,
           "grid_cols" => columns,
           "frame_count" => frame_count,
           "frame_indexes" => frame_indexes,
           "animations" => []
         }}
    end
  end

  defp png_alpha_rows(path) do
    with {:ok, bytes} <- File.read(path),
         {:ok, png} <- parse_png_chunks(bytes),
         {:alpha, channels, bits_per_channel} <- png_alpha_channels(png),
         {:ok, raw} <- inflate_png_data(png.idat),
         {:ok, rows} <- unfilter_png_rows(raw, png.width, channels, bits_per_channel) do
      {:ok, Enum.map(rows, &alpha_values(&1, channels))}
    end
  end

  defp parse_png_chunks(<<137, 80, 78, 71, 13, 10, 26, 10, chunks::binary>>) do
    parse_png_chunks(chunks, %{width: nil, height: nil, color_type: nil, bit_depth: nil, idat: []})
  end

  defp parse_png_chunks(_bytes), do: {:error, :not_png}

  defp parse_png_chunks(<<>>, png), do: finalize_png_chunks(png)

  defp parse_png_chunks(
         <<length::32, type::binary-size(4), data::binary-size(length), _crc::32, rest::binary>>,
         png
       ) do
    png =
      case type do
        "IHDR" ->
          <<width::32, height::32, bit_depth::8, color_type::8, _rest::binary>> = data
          %{png | width: width, height: height, bit_depth: bit_depth, color_type: color_type}

        "IDAT" ->
          %{png | idat: [data | png.idat]}

        _ ->
          png
      end

    if type == "IEND", do: finalize_png_chunks(png), else: parse_png_chunks(rest, png)
  end

  defp parse_png_chunks(_chunks, _png), do: {:error, :malformed_png}

  defp finalize_png_chunks(%{width: width, height: height, idat: idat} = png)
       when is_integer(width) and is_integer(height) and idat != [] do
    {:ok, %{png | idat: IO.iodata_to_binary(Enum.reverse(idat))}}
  end

  defp finalize_png_chunks(_png), do: {:error, :missing_png_data}

  defp png_alpha_channels(%{color_type: 6, bit_depth: 8}), do: {:alpha, 4, 8}
  defp png_alpha_channels(%{color_type: 4, bit_depth: 8}), do: {:alpha, 2, 8}
  defp png_alpha_channels(%{color_type: type}) when type in [0, 2, 3], do: :opaque
  defp png_alpha_channels(_png), do: {:error, :unsupported_png_color}

  defp inflate_png_data(idat) do
    {:ok, :zlib.uncompress(idat)}
  rescue
    _ -> {:error, :bad_png_data}
  end

  defp unfilter_png_rows(raw, width, channels, 8) do
    bytes_per_pixel = channels
    row_length = width * channels
    unfilter_png_rows(raw, row_length, bytes_per_pixel, [], <<0::size(row_length * 8)>>)
  end

  defp unfilter_png_rows(_raw, _width, _channels, _bits), do: {:error, :unsupported_png_depth}

  defp unfilter_png_rows(raw, row_length, bpp, rows, previous) do
    case raw do
      <<>> ->
        {:ok, Enum.reverse(rows)}

      <<filter::8, row::binary-size(row_length), rest::binary>> ->
        unfiltered = unfilter_png_row(filter, row, previous, bpp)
        unfilter_png_rows(rest, row_length, bpp, [unfiltered | rows], unfiltered)

      _ ->
        {:error, :malformed_scanlines}
    end
  end

  defp unfilter_png_row(0, row, _previous, _bpp), do: row

  defp unfilter_png_row(filter, row, previous, bpp) when filter in 1..4 do
    row_bytes = :binary.bin_to_list(row)
    previous_bytes = :binary.bin_to_list(previous)

    row_bytes
    |> Enum.with_index()
    |> Enum.reduce([], fn {byte, index}, acc ->
      left = if index >= bpp, do: Enum.at(acc, bpp - 1), else: 0
      up = Enum.at(previous_bytes, index)
      upper_left = if index >= bpp, do: Enum.at(previous_bytes, index - bpp), else: 0

      predictor =
        case filter do
          1 -> left
          2 -> up
          3 -> div(left + up, 2)
          4 -> paeth(left, up, upper_left)
        end

      [rem(byte + predictor, 256) | acc]
    end)
    |> Enum.reverse()
    |> :binary.list_to_bin()
  end

  defp alpha_values(row, channels) do
    row
    |> :binary.bin_to_list()
    |> Enum.chunk_every(channels)
    |> Enum.map(&Enum.at(&1, channels - 1))
  end

  defp paeth(left, up, upper_left) do
    estimate = left + up - upper_left
    left_distance = abs(estimate - left)
    up_distance = abs(estimate - up)
    upper_left_distance = abs(estimate - upper_left)

    cond do
      left_distance <= up_distance and left_distance <= upper_left_distance -> left
      up_distance <= upper_left_distance -> up
      true -> upper_left
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
