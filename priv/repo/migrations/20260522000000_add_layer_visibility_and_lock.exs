defmodule Boxland.Repo.Migrations.AddLayerVisibilityAndLock do
  use Ecto.Migration

  def change do
    alter table(:map_layers) do
      add :visible, :boolean, null: false, default: true
      add :locked, :boolean, null: false, default: false
      add :opacity, :integer, null: false, default: 100
    end
  end
end
