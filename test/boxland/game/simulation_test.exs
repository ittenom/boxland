defmodule Boxland.Game.SimulationTest do
  use ExUnit.Case, async: true

  alias Boxland.Game.{Eca, Simulation}

  # A walker that loops a path, plus a "random" walker, so determinism
  # covers the previously-nondeterministic auto-mover branch.
  defp walker(id, waypoints, mode) do
    %{
      id: id,
      type_slug: "walker",
      tag: nil,
      cell_x: 0,
      cell_y: 0,
      z: 0,
      properties: %{},
      alive: true,
      actions: [],
      size: %{"w" => 1, "h" => 1},
      waypoints: waypoints,
      movement: %{"mode" => mode}
    }
  end

  defp world(entities, seed) do
    %{
      entities: Map.new(entities, &{&1.id, &1}),
      types: %{},
      player: %{cell_x: 0, cell_y: 0, z: 0},
      bounds: {10, 10},
      blocked_by_z: %{},
      prev: %{},
      depth: 0,
      tick: 0,
      seed: seed,
      warnings: []
    }
  end

  defp random_world(seed) do
    wps = [
      %{"x" => 1, "y" => 0},
      %{"x" => 3, "y" => 0},
      %{"x" => 5, "y" => 0},
      %{"x" => 7, "y" => 0}
    ]

    world([walker("r", wps, "random")], seed)
  end

  describe "engine determinism" do
    test "random auto-mover replays identically for the same seed" do
      run = fn ->
        Enum.scan(1..40, random_world(42), fn _, w -> Eca.tick(w) end)
        |> Enum.map(&{&1.entities["r"].cell_x, &1.entities["r"].cell_y})
      end

      assert run.() == run.()
    end

    test "different seeds can diverge" do
      trace = fn seed ->
        Enum.scan(1..40, random_world(seed), fn _, w -> Eca.tick(w) end)
        |> Enum.map(&{&1.entities["r"].cell_x, &1.entities["r"].cell_y})
      end

      # Not a hard guarantee for every pair, but seeds 1 and 999 differ here.
      assert trace.(1) != trace.(999)
    end

    test "tick counter increments" do
      w = random_world(7) |> Eca.tick() |> Eca.tick() |> Eca.tick()
      assert w.tick == 3
    end
  end

  describe "Simulation scrub/replay" do
    setup do
      sim = random_world(123) |> Simulation.new()
      {:ok, sim: sim}
    end

    test "tick 0 is the untouched spawn state", %{sim: sim} do
      assert sim.tick == 0
      assert sim.current.entities["r"].cell_x == 0
    end

    test "at/2 reconstructs any past tick equal to forward iteration", %{sim: sim} do
      head = Enum.reduce(1..50, sim, fn _, s -> Simulation.advance(s) end)
      assert head.max_tick == 50

      for t <- [0, 1, 7, 32, 33, 49, 50] do
        scrubbed = Simulation.at(head, t)

        forward =
          Enum.reduce(1..t//1, Simulation.new(random_world(123)), fn _, s ->
            Simulation.advance(s)
          end)

        assert scrubbed.tick == t
        assert pos(scrubbed.current) == pos(forward.current), "mismatch at tick #{t}"
      end
    end

    test "scrubbing back and forth is stable", %{sim: sim} do
      head = Enum.reduce(1..40, sim, fn _, s -> Simulation.advance(s) end)
      a = Simulation.at(head, 10)
      b = head |> Simulation.at(35) |> Simulation.at(10)
      assert pos(a.current) == pos(b.current)
    end

    test "at/2 does not move the head", %{sim: sim} do
      head = Enum.reduce(1..40, sim, fn _, s -> Simulation.advance(s) end)
      scrubbed = Simulation.at(head, 5)
      assert scrubbed.max_tick == 40
      refute Simulation.at_head?(scrubbed)
    end

    test "queued player moves replay deterministically" do
      w =
        %{
          entities: %{},
          types: %{},
          player: %{cell_x: 0, cell_y: 0, z: 0},
          bounds: {10, 10},
          blocked_by_z: %{0 => MapSet.new([{2, 0}])},
          prev: %{},
          depth: 0,
          tick: 0,
          seed: 1,
          warnings: []
        }

      sim =
        Simulation.new(w)
        |> Simulation.queue_move({1, 0})
        |> Simulation.advance()
        |> Simulation.queue_move({1, 0})
        |> Simulation.advance()

      # Second move into the blocked cell {2,0} is a no-op.
      assert player(sim.current) == {1, 0}

      replayed = Simulation.at(sim, 1)
      assert player(replayed.current) == {1, 0}
    end

    test "reset returns to spawn state and clears inputs", %{sim: sim} do
      head =
        sim
        |> Simulation.queue_move({1, 0})
        |> Simulation.advance()
        |> Simulation.advance()

      r = Simulation.reset(head)
      assert r.tick == 0
      assert r.max_tick == 0
      assert r.inputs == %{}
    end
  end

  defp pos(world), do: {world.entities["r"].cell_x, world.entities["r"].cell_y}
  defp player(world), do: {world.player.cell_x, world.player.cell_y}
end
