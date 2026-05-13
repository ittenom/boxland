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
end
