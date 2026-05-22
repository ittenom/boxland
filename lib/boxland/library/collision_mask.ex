defmodule Boxland.Library.CollisionMask do
  @moduledoc "Helpers for 32x32 collision masks."

  @size 32
  @pixel_count @size * @size

  def size, do: @size

  def none, do: %{"mode" => "none", "bits" => bits(false)}
  def full, do: %{"mode" => "full", "bits" => bits(true)}

  def rectangle(x, y, width, height) do
    mask =
      for row <- 0..(@size - 1), col <- 0..(@size - 1) do
        col >= x and col < x + width and row >= y and row < y + height
      end

    %{
      "mode" => "rectangle",
      "rect" => %{"x" => x, "y" => y, "width" => width, "height" => height},
      "bits" => bits(mask)
    }
  end

  def polygon(points) when is_list(points) do
    mask =
      for row <- 0..(@size - 1), col <- 0..(@size - 1) do
        point_in_polygon?({col + 0.5, row + 0.5}, points)
      end

    %{
      "mode" => "polygon",
      "points" => Enum.map(points, fn {x, y} -> %{"x" => x, "y" => y} end),
      "bits" => bits(mask)
    }
  end

  def colors(hex_colors) when is_list(hex_colors) do
    colors =
      hex_colors
      |> Enum.map(&String.trim/1)
      |> Enum.reject(&(&1 == ""))
      |> Enum.map(&normalize_hex/1)

    %{"mode" => "colors", "colors" => colors, "bits" => bits(false)}
  end

  def toggle_pixel(mask, x, y) do
    list = to_booleans(mask)
    index = y * @size + x

    updated =
      List.update_at(list, index, fn value -> not value end)

    mask
    |> Map.put("mode", "manual")
    |> Map.put("bits", bits(updated))
  end

  def set_pixels(mask, pixels, value) when is_list(pixels) and is_boolean(value) do
    list = to_booleans(mask)

    coords =
      pixels
      |> Enum.map(&normalize_coord/1)
      |> Enum.reject(&is_nil/1)
      |> MapSet.new()

    updated =
      list
      |> Enum.with_index()
      |> Enum.map(fn {existing, index} ->
        x = rem(index, @size)
        y = div(index, @size)
        if MapSet.member?(coords, {x, y}), do: value, else: existing
      end)

    mask
    |> Map.put("mode", "manual")
    |> Map.put("bits", bits(updated))
  end

  def invert(mask) do
    inverted = mask |> to_booleans() |> Enum.map(&(not &1))

    mask
    |> Map.put("mode", "manual")
    |> Map.put("bits", bits(inverted))
  end

  defp normalize_coord({x, y}) when is_integer(x) and is_integer(y), do: clamp_coord(x, y)
  defp normalize_coord([x, y]) when is_integer(x) and is_integer(y), do: clamp_coord(x, y)

  defp normalize_coord(%{"x" => x, "y" => y}) do
    with {:ok, xi} <- to_int(x), {:ok, yi} <- to_int(y) do
      clamp_coord(xi, yi)
    else
      _ -> nil
    end
  end

  defp normalize_coord(_), do: nil

  defp clamp_coord(x, y) when x in 0..(@size - 1)//1 and y in 0..(@size - 1)//1, do: {x, y}
  defp clamp_coord(_, _), do: nil

  defp to_int(value) when is_integer(value), do: {:ok, value}

  defp to_int(value) when is_binary(value) do
    case Integer.parse(value) do
      {int, _} -> {:ok, int}
      :error -> :error
    end
  end

  defp to_int(_), do: :error

  def to_booleans(%{"bits" => bit_string}) when is_binary(bit_string) do
    bit_string
    |> String.graphemes()
    |> Enum.map(&(&1 == "1"))
    |> pad()
    |> Enum.take(@pixel_count)
  end

  def to_booleans(_mask), do: List.duplicate(false, @pixel_count)

  def rows(mask) do
    mask
    |> to_booleans()
    |> Enum.chunk_every(@size)
  end

  defp bits(value) when is_boolean(value) do
    value
    |> List.duplicate(@pixel_count)
    |> bits()
  end

  defp bits(values) when is_list(values) do
    values
    |> pad()
    |> Enum.take(@pixel_count)
    |> Enum.map_join(fn
      true -> "1"
      _ -> "0"
    end)
  end

  defp pad(values) do
    values ++ List.duplicate(false, max(@pixel_count - length(values), 0))
  end

  defp normalize_hex("#" <> rest), do: "#" <> String.upcase(rest)
  defp normalize_hex(rest), do: "#" <> String.upcase(rest)

  defp point_in_polygon?(_point, []), do: false

  defp point_in_polygon?({x, y}, points) do
    points
    |> Enum.zip(tl(points) ++ [hd(points)])
    |> Enum.reduce(false, fn {{xi, yi}, {xj, yj}}, inside ->
      intersects = yi > y != yj > y and x < (xj - xi) * (y - yi) / (yj - yi) + xi
      if intersects, do: not inside, else: inside
    end)
  end
end
