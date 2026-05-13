defmodule Boxland.Library.CollisionMaskTest do
  use ExUnit.Case, async: true

  alias Boxland.Library.CollisionMask

  test "full and none masks are 32x32 bitsets" do
    assert CollisionMask.none()["bits"] == String.duplicate("0", 1024)
    assert CollisionMask.full()["bits"] == String.duplicate("1", 1024)
  end

  test "rectangle mask marks only the requested area" do
    mask = CollisionMask.rectangle(1, 2, 3, 4)
    rows = CollisionMask.rows(mask)

    assert rows |> Enum.at(2) |> Enum.at(1)
    assert rows |> Enum.at(5) |> Enum.at(3)
    refute rows |> Enum.at(1) |> Enum.at(1)
    refute rows |> Enum.at(6) |> Enum.at(3)
  end

  test "manual toggle flips one pixel" do
    mask = CollisionMask.none()
    mask = CollisionMask.toggle_pixel(mask, 7, 9)

    assert mask["mode"] == "manual"
    assert mask |> CollisionMask.rows() |> Enum.at(9) |> Enum.at(7)
  end
end
