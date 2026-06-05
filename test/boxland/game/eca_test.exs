defmodule Boxland.Game.EcaTest do
  use ExUnit.Case, async: true

  alias Boxland.Game.Eca

  defp entity(id, attrs) do
    Map.merge(
      %{
        id: id,
        type_slug: "thing",
        tag: nil,
        cell_x: 0,
        cell_y: 0,
        z: 0,
        properties: %{},
        alive: true,
        actions: [],
        size: %{"w" => 1, "h" => 1}
      },
      attrs
    )
  end

  defp world(entities, opts \\ []) do
    %{
      entities: Map.new(entities, &{&1.id, &1}),
      types: opts[:types] || %{},
      player: opts[:player] || %{cell_x: 0, cell_y: 0, z: 0},
      prev: opts[:prev] || %{},
      depth: 0,
      warnings: []
    }
  end

  describe "spawn trigger" do
    test "fires on first tick because prev was empty" do
      action = %{
        "id" => "a1",
        "trigger" => %{"kind" => "spawn"},
        "function" => %{"kind" => "modify_property", "key" => "hp", "op" => "set", "value" => 5}
      }

      w = world([entity("e1", %{actions: [action]})])
      w2 = Eca.tick(w)

      assert w2.entities["e1"].properties["hp"] == 5
    end

    test "does not re-fire on next tick" do
      action = %{
        "id" => "a1",
        "trigger" => %{"kind" => "spawn"},
        "function" => %{"kind" => "modify_property", "key" => "hp", "op" => "add", "value" => 1}
      }

      w = world([entity("e1", %{properties: %{"hp" => 0}, actions: [action]})])
      w2 = Eca.tick(w)
      w3 = Eca.tick(w2)

      assert w2.entities["e1"].properties["hp"] == 1
      assert w3.entities["e1"].properties["hp"] == 1
    end
  end

  describe "proximity trigger" do
    test "fires when player enters range" do
      action = %{
        "id" => "p1",
        "trigger" => %{
          "kind" => "proximity",
          "target" => %{"kind" => "player"},
          "distance" => 2
        },
        "function" => %{
          "kind" => "modify_property",
          "key" => "touched",
          "op" => "set",
          "value" => true
        }
      }

      e = entity("guard", %{cell_x: 5, cell_y: 5, actions: [action]})
      w = world([e], player: %{cell_x: 10, cell_y: 10, z: 0})

      # Out of range — no fire
      w2 = Eca.tick(w)
      refute Map.get(w2.entities["guard"].properties, "touched")

      # Move player adjacent — should fire (edge transition)
      w3 = w2 |> Eca.set_player({6, 5}) |> Eca.tick()
      assert w3.entities["guard"].properties["touched"] == true
    end

    test "different z means no proximity" do
      action = %{
        "id" => "p1",
        "trigger" => %{
          "kind" => "proximity",
          "target" => %{"kind" => "player"},
          "distance" => 1
        },
        "function" => %{
          "kind" => "modify_property",
          "key" => "touched",
          "op" => "set",
          "value" => true
        }
      }

      e = entity("guard", %{cell_x: 0, cell_y: 0, z: 0, actions: [action]})
      w = world([e], player: %{cell_x: 0, cell_y: 0, z: 5})
      w2 = Eca.tick(w)
      refute Map.get(w2.entities["guard"].properties, "touched")
    end
  end

  describe "property trigger" do
    test "fires when hp falls at or below zero" do
      action = %{
        "id" => "death",
        "trigger" => %{"kind" => "property", "key" => "hp", "op" => "<=", "value" => 0},
        "function" => %{"kind" => "despawn_self"}
      }

      e = entity("hero", %{properties: %{"hp" => 5}, actions: [action]})
      w = world([e])
      w = Eca.tick(w)
      assert w.entities["hero"].alive

      w =
        update_in(w, [:entities, "hero", :properties, "hp"], fn _ -> 0 end)
        |> Eca.tick()

      refute w.entities["hero"].alive
    end
  end

  describe "ref resolution" do
    test "tag targets fire once per match" do
      action = %{
        "id" => "blast",
        "trigger" => %{"kind" => "spawn"},
        "function" => %{
          "kind" => "modify_property",
          "target" => %{"kind" => "tag", "tag" => "goon"},
          "key" => "hp",
          "op" => "subtract",
          "value" => 1
        }
      }

      attacker = entity("a", %{tag: "boss", actions: [action]})
      g1 = entity("g1", %{tag: "goon", properties: %{"hp" => 3}})
      g2 = entity("g2", %{tag: "goon", properties: %{"hp" => 3}})

      w = world([attacker, g1, g2]) |> Eca.tick()
      assert w.entities["g1"].properties["hp"] == 2
      assert w.entities["g2"].properties["hp"] == 2
    end
  end

  describe "cascading" do
    test "property change cascades into despawn within same tick" do
      hit = %{
        "id" => "h",
        "trigger" => %{"kind" => "spawn"},
        "function" => %{
          "kind" => "modify_property",
          "key" => "hp",
          "op" => "set",
          "value" => 0
        }
      }

      die = %{
        "id" => "d",
        "trigger" => %{"kind" => "property", "key" => "hp", "op" => "<=", "value" => 0},
        "function" => %{"kind" => "despawn_self"}
      }

      e = entity("e", %{properties: %{"hp" => 5}, actions: [hit, die]})
      w = world([e]) |> Eca.tick()
      refute w.entities["e"].alive
    end

    test "each action fires at most once per tick (no infinite loop)" do
      bounce = %{
        "id" => "b",
        "trigger" => %{"kind" => "property", "key" => "x", "op" => "==", "value" => 0},
        "function" => %{
          "kind" => "modify_property",
          "key" => "x",
          "op" => "add",
          "value" => 1
        }
      }

      e = entity("e", %{properties: %{"x" => 0}, actions: [bounce]})
      w = world([e]) |> Eca.tick()

      assert w.entities["e"].properties["x"] == 1
    end
  end

  describe "auto-mover (waypoints)" do
    test "steps one cell toward the current target each tick (loop mode)" do
      e =
        entity("walker", %{cell_x: 0, cell_y: 0})
        |> Map.put(:waypoints, [%{"x" => 2, "y" => 0}, %{"x" => 2, "y" => 2}])
        |> Map.put(:movement, %{"mode" => "loop"})

      w = world([e]) |> Map.put(:bounds, {5, 5})

      w1 = Eca.tick(w)
      assert {w1.entities["walker"].cell_x, w1.entities["walker"].cell_y} == {1, 0}

      w2 = Eca.tick(w1)
      assert {w2.entities["walker"].cell_x, w2.entities["walker"].cell_y} == {2, 0}

      # After arriving at wp[0], next tick advances toward wp[1] = (2, 2).
      w3 = Eca.tick(w2)
      assert {w3.entities["walker"].cell_x, w3.entities["walker"].cell_y} == {2, 1}
    end

    test "no waypoints is a no-op" do
      e = entity("a", %{cell_x: 0, cell_y: 0}) |> Map.put(:waypoints, [])
      w = world([e]) |> Map.put(:bounds, {5, 5}) |> Eca.tick()
      assert {w.entities["a"].cell_x, w.entities["a"].cell_y} == {0, 0}
    end

    test "movement.mode == \"off\" disables auto-move even with waypoints" do
      e =
        entity("a", %{cell_x: 0, cell_y: 0})
        |> Map.put(:waypoints, [%{"x" => 2, "y" => 0}])
        |> Map.put(:movement, %{"mode" => "off"})

      w = world([e]) |> Map.put(:bounds, {5, 5}) |> Eca.tick()
      assert {w.entities["a"].cell_x, w.entities["a"].cell_y} == {0, 0}
    end

    test "routes around blocked cells" do
      e =
        entity("a", %{cell_x: 0, cell_y: 0, z: 0})
        |> Map.put(:waypoints, [%{"x" => 2, "y" => 0}])

      # (1, 0) is blocked → first step must detour through (0, 1).
      blocked_by_z = %{0 => MapSet.new([{1, 0}])}
      w = world([e]) |> Map.put(:bounds, {5, 5}) |> Map.put(:blocked_by_z, blocked_by_z)
      w = Eca.tick(w)

      step = {w.entities["a"].cell_x, w.entities["a"].cell_y}
      assert step in [{0, 1}]
    end

    test "ping_pong reverses direction at endpoints" do
      e =
        entity("p", %{cell_x: 0, cell_y: 0})
        |> Map.put(:waypoints, [%{"x" => 0, "y" => 0}, %{"x" => 2, "y" => 0}])
        |> Map.put(:movement, %{"mode" => "ping_pong"})

      w = world([e]) |> Map.put(:bounds, {5, 5})

      positions =
        Enum.scan(1..6, w, fn _, last -> Eca.tick(last) end)
        |> Enum.map(fn t -> {t.entities["p"].cell_x, t.entities["p"].cell_y} end)

      # Tick 1: cur == wp[0] → advance only; pos still (0,0).
      # Tick 2: → (1,0). Tick 3: → (2,0) and bounce. Tick 4: → (1,0).
      # Tick 5: → (0,0) and bounce. Tick 6: → (1,0).
      assert positions == [{0, 0}, {1, 0}, {2, 0}, {1, 0}, {0, 0}, {1, 0}]
    end

    test "once stops at the last waypoint" do
      e =
        entity("o", %{cell_x: 0, cell_y: 0})
        |> Map.put(:waypoints, [%{"x" => 1, "y" => 0}, %{"x" => 2, "y" => 0}])
        |> Map.put(:movement, %{"mode" => "once"})

      w =
        world([e])
        |> Map.put(:bounds, {5, 5})
        |> Eca.tick()
        |> Eca.tick()
        |> Eca.tick()
        |> Eca.tick()

      # Reaches (2, 0) and parks there.
      assert {w.entities["o"].cell_x, w.entities["o"].cell_y} == {2, 0}

      w2 = Eca.tick(w)
      assert {w2.entities["o"].cell_x, w2.entities["o"].cell_y} == {2, 0}
    end

    test "ticks_per_step slows motion" do
      e =
        entity("slow", %{cell_x: 0, cell_y: 0})
        |> Map.put(:waypoints, [%{"x" => 2, "y" => 0}])
        |> Map.put(:movement, %{"mode" => "loop", "ticks_per_step" => 2})

      w = world([e]) |> Map.put(:bounds, {5, 5})

      w1 = Eca.tick(w)
      assert {w1.entities["slow"].cell_x, w1.entities["slow"].cell_y} == {0, 0}

      w2 = Eca.tick(w1)
      assert {w2.entities["slow"].cell_x, w2.entities["slow"].cell_y} == {1, 0}
    end
  end

  describe "transform function" do
    defp spawn_transform(op_fields) do
      %{
        "id" => "t1",
        "trigger" => %{"kind" => "spawn"},
        "function" => Map.merge(%{"kind" => "transform"}, op_fields)
      }
    end

    test "mirror_x toggles by default" do
      e = entity("e", %{actions: [spawn_transform(%{"op" => "mirror_x"})]})
      w = world([e]) |> Eca.tick()

      assert w.entities["e"].transform["mirror_x"] == true
      # Toggle again via a forced spawn edge.
      w = put_in(w, [:entities, "e", :alive], false) |> Eca.tick() |> Eca.tick({:spawn, "e"})
      assert w.entities["e"].transform["mirror_x"] == false
    end

    test "mirror with mode set uses the absolute value" do
      e =
        entity("e", %{
          actions: [spawn_transform(%{"op" => "mirror_y", "mode" => "set", "value" => true})]
        })

      w = world([e]) |> Eca.tick()
      assert w.entities["e"].transform["mirror_y"] == true
    end

    test "rotate set replaces; rotate add accumulates and normalizes" do
      set_350 = %{
        "id" => "r1",
        "trigger" => %{"kind" => "spawn"},
        "function" => %{"kind" => "transform", "op" => "rotate", "value" => 350}
      }

      add_20 = %{
        "id" => "r2",
        "trigger" => %{"kind" => "spawn"},
        "function" => %{"kind" => "transform", "op" => "rotate", "mode" => "add", "value" => 20}
      }

      e = entity("e", %{actions: [set_350, add_20]})
      w = world([e]) |> Eca.tick()

      assert w.entities["e"].transform["rotation"] == 10
    end

    test "scale set replaces; scale multiply compounds; invalid values fall back" do
      set_2 = %{
        "id" => "s1",
        "trigger" => %{"kind" => "spawn"},
        "function" => %{"kind" => "transform", "op" => "scale", "value" => 2}
      }

      mul_3 = %{
        "id" => "s2",
        "trigger" => %{"kind" => "spawn"},
        "function" => %{
          "kind" => "transform",
          "op" => "scale",
          "mode" => "multiply",
          "value" => 3
        }
      }

      e = entity("e", %{actions: [set_2, mul_3]})
      w = world([e]) |> Eca.tick()
      assert w.entities["e"].transform["scale"] == 6

      neg = entity("n", %{actions: [spawn_transform(%{"op" => "scale", "value" => -5})]})
      w2 = world([neg]) |> Eca.tick()
      assert w2.entities["n"].transform["scale"] == 1.0
    end

    test "applies to every entity matched by a tag target" do
      action = %{
        "id" => "flip_all",
        "trigger" => %{"kind" => "spawn"},
        "function" => %{
          "kind" => "transform",
          "op" => "mirror_x",
          "target" => %{"kind" => "tag", "tag" => "goon"}
        }
      }

      boss = entity("boss", %{actions: [action]})
      g1 = entity("g1", %{tag: "goon"})
      g2 = entity("g2", %{tag: "goon"})

      w = world([boss, g1, g2]) |> Eca.tick()
      assert w.entities["g1"].transform["mirror_x"] == true
      assert w.entities["g2"].transform["mirror_x"] == true
      refute w.entities["boss"][:transform]
    end

    test "player target is silently skipped" do
      action = %{
        "id" => "t1",
        "trigger" => %{"kind" => "spawn"},
        "function" => %{
          "kind" => "transform",
          "op" => "mirror_x",
          "target" => %{"kind" => "player"}
        }
      }

      e = entity("e", %{actions: [action]})
      w = world([e]) |> Eca.tick()
      assert w.player == %{cell_x: 0, cell_y: 0, z: 0}
    end
  end

  describe "waypoint trigger" do
    defp counting_waypoint_action(index_field \\ %{}) do
      %{
        "id" => "wp_hit",
        "trigger" => Map.merge(%{"kind" => "waypoint"}, index_field),
        "function" => %{"kind" => "modify_property", "key" => "hits", "op" => "add", "value" => 1}
      }
    end

    defp walker(actions, movement \\ %{"mode" => "loop"}) do
      entity("walker", %{cell_x: 0, cell_y: 0, actions: actions})
      |> Map.put(:waypoints, [%{"x" => 2, "y" => 0}, %{"x" => 2, "y" => 2}])
      |> Map.put(:movement, movement)
    end

    test "fires exactly once on the tick after arrival" do
      w = world([walker([counting_waypoint_action()])]) |> Map.put(:bounds, {5, 5})

      # Tick 1 → (1,0); tick 2 → (2,0) = arrival at wp 0 (marker stamped
      # after the cascade); tick 3 = the cascade sees the marker and fires.
      w = w |> Eca.tick() |> Eca.tick()
      assert Map.get(w.entities["walker"].properties, "hits") == nil

      w = Eca.tick(w)
      assert w.entities["walker"].properties["hits"] == 1

      # No re-fire while walking the next leg.
      w = Eca.tick(w)
      assert w.entities["walker"].properties["hits"] == 1
    end

    test "fires exactly once across a wait_at_waypoint window" do
      movement = %{"mode" => "loop", "wait_at_waypoint" => 3}

      w =
        world([walker([counting_waypoint_action(%{"index" => 0})], movement)])
        |> Map.put(:bounds, {5, 5})

      # Arrival at wp0 on tick 2; fire on tick 3; ticks 3-5 wait; tick 6
      # departs; tick 7 arrives at wp1 (index mismatch — no fire).
      w = Enum.reduce(1..8, w, fn _, acc -> Eca.tick(acc) end)
      assert w.entities["walker"].properties["hits"] == 1
    end

    test "fires at every waypoint with index any, and again on re-arrival" do
      w =
        world([walker([counting_waypoint_action(%{"index" => "any"})])])
        |> Map.put(:bounds, {5, 5})

      # Loop arrivals on ticks 2, 4, 6, 8 (legs are 2 steps each); each
      # fires on the following tick → 4 fires within 10 ticks.
      w = Enum.reduce(1..10, w, fn _, acc -> Eca.tick(acc) end)
      assert w.entities["walker"].properties["hits"] == 4
    end

    test "specific index fires only at that waypoint" do
      w = world([walker([counting_waypoint_action(%{"index" => 1})])]) |> Map.put(:bounds, {5, 5})

      # wp0 arrival (tick 2) must not fire; wp1 arrival (tick 4) fires on tick 5.
      w = Enum.reduce(1..4, w, fn _, acc -> Eca.tick(acc) end)
      assert Map.get(w.entities["walker"].properties, "hits") == nil

      w = Eca.tick(w)
      assert w.entities["walker"].properties["hits"] == 1
    end
  end

  describe "auto-facing and moving flag" do
    test "auto_facing mirrors on horizontal direction changes" do
      e =
        entity("p", %{cell_x: 0, cell_y: 0})
        |> Map.put(:waypoints, [%{"x" => 0, "y" => 0}, %{"x" => 2, "y" => 0}])
        |> Map.put(:movement, %{"mode" => "ping_pong", "auto_facing" => true})

      w = world([e]) |> Map.put(:bounds, {5, 5})

      # Tick 1: advance only (standing on wp0) — no facing change yet.
      w1 = Eca.tick(w)
      refute w1.entities["p"][:transform]

      # Ticks 2-3 step right; tick 4 bounces and steps left.
      w3 = w1 |> Eca.tick() |> Eca.tick()
      assert w3.entities["p"].transform["mirror_x"] == false

      w4 = Eca.tick(w3)
      assert w4.entities["p"].transform["mirror_x"] == true
    end

    test "without auto_facing the transform is untouched by motion" do
      e =
        entity("p", %{cell_x: 0, cell_y: 0})
        |> Map.put(:waypoints, [%{"x" => 2, "y" => 0}])
        |> Map.put(:movement, %{"mode" => "loop"})

      w = world([e]) |> Map.put(:bounds, {5, 5}) |> Eca.tick()
      refute w.entities["p"][:transform]
    end

    test "moving is true only on translate ticks" do
      e =
        entity("p", %{cell_x: 0, cell_y: 0})
        |> Map.put(:waypoints, [%{"x" => 0, "y" => 0}, %{"x" => 2, "y" => 0}])
        |> Map.put(:movement, %{"mode" => "ping_pong", "wait_at_waypoint" => 1})

      w = world([e]) |> Map.put(:bounds, {5, 5})

      # Tick 1: standing on wp0 → advance only → not moving.
      w1 = Eca.tick(w)
      assert w1.entities["p"].moving == false

      # Tick 2: wait tick → not moving. Tick 3: steps → moving.
      w2 = Eca.tick(w1)
      assert w2.entities["p"].moving == false

      w3 = Eca.tick(w2)
      assert w3.entities["p"].moving == true
    end
  end

  describe "init_world/2" do
    test "uses LevelEntity → entity map, merges properties" do
      type = %{
        id: 1,
        slug: "boss",
        default_z_index: 25,
        size: %{"w" => 1, "h" => 1},
        properties: [%{"key" => "hp", "type" => "number", "default" => 40}],
        actions: []
      }

      entity = %{
        id: 99,
        entity_type: type,
        entity_type_id: 1,
        tag: "boss1",
        pos_x: 64,
        pos_y: 96,
        z_index_override: nil,
        properties: %{"hp" => 30},
        script_state: %{"alive" => true}
      }

      w = Eca.init_world([entity], {0, 0})
      assert w.entities[99].properties["hp"] == 30
      assert w.entities[99].cell_x == 2
      assert w.entities[99].cell_y == 3
      assert w.entities[99].z == 25
      assert w.types["boss"]
    end

    test "seeds transform from instance_overrides, defaulting when absent" do
      type = %{id: 1, slug: "t", default_z_index: 0, size: nil, properties: [], actions: []}

      base = %{
        id: 1,
        entity_type: type,
        entity_type_id: 1,
        tag: nil,
        pos_x: 0,
        pos_y: 0,
        z_index_override: nil,
        properties: %{},
        script_state: %{}
      }

      mirrored =
        Map.merge(base, %{
          id: 2,
          instance_overrides: %{"transform" => %{"mirror_x" => true, "rotation" => 450}}
        })

      w = Eca.init_world([base, mirrored], {0, 0})

      assert w.entities[1].transform == Eca.default_transform()
      assert w.entities[1].moving == false
      assert w.entities[2].transform["mirror_x"] == true
      assert w.entities[2].transform["rotation"] == 90
    end
  end

  describe "spawn_other transform" do
    test "spawned entities get a normalized transform" do
      action = %{
        "id" => "sp",
        "trigger" => %{"kind" => "spawn"},
        "function" => %{
          "kind" => "spawn_other",
          "type_slug" => "pup",
          "id" => "new1",
          "cell_x" => 1,
          "cell_y" => 1,
          "transform" => %{"mirror_x" => true}
        }
      }

      types = %{
        "pup" => %{slug: "pup", default_z_index: 0, properties: [], actions: [], size: nil}
      }

      e = entity("spawner", %{actions: [action]})
      w = world([e], types: types) |> Eca.tick()

      assert w.entities["new1"].transform["mirror_x"] == true
      assert w.entities["new1"].transform["scale"] == 1.0
      assert w.entities["new1"].moving == false
    end
  end

  describe "init_world_from_snapshot/3" do
    test "builds the same world shape from snapshot JSON with defaults for old snapshots" do
      snapshot = %{
        "entity_types" => [
          %{
            "id" => 7,
            "slug" => "crab",
            "default_z_index" => 2,
            "properties" => [],
            "actions" => [%{"id" => "a", "trigger" => %{"kind" => "spawn"}, "function" => %{}}],
            "size" => %{"w" => 1, "h" => 1}
          }
        ],
        "entities" => [
          %{
            "id" => 42,
            "entity_type_id" => 7,
            "tag" => "crabby",
            "pos_x" => 64,
            "pos_y" => 32,
            "z_index" => 2,
            "properties" => %{"hp" => 3},
            "waypoints" => [%{"x" => 4, "y" => 1}],
            "movement" => %{"mode" => "loop"},
            "transform" => %{"mirror_x" => true},
            "alive" => true
          },
          # Old-snapshot entity without the new keys.
          %{"id" => 43, "entity_type_id" => 7, "pos_x" => 0, "pos_y" => 0}
        ]
      }

      w = Eca.init_world_from_snapshot(snapshot, {0, 0}, bounds: {10, 10}, seed: 5)

      assert w.entities[42].type_slug == "crab"
      assert w.entities[42].cell_x == 2
      assert w.entities[42].cell_y == 1
      assert w.entities[42].transform["mirror_x"] == true
      assert w.entities[42].waypoints == [%{"x" => 4, "y" => 1}]
      assert w.entities[42].actions != []

      assert w.entities[43].transform == Eca.default_transform()
      assert w.entities[43].waypoints == []
      assert w.entities[43].alive == true

      assert w.bounds == {10, 10}
      assert w.seed == 5
      assert w.types["crab"].default_z_index == 2

      # The snapshot world must tick like any other.
      w2 = Eca.tick(w)
      assert {w2.entities[42].cell_x, w2.entities[42].cell_y} == {3, 1}
    end
  end
end
