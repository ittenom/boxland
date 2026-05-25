defmodule Boxland.Repo.Migrations.AddLevelEntityMovement do
  use Ecto.Migration

  def change do
    alter table(:level_entities) do
      add :movement, :map, null: false, default: %{}
    end
  end
end
