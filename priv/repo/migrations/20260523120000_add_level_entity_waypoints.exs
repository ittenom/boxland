defmodule Boxland.Repo.Migrations.AddLevelEntityWaypoints do
  use Ecto.Migration

  def change do
    alter table(:level_entities) do
      add :waypoints, {:array, :map}, null: false, default: []
    end
  end
end
