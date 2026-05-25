defmodule Boxland.PathfindingTest do
  use ExUnit.Case, async: true

  alias Boxland.Pathfinding

  test "straight horizontal path" do
    assert {:ok, path} = Pathfinding.shortest_path({0, 0}, {3, 0}, bounds: {5, 5})
    assert path == [{0, 0}, {1, 0}, {2, 0}, {3, 0}]
  end

  test "start equals goal returns single-cell path" do
    assert {:ok, [{2, 2}]} = Pathfinding.shortest_path({2, 2}, {2, 2}, bounds: {5, 5})
  end

  test "routes around an obstacle" do
    blocked = MapSet.new([{1, 0}, {1, 1}])

    assert {:ok, path} =
             Pathfinding.shortest_path({0, 0}, {2, 0},
               bounds: {5, 5},
               blocked?: &MapSet.member?(blocked, &1)
             )

    refute Enum.any?(path, &MapSet.member?(blocked, &1))
    assert List.first(path) == {0, 0}
    assert List.last(path) == {2, 0}
  end

  test "returns :no_path when fully walled in" do
    blocked = MapSet.new([{1, 0}, {0, 1}])

    assert {:error, :no_path} =
             Pathfinding.shortest_path({0, 0}, {2, 2},
               bounds: {2, 2},
               blocked?: &MapSet.member?(blocked, &1)
             )
  end

  test "goal out of bounds is no_path" do
    assert {:error, :no_path} = Pathfinding.shortest_path({0, 0}, {10, 0}, bounds: {5, 5})
  end

  test "blocked goal is no_path" do
    assert {:error, :no_path} =
             Pathfinding.shortest_path({0, 0}, {2, 2},
               bounds: {5, 5},
               blocked?: fn cell -> cell == {2, 2} end
             )
  end

  describe "preview_path/3" do
    test "returns :empty when there are no waypoints" do
      assert :empty = Pathfinding.preview_path({0, 0}, [], bounds: {5, 5})
    end

    test "single waypoint does not close into a loop" do
      assert {:ok, [{0, 0}, {1, 0}, {2, 0}]} =
               Pathfinding.preview_path({0, 0}, [%{"x" => 2, "y" => 0}], bounds: {5, 5})
    end

    test "multiple waypoints close back to the first" do
      assert {:ok, cells} =
               Pathfinding.preview_path(
                 {0, 0},
                 [%{"x" => 2, "y" => 0}, %{"x" => 2, "y" => 2}],
                 bounds: {5, 5}
               )

      assert List.first(cells) == {0, 0}
      # Closes the loop back to wp[0] = {2, 0}.
      assert List.last(cells) == {2, 0}
      assert {2, 2} in cells
    end

    test "partial path reports the failing leg index" do
      # Start at (0,0), wp1 = (3,0). The cells {1,0} and {0,1} wall the
      # start in completely, so leg 0 (start → wp1) is unreachable.
      blocked = MapSet.new([{1, 0}, {0, 1}])

      assert {:partial, [], 0} =
               Pathfinding.preview_path(
                 {0, 0},
                 [%{"x" => 3, "y" => 0}],
                 bounds: {5, 5},
                 blocked?: &MapSet.member?(blocked, &1)
               )
    end
  end
end
