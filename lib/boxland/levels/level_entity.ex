defmodule Boxland.Levels.LevelEntity do
  @moduledoc """
  An entity placement in a level (the instance of an entity_type at a
  specific coordinate, with optional per-instance overrides).

  `tag` is a designer-assigned label used as a reference target by ECA
  Actions ("send damage to tag:goons"). Multiple instances may share a
  tag.

  `properties` are instance overrides for declared properties on the
  entity type. Merged at runtime — instance values win.

  `group_id` binds the entity to a tile group on its level's map. When
  the entity moves, every tile in the group translates with it (across
  x/y, and across layers for z).

  `waypoints` is the ordered path the entity walks; combined with
  `movement` (mode/ticks_per_step/wait_at_waypoint) the runtime drives
  motion automatically each tick. Defaults to `loop` once two or more
  waypoints exist; `off` disables the auto-mover.
  """
  use Ecto.Schema
  import Ecto.Changeset

  @movement_modes ~w(off loop ping_pong once random)

  schema "level_entities" do
    field :pos_x, :integer
    field :pos_y, :integer
    field :z_index_override, :integer
    field :tag, :string
    field :group_id, :string
    field :properties, :map, default: %{}
    field :waypoints, {:array, :map}, default: []
    field :movement, :map, default: %{}
    field :instance_overrides, :map, default: %{}
    field :script_state, :map, default: %{}

    belongs_to :level, Boxland.Levels.Level
    belongs_to :entity_type, Boxland.Entities.EntityType

    timestamps(type: :utc_datetime)
  end

  def changeset(level_entity, attrs) do
    level_entity
    |> cast(attrs, [
      :level_id,
      :entity_type_id,
      :pos_x,
      :pos_y,
      :z_index_override,
      :tag,
      :group_id,
      :properties,
      :waypoints,
      :movement,
      :instance_overrides,
      :script_state
    ])
    |> validate_required([:level_id, :entity_type_id, :pos_x, :pos_y])
    |> validate_movement()
    |> foreign_key_constraint(:level_id)
    |> foreign_key_constraint(:entity_type_id)
  end

  def movement_modes, do: @movement_modes

  @doc """
  Normalize a movement map for runtime/UI use. Returns
  `%{"mode" => ..., "ticks_per_step" => int, "wait_at_waypoint" => int}`
  with sensible defaults.
  """
  def normalize_movement(movement) do
    raw = movement || %{}

    mode =
      case Map.get(raw, "mode") do
        m when m in @movement_modes -> m
        _ -> "loop"
      end

    %{
      "mode" => mode,
      "ticks_per_step" => clamp_int(Map.get(raw, "ticks_per_step"), 1, 1, 100),
      "wait_at_waypoint" => clamp_int(Map.get(raw, "wait_at_waypoint"), 0, 0, 1000),
      "auto_facing" => Map.get(raw, "auto_facing", false) in [true, "true", "on"]
    }
  end

  defp validate_movement(changeset) do
    case get_field(changeset, :movement) do
      nil ->
        changeset

      m when is_map(m) ->
        case Map.get(m, "mode") do
          nil ->
            changeset

          mode when mode in @movement_modes ->
            changeset

          _ ->
            add_error(
              changeset,
              :movement,
              "mode must be one of #{Enum.join(@movement_modes, "/")}"
            )
        end

      _ ->
        add_error(changeset, :movement, "must be a map")
    end
  end

  defp clamp_int(n, _default, lo, hi) when is_integer(n), do: max(lo, min(n, hi))

  defp clamp_int(n, default, lo, hi) when is_binary(n) do
    case Integer.parse(n) do
      {i, ""} -> clamp_int(i, default, lo, hi)
      _ -> default
    end
  end

  defp clamp_int(_, default, _lo, _hi), do: default
end
