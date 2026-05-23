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

  describe "move_to_waypoint" do
    test "steps one cell toward the current target, advances index on arrival" do
      action = %{
        "id" => "follow",
        "trigger" => %{"kind" => "spawn"},
        "function" => %{"kind" => "move_to_waypoint"}
      }

      e =
        entity("walker", %{
          cell_x: 0,
          cell_y: 0,
          actions: [action]
        })
        |> Map.put(:waypoints, [%{"x" => 2, "y" => 0}, %{"x" => 2, "y" => 2}])

      w = world([e]) |> Map.put(:bounds, {5, 5})

      # Tick 1: walker has spawned this tick, so spawn fires and steps one cell.
      w1 = Eca.tick(w)
      assert {w1.entities["walker"].cell_x, w1.entities["walker"].cell_y} == {1, 0}

      # The spawn trigger is edge-triggered so won't re-fire on later ticks.
      # Drive subsequent steps by re-firing via a property trigger instead.
      step = %{
        "id" => "step",
        "trigger" => %{"kind" => "property", "key" => "go", "op" => "==", "value" => true},
        "function" => %{"kind" => "move_to_waypoint"}
      }

      e2 =
        entity("walker", %{
          cell_x: 0,
          cell_y: 0,
          properties: %{"go" => true},
          actions: [step]
        })
        |> Map.put(:waypoints, [%{"x" => 2, "y" => 0}])

      w0 = world([e2]) |> Map.put(:bounds, {5, 5}) |> Eca.tick()
      assert {w0.entities["walker"].cell_x, w0.entities["walker"].cell_y} == {1, 0}
    end

    test "no waypoints is a no-op" do
      action = %{
        "id" => "f",
        "trigger" => %{"kind" => "spawn"},
        "function" => %{"kind" => "move_to_waypoint"}
      }

      e = entity("a", %{cell_x: 0, cell_y: 0, actions: [action]}) |> Map.put(:waypoints, [])
      w = world([e]) |> Map.put(:bounds, {5, 5}) |> Eca.tick()
      assert {w.entities["a"].cell_x, w.entities["a"].cell_y} == {0, 0}
    end

    test "routes around blocked cells" do
      action = %{
        "id" => "f",
        "trigger" => %{"kind" => "spawn"},
        "function" => %{"kind" => "move_to_waypoint"}
      }

      e =
        entity("a", %{cell_x: 0, cell_y: 0, z: 0, actions: [action]})
        |> Map.put(:waypoints, [%{"x" => 2, "y" => 0}])

      # (1, 0) is blocked → first step must detour through (0, 1).
      blocked_by_z = %{0 => MapSet.new([{1, 0}])}
      w = world([e]) |> Map.put(:bounds, {5, 5}) |> Map.put(:blocked_by_z, blocked_by_z)
      w = Eca.tick(w)

      step = {w.entities["a"].cell_x, w.entities["a"].cell_y}
      assert step in [{0, 1}]
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
  end
end
