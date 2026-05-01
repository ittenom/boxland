defmodule Boxland.Game.LevelState do
  @moduledoc """
  Persisted snapshot of mutable game state for a single running level
  instance. The runtime ECS world boots from this row + replays the
  Redis WAL since `flushed_at`.
  """
  use Ecto.Schema
  import Ecto.Changeset

  schema "level_state" do
    field :instance_key, :string
    field :state, :binary
    field :flushed_at, :utc_datetime

    belongs_to :level, Boxland.Levels.Level

    timestamps(type: :utc_datetime, updated_at: false)
  end

  def changeset(level_state, attrs) do
    level_state
    |> cast(attrs, [:level_id, :instance_key, :state, :flushed_at])
    |> validate_required([:level_id, :instance_key, :state, :flushed_at])
    |> unique_constraint([:level_id, :instance_key])
    |> foreign_key_constraint(:level_id)
  end
end
