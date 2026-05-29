defmodule Boxland.Game.Simulation do
  @moduledoc """
  Deterministic, scrubbable wrapper around `Boxland.Game.Eca`.

  A simulation is reconstructible from `(initial_world, seed, inputs)`:
  the same seed and the same per-tick player-input log always replay to
  the same world at any tick. This is what makes the Level Editor's
  timeline scrubbable — any past tick can be rebuilt by re-simulating
  forward from the nearest cached keyframe.

  Time model:

    - `tick 0` is the spawn state — entities placed exactly as the level
      starts, *before* any ECA tick fires. This is the faithful "as it
      will start when the level spawns" preview.
    - advancing to tick N applies `inputs[N]` (queued player moves) and
      then runs one `Eca.tick`.

  Head vs view:

    - `max_tick` / `head` track the furthest tick simulated (the live
      timeline head). `advance/1` extends the head.
    - `tick` / `current` are where the scrubber currently sits. `at/2`
      moves the scrubber as a *pure read-only view* — it never mutates
      the head or the input log, so dragging back and forth is stable.
    - play/step always continue from the head, snapping the view to it.
  """

  alias Boxland.Game.Eca

  @keyframe_interval 32

  @enforce_keys [:initial, :seed]
  defstruct initial: nil,
            seed: 0,
            inputs: %{},
            tick: 0,
            max_tick: 0,
            current: nil,
            head: nil,
            keyframes: %{}

  @type move :: {integer(), integer()}
  @type t :: %__MODULE__{
          initial: map(),
          seed: integer(),
          inputs: %{optional(non_neg_integer()) => [move()]},
          tick: non_neg_integer(),
          max_tick: non_neg_integer(),
          current: map(),
          head: map(),
          keyframes: %{optional(non_neg_integer()) => map()}
        }

  @doc """
  Build a simulation from a starting world (already built via
  `Eca.init_world/3`; its `:seed` is reused). Both the head and the view
  start at the untouched tick-0 spawn state.
  """
  @spec new(map()) :: t()
  def new(initial_world) when is_map(initial_world) do
    initial = Elixir.Map.put(initial_world, :tick, 0)
    seed = Elixir.Map.get(initial, :seed, 0)

    %__MODULE__{
      initial: initial,
      seed: seed,
      inputs: %{},
      tick: 0,
      max_tick: 0,
      current: initial,
      head: initial,
      keyframes: %{0 => initial}
    }
  end

  @doc """
  Queue a player move to be applied on the next tick (the tick after the
  live head), so it replays deterministically.
  """
  @spec queue_move(t(), move()) :: t()
  def queue_move(%__MODULE__{} = sim, {_dx, _dy} = move) do
    target = sim.max_tick + 1
    inputs = Elixir.Map.update(sim.inputs, target, [move], &(&1 ++ [move]))
    %{sim | inputs: inputs}
  end

  @doc """
  Advance the live head by one tick and snap the scrubber view to it.
  Records a keyframe every #{@keyframe_interval} ticks.
  """
  @spec advance(t()) :: t()
  def advance(%__MODULE__{} = sim) do
    next_tick = sim.max_tick + 1
    world = step(sim.head, sim, next_tick)

    keyframes =
      if rem(next_tick, @keyframe_interval) == 0,
        do: Elixir.Map.put(sim.keyframes, next_tick, world),
        else: sim.keyframes

    %{
      sim
      | current: world,
        head: world,
        tick: next_tick,
        max_tick: next_tick,
        keyframes: keyframes
    }
  end

  @doc """
  Move the scrubber to `target_tick` (clamped to `0..max_tick`) by
  re-simulating forward from the nearest keyframe. Pure read-only view:
  does not change the head or the input log.
  """
  @spec at(t(), integer()) :: t()
  def at(%__MODULE__{} = sim, target_tick) do
    target = target_tick |> max(0) |> min(sim.max_tick)
    world = sim.keyframes |> nearest(target) |> replay_to(sim, target)
    %{sim | current: world, tick: target}
  end

  @doc "Restart the run: back to the tick-0 spawn state, input log cleared."
  @spec reset(t()) :: t()
  def reset(%__MODULE__{} = sim) do
    %{
      sim
      | inputs: %{},
        tick: 0,
        max_tick: 0,
        current: sim.initial,
        head: sim.initial,
        keyframes: %{0 => sim.initial}
    }
  end

  @doc "True when the scrubber is sitting on the live head."
  @spec at_head?(t()) :: boolean()
  def at_head?(%__MODULE__{tick: t, max_tick: t}), do: true
  def at_head?(%__MODULE__{}), do: false

  # === Replay internals ===

  # Re-simulate from `from_world` (whose internal :tick equals its sim
  # tick) up to and including `target`, applying logged inputs each step.
  defp replay_to(from_world, %__MODULE__{} = sim, target) do
    from = Elixir.Map.get(from_world, :tick, 0)

    if from >= target do
      from_world
    else
      Enum.reduce((from + 1)..target, from_world, fn t, world -> step(world, sim, t) end)
    end
  end

  # One deterministic tick into tick `t`: apply that tick's queued moves,
  # then run the ECA tick (which increments world.tick to `t`).
  defp step(world, %__MODULE__{} = sim, t) do
    moves = Elixir.Map.get(sim.inputs, t, [])

    world
    |> apply_moves(moves)
    |> Eca.tick()
  end

  defp apply_moves(world, moves), do: Enum.reduce(moves, world, &Eca.apply_player_move(&2, &1))

  # The keyframe at the greatest stored tick that is <= target.
  defp nearest(keyframes, target) do
    key =
      keyframes
      |> Elixir.Map.keys()
      |> Enum.filter(&(&1 <= target))
      |> Enum.max(fn -> 0 end)

    Elixir.Map.fetch!(keyframes, key)
  end
end
