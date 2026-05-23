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
end
