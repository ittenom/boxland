defmodule Boxland.Repo.Migrations.AddPublishedLevelVersions do
  use Ecto.Migration

  def change do
    create table(:published_level_versions) do
      add :level_id, references(:levels, on_delete: :delete_all), null: false
      add :version, :integer, null: false
      add :snapshot, :map, null: false
      timestamps(type: :utc_datetime, updated_at: false)
    end

    create unique_index(:published_level_versions, [:level_id, :version])
    create index(:published_level_versions, [:level_id])
  end
end
