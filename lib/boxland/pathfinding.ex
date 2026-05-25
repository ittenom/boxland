defmodule Boxland.Pathfinding do
  @moduledoc """
  A* shortest-path on a 2-D grid with 4-neighbor connectivity.

  `shortest_path(start, goal, opts)` returns `{:ok, [cells]}` (inclusive
  of start and goal) or `{:error, :no_path}`.

  Required `opts`:
    * `:bounds` — `{width, height}` (inclusive 0..width-1, 0..height-1).

  Optional `opts`:
    * `:blocked?` — `(cell) -> boolean`. Defaults to `fn _ -> false end`.
      The start cell is always considered passable even if `blocked?`
      returns true, so an entity already standing on a blocked tile can
      still leave.
  """

  @type cell :: {integer, integer}

  @spec shortest_path(cell, cell, keyword) :: {:ok, [cell]} | {:error, :no_path}
  def shortest_path({_sx, _sy} = start, {_gx, _gy} = goal, opts) do
    {w, h} = Keyword.fetch!(opts, :bounds)
    blocked? = Keyword.get(opts, :blocked?, fn _ -> false end)

    cond do
      start == goal ->
        {:ok, [start]}

      out_of_bounds?(goal, w, h) or out_of_bounds?(start, w, h) ->
        {:error, :no_path}

      blocked?.(goal) ->
        {:error, :no_path}

      true ->
        run_astar(start, goal, w, h, blocked?)
    end
  end

  defp run_astar(start, goal, w, h, blocked?) do
    open = :gb_sets.add({manhattan(start, goal), 0, start}, :gb_sets.empty())
    g_scores = %{start => 0}
    came_from = %{}

    case loop(open, g_scores, came_from, goal, w, h, blocked?) do
      {:ok, came_from} -> {:ok, rebuild(came_from, start, goal)}
      :no_path -> {:error, :no_path}
    end
  end

  defp loop(open, g_scores, came_from, goal, w, h, blocked?) do
    case :gb_sets.is_empty(open) do
      true ->
        :no_path

      false ->
        {{_f, g, current}, open} = :gb_sets.take_smallest(open)

        if current == goal do
          {:ok, came_from}
        else
          {open, g_scores, came_from} =
            neighbors(current, w, h)
            |> Enum.reduce({open, g_scores, came_from}, fn n, {o, gs, cf} ->
              cond do
                blocked?.(n) and n != goal ->
                  {o, gs, cf}

                true ->
                  tentative = g + 1

                  case Map.get(gs, n) do
                    nil ->
                      {:gb_sets.add({tentative + manhattan(n, goal), tentative, n}, o),
                       Map.put(gs, n, tentative), Map.put(cf, n, current)}

                    prev when prev > tentative ->
                      {:gb_sets.add({tentative + manhattan(n, goal), tentative, n}, o),
                       Map.put(gs, n, tentative), Map.put(cf, n, current)}

                    _ ->
                      {o, gs, cf}
                  end
              end
            end)

          loop(open, g_scores, came_from, goal, w, h, blocked?)
        end
    end
  end

  defp neighbors({x, y}, w, h) do
    [{x + 1, y}, {x - 1, y}, {x, y + 1}, {x, y - 1}]
    |> Enum.reject(&out_of_bounds?(&1, w, h))
  end

  defp out_of_bounds?({x, y}, w, h), do: x < 0 or y < 0 or x >= w or y >= h

  defp manhattan({x1, y1}, {x2, y2}), do: abs(x1 - x2) + abs(y1 - y2)

  defp rebuild(came_from, start, goal) do
    rebuild_step(came_from, start, goal, [goal])
  end

  defp rebuild_step(_came_from, start, start, acc), do: acc

  defp rebuild_step(came_from, start, current, acc) do
    case Map.fetch(came_from, current) do
      :error -> acc
      {:ok, prev} -> rebuild_step(came_from, start, prev, [prev | acc])
    end
  end

  @doc """
  Trace the full looped route an entity will walk through its waypoints.

  Builds the sequence `start → wp[0] → wp[1] → ... → wp[N-1] → wp[0]` and
  concatenates the A* path between each pair, deduplicating joint cells.
  The closing leg back to wp[0] only runs when there are 2+ waypoints
  (matches `Eca.move_to_waypoint` cycling via `rem(idx + 1, len)`).

  `waypoints` is a list of `%{"x" => integer, "y" => integer}`.

  Returns:
    * `:empty` — no waypoints; nothing to draw.
    * `{:ok, [cells]}` — every leg routed.
    * `{:partial, [cells], unreachable_index}` — A* failed at this leg
      (0 = start→wp[0], 1 = wp[0]→wp[1], ..., N = wp[N-1]→wp[0]). `cells`
      holds the reachable prefix so the designer still sees how far it gets.
  """
  @spec preview_path(cell, [map], keyword) ::
          :empty | {:ok, [cell]} | {:partial, [cell], non_neg_integer}
  def preview_path(start, waypoints, opts) do
    targets = Enum.map(waypoints, &waypoint_cell/1)

    case targets do
      [] ->
        :empty

      [single] ->
        do_legs([start, single], opts, [], 0)

      [first | _] ->
        do_legs([start | targets] ++ [first], opts, [], 0)
    end
  end

  defp waypoint_cell(wp) do
    {coerce_int(Map.get(wp, "x")), coerce_int(Map.get(wp, "y"))}
  end

  defp coerce_int(n) when is_integer(n), do: n
  defp coerce_int(n) when is_binary(n), do: String.to_integer(n)
  defp coerce_int(_), do: 0

  defp do_legs([_last], _opts, acc, _leg), do: {:ok, Enum.reverse(acc)}

  defp do_legs([a, b | rest], opts, acc, leg) do
    case shortest_path(a, b, opts) do
      {:ok, cells} ->
        do_legs([b | rest], opts, prepend_leg(acc, cells), leg + 1)

      {:error, :no_path} ->
        {:partial, Enum.reverse(acc), leg}
    end
  end

  # First leg seeds the accumulator with the full path (reversed).
  # Subsequent legs share their head cell with the previous tail, so drop it.
  defp prepend_leg([], cells), do: Enum.reverse(cells)

  defp prepend_leg(acc, [_dup | tail]), do: Enum.reduce(tail, acc, &[&1 | &2])

  defp prepend_leg(acc, []), do: acc
end
