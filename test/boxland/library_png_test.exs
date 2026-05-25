defmodule Boxland.LibraryPngTest do
  use ExUnit.Case, async: true

  alias Boxland.Library

  test "parse_png_dimensions reads PNG IHDR dimensions" do
    path = Path.join(System.tmp_dir!(), "boxland-test-#{System.unique_integer([:positive])}.png")

    bytes =
      <<137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, "IHDR", 384::32, 608::32, 8, 6, 0, 0, 0,
        0::32>>

    File.write!(path, bytes)

    try do
      assert Library.parse_png_dimensions(path) == {:ok, {384, 608}}
    after
      File.rm(path)
    end
  end

  test "visible_tile_indexes drops fully transparent tiles" do
    path =
      Path.join(System.tmp_dir!(), "boxland-alpha-test-#{System.unique_integer([:positive])}.png")

    File.write!(path, rgba_png_bytes(64, 32, fn x, _y -> if x < 32, do: 0, else: 255 end))

    try do
      assert Library.visible_tile_indexes(path, 64, 32) == {:ok, [1]}
    after
      File.rm(path)
    end
  end

  defp rgba_png_bytes(width, height, alpha_fun) do
    rows =
      for y <- 0..(height - 1), into: <<>> do
        pixels =
          for x <- 0..(width - 1), into: <<>> do
            <<255, 255, 255, alpha_fun.(x, y)>>
          end

        <<0>> <> pixels
      end

    png_signature() <>
      png_chunk("IHDR", <<width::32, height::32, 8, 6, 0, 0, 0>>) <>
      png_chunk("IDAT", :zlib.compress(rows)) <>
      png_chunk("IEND", <<>>)
  end

  defp png_signature, do: <<137, 80, 78, 71, 13, 10, 26, 10>>

  defp png_chunk(type, data) do
    <<byte_size(data)::32, type::binary, data::binary, 0::32>>
  end
end
