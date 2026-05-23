defmodule Boxland.Repo.Migrations.AddEntityPropertiesActions do
  use Ecto.Migration

  def change do
    alter table(:entity_types) do
      add :properties, {:array, :map}, null: false, default: []
      add :actions, {:array, :map}, null: false, default: []
      add :size, :map, null: false, default: %{"w" => 1, "h" => 1}
    end

    alter table(:level_entities) do
      add :tag, :string
      add :properties, :map, null: false, default: %{}
      add :group_id, :string
    end

    create index(:level_entities, [:level_id, :tag])
    create index(:level_entities, [:level_id, :group_id])
  end
end
