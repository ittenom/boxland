defmodule Boxland.Levels.PublishedLevelVersion do
  @moduledoc "An immutable playable snapshot of a level at publish time."
  use Ecto.Schema
  import Ecto.Changeset

  schema "published_level_versions" do
    field :version, :integer
    field :snapshot, :map

    belongs_to :level, Boxland.Levels.Level

    timestamps(type: :utc_datetime, updated_at: false)
  end

  def changeset(version, attrs) do
    version
    |> cast(attrs, [:level_id, :version, :snapshot])
    |> validate_required([:level_id, :version, :snapshot])
    |> validate_number(:version, greater_than: 0)
    |> unique_constraint([:level_id, :version])
    |> foreign_key_constraint(:level_id)
  end
end
