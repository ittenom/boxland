defmodule Boxland.Game.Eca do
  @moduledoc """
  Event-Condition-Action dispatcher for the Sandbox runtime.

  A "world" is a plain Elixir map:

      %{
        entities: %{id => entity_map},
        types: %{type_slug => type_map},
        player: %{cell_x: integer, cell_y: integer, z: integer},
        prev: %{id => prev_snapshot},
        depth: 0
      }

  Each `entity_map` has `id, type_slug, tag, cell_x, cell_y, z, properties,
  alive, actions, size`.

  `tick/2` runs through every alive entity, evaluates triggers
  edge-style (prev → current transitions), applies the matching
  functions, then re-snapshots `prev` for the next tick.

  Cascading is bounded at depth 16 — beyond that the dispatcher returns
  the partial world with `:max_depth_reached` warning in metadata.
  """

  @max_cascade_depth 16

  # Properties prefixed with underscore are runtime bookkeeping (auto-mover
  # state) and should be filtered out of designer-facing inspectors.
  @movement_state_keys ~w(_waypoint_index _waypoint_dir _wait_remaining _tick_counter)
  def movement_state_keys, do: @movement_state_keys

  @doc """
  Tick the world once: evaluate triggers, apply functions, run the
  auto-mover (entities with waypoints + non-`off` mode move one step),
  return the new world. `event` is `:tick | {:spawn, id} | {:despawn, id}` —
  for forcing a lifecycle trigger directly.
  """
  def tick(world, event \\ :tick)

  def tick(world, :tick), do: run(world)

  def tick(world, {:spawn, id}) do
    world
    |> put_in([:entities, id, :alive], true)
    |> run()
  end

  def tick(world, {:despawn, id}) do
    world
    |> put_in([:entities, id, :alive], false)
    |> run()
  end

  defp run(world) do
    world
    |> Elixir.Map.put(:fired, MapSet.new())
    |> cascade(0)
    |> auto_move()
    |> snapshot_prev()
  end

  defp cascade(world, depth) when depth > @max_cascade_depth do
    Elixir.Map.put(world, :warnings, [:max_depth_reached | world[:warnings] || []])
  end

  defp cascade(world, depth) do
    {world, fired_any?} = evaluate_pass(world)

    if fired_any? do
      cascade(world, depth + 1)
    else
      world
    end
  end

  defp evaluate_pass(world) do
    Enum.reduce(world.entities, {world, false}, fn {id, _stale_entity}, {acc, fired?} ->
      # Re-read the entity from the accumulator — earlier actions may have mutated it.
      current_entity = acc.entities[id]

      if current_entity.alive do
        case evaluate_entity(acc, id, current_entity) do
          {:no_change, acc} -> {acc, fired?}
          {:fired, acc} -> {acc, true}
        end
      else
        # Despawn-edge: fire despawn triggers once if prev.alive was true.
        if dig(world, [:prev, id, :alive]) == true and not already_fired?(acc, id, :despawn) do
          case fire_lifecycle(acc, current_entity, "despawn") do
            {:no_change, acc} -> {acc, fired?}
            {:fired, acc} -> {mark_fired(acc, id, :despawn), true}
          end
        else
          {acc, fired?}
        end
      end
    end)
  end

  defp evaluate_entity(world, id, entity) do
    Enum.reduce(entity.actions, {:no_change, world}, fn action, {status, acc} ->
      cond do
        Elixir.Map.get(action, "enabled", true) == false ->
          {status, acc}

        already_fired?(acc, id, action["id"]) ->
          {status, acc}

        trigger_fires?(acc, entity, action["trigger"]) ->
          {:fired,
           acc
           |> apply_function(entity, action["function"])
           |> mark_fired(id, action["id"])}

        true ->
          {status, acc}
      end
    end)
  end

  defp fire_lifecycle(world, entity, kind) do
    Enum.reduce(entity.actions, {:no_change, world}, fn action, {status, acc} ->
      case action["trigger"] do
        %{"kind" => ^kind} ->
          if Elixir.Map.get(action, "enabled", true) != false do
            {:fired, apply_function(acc, entity, action["function"])}
          else
            {status, acc}
          end

        _ ->
          {status, acc}
      end
    end)
  end

  defp already_fired?(world, entity_id, action_id),
    do: MapSet.member?(world.fired, {entity_id, action_id})

  defp mark_fired(world, entity_id, action_id),
    do: Elixir.Map.update!(world, :fired, &MapSet.put(&1, {entity_id, action_id}))

  # === Trigger evaluation (edge-triggered) ===

  defp trigger_fires?(world, entity, %{"kind" => "spawn"}) do
    !dig(world, [:prev, entity.id, :alive]) and entity.alive
  end

  defp trigger_fires?(_world, _entity, %{"kind" => "despawn"}), do: false

  defp trigger_fires?(world, entity, %{"kind" => "proximity"} = t) do
    now? = proximity_now?(world, entity, t)
    was? = dig(world, [:prev, entity.id, :proximity, t["target"] || %{}]) == true
    now? and not was?
  end

  defp trigger_fires?(world, entity, %{"kind" => "property"} = t) do
    now? = property_predicate?(world, entity, t)
    was? = dig(world, [:prev, entity.id, :property, predicate_key(t)]) == true
    now? and not was?
  end

  defp trigger_fires?(_world, _entity, _), do: false

  defp dig(nil, _), do: nil
  defp dig(value, []), do: value
  defp dig(map, [k | rest]) when is_map(map), do: dig(Elixir.Map.get(map, k), rest)
  defp dig(_, _), do: nil

  defp proximity_now?(world, self_entity, %{"target" => target, "distance" => dist}) do
    targets = resolve_targets(world, self_entity, target)

    Enum.any?(targets, fn t ->
      cheby(self_entity, t) <= dist
    end)
  end

  defp proximity_now?(_world, _entity, _), do: false

  defp property_predicate?(world, self_entity, %{"key" => key, "op" => op, "value" => v} = t) do
    target = Elixir.Map.get(t, "target", %{"kind" => "self"})

    world
    |> resolve_targets(self_entity, target)
    |> Enum.any?(fn e ->
      compare(Elixir.Map.get(e.properties || %{}, key), op, v)
    end)
  end

  defp property_predicate?(_world, _entity, _), do: false

  defp predicate_key(%{"key" => k, "op" => op, "value" => v, "target" => tgt}),
    do: {k, op, v, tgt}

  defp predicate_key(%{"key" => k, "op" => op, "value" => v}),
    do: {k, op, v, %{"kind" => "self"}}

  defp compare(a, "==", b), do: a == b
  defp compare(a, "!=", b), do: a != b
  defp compare(a, "<", b), do: is_number(a) and is_number(b) and a < b
  defp compare(a, "<=", b), do: is_number(a) and is_number(b) and a <= b
  defp compare(a, ">", b), do: is_number(a) and is_number(b) and a > b
  defp compare(a, ">=", b), do: is_number(a) and is_number(b) and a >= b
  defp compare(_, _, _), do: false

  defp cheby(a, b) do
    if a.z == b.z do
      max(abs(a.cell_x - b.cell_x), abs(a.cell_y - b.cell_y))
    else
      1_000_000_000
    end
  end

  # === Ref resolution ===

  def resolve_targets(world, self_entity, ref) do
    case ref do
      %{"kind" => "self"} ->
        [self_entity]

      %{"kind" => "id", "id" => id} ->
        case world.entities[id] do
          nil -> []
          e -> if e.alive, do: [e], else: []
        end

      %{"kind" => "type", "slug" => slug} ->
        world.entities
        |> Elixir.Map.values()
        |> Enum.filter(&(&1.alive and &1.type_slug == slug))

      %{"kind" => "tag", "tag" => tag} ->
        world.entities
        |> Elixir.Map.values()
        |> Enum.filter(&(&1.alive and &1.tag == tag))

      %{"kind" => "player"} ->
        case world[:player] do
          nil -> []
          p -> [player_to_entity(p)]
        end

      _ ->
        []
    end
  end

  defp player_to_entity(p) do
    %{
      id: :player,
      type_slug: "_player",
      tag: nil,
      cell_x: p.cell_x,
      cell_y: p.cell_y,
      z: p.z,
      properties: %{},
      alive: true,
      actions: [],
      size: %{"w" => 1, "h" => 1}
    }
  end

  # === Function dispatch ===

  defp apply_function(world, self_entity, %{"kind" => "spawn_self"}) do
    put_in(world, [:entities, self_entity.id, :alive], true)
  end

  defp apply_function(world, self_entity, %{"kind" => "despawn_self"}) do
    put_in(world, [:entities, self_entity.id, :alive], false)
  end

  defp apply_function(world, _self_entity, %{"kind" => "spawn_other"} = f) do
    case world.types[f["type_slug"]] do
      nil ->
        world

      type ->
        id = f["id"] || {:spawned, System.unique_integer([:positive])}

        spawned = %{
          id: id,
          type_slug: f["type_slug"],
          tag: f["tag"],
          cell_x: f["cell_x"] || 0,
          cell_y: f["cell_y"] || 0,
          z: f["z"] || type.default_z_index || 0,
          properties: Elixir.Map.merge(default_properties(type), f["properties"] || %{}),
          alive: true,
          actions: type.actions || [],
          size: type.size || %{"w" => 1, "h" => 1}
        }

        put_in(world, [:entities, id], spawned)
    end
  end

  defp apply_function(world, self_entity, %{"kind" => "despawn_other"} = f) do
    targets = resolve_targets(world, self_entity, f["target"] || %{"kind" => "self"})

    Enum.reduce(targets, world, fn t, acc ->
      put_in(acc, [:entities, t.id, :alive], false)
    end)
  end

  defp apply_function(world, self_entity, %{"kind" => "modify_property"} = f) do
    targets = resolve_targets(world, self_entity, f["target"] || %{"kind" => "self"})
    key = f["key"]
    op = f["op"]
    val = f["value"]

    Enum.reduce(targets, world, fn t, acc ->
      update_in(acc, [:entities, t.id, :properties], fn props ->
        Elixir.Map.put(props || %{}, key, apply_op(Elixir.Map.get(props || %{}, key), op, val))
      end)
    end)
  end

  defp apply_function(world, _self_entity, _), do: world

  # === Auto-mover ===
  #
  # Runs after triggers in every tick. Any alive entity with a non-empty
  # `waypoints` list and a movement.mode other than "off" advances along
  # its A* path toward the current target waypoint. Patterns:
  #
  #   - loop:      wp[0] → wp[1] → ... → wp[N-1] → wp[0] → ...
  #   - ping_pong: bounce at endpoints
  #   - once:      walk to wp[N-1] and stop
  #   - random:    after each waypoint, pick a different one at random
  #
  # `ticks_per_step` slows motion (entity moves every Nth tick).
  # `wait_at_waypoint` pauses for N ticks after arrival.
  #
  # Bookkeeping lives in entity.properties under reserved underscore keys
  # so it survives ticks without needing schema changes.

  defp auto_move(world) do
    Enum.reduce(world.entities, world, fn {id, _e}, acc ->
      entity = acc.entities[id]

      if movable?(entity) do
        step_entity(acc, entity)
      else
        acc
      end
    end)
  end

  defp movable?(%{alive: true, waypoints: [_ | _]} = entity) do
    mode = movement_mode(entity)
    mode != "off"
  end

  defp movable?(_), do: false

  defp movement_mode(entity) do
    case Elixir.Map.get(entity, :movement) || %{} do
      %{"mode" => m} when m in ~w(off loop ping_pong once random) -> m
      _ -> "loop"
    end
  end

  defp movement_int(entity, key, default) do
    raw = Elixir.Map.get(entity, :movement) || %{}

    case Elixir.Map.get(raw, key) do
      n when is_integer(n) and n >= 0 -> n
      _ -> default
    end
  end

  defp step_entity(world, entity) do
    props = entity.properties || %{}
    waypoints = entity.waypoints
    len = length(waypoints)
    mode = movement_mode(entity)
    ticks_per_step = max(1, movement_int(entity, "ticks_per_step", 1))
    wait = movement_int(entity, "wait_at_waypoint", 0)

    counter = Elixir.Map.get(props, "_tick_counter", 0) + 1

    if rem(counter, ticks_per_step) != 0 do
      put_props(world, entity.id, Elixir.Map.put(props, "_tick_counter", counter))
    else
      props = Elixir.Map.put(props, "_tick_counter", 0)
      remaining = Elixir.Map.get(props, "_wait_remaining", 0)

      if remaining > 0 do
        put_props(world, entity.id, Elixir.Map.put(props, "_wait_remaining", remaining - 1))
      else
        idx = Elixir.Map.get(props, "_waypoint_index", 0) |> normalize_idx(len)
        dir = Elixir.Map.get(props, "_waypoint_dir", 1)

        target_wp = Enum.at(waypoints, idx)
        tgt = {Elixir.Map.get(target_wp, "x", 0), Elixir.Map.get(target_wp, "y", 0)}
        cur = {entity.cell_x, entity.cell_y}

        if cur == tgt do
          # Already on the current waypoint — advance the index. No step
          # this tick so designers can see the entity "land" between
          # legs. (Mirrors the original move_to_waypoint behavior.)
          advance(world, entity, props, idx, dir, len, mode, wait)
        else
          move_one_step(world, entity, cur, tgt, props, idx, dir, len, mode, wait)
        end
      end
    end
  end

  defp move_one_step(world, entity, cur, tgt, props, idx, dir, len, mode, wait) do
    bounds = world[:bounds] || {1_000_000, 1_000_000}
    blocked_set = dig(world, [:blocked_by_z, entity.z]) || MapSet.new()

    blocked? = fn cell ->
      cell != cur and cell != tgt and MapSet.member?(blocked_set, cell)
    end

    case Boxland.Pathfinding.shortest_path(cur, tgt, bounds: bounds, blocked?: blocked?) do
      {:ok, [_start, next | _]} ->
        world =
          world
          |> put_in([:entities, entity.id, :cell_x], elem(next, 0))
          |> put_in([:entities, entity.id, :cell_y], elem(next, 1))

        if next == tgt do
          # Arrived in the same tick — advance index now so the next tick
          # heads to the next waypoint without an idle "arrive" step.
          advance(world, entity, props, idx, dir, len, mode, wait)
        else
          put_props(world, entity.id, ensure_dir(props, idx, dir))
        end

      _ ->
        # Path is blocked; remember bookkeeping and stay put.
        put_props(world, entity.id, ensure_dir(props, idx, dir))
    end
  end

  defp advance(world, entity, props, idx, dir, len, mode, wait) do
    {new_idx, new_dir} = next_waypoint(mode, idx, dir, len)

    props =
      props
      |> Elixir.Map.put("_waypoint_index", new_idx)
      |> Elixir.Map.put("_waypoint_dir", new_dir)
      |> Elixir.Map.put("_wait_remaining", wait)

    put_props(world, entity.id, props)
  end

  defp next_waypoint("loop", idx, _dir, len), do: {rem(idx + 1, len), 1}

  defp next_waypoint("ping_pong", _idx, _dir, len) when len <= 1, do: {0, 1}

  defp next_waypoint("ping_pong", idx, dir, len) do
    nxt = idx + dir

    cond do
      nxt >= len -> {len - 2, -1}
      nxt < 0 -> {1, 1}
      true -> {nxt, dir}
    end
  end

  defp next_waypoint("once", idx, _dir, len) do
    if idx + 1 >= len, do: {idx, 0}, else: {idx + 1, 1}
  end

  defp next_waypoint("random", _idx, _dir, 1), do: {0, 1}

  defp next_waypoint("random", idx, _dir, len) do
    pick =
      Stream.repeatedly(fn -> :rand.uniform(len) - 1 end)
      |> Enum.find(fn n -> n != idx end)

    {pick, 1}
  end

  defp next_waypoint(_, idx, _, len), do: {rem(idx + 1, len), 1}

  defp normalize_idx(idx, len) when len > 0, do: rem(max(idx, 0), len)
  defp normalize_idx(_, _), do: 0

  defp ensure_dir(props, idx, dir) do
    props
    |> Elixir.Map.put("_waypoint_index", idx)
    |> Elixir.Map.put("_waypoint_dir", dir)
  end

  defp put_props(world, id, props) do
    put_in(world, [:entities, id, :properties], props)
  end

  defp apply_op(_old, "set", v), do: v
  defp apply_op(old, "add", v) when is_number(old) and is_number(v), do: old + v
  defp apply_op(nil, "add", v) when is_number(v), do: v
  defp apply_op(old, "subtract", v) when is_number(old) and is_number(v), do: old - v
  defp apply_op(nil, "subtract", v) when is_number(v), do: -v
  defp apply_op(old, "mul", v) when is_number(old) and is_number(v), do: old * v
  defp apply_op(old, _, _), do: old

  defp default_properties(type) do
    (type.properties || [])
    |> Enum.into(%{}, fn %{"key" => k, "default" => v} -> {k, v} end)
  end

  # === Snapshot for next-tick edge detection ===

  defp snapshot_prev(world) do
    prev =
      Enum.into(world.entities, %{}, fn {id, e} ->
        {id,
         %{
           alive: e.alive,
           properties: e.properties,
           proximity: proximity_snapshot(world, e),
           property: property_snapshot(world, e)
         }}
      end)

    Elixir.Map.put(world, :prev, prev)
  end

  defp proximity_snapshot(world, entity) do
    entity.actions
    |> Enum.filter(&match?(%{"trigger" => %{"kind" => "proximity"}}, &1))
    |> Enum.into(%{}, fn %{"trigger" => t} ->
      {t["target"] || %{}, entity.alive and proximity_now?(world, entity, t)}
    end)
  end

  defp property_snapshot(world, entity) do
    entity.actions
    |> Enum.filter(&match?(%{"trigger" => %{"kind" => "property"}}, &1))
    |> Enum.into(%{}, fn %{"trigger" => t} ->
      {predicate_key(t), entity.alive and property_predicate?(world, entity, t)}
    end)
  end

  # === World construction helpers (for tests + SandboxLive) ===

  @doc """
  Build a starting world from a list of LevelEntity structs (preloaded
  entity_type) and a player cell. Initial `prev` is empty so spawn
  triggers fire on first tick.
  """
  def init_world(entities, player_cell, opts \\ []) do
    types =
      entities
      |> Enum.map(& &1.entity_type)
      |> Enum.uniq_by(& &1.id)
      |> Enum.into(%{}, fn et ->
        {et.slug,
         %{
           slug: et.slug,
           default_z_index: et.default_z_index,
           properties: et.properties,
           actions: et.actions,
           size: et.size
         }}
      end)

    entity_maps =
      Enum.into(entities, %{}, fn e ->
        type = e.entity_type

        {e.id,
         %{
           id: e.id,
           type_slug: type.slug,
           tag: e.tag,
           cell_x: div(e.pos_x, 32),
           cell_y: div(e.pos_y, 32),
           z: e.z_index_override || type.default_z_index,
           properties: Boxland.Entities.merge_properties(type, e.properties),
           waypoints: Elixir.Map.get(e, :waypoints, []) || [],
           movement: Elixir.Map.get(e, :movement, %{}) || %{},
           alive: Elixir.Map.get(e.script_state || %{}, "alive", true),
           actions: type.actions || [],
           size: type.size || %{"w" => 1, "h" => 1}
         }}
      end)

    {px, py} = player_cell
    pz = Keyword.get(opts, :player_z, 0)

    %{
      entities: entity_maps,
      types: types,
      player: %{cell_x: px, cell_y: py, z: pz},
      bounds: Keyword.get(opts, :bounds, {1_000_000, 1_000_000}),
      blocked_by_z: Keyword.get(opts, :blocked_by_z, %{}),
      prev: %{},
      depth: 0,
      warnings: []
    }
  end

  @doc "Update the player's position in an existing world (does not tick)."
  def set_player(world, {cell_x, cell_y}) do
    put_in(world, [:player, :cell_x], cell_x)
    |> put_in([:player, :cell_y], cell_y)
  end
end
